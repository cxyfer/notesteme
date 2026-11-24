// Package hubauth 是 Hub 的登入（HACC-R1～R8）：OAuth 授權碼流程（一律 PKCE S256，Google 另有 OIDC nonce）、
// 帳號連結與移除、瀏覽器 session 與 CSRF。HTTP 的接線在 hubapi；這裡不處理權限（S2）。
//
// 安全重點：
//   - state 是 32 bytes 的隨機值，cookie notesteme_hub_oauth 與 oauth_states（只存 hash）都要相符，取用是原子操作，只能用一次。
//   - PKCE verifier 以 session 金鑰（NOTESTEME_HUB_SESSION_KEY 以 HKDF 推導）加密存放，AAD 綁定 state 與供應商。
//   - redirect_uri 一律由設定的 public_url 組成，不看 Host 標頭；登入後轉址的目的地固定（/、/account），不接受參數。
//   - 對供應商的請求只接受 https、不跟隨轉址、10 秒逾時；錯誤與 log 不帶 code、token、client secret 或回應內容。
package hubauth

import (
	"context"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/idtoken"
	"github.com/cxyfer/notesteme/internal/websession"
)

// Cookie 名稱（HACC-R2、R4、R8）。一律用 __Host- 前綴（安全修補 A）：瀏覽器只接受帶 Secure、Path=/、沒有 Domain 的
// 這種 cookie，子網域或 http 的頁面不能寫入或覆蓋它們（防 login CSRF 與 session fixation）。Hub 的 cookie 本來就一律
// Secure；瀏覽器把 http://localhost 視為安全來源，本機開發同樣可用，所以沒有無前綴的退路。
const (
	OAuthCookie   = "__Host-notesteme_hub_oauth"
	SessionCookie = "__Host-notesteme_hub_session"
	CSRFCookie    = "__Host-notesteme_hub_csrf"
	CSRFHeader    = websession.CSRFHeader
	// OAuthCookiePath 是等待中登入的 cookie 路徑：__Host- 前綴要求 Path=/。
	OAuthCookiePath = "/"
)

// 時間（HACC-R2、R3、R6、R8）。
const (
	StateTTL = 10 * time.Minute
	// ReauthWindow 是連結登入方式時 session 登入的時間上限（HACC-R6、A3）。
	ReauthWindow       = 10 * time.Minute
	SessionIdleTimeout = 30 * 24 * time.Hour
	// SessionMaxLifetime 是 session 的絕對期限（安全修補 A）：從登入起算，持續使用也不會超過。
	SessionMaxLifetime = 90 * 24 * time.Hour
	// sessionTouchInterval 是延長 session 期限的最短間隔，避免每個請求都寫 DB。
	sessionTouchInterval = time.Hour
	// ProviderTimeout 是一次 callback 對供應商的全部請求的時間上限。
	ProviderTimeout = 10 * time.Second
)

// 登入後的轉址目的地（固定值，不接受參數，避免 open redirect）。
const (
	LoginRedirect = "/"
	LinkRedirect  = "/account"
)

// MaxDisplayName 是 display_name 的字元上限（HACC-R4）。
const MaxDisplayName = 40

// 錯誤代碼（HACC 規格）。
const (
	CodeProviderNotConfigured = "provider_not_configured"
	CodeStateInvalid          = "oauth_state_invalid"
	CodeDenied                = "oauth_denied"
	CodeProviderError         = "oauth_provider_error"
	CodeReauthRequired        = "reauth_required"
	CodeProviderLinked        = "provider_already_linked"
	CodeIdentityInUse         = "identity_in_use"
	CodeLastIdentity          = "last_identity"
	CodeUnauthorized          = "unauthorized"
	CodeCSRF                  = "csrf_invalid"
	CodeSuspended             = "account_suspended"
)

// 錯誤。
func errProviderNotConfigured() *apierr.Error {
	return apierr.New(http.StatusNotFound, CodeProviderNotConfigured, "這個登入方式尚未啟用。")
}

func errStateInvalid() *apierr.Error {
	return apierr.New(http.StatusBadRequest, CodeStateInvalid, "登入請求已過期或無效，請重新登入。")
}

