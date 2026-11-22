package pipeline

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 下載 <podcast:transcript> 字幕檔的逾時（大小上限是 netguard.LimitFeed）。
const transcriptFetchTimeout = 30 * time.Second

// msgPodcastNoSnapshot 是影音還沒有 metadata 快照時的訊息：transcript 只讀快照，不會重新抓 feed。
const msgPodcastNoSnapshot = "請從 metadata 重新處理。"

// podcastMeta 是 Podcast 單集寫進 items.metadata_json 的內容（metadata 步驟每次寫完整內容）。
// transcript 與 ASR 步驟只讀這份快照，不重新抓 feed：feed 輪替後仍然可以繼續（重跑 metadata 才會發現集數消失）。
type podcastMeta struct {
	Description string        `json:"description"`
	Channel     string        `json:"channel"`
	Podcast     podcastInfo   `json:"podcast"`
	Audio       *episodeAudio `json:"audio,omitempty"`
}

type podcastInfo struct {
	GUID            string              `json:"guid"`
	FeedURL         string              `json:"feed_url"`
	FeedLanguage    string              `json:"feed_language"`
	Link            string              `json:"link"`
	ItunesDurationS *float64            `json:"itunes_duration_s,omitempty"`
	Enclosure       podcastEnclosure    `json:"enclosure"`
	Transcripts     []podcastTranscript `json:"transcripts"`
}

type podcastEnclosure struct {
	URL    string `json:"url"`
	Type   string `json:"type"`
	Length int64  `json:"length"`
}

// podcastTranscript 是 <podcast:transcript> 的快照；language 是屬性的原始值。
type podcastTranscript struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Rel      string `json:"rel"`
}

// readPodcastMeta 讀出 metadata 快照；還沒有 metadata（內容是 {}）或沒有音檔時 ok 為 false。
func readPodcastMeta(raw json.RawMessage) (podcastMeta, bool) {
	var m podcastMeta
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil || m.Audio == nil || m.Podcast.GUID == "" {
		return podcastMeta{}, false
	}
	return m, true
}

// podcastTracks 把 feed 中的 <podcast:transcript> 轉成字幕軌：語言是 language 屬性，省略時用 feed 的語言；
// 只收 vtt、srt、json（html、plain 等忽略）。沒有宣告 type 而且看不出格式的，留到下載後看內容判斷（Format 為空）。
func podcastTracks(ts []podcastTranscript, feedLang string) []transcript.Track {
	var out []transcript.Track
	for _, t := range ts {
		pt := podcast.Transcript{URL: t.URL, Type: t.Type}
		if !usableTranscript(pt) {
			continue
		}
		format := podcast.TranscriptFormat(pt)
		out = append(out, transcript.Track{
			Lang: podcast.Lang(cmp.Or(t.Language, feedLang)), Kind: transcript.KindPodcast, Handle: t.URL, Format: format,
		})
	}
	return out
}

// usableTranscript 回報 <podcast:transcript> 是不是我們可能讀得懂的字幕：宣告或副檔名是 vtt、srt、json，
// 或沒有宣告 type、要下載後看內容判斷的。宣告了其他 type（html、plain 等）的不採用。
// 辨識結果的 has_transcript 與 transcript 步驟的選軌共用它。
func usableTranscript(t podcast.Transcript) bool {
	return podcast.TranscriptFormat(t) != "" || strings.TrimSpace(t.Type) == ""
}

// formatSniffer 由下載後才判斷字幕格式的平台實作（Podcast）：回傳實際用來解析的格式，
// 讓來源紀錄的 timing 依實際的格式記錄（沒有宣告 type 的 JSON 字幕也能記成 word 或 segment）。
type formatSniffer interface {
	fetchCuesWithFormat(ctx context.Context, t transcript.Track) ([]subtitle.Cue, string, error)
}

// podcastAdapter 是 Podcast 單集的平台功能：只讀 metadata 快照，字幕檔經對外 client 下載。
type podcastAdapter struct{ http *http.Client }

