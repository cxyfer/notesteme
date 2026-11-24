// Package idtoken 驗證 OIDC 的 id_token（RS256 的 JWS compact 格式）與 JWKS 快取（HACC-R3）。
//
// 只接受 alg=RS256（拒絕 none、HS256 等，避免演算法混淆）；驗證簽章、iss、aud、exp、iat 與 nonce。
// 時間取自注入的 clock。這個套件不 import 任何 domain 套件：Hub 與之後 Client 的 OIDC（AUTH）都可以使用。
// 錯誤訊息不帶 token 內容。
package idtoken

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// 驗證失敗的原因；呼叫端一律以同一個訊息回應使用者，原因只寫進 log。
var (
	ErrMalformed  = errors.New("idtoken: 格式不正確")
	ErrAlgorithm  = errors.New("idtoken: 不接受的簽章演算法")
	ErrSignature  = errors.New("idtoken: 簽章不正確")
	ErrUnknownKey = errors.New("idtoken: 找不到簽章金鑰")
	ErrIssuer     = errors.New("idtoken: iss 不符")
	ErrAudience   = errors.New("idtoken: aud 不符")
	ErrExpired    = errors.New("idtoken: 已過期")
	ErrNotYet     = errors.New("idtoken: 簽發時間在未來")
	ErrNonce      = errors.New("idtoken: nonce 不符")
	ErrJWKS       = errors.New("idtoken: 無法取得 JWKS")
)

// MaxTokenBytes 是 id_token 的大小上限。
const MaxTokenBytes = 16 << 10

// MaxJWKSBytes 是 JWKS 回應的大小上限。
const MaxJWKSBytes = 64 << 10

// leeway 是時間比對的容許誤差（時鐘不同步）。
const leeway = time.Minute

// Claims 是驗證後的 claim。
type Claims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	// HostedDomain 是 Google Workspace 的網域（hd）；個人帳號沒有。
	HostedDomain string
	Nonce        string
	Expiry       time.Time
	IssuedAt     time.Time
}

// Expect 是驗證條件。
type Expect struct {
	// Issuers 是接受的 iss（Google 同時有 https://accounts.google.com 與 accounts.google.com）。
	Issuers  []string
	Audience string
	// Nonce 必須與 id_token 的 nonce 相同（常數時間比對）；空字串時拒絕（呼叫端一律要送 nonce）。
	Nonce string
	Now   time.Time
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// audience 接受字串或字串陣列（RFC 7519 4.1.3）。
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// boolish 接受 true／false 或 "true"／"false"（部分供應商把 email_verified 寫成字串）。
type boolish bool

func (v *boolish) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", `"true"`:
		*v = true
	case "false", `"false"`, "null":
		*v = false
	default:
		return fmt.Errorf("不是布林值")
	}
	return nil
}

type rawClaims struct {
	Iss           string   `json:"iss"`
	Sub           string   `json:"sub"`
	Aud           audience `json:"aud"`
	Exp           *float64 `json:"exp"`
	Iat           *float64 `json:"iat"`
	Nonce         string   `json:"nonce"`
	Email         string   `json:"email"`
	EmailVerified boolish  `json:"email_verified"`
	Name          string   `json:"name"`
	HD            string   `json:"hd"`
}

// Parsed 是拆開、尚未驗證簽章的 id_token。
type Parsed struct {
	header       header
	claims       rawClaims
	signingInput []byte
	signature    []byte
}

// Kid 回傳 header 的 kid。
func (p *Parsed) Kid() string { return p.header.Kid }

