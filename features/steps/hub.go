package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakeoauth"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
)

// registerHub 註冊「Hub 實例與帳號」的步驟（steps.md 4.30，M5）。
//
// M5-S1 實作閘門、Hub 設定、假供應商、登入流程、帳號數與送到 Hub 的請求（session 與匿名）；
// Hub token、群組、停用與 MCP 的句型仍是步驟樁（S2、S5、S6）。閘門只對登入範圍的 HACC 場景放行（見 hubGate）。
func registerHub(r *Registry, s *Scenario) {
	r.Step(`a Hub is running`, s.hubIsRunning)
	r.Step(`the Hub config contains:`, s.hubConfigContains)
	r.Step(`the OAuth provider {string} has the user {string} with:`, s.oauthProviderHasUser)
	r.Step(`the OAuth provider {string} denies the next sign-in`, s.oauthProviderDeniesNext)
	r.Step(`{string} starts signing in to the Hub with {string}`, s.startsSigningIn)
	r.Step(`the OAuth provider {string} completes the pending sign-in`, func(p string) error { return s.completePendingSignIn(p, false) })
	r.Step(`the pending sign-in returns with a different state`, s.pendingSignInWithDifferentState)
	r.Step(`the last sign-in callback is replayed`, s.replayLastCallback)
	r.Step(`{string} signs in to the Hub with {string}`, func(who, p string) error { return s.signInToHub(who, p, false) })
	r.Step(`the hub member {string} signed in with {string}`, func(who, p string) error { return s.signInToHub(who, p, true) })
	r.Step(`the hub member {string} links the {string} user {string}`, s.hubMemberLinks)
	r.Step(`the OAuth provider {string} received a valid PKCE verifier`, s.oauthProviderReceivedValidPKCE)
	r.Step(`the number of Hub accounts is {int}`, s.numberOfHubAccounts)
	r.Step(`the response does not contain {string}`, s.responseDoesNotContain)
	r.Step(`the hub member {string} sends {string}`, func(who, spec string) error { return s.hubMemberSends(who, spec, nil, false) })
	r.Step(`the hub member {string} sends {string} with:`, func(who, spec string, doc *godog.DocString) error {
		return s.hubMemberSends(who, spec, s.requestBody(doc), false)
	})
	r.Step(`the hub member {string} sends {string} without a CSRF token`, func(who, spec string) error { return s.hubMemberSends(who, spec, nil, true) })
	r.Step(`the Hub client sends {string}`, func(spec string) error { return s.hubClientSends(spec, nil) })
	r.Step(`the Hub client sends {string} with:`, func(spec string, doc *godog.DocString) error { return s.hubClientSends(spec, s.requestBody(doc)) })
	r.Step(`a Hub token {string} for {string} with the permissions {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string} and:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string} from the origin {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} is in the group {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} is suspended`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub MCP client with the Hub token {string} lists the tools`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub MCP client with the Hub token {string} calls {string} with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
}

// hubState 是場景中 Hub 的瀏覽器狀態：每個人一份 cookie（以名稱保存，最後一次成功登入的 session 生效）、等待中的登入與最近一次 callback。
type hubState struct {
	cookies      map[string]map[string]string
	pending      *pendingSignIn
	lastCallback *hubCallback
}

// pendingSignIn 是 `starts signing in` 或連結之後、還沒完成的登入。
type pendingSignIn struct {
	person   string // 瀏覽器的主人（cookie 存在這裡）
	user     string // 在供應商以哪個使用者完成 authorize
	provider fakeoauth.Kind
	location string // 供應商的 authorize URL
	oauth    string // notesteme_hub_oauth cookie
}

type hubCallback struct {
	person, url, oauth string
}

// hubLoginRules 是 M5-S1 實作的 HACC 規則：登入、連結、移除與 session（R1～R8）。
// R10 只出現在「第一次登入建立 members 帳號」，和 R4 一起放行。
var hubLoginRules = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true}

// hubGate 讓真 Hub 的閘門只對登入範圍的 HACC 場景放行：至少一個 @HACC-R1～R8，而且沒有 R1～R8、R10 以外的 HACC 規則。
// 權限、PAT、群組、可見度、下架、限流（HACC 的其他規則）、HREP 與 HMCP 的場景在它們的切片實作之前維持 pending，
// 停在同一個閘門（同 M3-S1 的 spbGate）。回傳裸的 godog.ErrPending，junit 的訊息與切片之前相同。
func (s *Scenario) hubGate() error {
	login := false
	for _, t := range s.tags {
		v, ok := strings.CutPrefix(t, "@HACC-R")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		switch {
		case hubLoginRules[n]:
			login = true
		case n != 10:
			return godog.ErrPending
		}
	}
	if !login {
		return godog.ErrPending
	}
	return nil
}

func (s *Scenario) hubIsRunning() error {
	if err := s.hubGate(); err != nil {
		return err
	}
	return s.World.StartHub(s.ctx)
}

func (s *Scenario) hubConfigContains(doc *godog.DocString) error {
	return s.World.AddHubConfig(doc.Content)
}

// oauthProvider 回傳假供應商；Hub 沒有啟動時失敗。
func (s *Scenario) oauthProvider(name string) (*fakeoauth.Provider, fakeoauth.Kind, error) {
	k := fakeoauth.Kind(name)
	if !slices.Contains(harness.OAuthKinds, k) {
		return nil, "", fmt.Errorf("不認得的 OAuth 供應商 %q（google、github、linuxdo）", name)
	}
	p := s.World.OAuth(k)
	if p == nil {
		return nil, "", errors.New("假 OAuth 供應商沒有啟動（Background 要有 `Given a Hub is running`）")
	}
	return p, k, nil
}

// oauthProviderHasUser 以預設使用者（或之前登記的）為基礎修改欄位（steps.md 4.30）。
func (s *Scenario) oauthProviderHasUser(provider, name string, table *godog.Table) error {
	p, k, err := s.oauthProvider(provider)
	if err != nil {
		return err
	}
	u, ok := p.User(name)
	if !ok {
		u = fakeoauth.DefaultUser(k, name)
	}
	rows, err := fieldRows(table)
	if err != nil {
		return err
	}
	for _, r := range rows {
		switch r[0] {
		case "subject":
			u.Subject = r[1]
		case "login":
			u.Login = r[1]
		case "email":
			u.Email = r[1]
		case "email_verified":
			b, err := strconv.ParseBool(r[1])
			if err != nil {
				return fmt.Errorf("email_verified 要是 true 或 false：%q", r[1])
			}
			u.EmailVerified = b
		case "trust_level":
			if k != fakeoauth.LinuxDo {
				return errors.New("trust_level 只有 linuxdo")
			}
			n, err := strconv.Atoi(r[1])
			if err != nil {
				return fmt.Errorf("trust_level 要是整數：%q", r[1])
			}
			u.TrustLevel = n
		case "orgs":
			if k != fakeoauth.GitHub {
				return errors.New("orgs 只有 github")
			}
			u.Orgs = nil
			for _, o := range strings.Split(r[1], ",") {
				if o = strings.TrimSpace(o); o != "" {
					u.Orgs = append(u.Orgs, o)
				}
			}
		default:
			return fmt.Errorf("不認得的使用者欄位 %q", r[0])
		}
	}
	p.SetUser(u)
	return nil
}

// fieldRows 讀取 field、value 兩欄的表格（略過標題列）。
func fieldRows(table *godog.Table) ([][2]string, error) {
	var out [][2]string
	for i, row := range table.Rows {
		if len(row.Cells) != 2 {
			return nil, fmt.Errorf("表格第 %d 列要有 field、value 兩欄", i+1)
		}
		f, v := row.Cells[0].Value, row.Cells[1].Value
		if i == 0 && f == "field" && v == "value" {
			continue
		}
		out = append(out, [2]string{f, v})
	}
	return out, nil
}

func (s *Scenario) oauthProviderDeniesNext(provider string) error {
	p, _, err := s.oauthProvider(provider)
	if err != nil {
		return err
	}
	p.DenyNext()
	return nil
}

// ---- 送到 Hub 的請求 ----

type hubRequest struct {
	method, target string // target 是 /path 或完整的 https://hub.example.org/… 網址
	body           []byte
	cookies        map[string]string
	csrf           bool
	// preRoute403 表示這個請求缺少完整憑證（沒有身分或缺 CSRF），見 pendingIfNotImplemented。
	preRoute403 bool
}

// hubDo 送出請求到真 Hub（設定改變過時先重新啟動）並記成最近一次回應。
func (s *Scenario) hubDo(r hubRequest) (*response, error) {
	if s.World.HubKind() != harness.HubReal {
		return nil, errors.New("真 Hub 沒有在執行（Background 要有 `Given a Hub is running`）")
	}
	if err := s.World.RestartHubIfNeeded(s.ctx); err != nil {
		return nil, err
	}
	target := r.target
	if strings.HasPrefix(target, "/") {
		target = harness.HubPublicURL + target
	}
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(s.ctx, r.method, target, body)
	if err != nil {
		return nil, err
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, v := range r.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: v})
	}
	if r.csrf {
		if v := r.cookies[hubauth.CSRFCookie]; v != "" {
			req.Header.Set(hubauth.CSRFHeader, v)
		}
	}
	resp, err := s.World.HubHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s：%w", r.method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := &response{status: resp.StatusCode, header: resp.Header, body: b, step: s.step, preRoute403: r.preRoute403}
	s.last = out
	return out, nil
}

// memberCookies 回傳這個人的瀏覽器 cookie（第一次使用時建立）。
func (s *Scenario) memberCookies(who string) map[string]string {
	if s.hub.cookies == nil {
		s.hub.cookies = map[string]map[string]string{}
	}
	if s.hub.cookies[who] == nil {
		s.hub.cookies[who] = map[string]string{}
	}
	return s.hub.cookies[who]
}

// storeHubCookies 依回應的 Set-Cookie 更新這個人的 cookie；清除指令刻意忽略（同 storeCookies：驗證 session 在伺服器端失效）。
func (s *Scenario) storeHubCookies(who string, resp *response) {
	jar := s.memberCookies(who)
	for _, c := range (&http.Response{Header: resp.header}).Cookies() {
		if c.MaxAge < 0 || c.Value == "" || c.Name == hubauth.OAuthCookie {
			continue
		}
		jar[c.Name] = c.Value
	}
}

func setCookie(resp *response, name string) string {
	for _, c := range (&http.Response{Header: resp.header}).Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

func (s *Scenario) hubMemberSends(who, spec string, body []byte, noCSRF bool) error {
	method, path, err := parseRequest(spec)
	if err != nil {
		return err
	}
	jar := s.memberCookies(who)
	if jar[hubauth.SessionCookie] == "" {
		return fmt.Errorf("Hub 使用者 %q 還沒有登入（先用 `Given the hub member … signed in with …`）", who)
	}
	safe := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	_, err = s.hubDo(hubRequest{
		method: method, target: s.World.SubstRequest(path), body: body, cookies: jar,
		csrf: !noCSRF, preRoute403: noCSRF && !safe,
	})
	return err
}

func (s *Scenario) hubClientSends(spec string, body []byte) error {
	method, path, err := parseRequest(spec)
	if err != nil {
		return err
	}
	_, err = s.hubDo(hubRequest{method: method, target: s.World.SubstRequest(path), body: body, preRoute403: true})
	return err
}

// ---- 登入流程 ----

// startsSigningIn 是 GET /auth/<供應商>/start；成功（302）時記下等待中的登入。
func (s *Scenario) startsSigningIn(who, provider string) error {
	_, k, err := s.oauthProvider(provider)
	if err != nil {
		return err
	}
	s.hub.pending = nil
	resp, err := s.hubDo(hubRequest{method: http.MethodGet, target: "/auth/" + provider + "/start", cookies: s.memberCookies(who)})
	if err != nil {
		return err
	}
	if resp.status == http.StatusFound {
		s.hub.pending = &pendingSignIn{person: who, user: who, provider: k, location: resp.header.Get("Location"), oauth: setCookie(resp, hubauth.OAuthCookie)}
	}
	return nil
}

// authorize 以等待中登入的使用者打開供應商的 authorize URL，回傳供應商轉回的 callback URL。
func (s *Scenario) authorize(p *pendingSignIn) (string, error) {
	prov := s.World.OAuth(p.provider)
	prov.NextAs(p.user)
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, p.location, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.World.HubHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("打開供應商的 authorize URL：%w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("假供應商的 authorize 回 HTTP %d（預期 302）", resp.StatusCode)
	}
	back := resp.Header.Get("Location")
	if !strings.HasPrefix(back, harness.HubPublicURL+"/") {
		return "", fmt.Errorf("假供應商轉回的網址不是 Hub：%q", back)
	}
	return back, nil
}

// completePendingSignIn 完成等待中的登入；otherState 為 true 時把 callback 的 state 換成另一個隨機值（cookie 不變）。
func (s *Scenario) completePendingSignIn(provider string, otherState bool) error {
	p := s.hub.pending
	if p == nil {
		return errors.New("沒有等待中的登入（先用 `When … starts signing in to the Hub with …`）")
	}
	if provider != "" && string(p.provider) != provider {
		return fmt.Errorf("等待中的登入是 %s，不是 %s", p.provider, provider)
	}
	s.hub.pending = nil
	back, err := s.authorize(p)
	if err != nil {
		return err
	}
	if otherState {
		u, err := url.Parse(back)
		if err != nil {
			return err
		}
		q := u.Query()
		q.Set("state", "forged-"+strconv.Itoa(s.step)+"-0123456789abcdefghijklmnopqrstuv")
		u.RawQuery = q.Encode()
		back = u.String()
	}
	return s.openCallback(hubCallback{person: p.person, url: back, oauth: p.oauth})
}

// openCallback 帶著等待中登入的 cookie 打開 callback；成功建立 session 時存進這個人的 cookie。
func (s *Scenario) openCallback(cb hubCallback) error {
	cookies := map[string]string{}
	for k, v := range s.memberCookies(cb.person) {
		cookies[k] = v
	}
	if cb.oauth != "" {
		cookies[hubauth.OAuthCookie] = cb.oauth
	}
	s.hub.lastCallback = &cb
	resp, err := s.hubDo(hubRequest{method: http.MethodGet, target: cb.url, cookies: cookies})
	if err != nil {
		return err
	}
	if resp.status == http.StatusFound {
		s.storeHubCookies(cb.person, resp)
	}
	return nil
}

func (s *Scenario) pendingSignInWithDifferentState() error { return s.completePendingSignIn("", true) }

func (s *Scenario) replayLastCallback() error {
	if s.hub.lastCallback == nil {
		return errors.New("還沒有打開過 callback")
	}
	return s.openCallback(*s.hub.lastCallback)
}

// signInToHub 是 starts signing in 加上 completes the pending sign-in；must 為 true 時登入沒有成功就失敗（前提用）。
func (s *Scenario) signInToHub(who, provider string, must bool) error {
	if err := s.startsSigningIn(who, provider); err != nil {
		return err
	}
	if s.hub.pending != nil {
		if err := s.completePendingSignIn(provider, false); err != nil {
			return err
		}
	}
	if must && (s.last.status != http.StatusFound || setCookie(s.last, hubauth.SessionCookie) == "") {
		return fmt.Errorf("%q 以 %s 登入 Hub 失敗：HTTP %d %s", who, provider, s.last.status, snippet(s.last.body))
	}
	return nil
}

// hubMemberLinks 送出 POST /api/v1/me/identities/<供應商>/link，以 user 在供應商完成 authorize 與 callback。
func (s *Scenario) hubMemberLinks(who, provider, user string) error {
	_, k, err := s.oauthProvider(provider)
	if err != nil {
		return err
	}
	if err := s.hubMemberSends(who, "POST /api/v1/me/identities/"+provider+"/link", nil, false); err != nil {
		return err
	}
	if s.last.status != http.StatusOK {
		return nil
	}
	var out struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(s.last.body, &out); err != nil || out.AuthorizeURL == "" {
		return fmt.Errorf("link 的回應沒有 authorize_url：%s", snippet(s.last.body))
	}
	s.hub.pending = &pendingSignIn{person: who, user: user, provider: k, location: out.AuthorizeURL, oauth: setCookie(s.last, hubauth.OAuthCookie)}
	return s.completePendingSignIn(provider, false)
}

func (s *Scenario) oauthProviderReceivedValidPKCE(provider string) error {
	p, _, err := s.oauthProvider(provider)
	if err != nil {
		return err
	}
	if !p.ValidPKCE() {
		return fmt.Errorf("假供應商 %s 最近一次 token 請求沒有符合 S256 challenge 的 code_verifier", provider)
	}
	return nil
}

func (s *Scenario) numberOfHubAccounts(want int) error {
	st, err := s.World.HubStore()
	if err != nil {
		return err
	}
	n, err := st.CountUsers(s.ctx)
	if err != nil {
		return err
	}
	if n != int64(want) {
		return fmt.Errorf("Hub 有 %d 個帳號，預期 %d", n, want)
	}
	return nil
}

// responseDoesNotContain 以原始文字確認最近一次回應不含 text；body 是空的時失敗（不能證明「不含」）。
func (s *Scenario) responseDoesNotContain(text string) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	if err := s.pendingIfNotImplemented(0); err != nil {
		return err
	}
	if len(s.last.body) == 0 {
		return errors.New("最近一次回應的 body 是空的，不能證明不含這段文字")
	}
	if bytes.Contains(s.last.body, []byte(text)) {
		return fmt.Errorf("回應含有 %q：%s", text, snippet(s.last.body))
	}
	return nil
}