func errProvider(p hubconfig.Provider) *apierr.Error {
	return apierr.New(http.StatusBadGateway, CodeProviderError, fmt.Sprintf("無法向 %s 完成登入，請稍後再試。", ProviderName(string(p))))
}

// ErrUnauthorized 是沒有登入或 session 已失效（HACC-R13 的訊息）。
func ErrUnauthorized() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, CodeUnauthorized, "請先登入 Hub。")
}

// ErrCSRF 是 session 請求缺少或帶了不正確的 CSRF token（HACC-R8）。
func ErrCSRF() *apierr.Error {
	return apierr.New(http.StatusForbidden, CodeCSRF, websession.ErrCSRF.Error())
}

// ErrSuspended 是帳號已停用（HACC-R17）。
func ErrSuspended() *apierr.Error {
	return apierr.New(http.StatusForbidden, CodeSuspended, "這個帳號已被停用。")
}

// Deps 是 Service 的依賴。
type Deps struct {
	Config *hubconfig.Config
	Store  hubstore.Store
	Clock  clock.Clock
	Log    *slog.Logger
	// HTTP 是對供應商的 client（經 netguard）；Service 會複製一份並關掉轉址跟隨。
	HTTP *http.Client
}

// Service 是登入與 session。
type Service struct {
	cfg   *hubconfig.Config
	store hubstore.Store
	clock clock.Clock
	log   *slog.Logger
	http  *http.Client
	aead  cipher.AEAD
	jwks  *idtoken.Keys
}

// New 建立 Service。有啟用登入方式卻沒有 session 金鑰時回 hubconfig.ErrNoSessionKey。
func New(d Deps) (*Service, error) {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{}
	}
	s := &Service{cfg: d.Config, store: d.Store, clock: d.Clock, log: d.Log, http: noRedirectClient(d.HTTP)}
	if len(d.Config.EnabledProviders()) > 0 {
		if d.Config.SessionKey == "" {
			return nil, hubconfig.ErrNoSessionKey
		}
		key, err := hkdf.Key(sha256.New, []byte(d.Config.SessionKey), nil, "notesteme-hub oauth verifier v1", chacha20poly1305.KeySize)
		if err != nil {
			return nil, errors.New("hubauth: 無法推導 session 金鑰")
		}
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, errors.New("hubauth: 無法初始化加密")
		}
		s.aead = aead
	}
	s.jwks = &idtoken.Keys{URL: Specs[hubconfig.Google].JWKSURL, Client: s.http, Now: s.clock.Now}
	return s, nil
}

// Provider 是 GET /api/v1/auth/providers 的一項。
type Provider struct {
	ID   string `json:"id" example:"github"`
	Name string `json:"name" example:"GitHub"`
}

// Providers 依 HACC-R1 的順序回傳已啟用的登入方式。
func (s *Service) Providers() []Provider {
	out := []Provider{}
	for _, p := range s.cfg.EnabledProviders() {
		out = append(out, Provider{ID: string(p), Name: ProviderName(string(p))})
	}
	return out
}

// enabled 回傳啟用中的登入方式；不認得或沒有啟用時回 provider_not_configured。
func (s *Service) enabled(provider string) (Spec, hubconfig.OAuthClient, *apierr.Error) {
	p := hubconfig.Provider(provider)
	spec, ok := Specs[p]
	c := s.cfg.OAuth[p]
	if !ok || !c.Enabled() || s.aead == nil {
		return Spec{}, hubconfig.OAuthClient{}, errProviderNotConfigured()
	}
	return spec, c, nil
}

func (s *Service) redirectURI(p hubconfig.Provider) string {
	return s.cfg.PublicURL + "/auth/" + string(p) + "/callback"
}

func (s *Service) needsGitHubOrgs() bool {
	for _, g := range s.cfg.Groups {
		for _, r := range g.Rules {
			if r.Provider == hubconfig.GitHub && len(r.OrgMember) > 0 {
				return true
			}
		}
	}
	return false
}

func hashState(state string) string { return websession.HashToken(state) }

func verifierAAD(stateHash, provider string) []byte {
	return []byte("oauth-verifier\x00" + stateHash + "\x00" + provider)
}

