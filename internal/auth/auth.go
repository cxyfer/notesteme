// Package auth 負責 owner 的密碼登入、session 與 CSRF（AUTH 規格、ADR-0014）。
//
// 密碼只以 argon2id hash 存在 DB；session token 與（之後的）bearer token 只存 hash。
// 時間一律取自注入的 clock，登入鎖定（AUTH-R6）之後也用同一個 clock。
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/websession"
)

// Cookie 與標頭名稱。
const (
	SessionCookie = "notesteme_session"
	CSRFCookie    = "notesteme_csrf"
	CSRFHeader    = websession.CSRFHeader
)

// SessionIdleTimeout 是 session 閒置多久後失效（AUTH-R4）。
const SessionIdleTimeout = 30 * 24 * time.Hour

// sessionTouchInterval 是延長 session 到期時間的最短間隔，避免每個請求都寫 DB。
const sessionTouchInterval = time.Hour

// sessionSeenInterval 是更新 last_seen_at 的最短間隔（AUTH-R20：最多每分鐘一次），和延長到期時間合併成同一次寫入。
const sessionSeenInterval = time.Minute

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
	// Proxy 是反向代理驗證（AUTH-R12～R15）；UserHeader 為空代表沒有啟用。
	Proxy ProxyConfig
	// OIDC 是 OIDC 登入（AUTH-R16～R19）；Provider 為 nil 代表沒有啟用。
	OIDC OIDCConfig
}

// Service 提供登入、session 驗證與登出。
type Service struct {
	store  *store.Store
	clock  clock.Clock
	params HashParams
	// verify 驗證密碼；測試可替換以計數實際進入驗證的次數。
	verify func(password, encoded string) (bool, error)
	// touch 更新 session 的 last_seen_at 與到期時間；測試可替換以模擬失敗。
	touch func(ctx context.Context, id string, lastSeen, expiresAt time.Time) error
	// touchToken 更新 API token 的最後使用時間；測試可替換以計數或模擬失敗。
	touchToken func(ctx context.Context, id string) error
	log        *slog.Logger

	// loginMu 讓同一時間只有一個登入在驗證密碼（全域，不分位址）。
	loginMu sync.Mutex

	// locks 以來源位址記錄登入失敗與鎖定；只存在記憶體，有筆數上限。
	locks   *lockouts
	trusted []netip.Prefix

	proxy    ProxyConfig
	proxyLog *logLimiter
	oidc     OIDCConfig
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
		locks: newLockouts(maxLockoutRecords), trusted: cfg.TrustedProxies,
		proxy: cfg.Proxy, proxyLog: newLogLimiter(maxLogLimiterRecords), oidc: cfg.OIDC}
	if cfg.Store != nil {
		s.touch = cfg.Store.TouchSession
		s.touchToken = cfg.Store.TouchAPIToken
	}
	return s
}

// EnsurePassword 確保 DB 有登入密碼（AUTH-R2、AUTH-R3）：
//   - DB 已有密碼：維持不變，忽略 envPassword（第一次啟動之後改密碼要走設定頁或 CLI）。
//   - DB 沒有、envPassword 非空：以 envPassword 設定。
//   - 兩者都沒有：回傳 ErrNoPassword。
//
// --insecure-no-auth（AUTH-R3）不呼叫這個方法，改用 HasPassword 確認 DB 沒有密碼。
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

// HasPassword 回報 DB 是否已有登入密碼（--insecure-no-auth 的啟動檢查，AUTH-R3）。
func (s *Service) HasPassword(ctx context.Context) (bool, error) {
	_, ok, err := s.store.GetSetting(ctx, settingPasswordHash)
	return ok, err
}

// Credentials 是登入成功後要交給瀏覽器的值。
type Credentials struct {
	// Token 是 session token 的明文，只在這裡出現一次，DB 只存 hash。
	Token string
	// CSRF 是對應的 CSRF token。
	CSRF      string
	ExpiresAt time.Time
	// SessionID 是新 session 的 ID（不是 secret）。
	SessionID string
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

	return s.newSession(ctx, store.NewSession{IP: ip, UserAgent: userAgent, Method: store.SessionPassword})
}

