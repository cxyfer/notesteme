// Package fakeoauth 是驗收測試用的假 OAuth 供應商（HACC，M5）：Google（OIDC）、GitHub、Linux.do。
//
// 每個 Provider 是一個 http.Handler，依路徑回應 authorize、token、使用者資訊（Google 另有 JWKS），
// 由 features/steps 以真的主機名稱（Hosts）登記到 fakenet（M5 的實作切片），
// 所以 Hub 寫死在程式中的 endpoint 不需要為測試改成可設定（ADR-0030 第 3 點）。
//
// authorize 不顯示同意畫面：以 NextAs 指定的使用者直接轉回 redirect_uri；DenyNext 讓下一次以
// error=access_denied 轉回。token endpoint 驗證 client 憑證、code、redirect_uri 與 PKCE（S256），
// 並記錄每個 authorize 與 token 請求，讓步驟斷言 Hub 送了正確的 challenge 與 verifier。
//
// Google 的主機名稱與路徑依 2026-11 的公開文件暫定，實作時對照官方文件（HACC 規格待決問題 A1）。
package fakeoauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind 是供應商的種類。
type Kind string

// 供應商。
const (
	Google  Kind = "google"
	GitHub  Kind = "github"
	LinuxDo Kind = "linuxdo"
)

// Hosts 是每個供應商用到的主機名稱（全部都由同一個 Provider handler 回應）。
var Hosts = map[Kind][]string{
	Google:  {"accounts.google.com", "oauth2.googleapis.com", "openidconnect.googleapis.com", "www.googleapis.com"},
	GitHub:  {"github.com", "api.github.com"},
	LinuxDo: {"connect.linux.do"},
}

// 各供應商的路徑。
var paths = map[Kind]struct{ authorize, token, user, orgs, jwks string }{
	Google:  {authorize: "/o/oauth2/v2/auth", token: "/token", user: "/v1/userinfo", jwks: "/oauth2/v3/certs"},
	GitHub:  {authorize: "/login/oauth/authorize", token: "/login/oauth/access_token", user: "/user", orgs: "/user/orgs"},
	LinuxDo: {authorize: "/oauth2/authorize", token: "/oauth2/token", user: "/api/user"},
}

// GoogleIssuer 是假 Google 簽發的 id_token 的 iss。
const GoogleIssuer = "https://accounts.google.com"

// User 是供應商的一個使用者。
type User struct {
	Name          string // 場景中的名稱
	Subject       string // Google 的 sub；GitHub、Linux.do 回傳 NumericID(Subject)
	Login         string
	Email         string
	EmailVerified bool
	TrustLevel    int      // Linux.do
	Orgs          []string // GitHub
}

// DefaultUser 回傳 steps.md 4.30 的預設使用者。
func DefaultUser(kind Kind, name string) User {
	return User{Name: name, Subject: string(kind) + "-" + name, Login: name, Email: name + "@example.com", EmailVerified: true}
}

// AuthRequest 是一個 authorize 請求的紀錄。
type AuthRequest struct {
	Query url.Values
}

// TokenRequest 是一個 token 請求的紀錄。
type TokenRequest struct {
	Form url.Values
	// Challenge、Method 是同一個 code 在 authorize 時收到的 code_challenge 與方法。
	Challenge, Method string
	OK                bool
}

type grant struct {
	user                        string
	redirect, challenge, method string
	nonce                       string
	used                        bool
}

// Provider 是一個假供應商；可以同時被多個 goroutine 使用。
type Provider struct {
	kind         Kind
	clientID     string
	clientSecret string
	now          func() time.Time

	mu      sync.Mutex
	users   map[string]User
	next    string
	deny    bool
	codes   map[string]*grant
	tokens  map[string]string // access token → 使用者名稱
	auths   []AuthRequest
	tokReqs []TokenRequest
	key     *rsa.PrivateKey
}

