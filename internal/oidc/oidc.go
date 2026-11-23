// Package oidc 是 Client 的 OpenID Connect relying party（AUTH-R16～R19）：discovery、JWKS、
// 以 authorization code + PKCE（S256）與 client_secret_basic 換 token，以及 id_token 的驗證。
//
// 和 Hub 用的 internal/idtoken 分開：Client 的規則（RS256 或 ES256、多個 aud 時檢查 azp、exp／iat 必填、
// 時鐘偏差 60 秒）比較嚴格，不改 Hub 的行為。這個套件不 import 任何 domain 套件，也不碰 DB。
//
// 對 IdP 的請求逾時 10 秒、回應上限 1 MiB、不跟隨轉址（AUTH-R16）。錯誤訊息不帶 code、token 與 client secret。
package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 對 IdP 請求的限制（AUTH-R16）。
const (
	RequestTimeout   = 10 * time.Second
	MaxResponseBytes = 1 << 20
	// CacheTTL 是 discovery 與 JWKS 的快取時間；MinJWKSRefresh 是遇到不認得的 kid 時重新取得 JWKS 的最短間隔。
	CacheTTL       = time.Hour
	MinJWKSRefresh = time.Minute
)

// 失敗的原因（AUTH-R18）：呼叫端一律以同一個訊息回應使用者，原因只寫進 log。
var (
	ErrDiscovery  = errors.New("oidc: discovery 失敗")
	ErrJWKS       = errors.New("oidc: 無法取得 JWKS")
	ErrExchange   = errors.New("oidc: 換 token 失敗")
	ErrMalformed  = errors.New("oidc: id_token 格式不正確")
	ErrAlgorithm  = errors.New("oidc: 不接受的簽章演算法")
	ErrUnknownKey = errors.New("oidc: kid 不在 JWKS 中")
	ErrSignature  = errors.New("oidc: 簽章不正確")
	ErrIssuer     = errors.New("oidc: iss 不符")
	ErrAudience   = errors.New("oidc: aud 不符")
	ErrAZP        = errors.New("oidc: azp 不符")
	ErrNonce      = errors.New("oidc: nonce 不符")
	ErrExpired    = errors.New("oidc: 已過期")
	ErrNotYet     = errors.New("oidc: 簽發時間在未來")
)

// Config 是 relying party 的設定（設定檔 auth.oidc 與環境變數 NOTESTEME_OIDC_CLIENT_SECRET）。
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// HTTP 是對 IdP 的 client；Client 會複製它並加上逾時與「不跟隨轉址」。nil 時用 http.DefaultClient 的設定。
	HTTP *http.Client
	// Now 是時間來源；nil 時用 time.Now。
	Now func() time.Time
}

// Provider 是一個 IdP 的 relying party；discovery 與 JWKS 快取在這個實例上（每個 server 一份）。
type Provider struct {
	cfg  Config
	http *http.Client

	mu        sync.Mutex
	disc      *discovery
	discAt    time.Time
	keys      map[string]jwk
	keysAt    time.Time
	keysTried time.Time
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// New 建立 Provider；不連線（第一次需要時才 discovery）。
func New(cfg Config) (*Provider, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: Issuer、ClientID、ClientSecret、RedirectURL 都必須指定")
	}
	if !isHTTPS(cfg.Issuer) {
		return nil, errors.New("oidc: issuer 必須是 https 網址")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := &http.Client{}
	if cfg.HTTP != nil {
		*c = *cfg.HTTP
	}
	c.Timeout = RequestTimeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Provider{cfg: cfg, http: c}, nil
}

// Issuer 回傳設定的 issuer。
func (p *Provider) Issuer() string { return p.cfg.Issuer }

func isHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

// get 以 GET 讀取 JSON，套用大小上限；狀態碼不是 200（含 3xx 轉址）一律失敗。
func (p *Provider) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := readLimited(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxResponseBytes {
		return nil, fmt.Errorf("回應超過 %d bytes", MaxResponseBytes)
	}
	return b, nil
}

// discover 回傳快取的 discovery，過期時重新取得（AUTH-R16：快取 1 小時）。
func (p *Provider) discover(ctx context.Context) (*discovery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.cfg.Now()
	if p.disc != nil && now.Sub(p.discAt) < CacheTTL && !now.Before(p.discAt) {
		return p.disc, nil
	}
	var d discovery
	u := strings.TrimSuffix(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	if err := p.get(ctx, u, &d); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrDiscovery, err)
	}
	if err := d.validate(p.cfg.Issuer); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrDiscovery, err)
	}
	p.disc, p.discAt = &d, now
	return &d, nil
}

