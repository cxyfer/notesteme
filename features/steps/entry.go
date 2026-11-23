package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// registerEntry 註冊「其他新增入口」的步驟（steps.md 4.17 的 ENTRY 段，M4）。
// 這些句型只出現在 ENTRY-entry-points.feature，所以不需要閘門。
func registerEntry(r *Registry, s *Scenario) {
	r.Step(`the browser opens {string}`, func(p string) error { return s.browserOpens(p, false) })
	r.Step(`the browser opens {string} signed in as the owner`, func(p string) error { return s.browserOpens(p, true) })
	r.Step(`the browser is sent to {string}`, s.browserIsSentTo)
	r.Step(`someone signs in with the password {string} to continue to {string}`, s.someoneSignsInToContinue)
}

// browserAccept 是瀏覽器開啟頁面時的 Accept 標頭。
const browserAccept = "text/html,application/xhtml+xml,*/*"

// browserOpens 以瀏覽器的方式送出 GET：路徑照字面送出（不代入、不再編碼），不跟隨轉址。
// signedIn 為 true 時帶 owner 的 session cookie（GET 不帶 CSRF token）。
func (s *Scenario) browserOpens(path string, signedIn bool) error {
	if path == "" || path[0] != '/' {
		return fmt.Errorf("路徑 %q 必須以 / 開頭", path)
	}
	if err := s.ensureServer(); err != nil {
		return err
	}
	o := requestOpts{who: asClient, headers: map[string]string{"Accept": browserAccept}}
	if signedIn {
		if !s.ownerSignInDeclared {
			return errors.New("場景沒有 Given the owner is signed in，不能以 owner 的身分開啟頁面")
		}
		if err := s.ensureOwner(); err != nil {
			return err
		}
		o.who = asOwner
	}
	resp, err := s.do(http.MethodGet, path, o)
	if err != nil {
		return err
	}
	resp.step = s.step
	s.last = resp
	return nil
}

// browserIsSentTo 斷言最近一次回應是 3xx，Location 與預期完全相同而且是相對路徑。
func (s *Scenario) browserIsSentTo(want string) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	if s.last.status/100 != 3 {
		return fmt.Errorf("回應不是轉址：HTTP %d %s", s.last.status, snippet(s.last.body))
	}
	got := s.last.header.Get("Location")
	if u, err := url.Parse(got); err != nil || u.IsAbs() || u.Host != "" {
		return fmt.Errorf("Location 必須是相對路徑，實際：%q", got)
	}
	if got != want {
		return fmt.Errorf("Location 不符\n預期：%q\n實際：%q", want, got)
	}
	return nil
}

// someoneSignsInToContinue 不帶身分地 POST /api/auth/login {password, next}，next 照字面送出。
func (s *Scenario) someoneSignsInToContinue(password, next string) error {
	body, err := json.Marshal(map[string]string{"password": password, "next": next})
	if err != nil {
		return err
	}
	return s.send("POST /api/auth/login", requestOpts{who: asClient, body: body})
}
