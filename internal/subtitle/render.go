package subtitle

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// M1 支援的輸出格式（OUT-R1）。
const (
	FormatSRT      = "srt"
	FormatVTT      = "vtt"
	FormatTXT      = "txt"
	FormatCuesJSON = "cues-json"
)

// ErrUnsupportedFormat 表示不支援的輸出格式。
var ErrUnsupportedFormat = errors.New("subtitle: unsupported format")

// ContentType 回傳格式對應的 Content-Type；未知格式回傳 application/octet-stream。
func ContentType(format string) string {
	switch format {
	case FormatSRT, FormatBilingualSRT:
		return "application/x-subrip; charset=utf-8"
	case FormatVTT:
		return "text/vtt; charset=utf-8"
	case FormatTXT:
		return "text/plain; charset=utf-8"
	case FormatCuesJSON:
		return "application/json; charset=utf-8"
	case FormatASS:
		return "text/x-ssa; charset=utf-8"
	}
	return "application/octet-stream"
}

// FileExt 回傳格式對應的副檔名（不含點）；未知格式回傳空字串。
func FileExt(format string) string {
	switch format {
	case FormatSRT, FormatVTT, FormatTXT, FormatASS:
		return format
	case FormatBilingualSRT:
		return "srt"
	case FormatCuesJSON:
		return "json"
	}
	return ""
}

// Render 依格式把字幕串流寫入 w。
func Render(w io.Writer, format string, t Transcript) error {
	cw, err := NewCueWriter(w, format, t)
	if err != nil {
		return err
	}
	for _, c := range t.Cues {
		if err := cw.Write(c); err != nil {
			return err
		}
	}
	return cw.Close()
}

// cueLines 切成非空的行，移除會破壞 SRT／VTT 結構的空行。
func cueLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// RenderSRT 串流輸出 SRT；編號依輸出順序從 1 起算。
func RenderSRT(w io.Writer, cues []Cue) error { return Render(w, FormatSRT, Transcript{Cues: cues}) }

var vttEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// RenderVTT 串流輸出 WebVTT；文字中的 &、<、> 依規格跳脫。
func RenderVTT(w io.Writer, cues []Cue) error { return Render(w, FormatVTT, Transcript{Cues: cues}) }

// RenderTXT 串流輸出純文字，每句一行。
func RenderTXT(w io.Writer, cues []Cue) error { return Render(w, FormatTXT, Transcript{Cues: cues}) }

// RenderCuesJSON 串流輸出 notesteme cues JSON（ADR-0005），逐 cue 編碼，不需整份載入記憶體。
// 時間以毫秒整數存放。
func RenderCuesJSON(w io.Writer, t Transcript) error { return Render(w, FormatCuesJSON, t) }

// CueWriter 逐句寫出單軌的字幕格式（SRT、VTT、純文字、cues JSON），不需要先拿到整份字幕（NFR-RES-3）。
// 輸出與 Render 完全相同；寫完要呼叫 Close 把緩衝寫出（cues JSON 也在這時補上結尾）。
type CueWriter struct {
	bw     *bufio.Writer
	format string
	n      int
}

// NewCueWriter 建立 format 的 CueWriter；header 的 Lang、Variant、Speakers 只用於 cues JSON，Cues 不會被寫出。
func NewCueWriter(w io.Writer, format string, header Transcript) (*CueWriter, error) {
	cw := &CueWriter{bw: bufio.NewWriter(w), format: format}
	switch format {
	case FormatSRT, FormatTXT:
	case FormatVTT:
		cw.bw.WriteString("WEBVTT\n\n")
	case FormatCuesJSON:
		lang, err := json.Marshal(header.Lang)
		if err != nil {
			return nil, err
		}
		variant, err := json.Marshal(header.Variant)
		if err != nil {
			return nil, err
		}
		speakers := header.Speakers
		if speakers == nil {
			speakers = []Speaker{}
		}
		sp, err := json.Marshal(speakers)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(cw.bw, `{"lang":%s,"variant":%s,"speakers":%s,"cues":[`, lang, variant, sp)
	default:
		return nil, ErrUnsupportedFormat
	}
	return cw, nil
}

