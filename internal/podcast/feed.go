// Package podcast 解析 Podcast 的 RSS 與 Atom feed，並提供單集識別的純邏輯（PODCAST 切片）。
//
// 這個套件只依賴 netguard 的大小上限；對外請求由呼叫端傳入的 *http.Client 發出，
// production 的 client 一律經過 netguard（ADR-0014）。
package podcast

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
)

// FetchTimeout 是抓取 feed 的逾時；內容大小用 netguard.LimitFeed（GRD-R5）。
const FetchTimeout = 15 * time.Second

// 辨識 feed 元素用的 namespace URI 與常見的前綴。
const (
	nsItunes  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	nsPodcast = "https://podcastindex.org/namespace/1.0"
	xmlURL    = "http://www.w3.org/XML/1998/namespace"
)

// maxXMLDepth 是 XML 元素巢狀的上限；超過就停止解析。真實的 feed 只有個位數層，
// 但 encoding/xml 每一層都會配置記憶體，惡意的深層巢狀會讓記憶體用量遠大於內容大小。
const maxXMLDepth = 64

// ErrNotFeed 表示內容不是 RSS 2.0 或 Atom（含巢狀超過 maxXMLDepth 層的 XML）。
var ErrNotFeed = errors.New("內容不是 RSS 或 Atom")

// HTTPError 表示抓取 feed 時伺服器回了非 2xx 的狀態。
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return "HTTP " + strconv.Itoa(e.StatusCode) + " " + http.StatusText(e.StatusCode)
}

// Feed 是解析後的 feed。
type Feed struct {
	Title    string
	Language string // <language>（Atom 是 xml:lang），沒有時為空字串；未正規化，見 Lang
	Link     string
	Episodes []Episode
}

// Episode 是 feed 中的一集。
type Episode struct {
	GUID        string
	Title       string
	Link        string
	Description string
	PublishedAt *time.Time // 一律是 UTC；沒有或格式不認得時為 nil
	DurationS   *float64   // itunes:duration；沒有或格式不認得時為 nil
	Enclosure   *Enclosure // 沒有音檔時為 nil
	Transcripts []Transcript
}

// Enclosure 是一集的音檔。Length 是 feed 宣稱的位元組數，沒有或不合法時為 0。
type Enclosure struct {
	URL, Type string
	Length    int64
}

// Transcript 是 <podcast:transcript>。Language 是屬性的原始值（去前後空白），省略時為空字串，
// 要不要改用 feed 的語言由使用的人決定；Rel 為 "captions" 代表有時間碼。
type Transcript struct{ URL, Type, Language, Rel string }

// Find 以 guid 找一集；guid 去前後空白後完全相同才算。
func (f *Feed) Find(guid string) (*Episode, bool) {
	guid = strings.TrimSpace(guid)
	if guid == "" {
		return nil, false
	}
	for i := range f.Episodes {
		if f.Episodes[i].GUID == guid {
			return &f.Episodes[i], true
		}
	}
	return nil, false
}

// Fetch 以 c 抓取並解析 feed：GET、只接受 2xx（否則 *HTTPError）、大小上限 netguard.LimitFeed、逾時 FetchTimeout。
// 超過上限時回傳 netguard.ErrTooLarge，不截斷也不接受部分內容（GRD-R5）；內容不是 feed 時回傳 ErrNotFeed。
func Fetch(ctx context.Context, c *http.Client, rawURL string) (*Feed, error) {
	if c == nil {
		return nil, errors.New("podcast: 沒有設定對外 HTTP client")
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		return nil, err
	}
	return Parse(bytes.NewReader(body))
}

// frame 是解析中尚未結束的元素。
type frame struct {
	name  xml.Name
	attrs []xml.Attr
	text  strings.Builder
}

// isNS 回報元素是否屬於某個 namespace：Space 等於 URI，或（宣告缺漏時）等於慣用的前綴。
func isNS(n xml.Name, uri, prefix string) bool {
	return n.Space == uri || strings.EqualFold(n.Space, prefix)
}