// Begin 開始一次登入（intent login）或連結（intent link，userID 是連結的帳號），回傳供應商的 authorize URL
// 與要放進 notesteme_hub_oauth cookie 的 state（HACC-R2）。
func (s *Service) Begin(ctx context.Context, provider, intent, userID string) (authorizeURL, state string, _ error) {
	spec, client, aerr := s.enabled(provider)
	if aerr != nil {
		return "", "", aerr
	}
	state, err := websession.RandomToken(32)
	if err != nil {
		return "", "", err
	}
	// 48 bytes 的 base64url 剛好 64 個字元（RFC 7636 的 43～128）。
	verifier, err := websession.RandomToken(48)
	if err != nil {
		return "", "", err
	}
	var nonce string
	if spec.OIDC {
		if nonce, err = websession.RandomToken(32); err != nil {
			return "", "", err
		}
	}
	sh := hashState(state)
	sealNonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(sealNonce); err != nil {
		return "", "", err
	}
	sealed := s.aead.Seal(nil, sealNonce, []byte(verifier), verifierAAD(sh, string(spec.ID)))
	now := s.clock.Now()
	if err := s.store.CreateOAuthState(ctx, hubstore.OAuthState{
		StateHash: sh, Provider: string(spec.ID), Intent: intent, UserID: userID,
		VerifierNonce: sealNonce, VerifierSealed: sealed, Nonce: nonce, CreatedAt: now, ExpiresAt: now.Add(StateTTL),
	}); err != nil {
		return "", "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {s.redirectURI(spec.ID)},
		"state":                 {state},
		"code_challenge":        {challengeS256(verifier)},
		"code_challenge_method": {"S256"},
	}
	switch {
	case spec.OIDC:
		q.Set("scope", "openid email profile")
		q.Set("nonce", nonce)
	case spec.ID == hubconfig.GitHub && s.needsGitHubOrgs():
		q.Set("scope", "read:org")
	}
	return spec.AuthorizeURL + "?" + q.Encode(), state, nil
}

func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return encodeB64(sum[:])
}

// Principal 是通過驗證的身分：瀏覽器 session（PAT 為 nil）或 Hub token（PAT 非 nil，Session 與 Token 是零值）。
type Principal struct {
	User    hubstore.User
	Session hubstore.Session
	// Token 是 session token 的明文（只在記憶體中，登出與 CSRF 檢查用）。
	Token string
	// PAT 是用來驗證的 Hub token（HACC-R9）。
	PAT *hubstore.PAT
}

// IsSession 回報這是不是瀏覽器 session。
func (p *Principal) IsSession() bool { return p != nil && p.PAT == nil }

// NewSession 是登入成功時建立的 session；Token 與 CSRF 放進 cookie。
type NewSession struct {
	Token, CSRF string
	ExpiresAt   time.Time
}

// Result 是 callback 的結果。
type Result struct {
	Intent string
	User   hubstore.User
	// Session 只在 intent login 時有值。
	Session *NewSession
	// Redirect 是成功後的轉址目的地（固定值）。
	Redirect string
}

