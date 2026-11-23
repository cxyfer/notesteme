// Package fakepodcast 是驗收測試用的假 Podcast（PODCAST 切片）：feed、音檔與字幕的 fake origin。
//
//   - feed 在 https://feeds.example.com/<名稱>.xml，每次請求都依當時的狀態產生 RSS
//     （宣告 itunes 與 podcast namespace、語言 en、enclosure 的實際長度、itunes:duration）。
//   - 音檔與字幕在 https://media.example.com/<名稱>/<檔名>（音檔也可以放在完整網址，由步驟登記）。
//     音檔是假媒體檔（faketool 的 marker）：回應時把 marker 的 item 改成這一集的代號，
//     其餘的 padding 原樣保留，支援 HEAD 與 Range。
//   - 字幕被請求的次數可以查詢（TranscriptCalls），供 `the source … was called … times` 使用。
//
// 這個套件只提供 handler，登記到 fakenet 由 features/steps 負責。
package fakepodcast

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// 預設的主機名稱。
const (
	FeedHost  = "feeds.example.com"
	MediaHost = "media.example.com"
)

// 產品用來辨識 feed namespace 的 URI。
const (
	nsItunes  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	nsPodcast = "https://podcastindex.org/namespace/1.0"
)

// pubDate 是每一集固定的發佈時間（2025-10-01 是星期三）。
const pubDate = "Wed, 01 Oct 2025 08:00:00 +0000"

// FeedURL 是 feed 的網址：https://feeds.example.com/<名稱>.xml。
func FeedURL(name string) string { return "https://" + FeedHost + "/" + name + ".xml" }

// MediaURL 是 feed 底下音檔或字幕的網址：https://media.example.com/<名稱>/<檔名>。
func MediaURL(name, file string) string { return "https://" + MediaHost + "/" + name + "/" + file }

// Ref 是一集的影音代號：pod:<feed>/<guid>。
func Ref(feed, guid string) string { return "pod:" + feed + "/" + guid }

// SplitRef 拆開影音代號；guid 可以含 /，以第一個 / 切開。
func SplitRef(ref string) (feed, guid string, ok bool) {
	rest, found := strings.CutPrefix(ref, "pod:")
	if !found {
		return "", "", false
	}
	feed, guid, found = strings.Cut(rest, "/")
	if !found || feed == "" || guid == "" {
		return "", "", false
	}
	return feed, guid, true
}

// ContentType 依副檔名（忽略 query）回傳 Content-Type；不認識的副檔名回 application/octet-stream。
func ContentType(name string) string {
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".mp4":
		return "video/mp4"
	case ".vtt":
		return "text/vtt"
	case ".srt":
		return "application/x-subrip"
	case ".json":
		return "application/json"
	}
	return "application/octet-stream"
}

// Transcript 是一集的字幕（<podcast:transcript>）。
type Transcript struct {
	URL  string
	Type string
	Body []byte
}

// Episode 是 feed 中的一集。
type Episode struct {
	GUID  string
	Title string
	// AudioURL 是 enclosure 的網址。
	AudioURL string
	// Audio 是假媒體檔的原始內容（第一行是 marker）。
	Audio []byte
	// Listed 是 feed 標示的長度（秒，itunes:duration）；0 代表沿用 marker 的 duration。
	// Put 時為 0 會讀 marker 的 listed= 欄位。
	Listed float64
	// Transcript 為 nil 代表 feed 沒有字幕標籤。
	Transcript *Transcript
}

// Feed 是一個 Podcast feed。
type Feed struct {
	Name     string
	Episodes []Episode
}

// Feeds 保存場景中所有的 feed，並行安全。零值不可用，請用 New。
type Feeds struct {
	mu              sync.Mutex
	feeds           map[string]*Feed
	transcriptCalls int
}

// New 建立空的 Feeds。
func New() *Feeds { return &Feeds{feeds: map[string]*Feed{}} }

// ListedFromMarker 讀出假媒體檔 marker 行的 listed=<秒>（feed 標示的長度）；沒有或不合法時回 0。
func ListedFromMarker(b []byte) float64 {
	line := string(b)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "listed="); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n < 0 {
				return 0
			}
			return n
		}
	}
	return 0
}

