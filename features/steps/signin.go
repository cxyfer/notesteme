package steps

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/features/internal/fakeoidc"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
)

// registerSignin 註冊「進階登入與工作階段」的步驟（steps.md 4.21，M4 AUTH）。
func registerSignin(r *Registry, s *Scenario) {
	r.Step(`the reverse proxy at {string} signs users in with the header {string}`, s.proxySignsIn)
	r.Step(`the reverse proxy allows the users {string}`, s.proxyAllows)
	r.Step(`the reverse proxy at {string} forwards {string} with the header {string} set to {string}`, s.proxyForwards)
	r.Step(`the reverse proxy at {string} forwards {string} with the header {string} set to {string} and the CSRF token`, s.proxyForwardsWithCSRF)
	r.Step(`the reverse proxy at {string} forwards {string} with the token {string} and the header {string} set to {string}`, s.proxyForwardsWithToken)
	r.Step(`the auth log contains {string}`, s.authLogContains)
	r.Step(`the OIDC provider {string} signs in the subject {string}`, s.oidcProviderSignsIn)
	r.Step(`the OIDC provider allows the subjects {string}`, s.oidcAllows)
	r.Step(`the OIDC provider issues ID tokens with {string}`, s.oidcFault)
	r.Step(`the OIDC provider denies the sign-in with {string}`, s.oidcDenies)
	r.Step(`the OIDC sign-in starts`, func() error { return s.oidcStart(nil) })
	r.Step(`the OIDC sign-in starts with the next page {string}`, func(next string) error { return s.oidcStart(&next) })
	r.Step(`the OIDC sign-in is completed`, func() error { return s.oidcComplete(nil) })
	r.Step(`the OIDC sign-in is completed with the state {string}`, func(state string) error { return s.oidcComplete(&state) })
	r.Step(`the OIDC callback is replayed`, s.oidcReplay)
	r.Step(`the OIDC authorization request has the parameter {string} set to {string}`, s.oidcAuthParam)
	r.Step(`the OIDC sign-in redirects to {string}`, s.oidcRedirectsTo)
	r.Step(`the OIDC callback set no session cookie`, s.oidcNoSessionCookie)
	r.Step(`the OIDC token request used PKCE with {string}`, s.oidcUsedPKCE)
	r.Step(`the OIDC session sends {string}`, s.oidcSessionSends)
	r.Step(`the session {string} is signed in from {string}`, s.namedSessionSignsIn)
	r.Step(`the session {string} sends {string}`, s.namedSessionSends)
	r.Step(`the session {string} is revoked by the owner`, s.namedSessionRevoked)
	r.Step(`the session list does not contain the secret of the session {string}`, s.sessionListHidesSecret)
	r.Step(`the token {string} is rotated as {string}`, s.tokenRotated)
}

// TestOIDCClientID 與 TestOIDCClientSecretValue 是 steps.md 4.21 指定的假 IdP 的 client 憑證。
const (
	TestOIDCClientID          = "notesteme"
	TestOIDCClientSecretValue = "fake-oidc-client-secret-7c1e"
)

// signinState 是場景的反向代理、OIDC 與具名 session 狀態。
type signinState struct {
	// 設定類步驟累積的值；startServer 時與 `the config file contains:` 的內容合併（writeSigninConfig）。
	trusted      []string
	proxyHeader  string
	proxyUsers   []string
	hasProxyUser bool
	oidcIssuer   string
	oidcSubjects []string
	hasSubjects  bool
	// base 是合併前的設定檔內容，written 是上次寫出的合併結果：重新啟動時不把自己寫的檔案當成新的 base。
	base     []byte
	baseOK   bool
	written  []byte
	idp      *fakeoidc.Server
	proxyJar map[string]string
	oidcJar  map[string]string
	start    *response
	callback string
	sessions map[string]*namedSession
}

type namedSession struct {
	from   string
	jar    map[string]string
	secret string
}

func (st *signinState) configured() bool {
	return len(st.trusted) > 0 || st.proxyHeader != "" || st.hasProxyUser || st.oidcIssuer != "" || st.hasSubjects
}

