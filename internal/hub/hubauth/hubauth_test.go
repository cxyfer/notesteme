package hubauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
)

// fakeIdP 以真的主機名稱回應三個供應商的 token、使用者資訊與 JWKS（authorize 由測試直接模擬）。
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]fakeGrant
	// 以下讓測試製造各種供應商的異常。
	tokenStatus   int
	tokenBody     string // 非空時原樣回傳（Content-Type 用 tokenCT）
	tokenCT       string
	idTokenMod    func(claims map[string]any)
	signWith      *rsa.PrivateKey
	userinfoSub   string
	githubUser    map[string]any
	tokenRequests []url.Values
}

type fakeGrant struct {
	challenge, nonce, sub string
}

const (
	testSecret   = "test-client-secret-9a8b7c"
	testAccess   = "test-access-token-1f2e3d"
	testKey      = "0123456789abcdef0123456789abcdef-session"
	testClientID = "client-id"
)

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: k, codes: map[string]fakeGrant{}, tokenStatus: http.StatusOK}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client 把所有主機名稱導到假供應商（測試的 TLS 憑證不驗證主機名稱）。
func (f *fakeIdP) client() *http.Client {
	addr := f.srv.Listener.Addr().String()
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 只在測試中連到 httptest
	}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeIdP) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Host + r.URL.Path {
	case "oauth2.googleapis.com/token", "github.com/login/oauth/access_token", "connect.linux.do/oauth2/token":
		_ = r.ParseForm()
		f.tokenRequests = append(f.tokenRequests, r.PostForm)
		if f.tokenBody != "" {
			w.Header().Set("Content-Type", f.tokenCT)
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write([]byte(f.tokenBody))
			return
		}
		if f.tokenStatus != http.StatusOK {
			if f.tokenStatus/100 == 3 {
				w.Header().Set("Location", "https://evil.example/steal")
			}
			w.WriteHeader(f.tokenStatus)
			return
		}
		g, ok := f.codes[r.PostForm.Get("code")]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(f.codes, r.PostForm.Get("code"))
		out := map[string]any{"access_token": testAccess, "token_type": "bearer"}
		if r.Host == "oauth2.googleapis.com" {
			out["id_token"] = f.idToken(g)
		}
		writeJSON(w, http.StatusOK, out)
	case "www.googleapis.com/oauth2/v3/certs":
		writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
	case "openidconnect.googleapis.com/v1/userinfo":
		sub := f.userinfoSub
		if sub == "" {
			sub = "g-alice"
		}
		writeJSON(w, http.StatusOK, map[string]any{"sub": sub, "name": "Alice", "email": "alice@example.com"})
	case "api.github.com/user":
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Authorization") != "Bearer "+testAccess {
			writeJSON(w, http.StatusForbidden, map[string]string{"message": "no"})
			return
		}
		u := f.githubUser
		if u == nil {
			u = map[string]any{"id": 42, "login": "alice"}
		}
		writeJSON(w, http.StatusOK, u)
	case "api.github.com/user/orgs":
		writeJSON(w, http.StatusOK, []map[string]any{{"login": "friends", "id": 4242}})
	case "connect.linux.do/api/user":
		if _, _, ok := r.BasicAuth(); ok {
			writeJSON(w, http.StatusBadRequest, nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": 7, "username": "alice", "trust_level": 2})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeIdP) idToken(g fakeGrant) string {
	now := time.Now()
	claims := map[string]any{
		"iss": "https://accounts.google.com", "aud": testClientID, "sub": g.sub, "email": "alice@example.com",
		"email_verified": true, "hd": "example.com", "nonce": g.nonce, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	if f.idTokenMod != nil {
		f.idTokenMod(claims)
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	in := enc(map[string]string{"alg": "RS256", "kid": "k1"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(in))
	key := f.key
	if f.signWith != nil {
		key = f.signWith
	}
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize 模擬使用者在供應商同意：記下 code 對應的 challenge 與 nonce，回傳 callback 的 query。
func (f *fakeIdP) authorize(t *testing.T, authorizeURL, sub string) url.Values {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	code := "code-" + q.Get("state")[:8]
	f.mu.Lock()
	f.codes[code] = fakeGrant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), sub: sub}
	f.mu.Unlock()
	return url.Values{"code": {code}, "state": {q.Get("state")}}
}

type env struct {
	svc   *Service
	store hubstore.Store
	clk   *clock.Fake
	idp   *fakeIdP
	log   *bytes.Buffer
	cfg   *hubconfig.Config
}

func testConfig() *hubconfig.Config {
	oauth := map[hubconfig.Provider]hubconfig.OAuthClient{}
	for _, p := range hubconfig.Providers {
		oauth[p] = hubconfig.OAuthClient{ClientID: testClientID, ClientSecret: testSecret}
	}
	return &hubconfig.Config{
		PublicURL: "https://hub.example.org", OAuth: oauth, SessionKey: testKey,
		Groups: map[string]hubconfig.Group{hubconfig.GroupMembers: {Name: "members", Permissions: []string{"read", "download"}}},
	}
}

func newEnv(t *testing.T, mutate ...func(*hubconfig.Config)) *env {
	t.Helper()
	cfg := testConfig()
	for _, m := range mutate {
		m(cfg)
	}
	clk := clock.NewFake(time.Now().Truncate(time.Second))
	st, err := sqlite.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	idp := newFakeIdP(t)
	var log bytes.Buffer
	svc, err := New(Deps{Config: cfg, Store: st, Clock: clk, HTTP: idp.client(), Log: slog.New(slog.NewTextHandler(&log, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, store: st, clk: clk, idp: idp, log: &log, cfg: cfg}
}

func code(err error) string {
	var e *apierr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "error:" + err.Error()
}

// login 完成一次登入，回傳結果與錯誤代碼。
func (e *env) login(t *testing.T, provider, sub string) (Result, error) {
	t.Helper()
	u, state, err := e.svc.Begin(context.Background(), provider, hubstore.IntentLogin, "")
	if err != nil {
		t.Fatal(err)
	}
	return e.svc.Complete(context.Background(), provider, e.idp.authorize(t, u, sub), state, nil)
}

func (e *env) accounts(t *testing.T) int64 {
	t.Helper()
	n, err := e.store.CountUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewNeedsSessionKey(t *testing.T) {
	cfg := testConfig()
	cfg.SessionKey = ""
	if _, err := New(Deps{Config: cfg}); !errors.Is(err, hubconfig.ErrNoSessionKey) {
		t.Fatalf("沒有 session 金鑰 = %v", err)
	}
	cfg.OAuth = nil
	if _, err := New(Deps{Config: cfg}); err != nil {
		t.Fatalf("沒有啟用登入方式時不需要金鑰：%v", err)
	}
}

func TestProvidersInOrderAndOnlyEnabled(t *testing.T) {
	e := newEnv(t, func(c *hubconfig.Config) { c.OAuth[hubconfig.Google] = hubconfig.OAuthClient{ClientID: "x"} })
	got := e.svc.Providers()
	if len(got) != 2 || got[0] != (Provider{"github", "GitHub"}) || got[1] != (Provider{"linuxdo", "Linux.do"}) {
		t.Fatalf("Providers = %+v", got)
	}
	for _, p := range []string{"google", "facebook", ""} {
		if _, _, err := e.svc.Begin(context.Background(), p, hubstore.IntentLogin, ""); code(err) != CodeProviderNotConfigured {
			t.Errorf("Begin(%q) = %v", p, err)
		}
	}
}

func TestBeginAuthorizeURL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, state, err := e.svc.Begin(ctx, "google", hubstore.IntentLogin, "")
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	if pu.Scheme+"://"+pu.Host+pu.Path != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Errorf("authorize endpoint = %s", u)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(state)
	switch {
	case q.Get("state") != state || len(raw) != 32:
		t.Errorf("state = %q（%d bytes）", q.Get("state"), len(raw))
	case q.Get("redirect_uri") != "https://hub.example.org/auth/google/callback":
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	case q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43:
		t.Errorf("challenge = %q %q", q.Get("code_challenge"), q.Get("code_challenge_method"))
	case q.Get("scope") != "openid email profile" || q.Get("nonce") == "" || q.Get("response_type") != "code" || q.Get("client_id") != testClientID:
		t.Errorf("query = %v", q)
	}
	// GitHub 只有 org_member 規則時才要 read:org。
	u, _, _ = e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	if q, _ := url.ParseQuery(strings.SplitN(u, "?", 2)[1]); q.Has("scope") || q.Has("nonce") {
		t.Errorf("github 沒有規則時 = %v", q)
	}
	e2 := newEnv(t, func(c *hubconfig.Config) {
		c.Groups["contributors"] = hubconfig.Group{Rules: []hubconfig.Rule{{Provider: hubconfig.GitHub, OrgMember: []string{"friends"}}}}
	})
	u, _, _ = e2.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	if q, _ := url.ParseQuery(strings.SplitN(u, "?", 2)[1]); q.Get("scope") != "read:org" {
		t.Errorf("github 有 org 規則時 scope = %q", q.Get("scope"))
	}
}

func TestLoginEachProvider(t *testing.T) {
	e := newEnv(t, func(c *hubconfig.Config) {
		c.Groups["contributors"] = hubconfig.Group{Rules: []hubconfig.Rule{{Provider: hubconfig.GitHub, OrgMember: []string{"friends"}}}}
	})
	ctx := context.Background()
	for _, c := range []struct{ provider, sub, subject, login, name string }{
		{"google", "g-alice", "g-alice", "alice@example.com", "Alice"},
		{"github", "", "42", "alice", "alice"},
		{"linuxdo", "", "7", "alice", "alice"},
	} {
		res, err := e.login(t, c.provider, c.sub)
		if err != nil || res.Session == nil || res.Redirect != "/" || res.User.DisplayName != c.name {
			t.Fatalf("%s 登入 = %+v, %v", c.provider, res, err)
		}
		ids, _ := e.store.Identities(ctx, res.User.ID)
		if len(ids) != 1 || ids[0].Subject != c.subject || ids[0].Login != c.login {
			t.Errorf("%s 的身分 = %+v", c.provider, ids)
		}
		if c.provider == "github" && (len(ids[0].Attrs.Orgs) != 1 || ids[0].Attrs.Orgs[0] != "friends" || len(ids[0].Attrs.OrgIDs) != 1 || ids[0].Attrs.OrgIDs[0] != "4242") {
			t.Errorf("github orgs = %v %v", ids[0].Attrs.Orgs, ids[0].Attrs.OrgIDs)
		}
		if c.provider == "google" && ids[0].Attrs.HostedDomain != "example.com" {
			t.Errorf("google hd = %q", ids[0].Attrs.HostedDomain)
		}
		if c.provider == "linuxdo" && (ids[0].Attrs.TrustLevel == nil || *ids[0].Attrs.TrustLevel != 2) {
			t.Errorf("linuxdo trust_level = %v", ids[0].Attrs.TrustLevel)
		}
		p, err := e.svc.Authenticate(ctx, res.Session.Token)
		if err != nil || p.User.ID != res.User.ID {
			t.Fatalf("Authenticate = %+v, %v", p, err)
		}
	}
	// 每個 token 請求都帶 PKCE verifier（64 個字元）；Linux.do 以 Basic auth 送憑證，其他放在 form。
	for i, f := range e.idp.tokenRequests {
		if len(f.Get("code_verifier")) != 64 || f.Get("redirect_uri") == "" {
			t.Errorf("token 請求 %d = %v", i, f)
		}
		if (i == 2) == (f.Get("client_secret") != "") {
			t.Errorf("token 請求 %d 的 client 憑證位置不對：%v", i, f.Has("client_secret"))
		}
	}
	if strings.Contains(e.log.String(), testSecret) || strings.Contains(e.log.String(), testAccess) {
		t.Errorf("log 含有秘密：%s", e.log)
	}
}

func TestCallbackStateAttacks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, state, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	q := e.idp.authorize(t, u, "")
	forged := url.Values{"code": q["code"], "state": {state + "x"}}
	cases := []struct {
		name     string
		provider string
		q        url.Values
		cookie   string
	}{
		{"沒有 cookie", "github", q, ""},
		{"cookie 不同", "github", q, "other"},
		{"state 被換掉（cookie 跟著換）", "github", forged, state + "x"},
		{"沒有 state", "github", url.Values{"code": q["code"]}, state},
		{"另一個供應商的 callback", "linuxdo", q, state},
	}
	for _, c := range cases {
		if _, err := e.svc.Complete(ctx, c.provider, c.q, c.cookie, nil); code(err) != CodeStateInvalid {
			t.Errorf("%s = %v", c.name, err)
		}
	}
	// 上面都沒有取用 state：正確的 callback 仍然成功，之後重送一律失敗。
	if _, err := e.svc.Complete(ctx, "github", q, state, nil); err != nil {
		t.Fatalf("正確的 callback = %v", err)
	}
	if _, err := e.svc.Complete(ctx, "github", q, state, nil); code(err) != CodeStateInvalid {
		t.Errorf("重送 = %v", err)
	}
	// 過期。
	u, state, _ = e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	q = e.idp.authorize(t, u, "")
	e.clk.Advance(StateTTL + time.Second)
	if _, err := e.svc.Complete(ctx, "github", q, state, nil); code(err) != CodeStateInvalid {
		t.Errorf("過期 = %v", err)
	}
	// 拒絕：state 已取用，之後同一個 state 帶 code 也失敗。
	u, state, _ = e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	q = e.idp.authorize(t, u, "")
	if _, err := e.svc.Complete(ctx, "github", url.Values{"error": {"access_denied"}, "state": {state}}, state, nil); code(err) != CodeDenied {
		t.Errorf("access_denied = %v", err)
	}
	if _, err := e.svc.Complete(ctx, "github", q, state, nil); code(err) != CodeStateInvalid {
		t.Errorf("拒絕之後再用同一個 state = %v", err)
	}
	// 其他錯誤與沒有 code：502。
	u, state, _ = e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	if _, err := e.svc.Complete(ctx, "github", url.Values{"error": {"server_error\n<script>"}, "state": {state}}, state, nil); code(err) != CodeProviderError {
		t.Errorf("server_error = %v", err)
	}
	if strings.Contains(e.log.String(), "<script>") {
		t.Errorf("log 寫進了供應商回傳的任意內容")
	}
	_ = u
	if n := e.accounts(t); n != 1 {
		t.Errorf("帳號數 = %d，預期只有成功的那一次", n)
	}
}

func TestSessionKeyRotationInvalidatesPendingSignIns(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, state, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	cfg := testConfig()
	cfg.SessionKey = testKey + "-rotated"
	svc2, err := New(Deps{Config: cfg, Store: e.store, Clock: e.clk, HTTP: e.idp.client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Complete(ctx, "github", e.idp.authorize(t, u, ""), state, nil); code(err) != CodeStateInvalid {
		t.Fatalf("換金鑰之後 = %v", err)
	}
}

func TestGoogleIDTokenAttacks(t *testing.T) {
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := []struct {
		name  string
		setup func(f *fakeIdP)
	}{
		{"其他金鑰的簽章", func(f *fakeIdP) { f.signWith = other }},
		{"aud 不符", func(f *fakeIdP) { f.idTokenMod = func(c map[string]any) { c["aud"] = "someone-else" } }},
		{"iss 不符", func(f *fakeIdP) { f.idTokenMod = func(c map[string]any) { c["iss"] = "https://evil.example" } }},
		{"nonce 不符", func(f *fakeIdP) { f.idTokenMod = func(c map[string]any) { c["nonce"] = "replayed" } }},
		{"已過期", func(f *fakeIdP) {
			f.idTokenMod = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }
		}},
		{"userinfo 的 sub 不同", func(f *fakeIdP) { f.userinfoSub = "g-mallory" }},
	}
	for _, c := range cases {
		e := newEnv(t)
		c.setup(e.idp)
		if _, err := e.login(t, "google", "g-alice"); code(err) != CodeProviderError {
			t.Errorf("%s = %v", c.name, err)
		}
		if n := e.accounts(t); n != 0 {
			t.Errorf("%s：建立了 %d 個帳號", c.name, n)
		}
	}
}

func TestTokenEndpointFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeIdP)
		ok    bool
	}{
		{"200 加 error（GitHub 的錯誤格式）", func(f *fakeIdP) { f.tokenBody, f.tokenCT = `{"error":"bad_verification_code"}`, "application/json" }, false},
		{"form-encoded 的成功回應", func(f *fakeIdP) {
			f.tokenBody, f.tokenCT = "access_token="+testAccess+"&token_type=bearer", "application/x-www-form-urlencoded"
		}, true},
		{"HTTP 500", func(f *fakeIdP) { f.tokenStatus = http.StatusInternalServerError }, false},
		{"轉址不跟隨", func(f *fakeIdP) { f.tokenStatus = http.StatusFound }, false},
		{"不是 JSON", func(f *fakeIdP) { f.tokenBody, f.tokenCT = "<html>", "text/html" }, false},
		{"使用者 id 不是數字", func(f *fakeIdP) { f.githubUser = map[string]any{"id": "abc", "login": "alice"} }, false},
		{"使用者沒有 login", func(f *fakeIdP) { f.githubUser = map[string]any{"id": 5} }, false},
	}
	for _, c := range cases {
		e := newEnv(t)
		c.setup(e.idp)
		_, err := e.login(t, "github", "")
		if c.ok != (err == nil) || (!c.ok && code(err) != CodeProviderError) {
			t.Errorf("%s = %v", c.name, err)
		}
		if strings.Contains(e.log.String(), testSecret) || strings.Contains(e.log.String(), "code-") {
			t.Errorf("%s：log 含有 client secret 或授權碼：%s", c.name, e.log)
		}
	}
	// 只接受 https（endpoint 寫死，這裡直接檢查 send）。
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodGet, "http://api.github.com/user", nil)
	if _, _, _, err := e.svc.send(req, "user"); err == nil {
		t.Error("http 的 endpoint 應該被拒絕")
	}
}

// 登入後的轉址目的地固定，callback 的其他參數不影響（open redirect）。
func TestCallbackRedirectIsFixed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, state, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	q := e.idp.authorize(t, u, "")
	q.Set("next", "https://evil.example/")
	q.Set("redirect_uri", "https://evil.example/")
	res, err := e.svc.Complete(ctx, "github", q, state, nil)
	if err != nil || res.Redirect != "/" {
		t.Fatalf("Redirect = %q, %v", res.Redirect, err)
	}
	if got := e.idp.tokenRequests[0].Get("redirect_uri"); got != "https://hub.example.org/auth/github/callback" {
		t.Errorf("token 請求的 redirect_uri = %q", got)
	}
}

func TestLinkFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, _ := e.login(t, "github", "")
	alice, _ := e.svc.Authenticate(ctx, res.Session.Token)
	res, _ = e.login(t, "linuxdo", "")
	bob, _ := e.svc.Authenticate(ctx, res.Session.Token)

	// 已經連結同一個供應商。
	if _, _, err := e.svc.BeginLink(ctx, alice, "github"); code(err) != CodeProviderLinked {
		t.Errorf("連結已有的供應商 = %v", err)
	}
	// 連結的 state 只能由發起連結的帳號完成。
	u, state, err := e.svc.BeginLink(ctx, alice, "google")
	if err != nil {
		t.Fatal(err)
	}
	q := e.idp.authorize(t, u, "g-alice")
	if _, err := e.svc.Complete(ctx, "google", q, state, bob); code(err) != CodeStateInvalid {
		t.Errorf("別的帳號完成連結 = %v", err)
	}
	u, state, _ = e.svc.BeginLink(ctx, alice, "google")
	if _, err := e.svc.Complete(ctx, "google", e.idp.authorize(t, u, "g-alice"), state, nil); code(err) != CodeStateInvalid {
		t.Errorf("沒有登入時完成連結 = %v", err)
	}
	u, state, _ = e.svc.BeginLink(ctx, alice, "google")
	got, err := e.svc.Complete(ctx, "google", e.idp.authorize(t, u, "g-alice"), state, alice)
	if err != nil || got.Redirect != "/account" || got.Session != nil {
		t.Fatalf("連結 = %+v, %v", got, err)
	}
	me, _ := e.svc.Me(ctx, alice)
	if len(me.Identities) != 2 || me.Identities[0].Provider != "github" || me.Identities[1].Provider != "google" {
		t.Errorf("identities = %+v", me.Identities)
	}
	if len(me.Groups) != 1 || me.Groups[0] != "members" || strings.Join(me.Permissions, ",") != "read,download" {
		t.Errorf("groups、permissions = %v %v", me.Groups, me.Permissions)
	}
	// bob 連結 alice 的 GitHub：409，兩個帳號都不變。
	u, state, _ = e.svc.BeginLink(ctx, bob, "github")
	if _, err := e.svc.Complete(ctx, "github", e.idp.authorize(t, u, ""), state, bob); code(err) != CodeIdentityInUse ||
		!strings.Contains(err.Error(), "GitHub") {
		t.Errorf("連結別人的身分 = %v", err)
	}
	// 超過 10 分鐘要重新登入。
	e.clk.Advance(ReauthWindow + time.Second)
	bob, _ = e.svc.Authenticate(ctx, bob.Token)
	if _, _, err := e.svc.BeginLink(ctx, bob, "google"); code(err) != CodeReauthRequired {
		t.Errorf("超過 10 分鐘 = %v", err)
	}
	// 移除。
	if err := e.svc.Unlink(ctx, bob, "linuxdo"); code(err) != CodeLastIdentity {
		t.Errorf("移除唯一的身分 = %v", err)
	}
	if err := e.svc.Unlink(ctx, alice, "linuxdo"); code(err) != apierr.CodeNotFound {
		t.Errorf("移除沒有的身分 = %v", err)
	}
	if err := e.svc.Unlink(ctx, alice, "google"); err != nil {
		t.Errorf("移除 = %v", err)
	}
	// 安全修補 A：發出移除的 session 仍然有效（其他 session 與 Hub token 由 store 一起失效）。
	if _, err := e.svc.Authenticate(ctx, alice.Token); err != nil {
		t.Errorf("移除後自己的 session = %v", err)
	}
	entries, _ := e.store.AuditEntries(ctx, 10)
	if len(entries) != 2 || entries[0].Action != hubstore.AuditIdentityUnlink || entries[1].Action != hubstore.AuditIdentityLink {
		t.Errorf("稽核紀錄 = %+v", entries)
	}
}

func TestSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, _ := e.login(t, "github", "")
	first := res.Session.Token
	p, err := e.svc.Authenticate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	// 再登入一次：新的 session，舊的作廢（輪替）。
	u, state, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	res, err = e.svc.Complete(ctx, "github", e.idp.authorize(t, u, ""), state, p)
	if err != nil || res.Session.Token == first {
		t.Fatalf("再登入 = %+v, %v", res, err)
	}
	if _, err := e.svc.Authenticate(ctx, first); !IsNoSession(err) {
		t.Errorf("輪替後的舊 session = %v", err)
	}
	// 閒置 30 天失效；使用中會延長。
	tok := res.Session.Token
	e.clk.Advance(SessionIdleTimeout - time.Hour)
	if _, err := e.svc.Authenticate(ctx, tok); err != nil {
		t.Fatalf("29 天後 = %v", err)
	}
	e.clk.Advance(2 * time.Hour)
	p, err = e.svc.Authenticate(ctx, tok)
	if err != nil {
		t.Fatalf("延長後 = %v", err)
	}
	if err := e.svc.Logout(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, tok); !IsNoSession(err) {
		t.Errorf("登出後 = %v", err)
	}
	res, _ = e.login(t, "github", "")
	e.clk.Advance(SessionIdleTimeout + time.Second)
	if _, err := e.svc.Authenticate(ctx, res.Session.Token); !IsNoSession(err) {
		t.Errorf("閒置 30 天後 = %v", err)
	}
	for _, bad := range []string{"", "x", strings.Repeat("a", 43)} {
		if _, err := e.svc.Authenticate(ctx, bad); !IsNoSession(err) {
			t.Errorf("Authenticate(%q) = %v", bad, err)
		}
	}
}