// Put 登記（或取代）feed。名稱不可為空、guid 不可重複或為空、音檔必須是假媒體檔。
// 登記時複製集數；Listed 為 0 的集改讀 marker 的 listed=。
func (f *Feeds) Put(feed Feed) error {
	if feed.Name == "" {
		return fmt.Errorf("fakepodcast: feed 名稱不可為空")
	}
	seen := map[string]bool{}
	eps := make([]Episode, len(feed.Episodes))
	for i, ep := range feed.Episodes {
		if ep.GUID == "" {
			return fmt.Errorf("fakepodcast: feed %q 的第 %d 集沒有 guid", feed.Name, i+1)
		}
		if seen[ep.GUID] {
			return fmt.Errorf("fakepodcast: feed %q 的 guid %q 重複", feed.Name, ep.GUID)
		}
		seen[ep.GUID] = true
		if _, ok := faketool.ParseMedia(ep.Audio); !ok {
			return fmt.Errorf("fakepodcast: %s 的音檔不是假媒體檔（第一行要是 NOTESTEME-FAKE-MEDIA 標記）", Ref(feed.Name, ep.GUID))
		}
		if ep.Listed == 0 {
			ep.Listed = ListedFromMarker(ep.Audio)
		}
		if ep.Transcript != nil {
			t := *ep.Transcript
			ep.Transcript = &t
		}
		eps[i] = ep
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[feed.Name] = &Feed{Name: feed.Name, Episodes: eps}
	return nil
}

// find 找到一集；呼叫時需持有 f.mu。
func (f *Feeds) find(ref string) (*Episode, bool) {
	name, guid, ok := SplitRef(ref)
	if !ok {
		return nil, false
	}
	feed := f.feeds[name]
	if feed == nil {
		return nil, false
	}
	for i := range feed.Episodes {
		if feed.Episodes[i].GUID == guid {
			return &feed.Episodes[i], true
		}
	}
	return nil, false
}

// Episode 回傳一集的複本；feed 或集不存在時 ok 為 false。
func (f *Feeds) Episode(ref string) (Episode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ep, ok := f.find(ref)
	if !ok {
		return Episode{}, false
	}
	out := *ep
	if out.Transcript != nil {
		t := *out.Transcript
		out.Transcript = &t
	}
	return out, true
}

// HasTranscript 回報這一集的 feed 是否帶有字幕標籤。
func (f *Feeds) HasTranscript(ref string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	ep, ok := f.find(ref)
	return ok && ep.Transcript != nil
}

// SetTranscript 設定（t 為 nil 時移除）這一集的字幕。集不存在時失敗；
// 同一個 feed 的另一集已經使用相同的字幕網址時也失敗（同一網址只能對應一集）。
func (f *Feeds) SetTranscript(ref string, t *Transcript) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ep, ok := f.find(ref)
	if !ok {
		return fmt.Errorf("fakepodcast: 找不到 %s", ref)
	}
	if t != nil {
		name, _, _ := SplitRef(ref)
		for _, other := range f.feeds[name].Episodes {
			if other.GUID != ep.GUID && other.Transcript != nil && other.Transcript.URL == t.URL {
				return fmt.Errorf("fakepodcast: 字幕網址 %s 已經是 %s 的字幕", t.URL, Ref(name, other.GUID))
			}
		}
		c := *t
		t = &c
	}
	ep.Transcript = t
	return nil
}

// TranscriptCalls 回傳字幕網址被請求的總次數（含 HEAD）。
func (f *Feeds) TranscriptCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transcriptCalls
}

// audioBytes 回傳實際提供的音檔內容：marker 的 item 改成這一集的代號，其餘的 padding 原樣保留。
// 不是 marker 時原樣回傳。
func audioBytes(ref string, audio []byte) []byte {
	m, ok := faketool.ParseMedia(audio)
	if !ok {
		return audio
	}
	m.Item = ref
	out := []byte(faketool.FormatMedia(m))
	if i := bytes.IndexByte(audio, '\n'); i >= 0 {
		out = append(out, audio[i+1:]...)
	}
	return out
}