// newSession 產生 session token 並建立 session；DB 只存 hash（AUTH-R4）。
func (s *Service) newSession(ctx context.Context, n store.NewSession) (Credentials, error) {
	token, err := randomToken()
	if err != nil {
		return Credentials{}, err
	}
	n.TokenHash, n.TTL = hashToken(token), SessionIdleTimeout
	sess, err := s.store.CreateSessionWith(ctx, n)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Token: token, CSRF: csrfFor(token), ExpiresAt: sess.ExpiresAt, SessionID: sess.ID}, nil
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
	// Kind 是 "session"、"token" 或 "local"。
	Kind string
	// TokenID 在 Kind == "token" 時記錄 token 的 ID；TokenName 是它的名稱（Agent 的身分，AGT-R10）。
	TokenID   string
	TokenName string
	// Scopes 為 nil 代表擁有全部權限（session／local）；token 列出允許的 scope。
	Scopes []string
	// SessionID 與 Method 在 Kind == "session" 時記錄 session 的 ID 與登入方式（password、proxy、oidc，AUTH-R20）。
	SessionID string
	Method    string
}

// AuthenticateSession 驗證 session token，並在需要時更新 last_seen_at 與延長閒置期限。
// 無效、不存在或已到期時回傳 ErrUnauthenticated。
//
// proxy session（AUTH-R14）只在 ctx 帶著相同的反向代理身分（WithProxyUser）時有效：
// 標頭消失或換成別的值時立即撤銷；ctx 沒有判斷過標頭（例如 /mcp，AUTH-R15）時只拒絕、不撤銷。
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
	if sess.Method == store.SessionProxy {
		user, judged := proxyUserFrom(ctx)
		switch {
		case !s.ProxyEnabled() || (judged && !proxyValueEqual(user, sess.ProxyUser)):
			// 反向代理驗證已關閉，或標頭消失／換人：這個 session 立即失效（AUTH-R14、待決問題 A5）。
			if err := s.store.DeleteSessionByHash(ctx, sess.TokenHash); err != nil {
				return Principal{}, err
			}
			s.log.InfoContext(ctx, "撤銷反向代理的 session：標頭已消失或改變", "session_id", sess.ID)
			return Principal{}, ErrUnauthenticated
		case !judged:
			return Principal{}, ErrUnauthenticated
		}
	}
	now := s.clock.Now()
	next := now.Add(SessionIdleTimeout)
	if next.Sub(sess.ExpiresAt) >= sessionTouchInterval || now.Sub(sess.LastSeenAt) >= sessionSeenInterval || now.Before(sess.LastSeenAt) {
		// 更新失敗不影響這次請求：session 仍然有效，下次請求會再試一次。
		exp := sess.ExpiresAt
		if next.After(exp) {
			exp = next
		}
		if err := s.touch(ctx, sess.ID, now, exp); err != nil {
			s.log.WarnContext(ctx, "更新 session 的使用時間失敗", "error", err)
		}
	}
	return Principal{Kind: "session", SessionID: sess.ID, Method: sess.Method}, nil
}

// CheckCSRF 檢查非 GET 的 session 請求（實作在 internal/websession，與 Hub 共用）。
func CheckCSRF(sessionToken, cookieValue, headerValue string) error {
	if err := websession.CheckCSRF(sessionToken, cookieValue, headerValue); err != nil {
		return ErrCSRF
	}
	return nil
}

// randomToken 產生 256 bits 的隨機 token。
func randomToken() (string, error) { return websession.RandomToken(32) }

// hashToken 回傳 token 的 SHA-256（hex）。
func hashToken(token string) string { return websession.HashToken(token) }

// csrfFor 由 session token 推導 CSRF token。
func csrfFor(sessionToken string) string { return websession.CSRFFor(sessionToken) }
