package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
)

// registerAccess 註冊「存取」的步驟，句型見 docs/specs/steps.md。
func registerAccess(r *Registry, s *Scenario) {
	r.Step(`the owner password is {string}`, s.ownerPasswordIs)
	r.Step(`the owner is signed in`, s.ownerIsSignedIn)
	r.Step(`a token {string} with scopes {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the token {string} expired`, func(string) error { return godog.ErrPending })
	r.Step(`someone signs in with the password {string}`, s.someoneSignsIn)
	r.Step(`someone signs in from {string} with the password {string}`, s.someoneSignsInFrom)
	r.Step(`the owner sends {string}`, s.ownerSends)
	r.Step(`the owner sends {string} with:`, s.ownerSendsWith)
	r.Step(`the owner sends {string} without a CSRF token`, s.ownerSendsWithoutCSRF)
	r.Step(`the client sends {string}`, s.clientSends)
	r.Step(`the client sends {string} with the token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the client sends {string} with the token {string} and:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the client sends {string} with the token {string} from the origin {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the owner sends {string} with the header {string} set to {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the response status is {int}`, s.responseStatusIs)
	r.Step(`the response contains:`, s.responseContains)
	r.Step(`the response header {string} contains {string}`, s.responseHeaderContains)
}

// ownerPasswordIs 設定登入密碼：第一次啟動時由 NOTESTEME_PASSWORD 寫入 DB。
func (s *Scenario) ownerPasswordIs(password string) error {
	s.World.Env["NOTESTEME_PASSWORD"] = password
	return nil
}

// ownerIsSignedIn 只表示意圖：session 在 owner 第一次送出請求時才建立（ADR-0015）。
// owner 的請求本來就會在尚未登入時先登入，所以這裡不需要動作。
func (s *Scenario) ownerIsSignedIn() error { return nil }

// someoneSignsIn 不帶身分地 POST /api/auth/login，回應的 cookie 不會變成 owner 的 session。
func (s *Scenario) someoneSignsIn(password string) error {
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		return err
	}
	return s.send("POST /api/auth/login", requestOpts{who: asClient, body: body})
}

// someoneSignsInFrom 與 someoneSignsIn 相同，但以指定的本機位址作為連線來源（AUTH-R6 依來源位址計數）。
func (s *Scenario) someoneSignsInFrom(addr, password string) error {
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		return err
	}
	return s.send("POST /api/auth/login", requestOpts{who: asClient, body: body, from: addr})
}

func (s *Scenario) ownerSends(spec string) error {
	return s.send(spec, requestOpts{who: asOwner})
}

func (s *Scenario) ownerSendsWith(spec string, doc *godog.DocString) error {
	return s.send(spec, requestOpts{who: asOwner, body: s.requestBody(doc)})
}

func (s *Scenario) ownerSendsWithoutCSRF(spec string) error {
	return s.send(spec, requestOpts{who: asOwner, noCSRF: true})
}

func (s *Scenario) clientSends(spec string) error {
	return s.send(spec, requestOpts{who: asClient})
}

// requestBody 取出 doc string 作為請求 body：虛擬路徑與 {token:…} 先代入。
func (s *Scenario) requestBody(doc *godog.DocString) []byte {
	return []byte(s.World.Subst(s.World.Expand(doc.Content)))
}

// pendingIfNotImplemented 讓「server 明確回應 501 not_implemented」的路由在斷言時視為 pending，
// 而不是 failed：這些路由屬於之後的切片，M1 期間是非 strict 模式（ADR-0015）。
// 只有 server 自己宣告的 501 才算；404、其他狀態碼與錯誤的內容仍然是 failed，不會被遮住。
// 預期的狀態碼本身就是 501 時不套用。
// 預期 401 時也不套用，預期 403 且請求缺少完整憑證（不帶身分、非 GET 缺 CSRF）時同樣不套用：
// 驗證與 CSRF 在路由之前決定，回 501 代表這些檢查被繞過，必須是 failed 而不是 pending。
// 帶著完整憑證卻預期 403 的場景（例如不能經 API 建立 command provider）是 handler 的規則，
// 屬於尚未實作的路由，仍視為 pending。
func (s *Scenario) pendingIfNotImplemented(wantStatus int) error {
	switch {
	case wantStatus == http.StatusNotImplemented, wantStatus == http.StatusUnauthorized,
		wantStatus == http.StatusForbidden && s.last.preRoute403:
		return nil
	}
	if s.last.status != http.StatusNotImplemented {
		return nil
	}
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(s.last.body, &e) == nil && e.Code == "not_implemented" {
		return godog.ErrPending
	}
	return nil
}

func (s *Scenario) responseStatusIs(want int) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	if err := s.pendingIfNotImplemented(want); err != nil {
		return err
	}
	if s.last.status != want {
		return fmt.Errorf("HTTP 狀態碼是 %d，預期 %d\n回應：%s", s.last.status, want, snippet(s.last.body))
	}
	return nil
}

// responseContains 以 JSON 子集比對回應內容，規則見 jsonSubset。
func (s *Scenario) responseContains(doc *godog.DocString) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	if err := s.pendingIfNotImplemented(0); err != nil {
		return err
	}
	want := []byte(s.World.Subst(s.World.Expand(doc.Content)))
	// 回應中的實際路徑換回虛擬路徑再比對。
	got := []byte(s.World.Unexpand(string(s.last.body)))
	return jsonSubset(want, got)
}

// responseHeaderContains 檢查標頭：同名標頭有多個值時（例如兩個 Set-Cookie），
// 任一個值包含指定文字就算符合。
func (s *Scenario) responseHeaderContains(name, want string) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	values := s.last.header.Values(name)
	if len(values) == 0 {
		return fmt.Errorf("回應沒有 %s 標頭", http.CanonicalHeaderKey(name))
	}
	for _, v := range values {
		if strings.Contains(v, want) {
			return nil
		}
	}
	return fmt.Errorf("標頭 %s 不含 %q\n實際：%q", name, want, values)
}