// Parse 拆開 id_token 並檢查格式與演算法；不驗證簽章（見 Verify）。
func Parse(raw string) (*Parsed, error) {
	if len(raw) > MaxTokenBytes {
		return nil, ErrMalformed
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, ErrMalformed
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil {
		return nil, ErrMalformed
	}
	if h.Alg != "RS256" {
		return nil, ErrAlgorithm
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var c rawClaims
	if err := json.Unmarshal(cb, &c); err != nil {
		return nil, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	return &Parsed{header: h, claims: c, signingInput: []byte(parts[0] + "." + parts[1]), signature: sig}, nil
}

// VerifyWith 以公鑰驗證簽章，並檢查 claim。
func (p *Parsed) VerifyWith(key *rsa.PublicKey, want Expect) (Claims, error) {
	if key == nil {
		return Claims{}, ErrUnknownKey
	}
	sum := sha256.Sum256(p.signingInput)
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], p.signature); err != nil {
		return Claims{}, ErrSignature
	}
	c := p.claims
	if !slices.Contains(want.Issuers, c.Iss) {
		return Claims{}, ErrIssuer
	}
	if want.Audience == "" || !slices.Contains([]string(c.Aud), want.Audience) {
		return Claims{}, ErrAudience
	}
	if c.Sub == "" {
		return Claims{}, ErrMalformed
	}
	if c.Exp == nil {
		return Claims{}, ErrExpired
	}
	exp := time.Unix(int64(*c.Exp), 0)
	if !want.Now.Before(exp.Add(leeway)) {
		return Claims{}, ErrExpired
	}
	var iat time.Time
	if c.Iat != nil {
		iat = time.Unix(int64(*c.Iat), 0)
		if iat.After(want.Now.Add(leeway)) {
			return Claims{}, ErrNotYet
		}
	}
	if want.Nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(want.Nonce)) != 1 {
		return Claims{}, ErrNonce
	}
	return Claims{
		Issuer: c.Iss, Subject: c.Sub, Email: c.Email, EmailVerified: bool(c.EmailVerified), Name: c.Name,
		HostedDomain: c.HD, Nonce: c.Nonce, Expiry: exp, IssuedAt: iat,
	}, nil
}

// ---- JWKS ----

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// minRSABits 是接受的 RSA 金鑰長度下限。
const minRSABits = 2048

// ParseJWKS 解析 JWKS，回傳 kid 對應的 RSA 公鑰；略過不是 RSA 簽章用的金鑰。
func ParseJWKS(b []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, ErrJWKS
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") || k.Kid == "" {
			continue
		}
		nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
		eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
			continue
		}
		n := new(big.Int).SetBytes(nb)
		e := int(new(big.Int).SetBytes(eb).Int64())
		if n.BitLen() < minRSABits || e < 3 || e%2 == 0 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: n, E: e}
	}
	return out, nil
}

// Keys 是 JWKS 的快取：kid 找不到或快取過期時重新取得，但兩次取得至少間隔 minRefresh（避免被偽造的 kid 拖著一直抓）。
// 每個 Hub 實例各有一份（不用 package 層級的狀態）。
type Keys struct {
	URL    string
	Client *http.Client
	Now    func() time.Time
	// TTL 是快取的有效時間；零值為 1 小時。
	TTL time.Duration

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

const minRefresh = time.Minute

// Key 回傳 kid 的公鑰。
func (k *Keys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	ttl := k.TTL
	if ttl == 0 {
		ttl = time.Hour
	}
	now := k.Now()
	fresh := k.keys != nil && now.Sub(k.fetched) < ttl
	if key, ok := k.keys[kid]; ok && fresh {
		return key, nil
	}
	if k.keys == nil || !fresh || now.Sub(k.fetched) >= minRefresh {
		keys, err := k.fetch(ctx)
		if err != nil {
			return nil, err
		}
		k.keys, k.fetched = keys, now
	}
	if key, ok := k.keys[kid]; ok {
		return key, nil
	}
	return nil, ErrUnknownKey
}

func (k *Keys) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	if !strings.HasPrefix(k.URL, "https://") {
		return nil, ErrJWKS
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.URL, nil)
	if err != nil {
		return nil, ErrJWKS
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w：%w", ErrJWKS, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w：HTTP %d", ErrJWKS, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxJWKSBytes+1))
	if err != nil || len(b) > MaxJWKSBytes {
		return nil, ErrJWKS
	}
	return ParseJWKS(b)
}

// Verify 拆開 id_token、以 JWKS 驗證簽章並檢查 claim。
func (k *Keys) Verify(ctx context.Context, raw string, want Expect) (Claims, error) {
	p, err := Parse(raw)
	if err != nil {
		return Claims{}, err
	}
	key, err := k.Key(ctx, p.Kid())
	if err != nil {
		return Claims{}, err
	}
	return p.VerifyWith(key, want)
}
