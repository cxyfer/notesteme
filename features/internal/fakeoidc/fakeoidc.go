// Package fakeoidc 是驗收測試用的假 OpenID Connect provider（AUTH-07，M4）。
//
// 只實作產品會用到的部分（AUTH-R16～R19）：
//
//	GET  /.well-known/openid-configuration   discovery；issuer 是建立時指定的字串，不是 httptest 的 URL
//	GET  /jwks                               JWKS（RS256 公鑰）
//	GET  /authorize                          不顯示畫面，直接以設定的 subject 同意，302 回 redirect_uri
//	POST /token                              authorization_code + PKCE（只接受 S256）+ client_secret_basic
//
// 時間一律取自注入的 Now（場景的 fake clock）。SetFault 讓之後簽發的 id_token 帶有指定的缺陷，
// 用來驗證產品真的檢查了簽章、kid、alg、iss、aud、nonce、exp、iat。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 由 features/steps 負責（M4 的實作切片）。
// 只用標準函式庫，不引入 JWT 套件。
package fakeoidc

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Fault 是 id_token 的缺陷種類。
type Fault string

// 可以設定的缺陷；名稱與 steps.md 4.21 的 `the OIDC provider issues ID tokens with {string}` 對應。
const (
	FaultNone          Fault = ""
	FaultBadSignature  Fault = "a bad signature"
	FaultUnknownKey    Fault = "an unknown key"
	FaultNoSignature   Fault = "no signature"
	FaultHMAC          Fault = "an HMAC signature"
	FaultWrongIssuer   Fault = "the wrong issuer"
	FaultWrongAudience Fault = "the wrong audience"
	FaultWrongNonce    Fault = "the wrong nonce"
	FaultExpired120s   Fault = "an expiry 120s ago"
	FaultExpired30s    Fault = "an expiry 30s ago"
	FaultFutureIat120s Fault = "an issue time 120s ahead"
	FaultFutureIat30s  Fault = "an issue time 30s ahead"
)

// Faults 是所有可以設定的缺陷（FaultNone 除外）。
var Faults = []Fault{
	FaultBadSignature, FaultUnknownKey, FaultNoSignature, FaultHMAC, FaultWrongIssuer,
	FaultWrongAudience, FaultWrongNonce, FaultExpired120s, FaultExpired30s,
	FaultFutureIat120s, FaultFutureIat30s,
}

// ParseFault 把步驟的參數轉成 Fault；不認得時回傳錯誤。
func ParseFault(s string) (Fault, error) {
	for _, f := range Faults {
		if string(f) == s {
			return f, nil
		}
	}
	return FaultNone, fmt.Errorf("fakeoidc: 不認得的 id_token 缺陷 %q", s)
}

// 金鑰 ID。
const (
	KeyID        = "fake-key-1"
	UnknownKeyID = "fake-key-unknown"
)

// Config 是建立假 IdP 的參數。
type Config struct {
	Issuer       string           // discovery 的 issuer，例如 https://id.example.com
	ClientID     string           // 唯一接受的 client_id
	ClientSecret string           // 唯一接受的 client secret（client_secret_basic）
	Subject      string           // authorize 時同意登入的 sub
	Now          func() time.Time // 時間來源；nil 時用 time.Now
}

// TokenRequest 是收到的一個 token 請求的紀錄。
type TokenRequest struct {
	ClientAuthOK bool   // client_secret_basic 的帳密正確
	PKCEMethod   string // authorize 時的 code_challenge_method
	PKCEOK       bool   // code_verifier 依 PKCEMethod 計算後與 code_challenge 相同
	Status       int    // 回應的 HTTP 狀態碼
}

type grant struct {
	clientID    string
	redirectURI string
	nonce       string
	challenge   string
	method      string
	subject     string
	used        bool
}

// Server 是假 IdP 的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	cfg     Config
	key     *rsa.PrivateKey
	unknown *rsa.PrivateKey

	mu       sync.Mutex
	fault    Fault
	deny     string
	grants   map[string]*grant
	tokens   []TokenRequest
	authReqs []url.Values
}

// New 建立假 IdP；產生兩把 RSA 金鑰（一把在 JWKS 中，一把不在）。
func New(cfg Config) (*Server, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("fakeoidc: Issuer、ClientID、ClientSecret 都必須指定")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	unknown, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, key: key, unknown: unknown, grants: map[string]*grant{}}, nil
}

// Issuer 回傳 discovery 的 issuer。
func (s *Server) Issuer() string { return s.cfg.Issuer }

// PublicKey 回傳 JWKS 中的公鑰（單元測試用）。
func (s *Server) PublicKey() *rsa.PublicKey { return &s.key.PublicKey }

// SetSubject 改變之後同意登入的 sub。
func (s *Server) SetSubject(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Subject = sub
}

// SetFault 讓之後簽發的 id_token 帶有指定的缺陷；FaultNone 恢復正常。
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
}

// Deny 讓 authorize 以 error=<code> 轉回 redirect_uri；空字串恢復正常。
func (s *Server) Deny(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deny = code
}

// TokenRequests 回傳收到的全部 token 請求。
func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokens)
}

