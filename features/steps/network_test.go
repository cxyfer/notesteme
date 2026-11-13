package steps

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// get 以 netguard 的真實檢查路徑對 fakenet 發出請求（不跟隨轉址）。
func get(t *testing.T, s *Scenario, url string) (*http.Response, []byte, error) {
	t.Helper()
	c := &http.Client{
		Transport:     netguard.Transport(s.World.Net.Deps()),
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(url)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, b, err
}

func TestResolvesSteps(t *testing.T) {
	s := newTestScenario(t)
	if err := s.hostResolvesTo("a.example.com", "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if err := s.hostResolvesToThen("rebind.example.com", "93.184.216.34", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.hostResolvesTo("bad.example.com", "not-an-ip"); err == nil {
		t.Fatal("不合法的 IP 應該失敗")
	}
	// rebinding：第一次公開 IP，之後都是內網 IP。
	for i, want := range []string{"93.184.216.34", "192.168.1.1", "192.168.1.1"} {
		got, err := s.World.Net.LookupNetIP(t.Context(), "ip", "rebind.example.com")
		if err != nil || len(got) != 1 || got[0].String() != want {
			t.Fatalf("lookup %d = %v, %v; want %s", i, got, err, want)
		}
	}
}

func TestRedirectAndServesSteps(t *testing.T) {
	s := newTestScenario(t)
	if err := s.urlRedirectsTo("https://feeds.example.com/show.xml", "https://feeds.example.com/real.xml"); err != nil {
		t.Fatal(err)
	}
	if err := s.urlServes("https://feeds.example.com/real.xml", "rsshub-feed.xml"); err != nil {
		t.Fatal(err)
	}
	resp, _, err := get(t, s, "https://feeds.example.com/show.xml")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://feeds.example.com/real.xml" {
		t.Fatalf("%d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body, err := get(t, s, "https://feeds.example.com/real.xml")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("serves: %v %v", resp, err)
	}
	want, err := os.ReadFile(s.World.FixturesDir + "/http/rsshub-feed.xml")
	if err != nil || string(body) != string(want) {
		t.Fatalf("內容與 fixture 不同：%v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	var feed struct {
		Title string `xml:"channel>title"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil || feed.Title == "" {
		t.Fatalf("fixture 不是合法的 RSS：%v", err)
	}
	// 沒有登記的路徑是 404。
	if resp, _, err := get(t, s, "https://feeds.example.com/other.xml"); err != nil || resp.StatusCode != 404 {
		t.Fatalf("未登記的路徑：%v %v", resp, err)
	}
}

func TestServesRejectsBadFixture(t *testing.T) {
	s := newTestScenario(t)
	for _, name := range []string{"no-such-fixture.xml", "../README.md", "", ".hidden", "sub/x.xml"} {
		err := s.urlServes("https://feeds.example.com/x.xml", name)
		if err == nil {
			t.Errorf("fixture %q 應該失敗", name)
		} else if err == godog.ErrPending {
			t.Errorf("fixture %q 不能轉成 pending", name)
		}
	}
}

// YouTube 播放清單的網址由 yt-dlp 讀取，不是 HTTP：serves 把 fixture（ytdlp/）登記到假 yt-dlp 的狀態，
// 不會在 fakenet 上建立路由（steps.md 4.10）。
func TestServesYtDlpPlaylistURL(t *testing.T) {
	s := newTestScenario(t)
	const rawURL = "https://www.youtube.com/playlist?list=PL123"
	if err := s.urlServes(rawURL, "playlist-3-videos.json"); err != nil {
		t.Fatal(err)
	}
	if got := s.World.Tools.Playlists["PL123"]; got != "playlist-3-videos.json" {
		t.Fatalf("Tools.Playlists = %v", s.World.Tools.Playlists)
	}
	st, err := faketool.ReadState(s.World.Root)
	if err != nil || st.Playlists["PL123"] != "playlist-3-videos.json" {
		t.Fatalf("狀態檔沒有同步：%+v, %v", st.Playlists, err)
	}
	// fixture 是 3 部影片、11 字元的 ID。
	b, err := os.ReadFile(filepath.Join(s.World.FixturesDir, "ytdlp", "playlist-3-videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pl struct {
		Entries []struct{ ID string } `json:"entries"`
	}
	if err := json.Unmarshal(b, &pl); err != nil || len(pl.Entries) != 3 {
		t.Fatalf("fixture 應有 3 部影片：%v, %v", pl, err)
	}
	for _, e := range pl.Entries {
		if len(e.ID) != 11 {
			t.Errorf("影片 ID %q 不是 11 個字元", e.ID)
		}
	}
	// 假的 yt-dlp 在 fakenet 上沒有任何連線。
	if err := s.noConnectionTo("www.youtube.com"); err != nil {
		t.Error(err)
	}
}

func TestServesYtDlpURLsOtherThanPlaylistsFail(t *testing.T) {
	s := newTestScenario(t)
	for _, u := range []string{
		"https://www.youtube.com/watch?v=abc123xxxxx", "https://youtu.be/abc123xxxxx",
		"https://www.bilibili.com/video/BV1xx411c7mD", "https://www.youtube.com/@somechannel",
	} {
		err := s.urlServes(u, "playlist-3-videos.json")
		if err == nil || err == godog.ErrPending || !strings.Contains(err.Error(), "yt-dlp") {
			t.Errorf("%s：err = %v，應該明確失敗並說明是 yt-dlp 的網址", u, err)
		}
	}
	// fixture 不存在時立即失敗，不是等到執行時才發現。
	err := s.urlServes("https://www.youtube.com/playlist?list=PL123", "no-such-playlist.json")
	if err == nil || err == godog.ErrPending {
		t.Errorf("不存在的 fixture：err = %v", err)
	}
	if len(s.World.Tools.Playlists) != 0 {
		t.Errorf("失敗時不應登記：%v", s.World.Tools.Playlists)
	}
}

func TestServesBytesStep(t *testing.T) {
	s := newTestScenario(t)
	const size = 3 << 20
	if err := s.urlServesBytes("https://feeds.example.com/huge.xml", size); err != nil {
		t.Fatal(err)
	}
	resp, body, err := get(t, s, "https://feeds.example.com/huge.xml")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContentLength != size || len(body) != size {
		t.Fatalf("ContentLength=%d，實際 %d bytes，預期 %d", resp.ContentLength, len(body), size)
	}
	var feed struct {
		XMLName xml.Name `xml:"rss"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("不是合法的 RSS：%v", err)
	}
	if err := s.urlServesBytes("https://feeds.example.com/tiny.xml", fakenet.SizedFeedMin-1); err == nil {
		t.Fatal("太小的大小應該失敗")
	}
}

func TestNoConnectionStep(t *testing.T) {
	s := newTestScenario(t)
	if err := s.noConnectionTo("192.168.1.1"); err != nil {
		t.Fatalf("還沒連線過：%v", err)
	}
	if err := s.urlServes("https://feeds.example.com/x.xml", "rsshub-feed.xml"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := get(t, s, "https://feeds.example.com/x.xml"); err != nil {
		t.Fatal(err)
	}
	if err := s.noConnectionTo("feeds.example.com"); err == nil {
		t.Fatal("已對 feeds.example.com 連線，應該失敗")
	}
	if err := s.noConnectionTo(fakenet.DefaultIP.String()); err == nil {
		t.Fatal("已對該 IP 連線，應該失敗")
	}
	if err := s.noConnectionTo("192.168.1.1"); err != nil {
		t.Fatalf("沒對 192.168.1.1 連線：%v", err)
	}
}

func TestConfigFileContains(t *testing.T) {
	s := newTestScenario(t)
	doc := &godog.DocString{Content: "inbox: /data/inbox\nmedia_roots: [/media]\n"}
	if err := s.configFileContains(doc); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.World.Env["NOTESTEME_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	want := "inbox: " + s.World.InboxDir() + "\nmedia_roots: [" + s.World.MediaDir() + "]\n"
	if string(b) != want {
		t.Fatalf("設定檔內容 = %q，預期 %q", b, want)
	}
	// 再寫一次取代內容。
	if err := s.configFileContains(&godog.DocString{Content: "listen: 127.0.0.1:0\n"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.World.Env["NOTESTEME_CONFIG"]); string(b) != "listen: 127.0.0.1:0\n" {
		t.Fatalf("設定檔內容 = %q", b)
	}
}

// server 已在執行時，設定檔不會重新載入：步驟必須失敗（不能默默忽略，也不能轉成 pending）。
func TestConfigFileContainsFailsWhenServerRunning(t *testing.T) {
	s := newTestScenario(t)
	if err := s.serverIsRunning(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.World.Env["NOTESTEME_CONFIG"])
	err := s.configFileContains(&godog.DocString{Content: "listen: 127.0.0.1:1\n"})
	if err == nil || err == godog.ErrPending {
		t.Fatalf("err = %v，應該失敗", err)
	}
	if !strings.Contains(err.Error(), "已經在執行") {
		t.Errorf("錯誤訊息沒有說明原因：%v", err)
	}
	if after, _ := os.ReadFile(s.World.Env["NOTESTEME_CONFIG"]); string(after) != string(before) {
		t.Fatal("失敗時不應改動設定檔")
	}
}
