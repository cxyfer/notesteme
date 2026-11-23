package faketool

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 假媒體檔標記 v2：
//
//	NOTESTEME-FAKE-MEDIA item=<ref> duration=<秒>[ streams=<type>/<codec>[/<lang>[/<fixture>]],…][ compressed=<bytes>][ silences=a-b,…][ offset=<秒>]
//
// - 沒有 streams 時等於一條音訊串流（相容 v1）。
// - fixture 是相對 features/fixtures/local/ 的路徑（內嵌字幕的內容，可含 /）；lang 用 ISO 639-2（eng、jpn）。
// - compressed 是 ASR 前處理（轉成 opus）之後的檔案大小；silences 是靜音區間（原始時間軸的秒）。
// - offset 出現代表這是從原檔切出來的一塊（起點在原檔的第 offset 秒，長度為 duration）。

// markerPrefix 是標記行的開頭。
const markerPrefix = "NOTESTEME-FAKE-MEDIA "

// 串流類型（同 ffprobe 的 codec_type）。
const (
	StreamAudio      = "audio"
	StreamVideo      = "video"
	StreamSubtitle   = "subtitle"
	StreamData       = "data"
	StreamAttachment = "attachment"
)

func validStreamType(t string) bool {
	switch t {
	case StreamAudio, StreamVideo, StreamSubtitle, StreamData, StreamAttachment:
		return true
	}
	return false
}

// MediaStream 是標記中的一條串流。
type MediaStream struct {
	Type    string // StreamAudio 等
	Codec   string
	Lang    string // ISO 639-2，空字串代表沒有語言標記
	Fixture string // 內嵌字幕的內容（相對 fixtures/local/），其他串流為空
}

// MediaSilence 是一段靜音（秒）。
type MediaSilence struct{ Start, End float64 }

// Media 是假媒體檔標記的內容。
type Media struct {
	Item     string
	Duration float64 // 秒
	// Streams 為空代表標記沒有寫 streams；用 EffectiveStreams 取得（預設一條音訊）。
	Streams    []MediaStream
	Compressed int64 // 0 代表沒有指定
	Silences   []MediaSilence
	Offset     float64
	// Width、Height 是影像串流的尺寸（size=WxH）；0 代表沒有指定。
	Width, Height int
	// HasOffset 區分「offset=0」（第一塊）與沒有 offset。
	HasOffset bool
}

// EffectiveStreams 回傳實際的串流：標記沒有 streams 時是一條音訊。
func (m Media) EffectiveStreams() []MediaStream {
	if len(m.Streams) == 0 {
		return []MediaStream{{Type: StreamAudio, Codec: "aac"}}
	}
	return m.Streams
}

// HasTextSubtitle 回報標記是否帶有文字字幕串流（有 fixture 的 subtitle 串流）。
func (m Media) HasTextSubtitle() bool {
	for _, s := range m.EffectiveStreams() {
		if s.Type == StreamSubtitle && s.Fixture != "" {
			return true
		}
	}
	return false
}

// formatSeconds 以最多三位小數輸出秒數，整數不帶小數點（60 → "60"，1502.5 → "1502.5"）。
func formatSeconds(x float64) string {
	return strconv.FormatFloat(math.Round(x*1000)/1000, 'f', -1, 64)
}

// FormatMedia 回傳標記行（含結尾換行）。
func FormatMedia(m Media) string {
	var sb strings.Builder
	sb.WriteString(markerPrefix)
	var fields []string
	if m.Item != "" {
		fields = append(fields, "item="+m.Item)
	}
	fields = append(fields, "duration="+formatSeconds(m.Duration))
	if len(m.Streams) > 0 {
		parts := make([]string, len(m.Streams))
		for i, s := range m.Streams {
			p := s.Type + "/" + s.Codec
			if s.Lang != "" || s.Fixture != "" {
				p += "/" + s.Lang
			}
			if s.Fixture != "" {
				p += "/" + s.Fixture
			}
			parts[i] = p
		}
		fields = append(fields, "streams="+strings.Join(parts, ","))
	}
	if m.Compressed > 0 {
		fields = append(fields, "compressed="+strconv.FormatInt(m.Compressed, 10))
	}
	if len(m.Silences) > 0 {
		parts := make([]string, len(m.Silences))
		for i, s := range m.Silences {
			parts[i] = formatSeconds(s.Start) + "-" + formatSeconds(s.End)
		}
		fields = append(fields, "silences="+strings.Join(parts, ","))
	}
	if m.HasOffset {
		fields = append(fields, "offset="+formatSeconds(m.Offset))
	}
	if m.Width > 0 && m.Height > 0 {
		fields = append(fields, fmt.Sprintf("size=%dx%d", m.Width, m.Height))
	}
	sb.WriteString(strings.Join(fields, " "))
	sb.WriteByte('\n')
	return sb.String()
}

