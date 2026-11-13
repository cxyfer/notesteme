package steps

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
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
func (s *Scenario) urlServes(rawURL, name string) error {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("fixture 名稱 %q 不正確：只能是 features/fixtures/http/ 下的檔名", name)
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
