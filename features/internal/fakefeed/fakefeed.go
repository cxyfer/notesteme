// Package fakefeed 是驗收測試用的假訂閱來源（SUB，M4）。
//
// Server 取代 YouTube 頻道的 RSS（Atom）：
//
//	GET /feeds/videos.xml?channel_id=<UC…>
//
// 依登記的影片產生 Atom feed（依發布時間由新到舊、最多 15 筆，同真實的 YouTube）；
// 每筆有 yt:videoId、yt:channelId、title、published、link，Shorts 的 link 是 /shorts/<ID>。
// 真實的 RSS 沒有片長與直播狀態，這裡也不輸出。
//
// 另外提供產生 RSS 2.0、OPML、超大回應、畸形 XML 與含 <!DOCTYPE> 的 XML 的函式，
// 讓場景與單元測試驗證 SUB-R7、SUB-R11 的上限與清理。SetRaw 讓任意路徑回應固定內容。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 由 features/steps 負責（M4 的 SUB 實作切片）。
package fakefeed

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Host 是 YouTube RSS 的主機名稱。
const Host = "www.youtube.com"

// FeedPath 是 YouTube RSS 的路徑。
const FeedPath = "/feeds/videos.xml"

// MaxAtomEntries 是 YouTube RSS 最多列出的影片數。
const MaxAtomEntries = 15

// DefaultPublished 是沒有指定發布時間的影片在 feed 中的發布時間。
var DefaultPublished = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Video 是一部登記的影片。
type Video struct {
	ID          string // 11 字元的影片 ID
	ChannelID   string
	Channel     string // 頻道名稱（feed 的 title）
	Title       string
	PublishedAt time.Time // 零值視為 DefaultPublished
	Shorts      bool
}

func (v Video) published() time.Time {
	if v.PublishedAt.IsZero() {
		return DefaultPublished
	}
	return v.PublishedAt
}

// Link 是影片在 feed 中的網址。
func (v Video) Link() string {
	if v.Shorts {
		return "https://www.youtube.com/shorts/" + v.ID
	}
	return "https://www.youtube.com/watch?v=" + v.ID
}

type raw struct {
	contentType string
	body        []byte
}

// Server 是假 YouTube RSS 的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	mu       sync.Mutex
	videos   map[string]Video // ID → 影片
	fail     map[string]int   // channelID → HTTP 狀態碼
	raws     map[string]raw   // 路徑（含 query）→ 固定內容
	requests []string         // 每個 feed 請求的 channel_id（固定內容的請求記為 "raw:<路徑>"）
}

// New 建立沒有任何影片的假 feed。
func New() *Server {
	return &Server{videos: map[string]Video{}, fail: map[string]int{}, raws: map[string]raw{}}
}

// Add 登記一部影片（ID 相同時取代）。
func (s *Server) Add(v Video) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videos[v.ID] = v
}

// SetVideos 以 vs 取代全部影片（harness 在每次請求時從假 yt-dlp 的影片同步）。
func (s *Server) SetVideos(vs []Video) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videos = make(map[string]Video, len(vs))
	for _, v := range vs {
		s.videos[v.ID] = v
	}
}

// Fail 讓這個頻道的 feed 請求都回應 status，直到 Recover。
func (s *Server) Fail(channelID string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[channelID] = status
}

// Recover 取消 Fail。
func (s *Server) Recover(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fail, channelID)
}

// SetRaw 讓 pathAndQuery（例如 "/big.xml"）回應固定內容。
func (s *Server) SetRaw(pathAndQuery, contentType string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raws[pathAndQuery] = raw{contentType: contentType, body: slices.Clone(body)}
}

// Requests 回傳 channel_id 等於 channelID 的 feed 請求數（含失敗的回應）。
func (s *Server) Requests(channelID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.requests {
		if c == channelID {
			n++
		}
	}
	return n
}

