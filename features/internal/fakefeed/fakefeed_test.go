package fakefeed

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type atomFeed struct {
	XMLName   xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ChannelID string   `xml:"http://www.youtube.com/xml/schemas/2015 channelId"`
	Title     string   `xml:"title"`
	Entries   []struct {
		VideoID   string `xml:"http://www.youtube.com/xml/schemas/2015 videoId"`
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Link      struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func get(t *testing.T, srv *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAtomListsChannelVideosNewestFirst(t *testing.T) {
	fake := New()
	fake.Add(Video{ID: "old001xxxxx", ChannelID: "UCfixture000000000000000", Channel: "Fixture Channel", Title: "Old", PublishedAt: at("2026-11-19T10:00:00Z")})
	fake.Add(Video{ID: "new001xxxxx", ChannelID: "UCfixture000000000000000", Channel: "Fixture Channel", Title: "New & <shiny>", PublishedAt: at("2026-11-20T10:30:00Z")})
	fake.Add(Video{ID: "short01xxxx", ChannelID: "UCfixture000000000000000", Channel: "Fixture Channel", Title: "Tip", Shorts: true})
	fake.Add(Video{ID: "other01xxxx", ChannelID: "UCother00000000000000000", Title: "Elsewhere"})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	code, body := get(t, srv, FeedPath+"?channel_id=UCfixture000000000000000")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var f atomFeed
	if err := xml.Unmarshal(body, &f); err != nil {
		t.Fatalf("Atom 解析失敗：%v\n%s", err, body)
	}
	if f.ChannelID != "UCfixture000000000000000" || f.Title != "Fixture Channel" {
		t.Errorf("feed = %q %q", f.ChannelID, f.Title)
	}
	var ids []string
	for _, e := range f.Entries {
		ids = append(ids, e.VideoID)
	}
	if got := strings.Join(ids, ","); got != "new001xxxxx,old001xxxxx,short01xxxx" {
		t.Fatalf("順序 = %s（依發布時間由新到舊，沒有時間的視為 2026-01-01）", got)
	}
	if f.Entries[0].Title != "New & <shiny>" || f.Entries[0].Published != "2026-11-20T10:30:00Z" {
		t.Errorf("entry 0 = %+v", f.Entries[0])
	}
	if f.Entries[2].Link.Href != "https://www.youtube.com/shorts/short01xxxx" || f.Entries[2].Published != "2026-01-01T00:00:00Z" {
		t.Errorf("Shorts entry = %+v", f.Entries[2])
	}
	if fake.Requests("UCfixture000000000000000") != 1 || fake.Requests("UCother00000000000000000") != 0 {
		t.Error("請求數不對")
	}
}

func TestAtomKeepsOnlyFifteenEntries(t *testing.T) {
	fake := New()
	base := at("2026-11-01T00:00:00Z")
	for i := range 20 {
		fake.Add(Video{ID: strings.Repeat(string(rune('a'+i)), 11), ChannelID: "UC1", PublishedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	_, body := get(t, srv, FeedPath+"?channel_id=UC1")
	var f atomFeed
	if err := xml.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != MaxAtomEntries || f.Entries[0].VideoID != strings.Repeat("t", 11) {
		t.Errorf("entries = %d, first = %s", len(f.Entries), f.Entries[0].VideoID)
	}
}

func TestFailRecoverAndRaw(t *testing.T) {
	fake := New()
	fake.Fail("UC1", http.StatusTooManyRequests)
	fake.SetRaw("/big.xml", "application/rss+xml", Oversized(2<<20))
	srv := httptest.NewServer(fake)
	defer srv.Close()

	if code, _ := get(t, srv, FeedPath+"?channel_id=UC1"); code != http.StatusTooManyRequests {
		t.Errorf("Fail = %d", code)
	}
	fake.Recover("UC1")
	if code, body := get(t, srv, FeedPath+"?channel_id=UC1"); code != http.StatusOK || !bytes.Contains(body, []byte("<feed")) {
		t.Errorf("Recover = %d", code)
	}
	if fake.Requests("UC1") != 2 {
		t.Errorf("失敗的請求也要計數，got %d", fake.Requests("UC1"))
	}
	code, body := get(t, srv, "/big.xml")
	if code != http.StatusOK || len(body) < 2<<20 {
		t.Errorf("raw = %d, %d bytes", code, len(body))
	}
	if code, _ := get(t, srv, "/feeds/other.xml"); code != http.StatusNotFound {
		t.Errorf("未知路徑 = %d", code)
	}
	if code, _ := get(t, srv, FeedPath); code != http.StatusBadRequest {
		t.Errorf("缺 channel_id = %d", code)
	}
}

func TestGeneratorsProduceWhatTheScenariosNeed(t *testing.T) {
	// RSS 與 OPML 是合法的 XML。
	rss := RSS("Show", []Item{{GUID: "ep-1", Title: "A & B", Enclosure: "https://media.example.com/show/ep41.mp3", PubDate: at("2026-11-20T08:00:00Z")}})
	if err := xml.Unmarshal(rss, new(struct{})); err != nil || !bytes.Contains(rss, []byte("A &amp; B")) || !bytes.Contains(rss, []byte("Fri, 20 Nov 2026 08:00:00 GMT")) {
		t.Errorf("RSS：%v\n%s", err, rss)
	}
	var o struct {
		Outlines []struct {
			URL string `xml:"xmlUrl,attr"`
		} `xml:"body>outline"`
	}
	if err := xml.Unmarshal(GeneratedOPML(501), &o); err != nil || len(o.Outlines) != 501 || o.Outlines[500].URL != "https://feeds.example.com/gen-501.xml" {
		t.Errorf("GeneratedOPML(501)：%v，%d 個", err, len(o.Outlines))
	}
	if n := len(GeneratedOPML(9000)); n <= 1<<20 {
		t.Errorf("9000 個 outline 應該超過 1 MiB，實際 %d", n)
	}
	if n := len(GeneratedOPML(501)); n >= 1<<20 {
		t.Errorf("501 個 outline 應該小於 1 MiB，實際 %d", n)
	}

	// 超大回應開頭是 RSS、整份仍是合法 XML。
	big := Oversized(11 << 20)
	if len(big) < 11<<20 || !bytes.HasPrefix(big, []byte(xml.Header+"<rss")) {
		t.Errorf("Oversized：%d bytes", len(big))
	}
	if err := xml.Unmarshal(big, new(struct{})); err != nil {
		t.Errorf("Oversized 不是合法的 XML：%v", err)
	}

	// 畸形 XML 解析失敗。
	if err := xml.Unmarshal(Malformed("SUB-MALFORMED-MARK"), new(struct{})); err == nil {
		t.Error("Malformed 應該解析失敗")
	}

	// 含 DOCTYPE；marker 只在 entity 定義中出現一次（沒有被展開）。
	dt := WithDoctype("SUB-ENTITY-EXPANDED")
	if !bytes.Contains(dt, []byte("<!DOCTYPE")) || bytes.Count(dt, []byte("SUB-ENTITY-EXPANDED")) != 1 {
		t.Errorf("WithDoctype：\n%s", dt)
	}
	// Go 的 encoding/xml 預設不展開自訂 entity：Strict 模式會回錯誤。
	d := xml.NewDecoder(bytes.NewReader(dt))
	var err error
	for err == nil {
		_, err = d.Token()
	}
	if err == io.EOF {
		t.Error("自訂 entity 應該讓 strict 的 decoder 失敗")
	}
}
