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
	"log/slog"
	"net/netip"
	"sync"
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

// 登入鎖定（AUTH-R6）：同一個來源位址連續 maxFailures 次密碼錯誤後，該位址鎖定 lockDuration。
const (
	maxFailures  = 5
	lockDuration = time.Minute
)

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

// LockedError 表示登入被鎖定（AUTH-R6）。訊息與密碼錯誤相同，不透露其他資訊；
// 剩餘時間由 RetryAfter 帶給呼叫端。
type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string { return ErrInvalidPassword.Error() }

// Config 是 Service 的設定。
type Config struct {
	Store *store.Store
	Clock clock.Clock
	// Params 是 argon2id 參數；零值使用 ProductionParams。
	Params HashParams
	// Log 是 log 輸出；nil 時使用 slog.Default()。
	Log *slog.Logger
	// TrustedProxies 是可信任的反向代理位址（設定檔 trusted_proxies）；預設為空。
	// 連線來自這些位址時，來源位址改取 X-Forwarded-For，見 SourceAddr。
	TrustedProxies []netip.Prefix
}

// Service 提供登入、session 驗證與登出。
type Service struct {
	store  *store.Store
	clock  clock.Clock
	params HashParams
	// verify 驗證密碼；測試可替換以計數實際進入驗證的次數。
	verify func(password, encoded string) (bool, error)
	// extend 延長 session 的到期時間；測試可替換以模擬失敗。
	extend func(ctx context.Context, id string, expiresAt time.Time) error
	log    *slog.Logger

	// loginMu 讓同一時間只有一個登入在驗證密碼（全域，不分位址）。
	loginMu sync.Mutex

	// locks 以來源位址記錄登入失敗與鎖定；只存在記憶體，有筆數上限。
	locks   *lockouts
	trusted []netip.Prefix
}

// New 建立 Service。
func New(cfg Config) *Service {
	p := cfg.Params
	if p == (HashParams{}) {
		p = ProductionParams
	}
	lg := cfg.Log
	if lg == nil {
		lg = slog.Default()
	}
	s := &Service{store: cfg.Store, clock: cfg.Clock, params: p, verify: verifyPassword, log: lg,
		locks: newLockouts(maxLockoutRecords), trusted: cfg.TrustedProxies}
	if cfg.Store != nil {
		s.extend = cfg.Store.ExtendSession
	}
	return s
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

// SourceAddr 依設定的 trusted proxies 決定請求的來源位址（AUTH-R6）。
func (s *Service) SourceAddr(remoteAddr string, forwardedFor []string) string {
	return SourceAddr(remoteAddr, forwardedFor, s.trusted)
}

// Login 驗證密碼並建立 session。ip 是 SourceAddr 決定的來源位址，失敗計數以它為單位。
// 同一個位址連續 5 次密碼錯誤後鎖定 1 分鐘，鎖定期間連正確的密碼也拒絕，回傳 *LockedError（AUTH-R6）。
func (s *Service) Login(ctx context.Context, password, ip, userAgent string) (Credentials, error) {
	// 「檢查鎖定 → 驗證 → 記錄失敗」必須是同一個臨界區，否則並行請求會在鎖定生效前全部進入驗證；
	// 序列化同時把並行的 argon2 驗證（每次約 64MiB）限制為一個。鎖定後排隊的請求會直接被拒絕。
	// 序列化是全域的：每個位址各自的臨界區仍不夠，因為 argon2 的記憶體用量是整個程序共用的。

	// 已鎖定的位址不必排隊等別人的驗證，先快速拒絕；真正的判斷在臨界區內再做一次。
	if retry := s.locks.remaining(ip, s.clock.Now()); retry > 0 {
		return Credentials{}, &LockedError{RetryAfter: retry}
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	if retry := s.locks.remaining(ip, s.clock.Now()); retry > 0 {
		return Credentials{}, &LockedError{RetryAfter: retry}
	}
	encoded, ok, err := s.store.GetSetting(ctx, settingPasswordHash)
	if err != nil {
		return Credentials{}, err
	}
	if !ok {
		return Credentials{}, ErrNoPassword
	}
	match, err := s.verify(password, encoded)
	if err != nil {
		return Credentials{}, err
	}
	if !match {
		s.locks.fail(ip, s.clock.Now())
		return Credentials{}, ErrInvalidPassword
	}
	s.locks.reset(ip)

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
		// 延長失敗不影響這次請求：session 仍然有效，下次請求會再試一次。
		if err := s.extend(ctx, sess.ID, next); err != nil {
			s.log.WarnContext(ctx, "延長 session 失敗", "error", err)
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