// Complete 處理 callback（HACC-R3～R6）。cookieState 是 notesteme_hub_oauth cookie 的值；current 是這個瀏覽器目前的 session
// （連結時必須是發起連結的帳號）。錯誤一律是 *apierr.Error。
func (s *Service) Complete(ctx context.Context, provider string, q url.Values, cookieState string, current *Principal) (Result, error) {
	spec, client, aerr := s.enabled(provider)
	if aerr != nil {
		return Result{}, aerr
	}
	state := q.Get("state")
	if state == "" || cookieState == "" || subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 {
		return Result{}, errStateInvalid()
	}
	// 先取用 state（原子操作）：重送與並行的 callback 只有一個會往下走。
	st, err := s.store.ConsumeOAuthState(ctx, hashState(state), string(spec.ID))
	if errors.Is(err, hubstore.ErrNotFound) {
		return Result{}, errStateInvalid()
	}
	if err != nil {
		return Result{}, err
	}
	if st.Intent == hubstore.IntentLink && (current == nil || current.User.ID != st.UserID) {
		return Result{}, errStateInvalid()
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return Result{}, apierr.New(http.StatusBadRequest, CodeDenied, "你取消了登入。")
		}
		s.log.WarnContext(ctx, "OAuth 供應商回報錯誤", "provider", spec.ID, "error", safeCode(e))
		return Result{}, errProvider(spec.ID)
	}
	code := q.Get("code")
	if code == "" {
		s.log.WarnContext(ctx, "OAuth callback 沒有 code", "provider", spec.ID)
		return Result{}, errProvider(spec.ID)
	}
	plain, err := s.aead.Open(nil, st.VerifierNonce, st.VerifierSealed, verifierAAD(st.StateHash, st.Provider))
	if err != nil {
		// session 金鑰換過：等待中的登入作廢。
		return Result{}, errStateInvalid()
	}
	pctx, cancel := context.WithTimeout(ctx, ProviderTimeout)
	defer cancel()
	tr, err := s.exchange(pctx, spec, client, code, string(plain))
	if err != nil {
		s.log.WarnContext(ctx, "OAuth 換 token 失敗", "provider", spec.ID, "reason", err.Error())
		return Result{}, errProvider(spec.ID)
	}
	ext, name, err := s.fetchIdentity(pctx, spec, client, tr, st.Nonce)
	if err != nil {
		s.log.WarnContext(ctx, "OAuth 取得身分失敗", "provider", spec.ID, "reason", err.Error())
		return Result{}, errProvider(spec.ID)
	}
	ext.Login = cleanText(ext.Login, 200)
	if st.Intent == hubstore.IntentLink {
		return s.completeLink(ctx, spec, st.UserID, ext)
	}
	user, _, err := s.store.SignIn(ctx, ext, DisplayName(name, spec))
	if err != nil {
		return Result{}, err
	}
	if user.Suspended() {
		return Result{}, ErrSuspended()
	}
	// 每次登入重新評估自動規則與設定檔的 admins（HACC-R11、R12），之後才建立 session。
	if err := s.applySignIn(ctx, user.ID, ext); err != nil {
		return Result{}, err
	}
	// 登入一律建立新的 session；這個瀏覽器原本的 session 作廢（session 輪替）。
	if current != nil {
		if err := s.store.DeleteSession(ctx, websession.HashToken(current.Token)); err != nil {
			return Result{}, err
		}
	}
	ns, err := s.createSession(ctx, user.ID)
	if err != nil {
		return Result{}, err
	}
	return Result{Intent: hubstore.IntentLogin, User: user, Session: &ns, Redirect: LoginRedirect}, nil
}