// New 建立一個假供應商；now 為 nil 時用 time.Now（場景中傳入 fake clock）。
func New(kind Kind, clientID, clientSecret string, now func() time.Time) (*Provider, error) {
	if _, ok := paths[kind]; !ok {
		return nil, fmt.Errorf("fakeoauth: 不支援的供應商 %q", kind)
	}
	if now == nil {
		now = time.Now
	}
	p := &Provider{kind: kind, clientID: clientID, clientSecret: clientSecret, now: now,
		users: map[string]User{}, codes: map[string]*grant{}, tokens: map[string]string{}}
	if kind == Google {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		p.key = k
	}
	return p, nil
}

// SetUser 登記或取代使用者。
func (p *Provider) SetUser(u User) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users[u.Name] = u
}

// User 回傳登記過的使用者（步驟以它為基礎修改欄位）。
func (p *Provider) User(name string) (User, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.users[name]
	return u, ok
}

// NextAs 讓下一次 authorize 以這個使用者完成；使用者不存在時以 DefaultUser 建立。
func (p *Provider) NextAs(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.users[name]; !ok {
		p.users[name] = DefaultUser(p.kind, name)
	}
	p.next = name
}

// DenyNext 讓下一次 authorize 以 error=access_denied 轉回。
func (p *Provider) DenyNext() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deny = true
}

// AuthRequests 回傳收到的 authorize 請求。
func (p *Provider) AuthRequests() []AuthRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.auths)
}

// TokenRequests 回傳收到的 token 請求。
func (p *Provider) TokenRequests() []TokenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.tokReqs)
}

// ValidPKCE 回報最近一次 token 請求是否帶了符合 S256 challenge 的 verifier（steps.md 4.30）。
func (p *Provider) ValidPKCE() bool {
	reqs := p.TokenRequests()
	if len(reqs) == 0 {
		return false
	}
	last := reqs[len(reqs)-1]
	v := last.Form.Get("code_verifier")
	return last.Method == "S256" && v != "" && S256(v) == last.Challenge
}