// Write 寫出一句。
func (cw *CueWriter) Write(c Cue) error {
	cw.n++
	bw := cw.bw
	switch cw.format {
	case FormatSRT:
		lines := cueLines(strings.ReplaceAll(c.Text, "-->", "->"))
		fmt.Fprintf(bw, "%d\n%s --> %s\n%s\n\n", cw.n, FormatSRTTime(c.StartMS), FormatSRTTime(c.EndMS), strings.Join(lines, "\n"))
	case FormatVTT:
		lines := cueLines(c.Text)
		for i, l := range lines {
			lines[i] = vttEscaper.Replace(l)
		}
		fmt.Fprintf(bw, "%s --> %s\n%s\n\n", FormatVTTTime(c.StartMS), FormatVTTTime(c.EndMS), strings.Join(lines, "\n"))
	case FormatTXT:
		bw.WriteString(strings.Join(cueLines(c.Text), " "))
		bw.WriteByte('\n')
	case FormatCuesJSON:
		if cw.n > 1 {
			bw.WriteByte(',')
		}
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		bw.Write(b)
	}
	// bufio.Writer 記住第一個寫入錯誤；這裡回報它，讓呼叫端在 client 斷線時停止讀取。
	_, err := bw.Write(nil)
	return err
}

// Close 寫出結尾並清空緩衝。
func (cw *CueWriter) Close() error {
	if cw.format == FormatCuesJSON {
		cw.bw.WriteString("]}\n")
	}
	return cw.bw.Flush()
}

// maxCuesJSONBytes 限制解碼的 cues JSON 大小（NFR-SEC-6：不可信輸入）。
const maxCuesJSONBytes = 64 << 20

// DecodeCuesJSON 解碼 cues JSON 並檢查變體、時間來源與時間範圍。
func DecodeCuesJSON(r io.Reader) (Transcript, error) {
	var t Transcript
	dec := json.NewDecoder(io.LimitReader(r, maxCuesJSONBytes))
	if err := dec.Decode(&t); err != nil {
		return Transcript{}, fmt.Errorf("subtitle: decode cues json: %w", err)
	}
	if !t.Variant.Valid() {
		return Transcript{}, fmt.Errorf("subtitle: unknown variant %q", t.Variant)
	}
	for i, c := range t.Cues {
		if c.Timing != "" && !c.Timing.Valid() {
			return Transcript{}, fmt.Errorf("subtitle: cue %d: unknown timing %q", i+1, c.Timing)
		}
		if c.StartMS < 0 || c.EndMS < c.StartMS {
			return Transcript{}, fmt.Errorf("subtitle: cue %d: invalid time range", i+1)
		}
	}
	return t, nil
}

// FilterRange 只保留和 [fromMS, toMS) 有重疊的 cue，時間維持原值（OUT-R3）。
// toMS 小於 0 表示沒有上限。
func FilterRange(cues []Cue, fromMS, toMS int64) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		if c.EndMS > fromMS && (toMS < 0 || c.StartMS < toMS) {
			out = append(out, c)
		}
	}
	return out
}

// variantPriority 為預設變體的優先順序（OUT-R2），由高到低。
var variantPriority = []Variant{
	VariantEdited, VariantTranslated, VariantCorrected, VariantSegmented, VariantNormalized,
}

// PickVariant 依 OUT-R2 選出處理得最完整的變體。
// 沒有任何加工過的變體時，若有 source 就回傳 source；都沒有時回傳 ("", false)。
func PickVariant(available []Variant) (Variant, bool) {
	has := make(map[Variant]bool, len(available))
	for _, v := range available {
		has[v] = true
	}
	for _, v := range variantPriority {
		if has[v] {
			return v, true
		}
	}
	if has[VariantSource] {
		return VariantSource, true
	}
	return "", false
}
