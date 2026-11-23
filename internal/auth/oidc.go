package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/oidc"
	"github.com/cxyfer/notesteme/internal/store"
)

// OIDC 登入（AUTH-R16～R19）。協定的細節在 internal/oidc；這裡負責登入狀態、帳號對應與 session。

// OIDCLoginTTL 是一次 OIDC 登入（start 到 callback）的有效時間（AUTH-R17）。
const OIDCLoginTTL = 10 * time.Minute

// MaxPendingOIDCLogins 是同時存在的未完成登入上限：start 是免驗證端點，不能讓它無限寫入 DB。
const MaxPendingOIDCLogins = 1000

// OIDCProvider 是 internal/oidc.Provider 的介面（測試可替換）。
type OIDCProvider interface {
	Issuer() string
	AuthURL(ctx context.Context, r oidc.AuthRequest) (string, error)
	Exchange(ctx context.Context, code, verifier string) (string, error)
	Verify(ctx context.Context, rawIDToken, nonce string) (oidc.Claims, error)
}

// OIDCConfig 是 OIDC 登入的設定；Provider 為 nil 代表沒有啟用。
type OIDCConfig struct {
	Provider        OIDCProvider
	AllowedSubjects []string
}

var (
	// ErrOIDCDisabled 表示沒有設定 OIDC（路由回 404，AUTH-R16）。
	ErrOIDCDisabled = errors.New("oidc disabled")
	// ErrOIDCState 表示 state 缺少、不認得、過期、已用過，或與 cookie 不符（AUTH-R18）。
	ErrOIDCState = errors.New("登入請求已失效，請重新登入。")
	// ErrOIDCNotAllowed 表示 sub 不在 allowed_subjects 中（AUTH-R19）。
	ErrOIDCNotAllowed = errors.New("這個帳號不能使用 Notesteme。")
	// ErrOIDCBusy 表示未完成的登入太多。
	ErrOIDCBusy = errors.New("目前有太多尚未完成的登入，請稍後再試。")
)

// OIDCDeniedError 表示 IdP 回傳了 error（AUTH-R18）；Code 已經過 SanitizeOIDCError。
type OIDCDeniedError struct{ Code string }

func (e *OIDCDeniedError) Error() string {
	return "身分提供者拒絕了登入（" + e.Code + "）。"
}

// OIDCFailedError 表示換 token 或驗證 id_token 失敗（AUTH-R18）；Reason 只寫進 log，不回給使用者。
type OIDCFailedError struct{ Reason error }

func (e *OIDCFailedError) Error() string {
	return "無法驗證身分提供者的回應，請重新登入。"
}

func (e *OIDCFailedError) Unwrap() error { return e.Reason }

// SanitizeOIDCError 把 IdP 的 error 參數限制成 [a-z_]、最多 64 字元，其他一律寫成 unknown（AUTH-R18）。
func SanitizeOIDCError(code string) string {
	if code == "" || len(code) > 64 {
		return "unknown"
	}
	for i := 0; i < len(code); i++ {
		if c := code[i]; (c < 'a' || c > 'z') && c != '_' {
			return "unknown"
		}
	}
	return code
}

// OIDCEnabled 回報是否設定了 OIDC。
func (s *Service) OIDCEnabled() bool { return s.oidc.Provider != nil }

// OIDCIssuer 回傳設定的 issuer（沒有啟用時是空字串）。
func (s *Service) OIDCIssuer() string {
	if s.oidc.Provider == nil {
		return ""
	}
	return s.oidc.Provider.Issuer()
}

// OIDCStart 是 OIDCStart 的結果。
type OIDCStart struct {
	// URL 是 IdP 的 authorization endpoint（含參數）。
	URL string
	// Cookie 是 notesteme_oidc 的值，綁定這次登入；DB 只存 hash。
	Cookie    string
	ExpiresAt time.Time
}