// updateJar 依回應的 Set-Cookie 更新 jar：清除指令（Max-Age < 0 或空值）刪除該 cookie。
func updateJar(jar map[string]string, h http.Header) {
	r := http.Response{Header: h}
	for _, c := range r.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(jar, c.Name)
			continue
		}
		jar[c.Name] = c.Value
	}
}

// ---- 設定 ----

func (s *Scenario) proxySignsIn(ip, header string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("反向代理的位址 %q 不是 IP", ip)
	}
	st := &s.signin
	if st.proxyHeader != "" && st.proxyHeader != header {
		return fmt.Errorf("已經設定了另一個標頭 %q", st.proxyHeader)
	}
	st.proxyHeader = header
	st.trusted = append(st.trusted, ip+"/32")
	return nil
}

func signinList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Scenario) proxyAllows(users string) error {
	s.signin.proxyUsers, s.signin.hasProxyUser = signinList(users), true
	return nil
}

func (s *Scenario) oidcProviderSignsIn(issuer, subject string) error {
	if s.server != nil {
		return errors.New("server 已經在執行：OIDC 的設定必須放在啟動 server 的步驟之前")
	}
	st := &s.signin
	if st.idp != nil && st.oidcIssuer != issuer {
		return fmt.Errorf("已經設定了另一個 OIDC provider %q", st.oidcIssuer)
	}
	// client secret 立刻寫進環境變數：之後的 `the environment variable … is not set` 才能把它移除。
	s.World.Env[harness.OIDCClientSecretEnv] = TestOIDCClientSecretValue
	idp, err := s.World.StartOIDC(fakeoidc.Config{
		Issuer: issuer, ClientID: TestOIDCClientID, ClientSecret: TestOIDCClientSecretValue, Subject: subject,
	})
	if err != nil {
		return err
	}
	idp.SetSubject(subject)
	st.idp, st.oidcIssuer = idp, issuer
	return nil
}

func (s *Scenario) oidcAllows(subjects string) error {
	s.signin.oidcSubjects, s.signin.hasSubjects = signinList(subjects), true
	return nil
}

func (s *Scenario) requireIdP() (*fakeoidc.Server, error) {
	if s.signin.idp == nil {
		return nil, errors.New("還沒有 `the OIDC provider … signs in the subject …`")
	}
	return s.signin.idp, nil
}

func (s *Scenario) oidcFault(fault string) error {
	idp, err := s.requireIdP()
	if err != nil {
		return err
	}
	f, err := fakeoidc.ParseFault(fault)
	if err != nil {
		return err
	}
	idp.SetFault(f)
	return nil
}

func (s *Scenario) oidcDenies(code string) error {
	idp, err := s.requireIdP()
	if err != nil {
		return err
	}
	idp.Deny(code)
	return nil
}

// oidcRedirectURL 是設定的 redirect_url：用設定的 listen 位址當 base URL（實際 port 在啟動後才知道）。
// callback 的步驟只取 IdP 轉回網址的 path 與 query，送到實際的位址，所以 host 不影響結果。
func (s *Scenario) oidcRedirectURL() string {
	listen := s.World.Env[config.EnvListen]
	if listen == "" {
		listen = config.DefaultListen
	}
	return "http://" + listen + "/api/auth/oidc/callback"
}