// S256 回傳 PKCE 的 S256 challenge：BASE64URL(SHA256(verifier))，不含 padding。
func S256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP 實作 http.Handler。
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps := paths[p.kind]
	switch {
	case r.Method == http.MethodGet && r.URL.Path == ps.authorize:
		p.authorize(w, r)
	case r.Method == http.MethodPost && r.URL.Path == ps.token:
		p.token(w, r)
	case r.Method == http.MethodGet && r.URL.Path == ps.user:
		p.userinfo(w, r)
	case ps.orgs != "" && r.Method == http.MethodGet && r.URL.Path == ps.orgs:
		p.orgs(w, r)
	case ps.jwks != "" && r.Method == http.MethodGet && r.URL.Path == ps.jwks:
		p.jwks(w)
	default:
		http.NotFound(w, r)
	}
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p.mu.Lock()
	p.auths = append(p.auths, AuthRequest{Query: q})
	deny, next := p.deny, p.next
	p.deny = false
	p.mu.Unlock()

	redirect := q.Get("redirect_uri")
	if q.Get("client_id") != p.clientID || q.Get("response_type") != "code" || redirect == "" || q.Get("state") == "" {
		http.Error(w, "invalid authorize request", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	back := target.Query()
	back.Set("state", q.Get("state"))
	switch {
	case deny:
		back.Set("error", "access_denied")
	case next == "":
		http.Error(w, "fakeoauth: 沒有指定要以哪個使用者完成 authorize（NextAs）", http.StatusBadRequest)
		return
	default:
		code := randomString()
		p.mu.Lock()
		p.codes[code] = &grant{user: next, redirect: redirect, challenge: q.Get("code_challenge"),
			method: q.Get("code_challenge_method"), nonce: q.Get("nonce")}
		p.mu.Unlock()
		back.Set("code", code)
	}
	target.RawQuery = back.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	form := r.PostForm
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = form.Get("client_id"), form.Get("client_secret")
	}

	p.mu.Lock()
	g := p.codes[form.Get("code")]
	rec := TokenRequest{Form: form}
	if g != nil {
		rec.Challenge, rec.Method = g.challenge, g.method
	}
	fail := func(code string) {
		p.tokReqs = append(p.tokReqs, rec)
		p.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
	}
	switch {
	case id != p.clientID || secret != p.clientSecret:
		fail("invalid_client")
		return
	case form.Get("grant_type") != "authorization_code" || g == nil || g.used || form.Get("redirect_uri") != g.redirect:
		fail("invalid_grant")
		return
	case g.challenge != "" && (g.method != "S256" || S256(form.Get("code_verifier")) != g.challenge):
		fail("invalid_grant")
		return
	}
	g.used = true
	rec.OK = true
	p.tokReqs = append(p.tokReqs, rec)
	access := randomString()
	p.tokens[access] = g.user
	user := p.users[g.user]
	p.mu.Unlock()

	out := map[string]any{"access_token": access, "token_type": "bearer"}
	if p.kind == Google {
		idToken, err := p.idToken(user, g.nonce)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
			return
		}
		out["id_token"] = idToken
		out["expires_in"] = 3600
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Provider) bearerUser(r *http.Request) (User, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		// GitHub 也接受 "token <access token>"。
		tok, ok = strings.CutPrefix(r.Header.Get("Authorization"), "token ")
	}
	if !ok {
		return User{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	name, ok := p.tokens[tok]
	if !ok {
		return User{}, false
	}
	return p.users[name], true
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	u, ok := p.bearerUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	switch p.kind {
	case Google:
		writeJSON(w, http.StatusOK, map[string]any{"sub": u.Subject, "email": u.Email, "email_verified": u.EmailVerified, "name": u.Login})
	case GitHub:
		writeJSON(w, http.StatusOK, map[string]any{"id": NumericID(u.Subject), "login": u.Login, "email": u.Email})
	case LinuxDo:
		writeJSON(w, http.StatusOK, map[string]any{"id": NumericID(u.Subject), "username": u.Login, "email": u.Email, "trust_level": u.TrustLevel, "active": true})
	}
}

// NumericID 回傳 GitHub 與 Linux.do 使用者資訊中的數字 id（兩者的真實 API 都回傳整數）：
// Subject 是數字時直接用；不是數字（例如預設的 github-alice）時用固定的雜湊值，讓預設使用者也能運作。
// 步驟用它推算 Hub 中的 subject。
func NumericID(subject string) int64 {
	if id, err := strconv.ParseInt(subject, 10, 64); err == nil {
		return id
	}
	sum := sha256.Sum256([]byte(subject))
	return new(big.Int).SetBytes(sum[:6]).Int64()
}

func (p *Provider) orgs(w http.ResponseWriter, r *http.Request) {
	u, ok := p.bearerUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	out := []map[string]string{}
	for _, o := range u.Orgs {
		out = append(out, map[string]string{"login": o})
	}
	writeJSON(w, http.StatusOK, out)
}

const keyID = "fakeoauth-1"

func (p *Provider) jwks(w http.ResponseWriter) {
	pub := p.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyID,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// idToken 簽發 RS256 的 id_token。
func (p *Provider) idToken(u User, nonce string) (string, error) {
	now := p.now()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": keyID}
	claims := map[string]any{
		"iss": GoogleIssuer, "aud": p.clientID, "sub": u.Subject, "email": u.Email,
		"email_verified": u.EmailVerified, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	enc := func(v any) (string, error) {
		b, err := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b), err
	}
	h, err := enc(header)
	if err != nil {
		return "", err
	}
	c, err := enc(claims)
	if err != nil {
		return "", err
	}
	signing := h + "." + c
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// PublicKey 回傳 Google 的簽章公鑰（只給單元測試驗證 id_token）。
func (p *Provider) PublicKey() *rsa.PublicKey {
	if p.key == nil {
		return nil
	}
	return &p.key.PublicKey
}