func attr(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Name.Local, local) && (a.Name.Space == "" || a.Name.Space == "xml") {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// Parse 解析 RSS 2.0 或 Atom：根元素是 rss，或 namespace 含 atom 的 feed，否則回傳 ErrNotFeed。
// 根元素確認之後只是盡力而為：內容被截斷或格式不完整時，回傳已經讀到的部分（不含沒有結束的那一集）。
// 沒有 guid 的集改用音檔網址，兩者都沒有就略過；guid 重複時第一個勝出。
func Parse(r io.Reader) (*Feed, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = charsetReader
	var root xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, ErrNotFeed
		}
		if se, ok := tok.(xml.StartElement); ok {
			root = se
			break
		}
	}
	atom := false
	switch name := strings.ToLower(root.Name.Local); {
	case name == "rss":
	case name == "feed" && strings.Contains(strings.ToLower(root.Name.Space), "atom"):
		atom = true
	default:
		return nil, ErrNotFeed
	}

	feed := &Feed{}
	if atom {
		for _, a := range root.Attr {
			if a.Name.Local == "lang" && (a.Name.Space == "xml" || a.Name.Space == xmlURL) {
				feed.Language = strings.TrimSpace(a.Value)
			}
		}
	}
	stack := []*frame{{name: root.Name, attrs: root.Attr}}
	var (
		cur      *epDraft
		curFrame *frame
		seen     = map[string]bool{}
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= maxXMLDepth {
				return nil, ErrNotFeed
			}
			f := &frame{name: t.Name, attrs: t.Attr}
			stack = append(stack, f)
			local := strings.ToLower(t.Name.Local)
			if cur == nil && ((!atom && local == "item") || (atom && local == "entry")) {
				cur, curFrame = &epDraft{}, f
			}
		case xml.CharData:
			stack[len(stack)-1].text.Write(t)
		case xml.EndElement:
			if len(stack) <= 1 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			parent := stack[len(stack)-1]
			switch {
			case cur != nil && f == curFrame:
				ep := cur.finish()
				if ep.GUID != "" && !seen[ep.GUID] {
					seen[ep.GUID] = true
					feed.Episodes = append(feed.Episodes, ep)
				}
				cur, curFrame = nil, nil
			case cur != nil && parent == curFrame:
				applyEpisodeField(cur, f, atom)
			case cur == nil && isFeedLevel(stack, atom):
				applyFeedField(feed, f, atom)
			}
		}
	}
	return feed, nil
}

// isFeedLevel 回報剛結束的元素是不是 feed 本身的直接子元素（stack 的最後一個是它的父元素）：
// RSS 是 channel 的子元素，Atom 是根元素 feed 的子元素。
func isFeedLevel(stack []*frame, atom bool) bool {
	if atom {
		return len(stack) == 1
	}
	return len(stack) == 2 && strings.EqualFold(stack[1].name.Local, "channel")
}

func applyFeedField(feed *Feed, f *frame, atom bool) {
	text := strings.TrimSpace(f.text.String())
	switch strings.ToLower(f.name.Local) {
	case "title":
		if feed.Title == "" && isStdNS(f.name) {
			feed.Title = text
		}
	case "language":
		if feed.Language == "" {
			feed.Language = text
		}
	case "link":
		if feed.Link != "" {
			return
		}
		if atom {
			if rel := attr(f.attrs, "rel"); rel == "" || rel == "alternate" {
				feed.Link = attr(f.attrs, "href")
			}
		} else if !strings.Contains(strings.ToLower(f.name.Space), "atom") {
			feed.Link = text
		}
	}
}

// epDraft 是解析中的一集：除了最後的 Episode，還記下需要「全部讀完再決定」的欄位。
type epDraft struct {
	Episode
	itunesSummary string     // 沒有 description 時的後備
	updated       *time.Time // Atom 的 updated，沒有 published 時的後備
}

// finish 套用後備欄位，回傳完成的 Episode。
func (d *epDraft) finish() Episode {
	ep := d.Episode
	if ep.GUID == "" && ep.Enclosure != nil {
		ep.GUID = ep.Enclosure.URL
	}
	if ep.Description == "" {
		ep.Description = d.itunesSummary
	}
	if ep.PublishedAt == nil {
		ep.PublishedAt = d.updated
	}
	return ep
}