// Render 依 feed 目前的狀態產生 RSS。enclosure 的 length 是實際提供的音檔位元組數；
// itunes:duration 有 Listed 時用它，否則用 marker 的 duration；有字幕時加上 <podcast:transcript>。
func Render(feed Feed) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var sb bytes.Buffer
		_ = xml.EscapeText(&sb, []byte(s))
		return sb.String()
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(&b, `<rss version="2.0" xmlns:itunes="%s" xmlns:podcast="%s">`+"\n", nsItunes, nsPodcast)
	b.WriteString("<channel>\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(feed.Name))
	fmt.Fprintf(&b, "<link>%s</link>\n", esc(FeedURL(feed.Name)))
	fmt.Fprintf(&b, "<description>%s</description>\n", esc("Fake podcast "+feed.Name))
	b.WriteString("<language>en</language>\n")
	for _, ep := range feed.Episodes {
		ref := Ref(feed.Name, ep.GUID)
		b.WriteString("<item>\n")
		fmt.Fprintf(&b, `<guid isPermaLink="false">%s</guid>`+"\n", esc(ep.GUID))
		fmt.Fprintf(&b, "<title>%s</title>\n", esc(ep.Title))
		fmt.Fprintf(&b, "<pubDate>%s</pubDate>\n", pubDate)
		fmt.Fprintf(&b, `<enclosure url="%s" type="%s" length="%d"/>`+"\n",
			esc(ep.AudioURL), ContentType(ep.AudioURL), len(audioBytes(ref, ep.Audio)))
		dur := ep.Listed
		if dur == 0 {
			if m, ok := faketool.ParseMedia(ep.Audio); ok {
				dur = m.Duration
			}
		}
		if dur > 0 {
			fmt.Fprintf(&b, "<itunes:duration>%s</itunes:duration>\n", strconv.FormatFloat(dur, 'f', -1, 64))
		}
		if ep.Transcript != nil {
			fmt.Fprintf(&b, `<podcast:transcript url="%s" type="%s"/>`+"\n", esc(ep.Transcript.URL), esc(ep.Transcript.Type))
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel>\n</rss>\n")
	return b.Bytes()
}

// FeedHandler 回傳 feed 的 handler：每次請求都依當時的狀態產生 RSS；feed 不存在回 404。
func (f *Feeds) FeedHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		feed := f.feeds[name]
		var snap Feed
		if feed != nil {
			snap = Feed{Name: feed.Name, Episodes: append([]Episode(nil), feed.Episodes...)}
		}
		f.mu.Unlock()
		if feed == nil {
			http.NotFound(w, r)
			return
		}
		body := Render(snap)
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	})
}

// AudioHandler 回傳一集音檔的 handler：marker 的 item 改成這一集的代號，
// 以 http.ServeContent 回應（支援 HEAD 與 Range）；集不存在回 404。
func (f *Feeds) AudioHandler(ref string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep, ok := f.Episode(ref)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ContentType(ep.AudioURL))
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(audioBytes(ref, ep.Audio)))
	})
}

// TranscriptHandler 回傳一集字幕的 handler，每次請求都計次；沒有字幕回 404。
func (f *Feeds) TranscriptHandler(ref string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.transcriptCalls++
		f.mu.Unlock()
		ep, ok := f.Episode(ref)
		if !ok || ep.Transcript == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ep.Transcript.Type)
		w.Header().Set("Content-Length", strconv.Itoa(len(ep.Transcript.Body)))
		_, _ = w.Write(ep.Transcript.Body)
	})
}

// Prepend 在既有的 feed 最前面加上一集（`the podcast feed … adds the episode …`，SUB）。
// feed 不存在、guid 為空或重複、音檔不是假媒體檔時回傳錯誤。
func (f *Feeds) Prepend(name string, ep Episode) error {
	if ep.GUID == "" {
		return fmt.Errorf("fakepodcast: guid 不可為空")
	}
	if _, ok := faketool.ParseMedia(ep.Audio); !ok {
		return fmt.Errorf("fakepodcast: %s 的音檔不是假媒體檔（第一行要是 NOTESTEME-FAKE-MEDIA 標記）", Ref(name, ep.GUID))
	}
	if ep.Listed == 0 {
		ep.Listed = ListedFromMarker(ep.Audio)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	feed := f.feeds[name]
	if feed == nil {
		return fmt.Errorf("fakepodcast: feed %q 不存在（先用 `a podcast feed … with the episodes:` 建立）", name)
	}
	for _, e := range feed.Episodes {
		if e.GUID == ep.GUID {
			return fmt.Errorf("fakepodcast: feed %q 的 guid %q 重複", name, ep.GUID)
		}
	}
	feed.Episodes = append([]Episode{ep}, feed.Episodes...)
	return nil
}