func (s *Service) completeLink(ctx context.Context, spec Spec, userID string, ext hubstore.ExternalIdentity) (Result, error) {
	_, err := s.store.LinkIdentity(ctx, userID, ext)
	switch {
	case errors.Is(err, hubstore.ErrIdentityInUse):
		return Result{}, apierr.New(http.StatusConflict, CodeIdentityInUse, fmt.Sprintf("這個 %s 帳號已連結到另一個 Hub 帳號。", spec.Name))
	case errors.Is(err, hubstore.ErrProviderLinked):
		return Result{}, errProviderLinked(spec.Name)
	case err != nil:
		return Result{}, err
	}
	user, err := s.store.User(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	// 連結也是以這個供應商完成驗證：同樣評估它的規則（HACC 待決問題 A12）。
	if err := s.applySignIn(ctx, userID, ext); err != nil {
		return Result{}, err
	}
	return Result{Intent: hubstore.IntentLink, User: user, Redirect: LinkRedirect}, nil
}

func errProviderLinked(name string) *apierr.Error {
	return apierr.New(http.StatusConflict, CodeProviderLinked, fmt.Sprintf("這個帳號已經連結了一個 %s 帳號。", name))
}

func (s *Service) createSession(ctx context.Context, userID string) (NewSession, error) {
	token, err := websession.RandomToken(32)
	if err != nil {
		return NewSession{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return NewSession{}, err
	}
	now := s.clock.Now()
	se := hubstore.Session{
		ID: id.String(), UserID: userID, TokenHash: websession.HashToken(token),
		CreatedAt: now, AuthenticatedAt: now, ExpiresAt: now.Add(SessionIdleTimeout),
	}
	if err := s.store.CreateSession(ctx, se); err != nil {
		return NewSession{}, err
	}
	return NewSession{Token: token, CSRF: websession.CSRFFor(token), ExpiresAt: se.ExpiresAt}, nil
}

// errNoSession 表示 session token 無效或已過期（呼叫端回 401）。
var errNoSession = errors.New("hubauth: 沒有有效的 session")

// IsNoSession 回報錯誤是否代表沒有有效的 session。
func IsNoSession(err error) bool { return errors.Is(err, errNoSession) }

// Authenticate 驗證 session token，並在需要時延長閒置期限。帳號已停用時回 ErrSuspended。
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, errNoSession
	}
	se, err := s.store.SessionByHash(ctx, websession.HashToken(token))
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	user, err := s.store.User(ctx, se.UserID)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	if user.Suspended() {
		return nil, ErrSuspended()
	}
	now := s.clock.Now()
	limit := se.CreatedAt.Add(SessionMaxLifetime)
	if !now.Before(limit) {
		// 超過絕對期限：刪掉 session（刪除失敗不影響結果，SessionByHash 之後也會因為 expires_at 拒絕）。
		if err := s.store.DeleteSession(ctx, se.TokenHash); err != nil {
			s.log.WarnContext(ctx, "刪除過期的 Hub session 失敗", "error", err)
		}
		return nil, errNoSession
	}
	next := now.Add(SessionIdleTimeout)
	if next.After(limit) {
		next = limit
	}
	if next.Sub(se.ExpiresAt) >= sessionTouchInterval {
		// 延長失敗不影響這次請求：session 仍然有效，下次請求會再試。
		if err := s.store.TouchSession(ctx, se.ID, next); err != nil {
			s.log.WarnContext(ctx, "延長 Hub session 失敗", "error", err)
		} else {
			se.ExpiresAt = next
		}
	}
	return &Principal{User: user, Session: se, Token: token}, nil
}

// Logout 讓 session 立即失效。
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.store.DeleteSession(ctx, websession.HashToken(p.Token))
}

// BeginLink 檢查能不能連結這個登入方式（HACC-R6），可以時開始連結。
func (s *Service) BeginLink(ctx context.Context, p *Principal, provider string) (authorizeURL, state string, _ error) {
	spec, _, aerr := s.enabled(provider)
	if aerr != nil {
		return "", "", aerr
	}
	if s.clock.Now().Sub(p.Session.AuthenticatedAt) > ReauthWindow {
		return "", "", apierr.New(http.StatusForbidden, CodeReauthRequired, "請重新登入後再連結新的登入方式。")
	}
	ids, err := s.store.Identities(ctx, p.User.ID)
	if err != nil {
		return "", "", err
	}
	if slices.ContainsFunc(ids, func(i hubstore.Identity) bool { return i.Provider == string(spec.ID) }) {
		return "", "", errProviderLinked(spec.Name)
	}
	return s.Begin(ctx, string(spec.ID), hubstore.IntentLink, p.User.ID)
}

// Unlink 移除登入方式（HACC-R7）。
func (s *Service) Unlink(ctx context.Context, p *Principal, provider string) error {
	keep := ""
	if p.IsSession() {
		keep = p.Session.ID
	}
	err := s.store.UnlinkIdentity(ctx, p.User.ID, provider, keep)
	switch {
	case errors.Is(err, hubstore.ErrNotFound):
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "這個帳號沒有連結這個登入方式。")
	case errors.Is(err, hubstore.ErrLastIdentity):
		return apierr.New(http.StatusConflict, CodeLastIdentity, "這是你唯一的登入方式，不能移除。")
	}
	return err
}

// DisplayName 去掉控制字元並截到 MaxDisplayName 個字元（HACC-R4）；結果是空的時用「<供應商> 使用者」。
func DisplayName(name string, spec Spec) string {
	if n := cleanText(name, MaxDisplayName); n != "" {
		return n
	}
	return spec.Name + " 使用者"
}

// cleanText 去掉控制字元與前後空白，最多保留 max 個字元。
func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || r == unicode.ReplacementChar || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
