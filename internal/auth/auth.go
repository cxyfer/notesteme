// Package auth 負責 owner 的密碼登入、session 與 CSRF（AUTH 規格、ADR-0014）。
//
// 密碼只以 argon2id hash 存在 DB；session token 與（之後的）bearer token 只存 hash。
// 時間一律取自注入的 clock，登入鎖定（AUTH-R6）之後也用同一個 clock。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// Cookie 與標頭名稱。
const (
	SessionCookie = "notesteme_session"
	CSRFCookie    = "notesteme_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// SessionIdleTimeout 是 session 閒置多久後失效（AUTH-R4）。
const SessionIdleTimeout = 30 * 24 * time.Hour

// sessionTouchInterval 是延長 session 到期時間的最短間隔，避免每個請求都寫 DB。
const sessionTouchInterval = time.Hour

// settingPasswordHash 是 settings 中存 owner 密碼 hash 的 key。
const settingPasswordHash = "owner_password_hash"

var (
	// ErrNoPassword 表示 DB 沒有密碼、環境變數也沒有提供（AUTH-R3）。
	ErrNoPassword = errors.New("尚未設定登入密碼。請設定 NOTESTEME_PASSWORD 後重新啟動。")
	// ErrInvalidPassword 表示密碼不正確（AUTH-R6：一律這個訊息，不透露其他資訊）。
	ErrInvalidPassword = errors.New("密碼不正確。")
	// ErrUnauthenticated 表示沒有有效的 session 或 token。
	ErrUnauthenticated = errors.New("尚未登入或登入已失效。")
	// ErrCSRF 表示 session 請求缺少或帶了不正確的 CSRF token（AUTH-R5）。
	ErrCSRF = errors.New("CSRF token 缺少或不正確。")
)

// Config 是 Service 的設定。
type Config struct {
	Store *store.Store
	Clock clock.Clock
	// Params 是 argon2id 參數；零值使用 ProductionParams。
	Params HashParams
}

// Service 提供登入、session 驗證與登出。
type Service struct {
	store  *store.Store
	clock  clock.Clock
	params HashParams
}

// New 建立 Service。
func New(cfg Config) *Service {
	p := cfg.Params
	if p == (HashParams{}) {
		p = ProductionParams
	}
	return &Service{store: cfg.Store, clock: cfg.Clock, params: p}
}

// EnsurePassword 確保 DB 有登入密碼（AUTH-R2、AUTH-R3）：
//   - DB 已有密碼：維持不變，忽略 envPassword（第一次啟動之後改密碼要走設定頁或 CLI）。
//   - DB 沒有、envPassword 非空：以 envPassword 設定。
//   - 兩者都沒有：回傳 ErrNoPassword。
//
// TODO: --insecure-no-auth 才允許在沒有密碼時啟動，並持續顯示警告（AUTH-R3）。
func (s *Service) EnsurePassword(ctx context.Context, envPassword string) error {
	if _, ok, err := s.store.GetSetting(ctx, settingPasswordHash); err != nil {
		return err
	} else if ok {
		return nil
	}
	if envPassword == "" {
		return ErrNoPassword
	}
	h, err := hashPassword(envPassword, s.params)
	if err != nil {
		return err
	}
	// 以 IfAbsent 寫入，避免和其他寫入者競爭時覆寫已存在的密碼。
	_, err = s.store.SetSettingIfAbsent(ctx, settingPasswordHash, h)
	return err
}

// Credentials 是登入成功後要交給瀏覽器的值。
type Credentials struct {
	// Token 是 session token 的明文，只在這裡出現一次，DB 只存 hash。
	Token string
	// CSRF 是對應的 CSRF token。
	CSRF      string
	ExpiresAt time.Time
}

// Login 驗證密碼並建立 session。
//
// TODO(AUTH-R6)：連續 5 次錯誤後鎖定 1 分鐘，鎖定期間回傳 429 與剩餘秒數。
func (s *Service) Login(ctx context.Context, password, ip, userAgent string) (Credentials, error) {
	encoded, ok, err := s.store.GetSetting(ctx, settingPasswordHash)
	if err != nil {
		return Credentials{}, err
	}
	if !ok {
		return Credentials{}, ErrNoPassword
	}
	match, err := verifyPassword(password, encoded)
	if err != nil {
		return Credentials{}, err
	}
	if !match {
		return Credentials{}, ErrInvalidPassword
	}

	token, err := randomToken()
	if err != nil {
		return Credentials{}, err
	}
	sess, err := s.store.CreateSession(ctx, hashToken(token), SessionIdleTimeout, ip, userAgent)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Token: token, CSRF: csrfFor(token), ExpiresAt: sess.ExpiresAt}, nil
}

// Logout 讓 session 立即失效；token 不存在時不算錯誤。
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.store.DeleteSessionByHash(ctx, hashToken(sessionToken))
}

// Principal 是通過驗證的身分。
type Principal struct {
	// Kind 是 "session" 或 "token"。
	Kind string
	// Scopes 為 nil 代表擁有全部權限（session）。
	Scopes []string
}

// AuthenticateSession 驗證 session token，並在需要時延長閒置期限。
// 無效、不存在或已到期時回傳 ErrUnauthenticated。
func (s *Service) AuthenticateSession(ctx context.Context, sessionToken string) (Principal, error) {
	if sessionToken == "" {
		return Principal{}, ErrUnauthenticated
	}
	sess, err := s.store.SessionByHash(ctx, hashToken(sessionToken))
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	now := s.clock.Now()
	if next := now.Add(SessionIdleTimeout); next.Sub(sess.ExpiresAt) >= sessionTouchInterval {
		if err := s.store.ExtendSession(ctx, sess.ID, next); err != nil {
			return Principal{}, err
		}
	}
	return Principal{Kind: "session"}, nil
}

// AuthenticateBearer 驗證 bearer token。
//
// TODO(AUTH-R7～R9)：下一段實作 token 的雜湊比對、scope、撤銷與到期；目前一律視為無效。
func (s *Service) AuthenticateBearer(ctx context.Context, token string) (Principal, error) {
	_, _ = ctx, token
	return Principal{}, ErrUnauthenticated
}

// CheckCSRF 檢查非 GET 的 session 請求：標頭必須等於 cookie，且等於由 session token 推導的值。
// 推導值讓攻擊者就算能寫入 cookie（例如子網域），沒有 HttpOnly 的 session token 也無法偽造。
func CheckCSRF(sessionToken, cookieValue, headerValue string) error {
	if sessionToken == "" || cookieValue == "" || headerValue == "" {
		return ErrCSRF
	}
	want := csrfFor(sessionToken)
	if subtle.ConstantTimeCompare([]byte(headerValue), []byte(cookieValue)) != 1 ||
		subtle.ConstantTimeCompare([]byte(headerValue), []byte(want)) != 1 {
		return ErrCSRF
	}
	return nil
}

// randomToken 產生 256 bits 的隨機 token。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken 回傳 token 的 SHA-256（hex）。token 本身是高熵隨機值，不需要慢雜湊。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// csrfFor 由 session token 推導 CSRF token。
func csrfFor(sessionToken string) string {
	sum := sha256.Sum256([]byte("notesteme-csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