// ParseMedia 解析假媒體檔的內容（只看第一行）。item 可以省略；沒有 duration、
// 欄位值不合法（負數、串流類型不明等）時 ok 為 false。不認識的欄位忽略。
func ParseMedia(b []byte) (m Media, ok bool) {
	line := string(b)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !strings.HasPrefix(line, markerPrefix) {
		return Media{}, false
	}
	var gotDur bool
	for _, f := range strings.Fields(strings.TrimPrefix(line, markerPrefix)) {
		k, v, found := strings.Cut(f, "=")
		if !found {
			continue
		}
		switch k {
		case "item":
			m.Item = v
		case "duration":
			d, err := strconv.ParseFloat(v, 64)
			if err != nil || d < 0 || math.IsNaN(d) || math.IsInf(d, 0) {
				return Media{}, false
			}
			m.Duration, gotDur = d, true
		case "streams":
			for _, p := range strings.Split(v, ",") {
				// fixture 可以含 /，所以最多切成四段。
				seg := strings.SplitN(p, "/", 4)
				if len(seg) < 2 || !validStreamType(seg[0]) || seg[1] == "" {
					return Media{}, false
				}
				s := MediaStream{Type: seg[0], Codec: seg[1]}
				if len(seg) > 2 {
					s.Lang = seg[2]
				}
				if len(seg) > 3 {
					s.Fixture = seg[3]
				}
				m.Streams = append(m.Streams, s)
			}
		case "compressed":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return Media{}, false
			}
			m.Compressed = n
		case "silences":
			for _, p := range strings.Split(v, ",") {
				a, bb, found := strings.Cut(p, "-")
				if !found {
					return Media{}, false
				}
				start, err1 := strconv.ParseFloat(a, 64)
				end, err2 := strconv.ParseFloat(bb, 64)
				if err1 != nil || err2 != nil || start < 0 || end < start {
					return Media{}, false
				}
				m.Silences = append(m.Silences, MediaSilence{Start: start, End: end})
			}
		case "size":
			w, h, ok := ParseSize(v)
			if !ok {
				return Media{}, false
			}
			m.Width, m.Height = w, h
		case "offset":
			o, err := strconv.ParseFloat(v, 64)
			if err != nil || o < 0 || math.IsNaN(o) || math.IsInf(o, 0) {
				return Media{}, false
			}
			m.Offset, m.HasOffset = o, true
		}
	}
	return m, gotDur
}

// MediaMarker 回傳假媒體檔的內容：一行 "NOTESTEME-FAKE-MEDIA item=<id> duration=<秒>"（沒有 streams，等於一條音訊）。
func MediaMarker(item string, durationSec int) string {
	return fmt.Sprintf("%sitem=%s duration=%d\n", markerPrefix, item, durationSec)
}

// ParseMediaMarker 是 ParseMedia 的簡化版（v1 相容）：只回傳 item 與整數秒數，item 與 duration 都要有。
func ParseMediaMarker(b []byte) (item string, durationSec int, ok bool) {
	m, ok := ParseMedia(b)
	if !ok || m.Item == "" {
		return "", 0, false
	}
	return m.Item, int(m.Duration), true
}

// ParseSize 解析 `寬x高`（兩者都是 1～16384 的整數）。
func ParseSize(s string) (w, h int, ok bool) {
	a, b, found := strings.Cut(s, "x")
	if !found {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(a)
	h, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || w < 1 || h < 1 || w > 16384 || h > 16384 {
		return 0, 0, false
	}
	return w, h, true
}