// writeSigninConfig 把設定類步驟累積的值與設定檔原本的內容合併後寫回（steps.md 4.21）；沒有累積任何值時不動作。
func (s *Scenario) writeSigninConfig() error {
	st := &s.signin
	if !st.configured() {
		return nil
	}
	path := s.World.Expand(s.World.Env[config.EnvConfig])
	if path == "" {
		return errors.New("Env 沒有設定 NOTESTEME_CONFIG，無法寫入設定檔")
	}
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !st.baseOK || !bytes.Equal(cur, st.written) {
		st.base, st.baseOK = cur, true
	}
	doc := map[string]any{}
	if len(bytes.TrimSpace(st.base)) > 0 {
		if err := yaml.Unmarshal(st.base, &doc); err != nil {
			return fmt.Errorf("解析設定檔：%w", err)
		}
		if doc == nil {
			doc = map[string]any{}
		}
	}
	set := func(path []string, v any) error {
		m := doc
		for _, k := range path[:len(path)-1] {
			next, ok := m[k]
			if !ok {
				nm := map[string]any{}
				m[k] = nm
				m = nm
				continue
			}
			nm, ok := next.(map[string]any)
			if !ok {
				return fmt.Errorf("設定檔的 %s 不是物件", k)
			}
			m = nm
		}
		last := path[len(path)-1]
		if _, ok := m[last]; ok {
			return fmt.Errorf("設定檔與步驟都設定了 %s", strings.Join(path, "."))
		}
		m[last] = v
		return nil
	}
	var errs []error
	if len(st.trusted) > 0 {
		errs = append(errs, set([]string{"trusted_proxies"}, st.trusted))
	}
	if st.proxyHeader != "" {
		errs = append(errs, set([]string{"auth", "proxy", "user_header"}, st.proxyHeader))
	}
	if st.hasProxyUser {
		errs = append(errs, set([]string{"auth", "proxy", "allowed_users"}, st.proxyUsers))
	}
	if st.oidcIssuer != "" {
		errs = append(errs,
			set([]string{"auth", "oidc", "issuer"}, st.oidcIssuer),
			set([]string{"auth", "oidc", "client_id"}, TestOIDCClientID),
			set([]string{"auth", "oidc", "redirect_url"}, s.oidcRedirectURL()))
	}
	if st.hasSubjects {
		errs = append(errs, set([]string{"auth", "oidc", "allowed_subjects"}, st.oidcSubjects))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	st.written = out
	return nil
}

// ---- 反向代理 ----

func (s *Scenario) proxyJar() map[string]string {
	if s.signin.proxyJar == nil {
		s.signin.proxyJar = map[string]string{}
	}
	return s.signin.proxyJar
}

func (s *Scenario) proxyForwards(from, spec, header, value string) error {
	return s.send(spec, requestOpts{who: asClient, from: from, headers: map[string]string{header: value}, jar: s.proxyJar()})
}

func (s *Scenario) proxyForwardsWithCSRF(from, spec, header, value string) error {
	jar := s.proxyJar()
	csrf := jar[auth.CSRFCookie]
	if csrf == "" {
		return errors.New("proxy 瀏覽器的 cookie jar 沒有 notesteme_csrf")
	}
	return s.send(spec, requestOpts{who: asClient, from: from, jar: jar,
		headers: map[string]string{header: value, auth.CSRFHeader: csrf}})
}

func (s *Scenario) proxyForwardsWithToken(from, spec, name, header, value string) error {
	headers, err := s.bearerHeader(name)
	if err != nil {
		return err
	}
	headers[header] = value
	var body []byte
	if method, path, err := parseRequest(spec); err == nil && method == http.MethodPost && path == "/api/items" {
		body = []byte(`{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	}
	return s.send(spec, requestOpts{who: asClient, from: from, headers: headers, body: body})
}

func (s *Scenario) authLogContains(want string) error {
	for _, line := range strings.Split(s.World.Log.String(), "\n") {
		if strings.Contains(line, want) {
			return nil
		}
	}
	return fmt.Errorf("server 的 log 沒有包含 %q 的一行", want)
}

// ---- OIDC ----

func (s *Scenario) oidcStart(next *string) error {
	s.signin.oidcJar = map[string]string{}
	path := "/api/auth/oidc/start"
	if next != nil {
		path += "?next=" + url.QueryEscape(*next)
	}
	if err := s.send("GET "+path, requestOpts{who: asClient, jar: s.signin.oidcJar}); err != nil {
		return err
	}
	s.signin.start = s.last
	s.signin.callback = ""
	return nil
}

func (s *Scenario) oidcComplete(state *string) error {
	idp, err := s.requireIdP()
	if err != nil {
		return err
	}
	start := s.signin.start
	if start == nil || start.status != http.StatusFound {
		return errors.New("最近一次 OIDC 登入的 start 不是 302")
	}
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil {
		return fmt.Errorf("start 的 Location 不是網址：%w", err)
	}
	iss, _ := url.Parse(idp.Issuer())
	if loc.Scheme != "https" || !strings.EqualFold(loc.Hostname(), iss.Hostname()) || loc.Path != "/authorize" {
		return fmt.Errorf("start 的 Location 不是假 IdP 的 authorize endpoint：%s", loc.Redacted())
	}
	// 在程序內呼叫假 IdP 的 authorize endpoint（不經過產品）。
	rec := httptest.NewRecorder()
	idp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, loc.String(), nil))
	if rec.Code != http.StatusFound {
		return fmt.Errorf("假 IdP 的 authorize 回應 %d：%s", rec.Code, snippet(rec.Body.Bytes()))
	}
	back, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		return fmt.Errorf("假 IdP 轉回的網址不正確：%w", err)
	}
	if state != nil {
		q := back.Query()
		q.Set("state", *state)
		back.RawQuery = q.Encode()
	}
	s.signin.callback = back.RequestURI()
	return s.send("GET "+s.signin.callback, requestOpts{who: asClient, jar: s.signin.oidcJar})
}

func (s *Scenario) oidcReplay() error {
	if s.signin.callback == "" {
		return errors.New("還沒有完成過 OIDC 登入")
	}
	return s.send("GET "+s.signin.callback, requestOpts{who: asClient, jar: s.signin.oidcJar})
}

func (s *Scenario) oidcAuthParam(name, want string) error {
	start := s.signin.start
	if start == nil {
		return errors.New("還沒有開始 OIDC 登入")
	}
	if err := s.pendingIfNotImplemented(0); err != nil {
		return err
	}
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil {
		return err
	}
	vals := loc.Query()[name]
	if len(vals) != 1 {
		return fmt.Errorf("參數 %s 出現 %d 次，預期 1 次", name, len(vals))
	}
	if want == "*" {
		if vals[0] == "" {
			return fmt.Errorf("參數 %s 是空的", name)
		}
		return nil
	}
	if vals[0] != want {
		return fmt.Errorf("參數 %s 是 %q，預期 %q", name, vals[0], want)
	}
	return nil
}

func (s *Scenario) oidcRedirectsTo(want string) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	if s.last.status != http.StatusFound {
		return fmt.Errorf("HTTP 狀態碼是 %d，預期 302\n回應：%s", s.last.status, snippet(s.last.body))
	}
	if got := s.last.header.Get("Location"); got != want {
		return fmt.Errorf("Location 是 %q，預期 %q", got, want)
	}
	return nil
}

func (s *Scenario) oidcNoSessionCookie() error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	r := http.Response{Header: s.last.header}
	for _, c := range r.Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" && c.MaxAge >= 0 {
			return errors.New("回應設定了 notesteme_session")
		}
	}
	if s.signin.oidcJar[auth.SessionCookie] != "" {
		return errors.New("OIDC 瀏覽器的 cookie jar 有 notesteme_session")
	}
	return nil
}

func (s *Scenario) oidcUsedPKCE(method string) error {
	idp, err := s.requireIdP()
	if err != nil {
		return err
	}
	reqs := idp.TokenRequests()
	if len(reqs) == 0 {
		return errors.New("假 IdP 沒有收到 token 請求")
	}
	last := reqs[len(reqs)-1]
	switch {
	case last.PKCEMethod != method:
		return fmt.Errorf("code_challenge_method 是 %q，預期 %q", last.PKCEMethod, method)
	case !last.PKCEOK:
		return errors.New("code_verifier 與 code_challenge 不符")
	case !last.ClientAuthOK:
		return errors.New("token 請求沒有以 client_secret_basic 帶正確的 client secret")
	}
	return nil
}

// jarCSRF 回傳非 GET 請求要帶的 CSRF 標頭。
func jarCSRF(spec string, jar map[string]string) map[string]string {
	method, _, err := parseRequest(spec)
	if err != nil || method == http.MethodGet || method == http.MethodHead {
		return nil
	}
	if v := jar[auth.CSRFCookie]; v != "" {
		return map[string]string{auth.CSRFHeader: v}
	}
	return nil
}

func (s *Scenario) oidcSessionSends(spec string) error {
	if s.signin.oidcJar == nil {
		return errors.New("還沒有開始 OIDC 登入")
	}
	return s.send(spec, requestOpts{who: asClient, jar: s.signin.oidcJar, headers: jarCSRF(spec, s.signin.oidcJar)})
}

// ---- 具名 session ----

func (s *Scenario) namedSession(name string) (*namedSession, error) {
	ns, ok := s.signin.sessions[name]
	if !ok {
		return nil, fmt.Errorf("還沒有登入名稱為 %q 的 session", name)
	}
	return ns, nil
}

func (s *Scenario) namedSessionSignsIn(name, from string) error {
	if err := s.ensureServer(); err != nil {
		return err
	}
	jar := map[string]string{}
	body, err := json.Marshal(map[string]string{"password": s.World.Env["NOTESTEME_PASSWORD"]})
	if err != nil {
		return err
	}
	resp, err := s.do(http.MethodPost, "/api/auth/login", requestOpts{who: asClient, body: body, from: from, jar: jar})
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("session %q 登入失敗：HTTP %d %s", name, resp.status, snippet(resp.body))
	}
	if jar[auth.SessionCookie] == "" {
		return fmt.Errorf("session %q 登入後沒有收到 session cookie", name)
	}
	if s.signin.sessions == nil {
		s.signin.sessions = map[string]*namedSession{}
	}
	s.signin.sessions[name] = &namedSession{from: from, jar: jar, secret: jar[auth.SessionCookie]}
	return nil
}

func (s *Scenario) namedSessionSends(name, spec string) error {
	ns, err := s.namedSession(name)
	if err != nil {
		return err
	}
	return s.send(spec, requestOpts{who: asClient, from: ns.from, jar: ns.jar, headers: jarCSRF(spec, ns.jar)})
}

func (s *Scenario) namedSessionRevoked(name string) error {
	ns, err := s.namedSession(name)
	if err != nil {
		return err
	}
	if err := s.ensureServer(); err != nil {
		return err
	}
	resp, err := s.do(http.MethodGet, "/api/auth/sessions", requestOpts{who: asClient, from: ns.from, jar: ns.jar})
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		if resp.status == http.StatusNotImplemented && isNotImplemented(resp.body) {
			return godog.ErrPending
		}
		return fmt.Errorf("session %q 列出工作階段失敗：HTTP %d %s", name, resp.status, snippet(resp.body))
	}
	var list struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(resp.body, &list); err != nil {
		return fmt.Errorf("解析工作階段列表：%w", err)
	}
	id := ""
	for _, x := range list.Sessions {
		if x.Current {
			id = x.ID
		}
	}
	if id == "" {
		return fmt.Errorf("session %q 的列表中沒有 current 的一筆", name)
	}
	return s.send("DELETE /api/auth/sessions/"+url.PathEscape(id), requestOpts{who: asOwner})
}

func (s *Scenario) sessionListHidesSecret(name string) error {
	ns, err := s.namedSession(name)
	if err != nil {
		return err
	}
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	sum := sha256.Sum256([]byte(ns.secret))
	for _, needle := range []string{
		ns.secret, hex.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]), base64.URLEncoding.EncodeToString(sum[:]),
	} {
		if bytes.Contains(s.last.body, []byte(needle)) {
			return fmt.Errorf("回應含有 session %q 的 secret 或它的 hash", name)
		}
	}
	return nil
}

func (s *Scenario) tokenRotated(name, as string) error {
	if err := s.send("POST /api/tokens/"+url.PathEscape(name)+"/rotate", requestOpts{who: asOwner}); err != nil {
		return err
	}
	if s.last.status != http.StatusOK {
		return nil
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(s.last.body, &out); err != nil || out.Token == "" {
		return nil
	}
	if s.World.Refs == nil {
		s.World.Refs = map[string]string{}
	}
	s.World.Refs["token:"+as] = out.Token
	return nil
}