// StartOIDC 開始一次 OIDC 登入（AUTH-R17）：產生 state、nonce、code verifier 與綁定用的 cookie，
// 存在 server 端（10 分鐘後失效、只能用一次），回傳要轉去的 authorization endpoint。next 以 SafeNext 檢查。
func (s *Service) StartOIDC(ctx context.Context, next string) (OIDCStart, error) {
	p := s.oidc.Provider
	if p == nil {
		return OIDCStart{}, ErrOIDCDisabled
	}
	var vals [4]string
	for i := range vals {
		v, err := randomToken()
		if err != nil {
			return OIDCStart{}, err
		}
		vals[i] = v
	}
	state, nonce, verifier, cookie := vals[0], vals[1], vals[2], vals[3]
	u, err := p.AuthURL(ctx, oidc.AuthRequest{State: state, Nonce: nonce, Verifier: verifier})
	if err != nil {
		return OIDCStart{}, &OIDCFailedError{Reason: err}
	}
	now := s.clock.Now()
	exp := now.Add(OIDCLoginTTL)
	err = s.store.CreateOIDCLogin(ctx, store.OIDCLogin{
		StateHash: hashToken(state), CookieHash: hashToken(cookie), Nonce: nonce, CodeVerifier: verifier,
		Next: SafeNext(next), CreatedAt: now, ExpiresAt: exp,
	}, MaxPendingOIDCLogins)
	if errors.Is(err, store.ErrTooMany) {
		return OIDCStart{}, ErrOIDCBusy
	}
	if err != nil {
		return OIDCStart{}, err
	}
	return OIDCStart{URL: u, Cookie: cookie, ExpiresAt: exp}, nil
}

// OIDCCallback 是 callback 收到的值。
type OIDCCallback struct {
	State    string
	Cookie   string // notesteme_oidc 的值
	Code     string
	IdPError string // IdP 回傳的 error 參數（原始值）
	IP       string
	UA       string
}

// FinishOIDC 完成 OIDC 登入（AUTH-R18、R19），回傳新 session 與登入後要前往的 next。
//
// 順序：先原子地取出並刪除 state（之後不論成功與否都不能再用），再比對 cookie 與到期時間，
// 然後才看 IdP 的 error、換 token、驗證 id_token、比對 sub。
func (s *Service) FinishOIDC(ctx context.Context, cb OIDCCallback) (Credentials, string, error) {
	p := s.oidc.Provider
	if p == nil {
		return Credentials{}, "", ErrOIDCDisabled
	}
	if cb.State == "" || len(cb.State) > 512 {
		return Credentials{}, "", ErrOIDCState
	}
	login, err := s.store.TakeOIDCLogin(ctx, hashToken(cb.State))
	if errors.Is(err, store.ErrNotFound) {
		return Credentials{}, "", ErrOIDCState
	}
	if err != nil {
		return Credentials{}, "", err
	}
	if !s.clock.Now().Before(login.ExpiresAt) ||
		cb.Cookie == "" || subtle.ConstantTimeCompare([]byte(hashToken(cb.Cookie)), []byte(login.CookieHash)) != 1 {
		return Credentials{}, "", ErrOIDCState
	}
	if cb.IdPError != "" {
		return Credentials{}, "", &OIDCDeniedError{Code: SanitizeOIDCError(cb.IdPError)}
	}
	if cb.Code == "" || len(cb.Code) > 4096 {
		return Credentials{}, "", &OIDCFailedError{Reason: errors.New("callback 沒有 code")}
	}
	raw, err := p.Exchange(ctx, cb.Code, login.CodeVerifier)
	if err != nil {
		return Credentials{}, "", &OIDCFailedError{Reason: err}
	}
	claims, err := p.Verify(ctx, raw, login.Nonce)
	if err != nil {
		return Credentials{}, "", &OIDCFailedError{Reason: err}
	}
	allowed := false
	for _, sub := range s.oidc.AllowedSubjects {
		if proxyValueEqual(claims.Subject, sub) {
			allowed = true
		}
	}
	if !allowed {
		s.log.WarnContext(ctx, "OIDC 登入被拒絕：sub 不在 allowed_subjects 中")
		return Credentials{}, "", ErrOIDCNotAllowed
	}
	cred, err := s.newSession(ctx, store.NewSession{IP: cb.IP, UserAgent: cb.UA, Method: store.SessionOIDC})
	if err != nil {
		return Credentials{}, "", err
	}
	if err := s.store.RecordOIDCIdentity(ctx, claims.Issuer, claims.Subject); err != nil {
		// 登入紀錄寫入失敗不影響這次登入。
		s.log.WarnContext(ctx, "記錄 OIDC 身分失敗", "error", err)
	}
	s.log.InfoContext(ctx, "OIDC 登入：建立 session", "session_id", cred.SessionID, "ip", cb.IP)
	return cred, SafeNext(login.Next), nil
}

// OIDCFailureReason 回傳寫進 log 的失敗原因（不含 code、token 與 client secret）。
func OIDCFailureReason(err error) string {
	var f *OIDCFailedError
	if errors.As(err, &f) && f.Reason != nil {
		return fmt.Sprint(f.Reason)
	}
	return ""
}