func TestDisplayName(t *testing.T) {
	spec := Specs[hubconfig.GitHub]
	cases := map[string]string{
		"alice":                 "alice",
		"  al\x00i​ce\n ":       "alice",
		strings.Repeat("字", 50): strings.Repeat("字", 40),
		"\x01\x02":              "GitHub 使用者",
	}
	for in, want := range cases {
		if got := DisplayName(in, spec); got != want {
			t.Errorf("DisplayName(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestParseTokenResponse(t *testing.T) {
	if tr, err := parseTokenResponse("application/json; charset=utf-8", []byte(`{"access_token":"a","id_token":"i"}`)); err != nil || tr.AccessToken != "a" || tr.IDToken != "i" {
		t.Errorf("JSON = %+v, %v", tr, err)
	}
	if tr, err := parseTokenResponse("application/x-www-form-urlencoded", []byte("access_token=a&scope=")); err != nil || tr.AccessToken != "a" {
		t.Errorf("form = %+v, %v", tr, err)
	}
	if _, err := parseTokenResponse("application/json", []byte("nope")); err == nil {
		t.Error("壞掉的 JSON 應該失敗")
	}
	if got := safeCode("bad code\n<x>" + strings.Repeat("a", 100)); got != "badcodexaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || len(got) > 40 {
		t.Errorf("safeCode = %q", got)
	}
}

func FuzzParseTokenResponse(f *testing.F) {
	f.Add("application/json", []byte(`{"access_token":"a"}`))
	f.Add("application/x-www-form-urlencoded", []byte("access_token=a&error=x"))
	f.Fuzz(func(t *testing.T, ct string, body []byte) {
		_, _ = parseTokenResponse(ct, body)
	})
}

// suspendedStore 讓所有帳號看起來都已停用（停用的路由是 S5；這裡只驗證登入與 session 的檢查）。
type suspendedStore struct{ hubstore.Store }

func (s suspendedStore) User(ctx context.Context, id string) (hubstore.User, error) {
	u, err := s.Store.User(ctx, id)
	t := time.Now()
	u.SuspendedAt = &t
	return u, err
}

func (s suspendedStore) SignIn(ctx context.Context, in hubstore.ExternalIdentity, name string) (hubstore.User, bool, error) {
	u, created, err := s.Store.SignIn(ctx, in, name)
	t := time.Now()
	u.SuspendedAt = &t
	return u, created, err
}

func TestSuspendedAccounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, err := e.login(t, "github", "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{Config: e.cfg, Store: suspendedStore{e.store}, Clock: e.clk, HTTP: e.idp.client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, res.Session.Token); code(err) != CodeSuspended {
		t.Errorf("停用帳號的 session = %v", err)
	}
	u, state, _ := svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	if res, err := svc.Complete(ctx, "github", e.idp.authorize(t, u, ""), state, nil); code(err) != CodeSuspended || res.Session != nil {
		t.Errorf("停用帳號登入 = %+v, %v", res, err)
	}
}

// 授權碼注入：A 的 code 配上 B 的 state 與 cookie，PKCE verifier 不符，供應商拒絕換 token，不建立帳號。
func TestAuthorizationCodeInjection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uA, _, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	_, stateB, _ := e.svc.Begin(ctx, "github", hubstore.IntentLogin, "")
	qA := e.idp.authorize(t, uA, "")
	injected := url.Values{"code": qA["code"], "state": {stateB}}
	if _, err := e.svc.Complete(ctx, "github", injected, stateB, nil); code(err) != CodeProviderError {
		t.Fatalf("授權碼注入 = %v，預期 oauth_provider_error", err)
	}
	if n := e.accounts(t); n != 0 {
		t.Fatalf("建立了 %d 個帳號", n)
	}
}

// 安全修補 A：session 有 90 天的絕對期限，持續使用也不會延長到超過它；之後的延長以絕對期限為上限。
func TestSessionMaxLifetime(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, _ := e.login(t, "github", "")
	tok := res.Session.Token
	for elapsed := time.Duration(0); elapsed < SessionMaxLifetime-48*time.Hour; elapsed += 24 * time.Hour {
		e.clk.Advance(24 * time.Hour)
		p, err := e.svc.Authenticate(ctx, tok)
		if err != nil {
			t.Fatalf("第 %v 天 = %v", elapsed/(24*time.Hour)+1, err)
		}
		if p.Session.ExpiresAt.After(p.Session.CreatedAt.Add(SessionMaxLifetime)) {
			t.Fatalf("延長超過絕對期限：%v", p.Session.ExpiresAt)
		}
	}
	e.clk.Advance(3 * 24 * time.Hour)
	if _, err := e.svc.Authenticate(ctx, tok); !IsNoSession(err) {
		t.Fatalf("超過 90 天 = %v", err)
	}
}