func (a podcastAdapter) probe(_ context.Context, it store.Item) (*probed, error) {
	m, ok := readPodcastMeta(it.Metadata)
	if !ok {
		return nil, podcastPermanent(msgPodcastNoSnapshot, nil)
	}
	return &probed{
		Title:          it.Title,
		Language:       podcast.Lang(m.Podcast.FeedLanguage),
		Description:    m.Description,
		Tracks:         podcastTracks(m.Podcast.Transcripts, m.Podcast.FeedLanguage),
		ContentSHA256:  m.Audio.SHA256,
		AudioDurationS: m.Audio.DurationS,
	}, nil
}

func (a podcastAdapter) fetchCues(ctx context.Context, _ store.Item, t transcript.Track, _ string) ([]subtitle.Cue, error) {
	cues, _, err := a.fetchCuesWithFormat(ctx, t)
	return cues, err
}

// fetchCuesWithFormat 下載並解析字幕，回傳實際用來解析的格式（沒有宣告時看內容判斷）。
func (a podcastAdapter) fetchCuesWithFormat(ctx context.Context, t transcript.Track) ([]subtitle.Cue, string, error) {
	data, err := a.get(ctx, t.Handle)
	if err != nil {
		return nil, "", err
	}
	format := t.Format
	if format == "" {
		format = sniffTranscript(data)
	}
	var cues []subtitle.Cue
	switch format {
	case "vtt":
		cues, err = subtitle.ParseVTT(data)
	case "json":
		cues, _, err = subtitle.ParsePodcastJSON(data)
	default:
		format = "srt"
		cues, err = subtitle.ParseSRT(data)
	}
	if err != nil {
		// 字幕檔的格式不對，重試也不會變好。
		return nil, "", podcastPermanent("字幕檔無法解析。", err)
	}
	return cues, format, nil
}

func (a podcastAdapter) toolVersions(context.Context) (map[string]string, error) { return nil, nil }

// get 下載字幕檔：只接受 2xx，大小上限 netguard.LimitFeed。4xx（429 與 408 除外）是 permanent；
// 429、5xx 與連線錯誤可以重試；被防護擋下的錯誤原樣傳出（呼叫端用 netguard.PolicyMessage 判斷）。
func (a podcastAdapter) get(ctx context.Context, rawURL string) ([]byte, error) {
	if a.http == nil {
		return nil, podcastPermanent("這個伺服器沒有設定對外連線。", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, transcriptFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, podcastPermanent("字幕網址不正確。", err)
	}
	req.Header.Set("Accept", "text/vtt, application/x-subrip, application/json;q=0.9, text/plain;q=0.5, */*;q=0.1")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下載字幕：%w", err)
	}
	defer resp.Body.Close()
	if err := statusError(resp.StatusCode, "字幕"); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		return nil, fmt.Errorf("下載字幕：%w", err)
	}
	return data, nil
}

// statusError 把非 2xx 的狀態對應成錯誤：429、408 與 5xx 可以重試，其他 4xx 與 3xx 是 permanent；2xx 回傳 nil。
func statusError(code int, what string) error {
	switch {
	case code >= 200 && code <= 299:
		return nil
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500:
		return &errclass.Error{Class: errclass.ClassRetryable, Source: "podcast", Detail: fmt.Sprintf("%s HTTP %d", what, code)}
	}
	return &errclass.Error{
		Class: errclass.ClassPermanent, Source: "podcast", Detail: fmt.Sprintf("%s HTTP %d", what, code),
		Message: fmt.Sprintf("%s下載失敗（HTTP %d）。", what, code),
	}
}

// podcastPermanent 建立 permanent 錯誤：重試也不會成功，msg 是給使用者看的訊息。
func podcastPermanent(msg string, err error) error {
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "podcast", Message: msg, Err: err}
}

// sniffTranscript 在沒有宣告格式時，看內容判斷：WEBVTT 開頭是 vtt，{ 開頭是 json，其他當 srt。
func sniffTranscript(data []byte) string {
	b := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	b = bytes.TrimLeft(b, " \t\r\n")
	switch {
	case bytes.HasPrefix(b, []byte("WEBVTT")):
		return "vtt"
	case bytes.HasPrefix(b, []byte("{")):
		return "json"
	}
	return "srt"
}