// channelVideos 回傳頻道的影片，依發布時間由新到舊（同時間依 ID）。
func (s *Server) channelVideos(channelID string) []Video {
	var out []Video
	for _, v := range s.videos {
		if v.ChannelID == channelID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i].published(), out[j].published()
		if !pi.Equal(pj) {
			return pi.After(pj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if rw, ok := s.raws[r.URL.RequestURI()]; ok {
		s.requests = append(s.requests, "raw:"+r.URL.RequestURI())
		s.mu.Unlock()
		w.Header().Set("Content-Type", rw.contentType)
		_, _ = w.Write(rw.body)
		return
	}
	if r.URL.Path != FeedPath {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	channelID := r.URL.Query().Get("channel_id")
	s.requests = append(s.requests, channelID)
	if r.Method != http.MethodGet {
		s.mu.Unlock()
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if channelID == "" {
		s.mu.Unlock()
		http.Error(w, "missing channel_id", http.StatusBadRequest)
		return
	}
	if code, ok := s.fail[channelID]; ok {
		s.mu.Unlock()
		http.Error(w, http.StatusText(code), code)
		return
	}
	videos := s.channelVideos(channelID)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/atom+xml; charset=UTF-8")
	_, _ = w.Write(Atom(channelID, videos))
}

// Atom 產生 YouTube 頻道 RSS 的 Atom 內容；videos 依給定順序，最多 MaxAtomEntries 筆。
func Atom(channelID string, videos []Video) []byte {
	title := channelID
	if len(videos) > 0 && videos[0].Channel != "" {
		title = videos[0].Channel
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">` + "\n")
	fmt.Fprintf(&b, "  <id>yt:channel:%s</id>\n", esc(channelID))
	fmt.Fprintf(&b, "  <yt:channelId>%s</yt:channelId>\n", esc(channelID))
	fmt.Fprintf(&b, "  <title>%s</title>\n", esc(title))
	fmt.Fprintf(&b, "  <link rel=\"alternate\" href=\"https://www.youtube.com/channel/%s\"/>\n", esc(channelID))
	for i, v := range videos {
		if i >= MaxAtomEntries {
			break
		}
		pub := v.published().UTC().Format(time.RFC3339)
		b.WriteString("  <entry>\n")
		fmt.Fprintf(&b, "    <id>yt:video:%s</id>\n", esc(v.ID))
		fmt.Fprintf(&b, "    <yt:videoId>%s</yt:videoId>\n", esc(v.ID))
		fmt.Fprintf(&b, "    <yt:channelId>%s</yt:channelId>\n", esc(channelID))
		fmt.Fprintf(&b, "    <title>%s</title>\n", esc(v.Title))
		fmt.Fprintf(&b, "    <link rel=\"alternate\" href=\"%s\"/>\n", esc(v.Link()))
		fmt.Fprintf(&b, "    <published>%s</published>\n", pub)
		fmt.Fprintf(&b, "    <updated>%s</updated>\n", pub)
		b.WriteString("  </entry>\n")
	}
	b.WriteString("</feed>\n")
	return b.Bytes()
}

// Item 是 RSS 2.0 的一集。
type Item struct {
	GUID      string
	Title     string // 照字面寫入（會跳脫）；要放進未跳脫的內容用 TitleXML
	TitleXML  string // 非空時直接寫入 <title> 內（不跳脫），用來產生含 entity 或標籤的標題
	Enclosure string
	PubDate   time.Time
}

// RSS 產生 RSS 2.0 的 Podcast feed。
func RSS(title string, items []Item) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<rss version=\"2.0\">\n  <channel>\n")
	fmt.Fprintf(&b, "    <title>%s</title>\n", esc(title))
	b.WriteString("    <language>en</language>\n")
	for _, it := range items {
		b.WriteString("    <item>\n")
		fmt.Fprintf(&b, "      <guid>%s</guid>\n", esc(it.GUID))
		if it.TitleXML != "" {
			fmt.Fprintf(&b, "      <title>%s</title>\n", it.TitleXML)
		} else {
			fmt.Fprintf(&b, "      <title>%s</title>\n", esc(it.Title))
		}
		if !it.PubDate.IsZero() {
			fmt.Fprintf(&b, "      <pubDate>%s</pubDate>\n", it.PubDate.UTC().Format(http.TimeFormat))
		}
		if it.Enclosure != "" {
			fmt.Fprintf(&b, "      <enclosure url=\"%s\" type=\"audio/mpeg\" length=\"1000\"/>\n", esc(it.Enclosure))
		}
		b.WriteString("    </item>\n")
	}
	b.WriteString("  </channel>\n</rss>\n")
	return b.Bytes()
}

// Outline 是 OPML 的一個訂閱。
type Outline struct {
	Text   string
	XMLURL string
}

// OPML 產生 OPML 2.0。
func OPML(title string, outlines []Outline) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<opml version=\"2.0\">\n")
	fmt.Fprintf(&b, "  <head><title>%s</title></head>\n  <body>\n", esc(title))
	for _, o := range outlines {
		fmt.Fprintf(&b, "    <outline type=\"rss\" text=\"%s\" xmlUrl=\"%s\"/>\n", esc(o.Text), esc(o.XMLURL))
	}
	b.WriteString("  </body>\n</opml>\n")
	return b.Bytes()
}

// GeneratedOPML 產生含 n 個 outline 的 OPML（`When the owner imports a generated OPML file with … feeds`）：
// 網址為 https://feeds.example.com/gen-<i>.xml，每個 text 約 100 個字元。
func GeneratedOPML(n int) []byte {
	outlines := make([]Outline, n)
	pad := strings.Repeat("x", 80)
	for i := range outlines {
		outlines[i] = Outline{Text: fmt.Sprintf("Generated show %05d %s", i+1, pad), XMLURL: fmt.Sprintf("https://feeds.example.com/gen-%d.xml", i+1)}
	}
	return OPML("Generated", outlines)
}

// Oversized 產生至少 size 位元組、開頭是合法 RSS 的內容（之後以 XML 註解填滿），
// 讓「超過大小上限」不會被「格式錯誤」搶先判斷。
func Oversized(size int) []byte {
	head := RSS("Oversized", []Item{{GUID: "ep-1", Title: "Episode 1", Enclosure: "https://media.example.com/oversized/ep41.mp3"}})
	head = bytes.TrimSuffix(head, []byte("  </channel>\n</rss>\n"))
	var b bytes.Buffer
	b.Write(head)
	chunk := "    <!-- " + strings.Repeat("p", 1000) + " -->\n"
	for b.Len() < size {
		b.WriteString(chunk)
	}
	b.WriteString("  </channel>\n</rss>\n")
	return b.Bytes()
}

// Malformed 產生沒有關閉標籤的 RSS；marker 放在標題中，用來驗證內容沒有寫進 log。
func Malformed(marker string) []byte {
	return []byte(xml.Header + "<rss version=\"2.0\">\n  <channel>\n    <title>" + esc(marker) + "\n    <item><guid>ep-1</guid><title>Never closed</title>\n  </channel>\n")
}

// WithDoctype 產生含內部 DTD 與巢狀 entity 的 RSS（billion laughs 的縮小版）；
// marker 只出現在 entity 的定義中，展開後才會出現在標題。
func WithDoctype(marker string) []byte {
	return []byte(xml.Header +
		"<!DOCTYPE rss [\n" +
		"  <!ENTITY m \"" + esc(marker) + "\">\n" +
		"  <!ENTITY m2 \"&m;&m;&m;&m;&m;&m;&m;&m;&m;&m;\">\n" +
		"]>\n" +
		"<rss version=\"2.0\">\n  <channel>\n    <title>&m2;</title>\n" +
		"    <item><guid>ep-1</guid><title>&m2;</title></item>\n" +
		"  </channel>\n</rss>\n")
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