// isStdNS 回報元素是否沒有 namespace，或屬於 RSS、Atom：標題與描述只收這些，
// 避免 media:title、dc:title 之類排在前面時取代原本的欄位。
func isStdNS(n xml.Name) bool {
	space := strings.ToLower(strings.TrimSpace(n.Space))
	switch space {
	case "", "http://backend.userland.com/rss2", "http://purl.org/rss/1.0/": // 注意 Media RSS 的 URI 也含 "rss"，不能用子字串比對
		return true
	}
	return strings.Contains(space, "atom")
}

func applyEpisodeField(d *epDraft, f *frame, atom bool) {
	ep := &d.Episode
	text := strings.TrimSpace(f.text.String())
	local := strings.ToLower(f.name.Local)
	switch {
	case isNS(f.name, nsItunes, "itunes"):
		// itunes:title 等不取；只讀長度，並讓 itunes:summary 當描述的後備。
		switch local {
		case "duration":
			if ep.DurationS == nil {
				if v, ok := ParseDuration(text); ok {
					ep.DurationS = &v
				}
			}
		case "summary":
			if d.itunesSummary == "" {
				d.itunesSummary = text
			}
		}
		return
	case isNS(f.name, nsPodcast, "podcast"):
		if local == "transcript" {
			if u := attr(f.attrs, "url"); u != "" {
				ep.Transcripts = append(ep.Transcripts, Transcript{
					URL: u, Type: attr(f.attrs, "type"), Language: attr(f.attrs, "language"), Rel: attr(f.attrs, "rel"),
				})
			}
		}
		return
	}
	switch local {
	case "guid", "id":
		if ep.GUID == "" && ((!atom && local == "guid") || (atom && local == "id")) {
			ep.GUID = text
		}
	case "title":
		if ep.Title == "" && isStdNS(f.name) {
			ep.Title = text
		}
	case "description", "summary", "content":
		if ep.Description == "" && isStdNS(f.name) {
			ep.Description = text
		}
	case "pubdate", "published":
		if ep.PublishedAt == nil {
			ep.PublishedAt = parseTime(text)
		}
	case "updated":
		if d.updated == nil {
			d.updated = parseTime(text)
		}
	case "link":
		if atom {
			switch rel := attr(f.attrs, "rel"); rel {
			case "enclosure":
				setEnclosure(ep, f)
			case "", "alternate":
				if ep.Link == "" {
					ep.Link = attr(f.attrs, "href")
				}
			}
		} else if ep.Link == "" && !strings.Contains(strings.ToLower(f.name.Space), "atom") {
			ep.Link = text
		}
	case "enclosure":
		if !atom {
			setEnclosure(ep, f)
		}
	}
}

// setEnclosure 記錄第一個有網址的音檔；網址取自 url（RSS）或 href（Atom）。
func setEnclosure(ep *Episode, f *frame) {
	if ep.Enclosure != nil {
		return
	}
	u := attr(f.attrs, "url")
	if u == "" {
		u = attr(f.attrs, "href")
	}
	if u == "" {
		return
	}
	n, err := strconv.ParseInt(attr(f.attrs, "length"), 10, 64)
	if err != nil || n < 0 {
		n = 0
	}
	ep.Enclosure = &Enclosure{URL: u, Type: attr(f.attrs, "type"), Length: n}
}

// 發佈時間的格式：RFC 5322 的常見變體（含日期只有一位數的寫法），以及 Atom 用的 RFC 3339。
var timeLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	time.RFC3339,
}

func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// charsetReader 處理 XML 宣告的編碼：UTF-8 與 ASCII 直接使用，ISO-8859-1 轉成 UTF-8；其他編碼不支援。
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		rs := make([]rune, len(b))
		for i, c := range b {
			rs[i] = rune(c)
		}
		return strings.NewReader(string(rs)), nil
	}
	return nil, fmt.Errorf("podcast: 不支援的編碼 %q", label)
}