func (d *discovery) validate(issuer string) error {
	if d.Issuer != issuer {
		return errors.New("issuer 與設定不同")
	}
	for _, e := range []struct{ name, v string }{
		{"authorization_endpoint", d.AuthorizationEndpoint}, {"token_endpoint", d.TokenEndpoint}, {"jwks_uri", d.JWKSURI},
	} {
		if !isHTTPS(e.v) {
			return fmt.Errorf("%s 必須是 https 網址", e.name)
		}
	}
	return nil
}

// AuthRequest 是一次登入要帶給 IdP 的值（AUTH-R17）。
type AuthRequest struct {
	State    string
	Nonce    string
	Verifier string // PKCE 的 code verifier；URL 中只放它的 S256 challenge
}

// AuthURL 回傳 authorization endpoint 的網址（AUTH-R17）。
func (p *Provider) AuthURL(ctx context.Context, r AuthRequest) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("%w：%w", ErrDiscovery, err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("response_mode", "query")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("scope", "openid")
	q.Set("state", r.State)
	q.Set("nonce", r.Nonce)
	q.Set("code_challenge", S256(r.Verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// S256 回傳 PKCE 的 S256 challenge（RFC 7636）。
func S256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Exchange 以 code 與 code verifier 換 token（client_secret_basic），回傳 id_token。
func (p *Provider) Exchange(ctx context.Context, code, verifier string) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURL},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w：%w", ErrExchange, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// RFC 6749 §2.3.1：帳號與密碼先以 form-urlencoded 編碼，再做 Basic。
	req.SetBasicAuth(url.QueryEscape(p.cfg.ClientID), url.QueryEscape(p.cfg.ClientSecret))
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w：%w", ErrExchange, redactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w：HTTP %d", ErrExchange, resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return "", fmt.Errorf("%w：回應不是 JSON", ErrExchange)
	}
	b, err := readLimited(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w：%w", ErrExchange, err)
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(b, &tr); err != nil || tr.IDToken == "" {
		return "", fmt.Errorf("%w：回應沒有 id_token", ErrExchange)
	}
	return tr.IDToken, nil
}

// redactURLError 去掉 *url.Error 中的網址（token endpoint 本身不是 secret，但保持 log 精簡）。
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// keyFor 回傳 kid 對應的金鑰：快取過期時重新取得；kid 不認得時重新取得，但每分鐘最多一次（AUTH-R16）。
func (p *Provider) keyFor(ctx context.Context, kid string) (jwk, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return jwk{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.cfg.Now()
	fresh := p.keys != nil && now.Sub(p.keysAt) < CacheTTL && !now.Before(p.keysAt)
	if k, ok := p.keys[kid]; ok && fresh {
		return k, nil
	}
	if !fresh || p.keysTried.IsZero() || now.Sub(p.keysTried) >= MinJWKSRefresh || now.Before(p.keysTried) {
		p.keysTried = now
		var raw json.RawMessage
		if err := p.get(ctx, d.JWKSURI, &raw); err != nil {
			return jwk{}, fmt.Errorf("%w：%w", ErrJWKS, err)
		}
		keys, err := ParseJWKS(raw)
		if err != nil {
			return jwk{}, err
		}
		p.keys, p.keysAt = keys, now
	}
	if k, ok := p.keys[kid]; ok {
		return k, nil
	}
	return jwk{}, ErrUnknownKey
}

// Claims 是驗證後的 id_token 內容（只留下 Notesteme 用到的）。
type Claims struct {
	Issuer  string
	Subject string
}

// Verify 驗證 id_token（AUTH-R18）：alg、kid、簽章、iss、aud／azp、nonce、exp、iat。
func (p *Provider) Verify(ctx context.Context, raw, nonce string) (Claims, error) {
	t, err := parseJWT(raw)
	if err != nil {
		return Claims{}, err
	}
	key, err := p.keyFor(ctx, t.header.Kid)
	if err != nil {
		return Claims{}, err
	}
	if err := t.verifySignature(key); err != nil {
		return Claims{}, err
	}
	return t.claims.check(Expect{Issuer: p.cfg.Issuer, ClientID: p.cfg.ClientID, Nonce: nonce, Now: p.cfg.Now()})
}