// AuthorizeRequests 回傳收到的全部 authorize 請求的 query。
func (s *Server) AuthorizeRequests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.authReqs)
}

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		s.discovery(w, r)
	case "/jwks":
		s.jwks(w, r)
	case "/authorize":
		s.authorize(w, r)
	case "/token":
		s.token(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	iss := strings.TrimSuffix(s.cfg.Issuer, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.cfg.Issuer,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"jwks_uri":                              iss + "/jwks",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		"scopes_supported":                      []string{"openid"},
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pub := s.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": KeyID,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return b64(b)
}

// authorize 不顯示畫面：參數正確就直接以設定的 subject 同意。
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	s.authReqs = append(s.authReqs, q)
	deny, subject := s.deny, s.cfg.Subject
	s.mu.Unlock()

	if q.Get("client_id") != s.cfg.ClientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" || redirect.Host == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	back := redirect.Query()
	back.Set("state", q.Get("state"))
	switch {
	case deny != "":
		back.Set("error", deny)
	case q.Get("response_type") != "code":
		back.Set("error", "unsupported_response_type")
	case !slices.Contains(strings.Fields(q.Get("scope")), "openid"):
		back.Set("error", "invalid_scope")
	case q.Get("code_challenge") == "":
		back.Set("error", "invalid_request")
	default:
		method := q.Get("code_challenge_method")
		if method == "" {
			method = "plain" // RFC 7636：省略時是 plain
		}
		code := randomToken()
		s.mu.Lock()
		s.grants[code] = &grant{
			clientID: q.Get("client_id"), redirectURI: q.Get("redirect_uri"), nonce: q.Get("nonce"),
			challenge: q.Get("code_challenge"), method: method, subject: subject,
		}
		s.mu.Unlock()
		back.Set("code", code)
	}
	redirect.RawQuery = back.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// S256 回傳 PKCE 的 S256 challenge。
func S256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64(sum[:])
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	rec := TokenRequest{}
	defer func() {
		s.mu.Lock()
		s.tokens = append(s.tokens, rec)
		s.mu.Unlock()
	}()
	fail := func(status int, code string) {
		rec.Status = status
		writeJSON(w, status, map[string]string{"error": code})
	}
	if r.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "invalid_request")
		return
	}
	user, pass, ok := r.BasicAuth()
	rec.ClientAuthOK = ok && subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.ClientID)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.ClientSecret)) == 1
	if !rec.ClientAuthOK {
		fail(http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		fail(http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	s.mu.Lock()
	g := s.grants[r.PostForm.Get("code")]
	if g != nil && !g.used {
		g.used = true
	} else {
		g = nil
	}
	fault := s.fault
	s.mu.Unlock()
	if g == nil || g.clientID != user || g.redirectURI != r.PostForm.Get("redirect_uri") {
		fail(http.StatusBadRequest, "invalid_grant")
		return
	}
	rec.PKCEMethod = g.method
	// 只接受 S256；plain 一律視為失敗（AUTH-R17 要求 S256）。
	rec.PKCEOK = g.method == "S256" && S256(r.PostForm.Get("code_verifier")) == g.challenge
	if !rec.PKCEOK {
		fail(http.StatusBadRequest, "invalid_grant")
		return
	}
	idToken, err := s.IDToken(g.subject, g.nonce, fault)
	if err != nil {
		fail(http.StatusInternalServerError, "server_error")
		return
	}
	rec.Status = http.StatusOK
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": randomToken(), "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
	})
}

// IDToken 依目前的時間簽發一個 id_token，套用指定的缺陷。
func (s *Server) IDToken(subject, nonce string, fault Fault) (string, error) {
	now := s.cfg.Now()
	iat := now
	exp := now.Add(5 * time.Minute)
	iss, aud := s.cfg.Issuer, s.cfg.ClientID
	switch fault {
	case FaultWrongIssuer:
		iss = "https://evil.example.com"
	case FaultWrongAudience:
		aud = "someone-else"
	case FaultWrongNonce:
		nonce = "not-the-nonce-" + nonce
	case FaultExpired120s:
		iat, exp = now.Add(-10*time.Minute), now.Add(-120*time.Second)
	case FaultExpired30s:
		iat, exp = now.Add(-10*time.Minute), now.Add(-30*time.Second)
	case FaultFutureIat120s:
		iat = now.Add(120 * time.Second)
		exp = iat.Add(5 * time.Minute)
	case FaultFutureIat30s:
		iat = now.Add(30 * time.Second)
		exp = iat.Add(5 * time.Minute)
	}
	claims := map[string]any{
		"iss": iss, "sub": subject, "aud": aud, "iat": iat.Unix(), "exp": exp.Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	header := map[string]string{"typ": "JWT", "alg": "RS256", "kid": KeyID}
	signer := s.key
	switch fault {
	case FaultUnknownKey:
		header["kid"], signer = UnknownKeyID, s.unknown
	case FaultNoSignature:
		header = map[string]string{"typ": "JWT", "alg": "none"}
	case FaultHMAC:
		header = map[string]string{"typ": "JWT", "alg": "HS256", "kid": KeyID}
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(cb)
	var sig []byte
	switch fault {
	case FaultNoSignature:
		sig = nil
	case FaultHMAC:
		m := hmac.New(sha256.New, []byte(s.cfg.ClientSecret))
		m.Write([]byte(signingInput))
		sig = m.Sum(nil)
	default:
		sum := sha256.Sum256([]byte(signingInput))
		sig, err = rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, sum[:])
		if err != nil {
			return "", err
		}
		if fault == FaultBadSignature {
			sig[len(sig)-1] ^= 0xff
		}
	}
	return signingInput + "." + b64(sig), nil
}
