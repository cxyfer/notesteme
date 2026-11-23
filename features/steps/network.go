package steps

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// registerNetwork 註冊「網路」的步驟，句型見 docs/specs/steps.md。
func registerNetwork(r *Registry, s *Scenario) {
	r.Step(`the host {string} resolves to {string}`, s.hostResolvesTo)
	r.Step(`the host {string} resolves to {string} and then {string}`, s.hostResolvesToThen)
	r.Step(`the URL {string} redirects to {string}`, s.urlRedirectsTo)
	r.Step(`the URL {string} serves {string}`, s.urlServes)
	r.Step(`the URL {string} serves {int} bytes`, s.urlServesBytes)
	r.Step(`no connection was made to {string}`, s.noConnectionTo)
}

func (s *Scenario) hostResolvesTo(host, ip string) error {
	return s.World.Net.Resolves(host, ip)
}

// hostResolvesToThen 登記 rebinding：第一次解析回傳 first，之後都回傳 then。
func (s *Scenario) hostResolvesToThen(host, first, then string) error {
	return s.World.Net.Resolves(host, first, then)
}

func (s *Scenario) urlRedirectsTo(rawURL, target string) error {
	return s.World.Net.Route(rawURL, fakenet.Redirect(target))
}

// urlServes 回傳 features/fixtures/http/<name> 的內容。fixture 在步驟執行時就讀入，不存在時立即失敗。
//
// YouTube 與 Bilibili 的網址不是 HTTP 請求，而是交給 yt-dlp 處理（ADR-0009）：
// 播放清單網址把 features/fixtures/ytdlp/<name> 登記為假 yt-dlp `--flat-playlist -J` 的輸出，不建立 fakenet 路由。
// 其他 YouTube、Bilibili 網址沒有這個步驟的語意，直接失敗。
func (s *Scenario) urlServes(rawURL, name string) error {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("fixture 名稱 %q 不正確：只能是 features/fixtures/ 子目錄下的檔名", name)
	}
	if isYtDlpHost(rawURL) {
		return s.servesYtDlpPlaylist(rawURL, name)
	}
	body, err := os.ReadFile(filepath.Join(s.World.FixturesDir, "http", name))
	if err != nil {
		return fmt.Errorf("讀取 fixture：%w", err)
	}
	ctype := mime.TypeByExtension(filepath.Ext(name))
	switch {
	case filepath.Ext(name) == ".xml":
		ctype = "application/rss+xml; charset=utf-8"
	case ctype == "":
		ctype = http.DetectContentType(body)
	}
	return s.World.Net.Route(rawURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(body)
	}))
}

// isYtDlpHost 回報網址的主機是否由 yt-dlp 處理（YouTube、Bilibili）。
func isYtDlpHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range []string{"youtube.com", "youtu.be", "bilibili.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// servesYtDlpPlaylist 把 ytdlp/<name> 登記為播放清單網址的 yt-dlp 輸出。fixture 不存在時立即失敗。
func (s *Scenario) servesYtDlpPlaylist(rawURL, name string) error {
	// 依網址決定登記到哪一張表：播放清單、Bilibili 合集（SUB，M4）、YouTube 頻道的 /videos（SUB，M4）。
	var table *map[string]string
	key, ok := faketool.PlaylistID(rawURL)
	switch {
	case ok:
		table = &s.World.Tools.Playlists
	default:
		if key, ok = faketool.CollectionKey(rawURL); ok {
			table = &s.World.Tools.Collections
		} else if key, ok = faketool.ChannelVideosID(rawURL); ok {
			table = &s.World.Tools.ChannelLists
		}
	}
	if table == nil {
		return fmt.Errorf("%s 是由 yt-dlp 處理的網址，serves 只支援 YouTube 播放清單（/playlist?list=…）、頻道的影片列表與 Bilibili 合集", rawURL)
	}
	if _, err := os.Stat(filepath.Join(s.World.FixturesDir, "ytdlp", name)); err != nil {
		return fmt.Errorf("讀取 fixture：%w", err)
	}
	if *table == nil {
		*table = map[string]string{}
	}
	(*table)[key] = name
	return s.World.SyncTools()
}

// urlServesBytes 回傳總長剛好 size bytes 的合法 RSS（見 fakenet.FixedSizeFeed）。
func (s *Scenario) urlServesBytes(rawURL string, size int) error {
	h, err := fakenet.FixedSizeFeed(size)
	if err != nil {
		return err
	}
	return s.World.Net.Route(rawURL, h)
}

// noConnectionTo 確認 fakenet 沒有收到對該 IP 或主機的連線嘗試。
func (s *Scenario) noConnectionTo(hostOrIP string) error {
	if s.World.Net.ConnectedTo(hostOrIP) {
		return fmt.Errorf("不應該連線到 %s，但曾經嘗試連線：%v", hostOrIP, s.World.Net.Connections())
	}
	return nil
}
