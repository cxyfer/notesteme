package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/netip"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/store"
)

// 反向代理驗證（AUTH-R12～R15）。

// ProxyConfig 是設定檔 auth.proxy；UserHeader 為空代表沒有啟用。
type ProxyConfig struct {
	UserHeader   string
	AllowedUsers []string
}

// MaxProxyUserBytes 是 user_header 的值的上限（AUTH-R13）；超過時忽略整個標頭。
const MaxProxyUserBytes = 256

// proxyUserShown 是 403 訊息中顯示的值的上限（字元數，AUTH-R14）。
const proxyUserShown = 64

// ErrProxyUserNotAllowed 表示受信任的 proxy 帶來的值不在 allowed_users 中（AUTH-R14）。
var ErrProxyUserNotAllowed = errors.New("proxy user not allowed")

// Now 回傳 Service 的時鐘時間（cookie 的 Max-Age 用）。
func (s *Service) Now() time.Time { return s.clock.Now() }

// ProxyEnabled 回報反向代理驗證是否啟用。
func (s *Service) ProxyEnabled() bool { return s.proxy.UserHeader != "" }

// ProxyHeader 回傳設定的標頭名稱（沒有啟用時是空字串）。
func (s *Service) ProxyHeader() string { return s.proxy.UserHeader }

type proxyCtxKey struct{}

type proxyIdentity struct{ user string }

// WithProxyUser 記下這個請求已經判斷過反向代理的標頭：user 是受信任的值，空字串代表沒有（或被忽略）。
// AuthenticateSession 依它決定 proxy session 是否仍然有效（AUTH-R14）。
func WithProxyUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, proxyCtxKey{}, proxyIdentity{user: user})
}

// ProxyUserFrom 取出 WithProxyUser 記下的值；第二個回傳值表示這個請求是否判斷過標頭。
func ProxyUserFrom(ctx context.Context) (string, bool) { return proxyUserFrom(ctx) }

func proxyUserFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(proxyCtxKey{}).(proxyIdentity)
	return v.user, ok
}

// proxyValueEqual 以常數時間比對兩個值；空值一律不相等。
func proxyValueEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ProxyUser 依 AUTH-R13 取出受信任的 user_header 值；沒有啟用、沒有標頭或被忽略時回傳空字串。
//
// remoteAddr 必須是 TCP 對端位址（http.Request.RemoteAddr），不是 X-Forwarded-For 推出來的位址：
// 只有連線本身來自 trusted_proxies 時才相信這個標頭。其他來源送來的、出現多次、超過 256 bytes、
// 含控制字元或不是合法 UTF-8 的值一律忽略，並記一筆 warn log（不記錄值；同一個位址每分鐘最多一筆）。
func (s *Service) ProxyUser(ctx context.Context, remoteAddr string, values []string) string {
	if !s.ProxyEnabled() || len(values) == 0 {
		return ""
	}
	peer := hostOf(remoteAddr)
	addr, err := netip.ParseAddr(peer)
	if err == nil {
		addr = addr.Unmap().WithZone("")
		peer = addr.String()
	}
	if err != nil || !inPrefixes(addr, s.trusted) {
		s.warnProxy(ctx, peer, "忽略來自不受信任位址 "+peer+" 的 "+s.proxy.UserHeader+" 標頭。")
		return ""
	}
	if len(values) != 1 || !validProxyValue(values[0]) {
		s.warnProxy(ctx, peer, "忽略來自 "+peer+" 的 "+s.proxy.UserHeader+" 標頭：標頭出現多次、過長或含控制字元。")
		return ""
	}
	return values[0]
}

// validProxyValue 檢查值的格式（AUTH-R13）：非空、最多 256 bytes、合法 UTF-8、不含控制字元。
func validProxyValue(v string) bool {
	if v == "" || len(v) > MaxProxyUserBytes || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

func (s *Service) warnProxy(ctx context.Context, peer, msg string) {
	if s.proxyLog.allow(peer, s.clock.Now()) {
		s.log.WarnContext(ctx, msg)
	}
}

// ProxyUserAllowed 回報值是否在 allowed_users 中（完全相等、區分大小寫，AUTH-R12）。
func (s *Service) ProxyUserAllowed(user string) bool {
	ok := false
	for _, u := range s.proxy.AllowedUsers {
		if proxyValueEqual(user, u) {
			ok = true
		}
	}
	return ok
}

// ProxyUserShown 是 403 訊息中顯示的值：最多 64 個字元（以 rune 截斷，不切斷 UTF-8）。
func ProxyUserShown(user string) string {
	n := 0
	for i := range user {
		if n == proxyUserShown {
			return user[:i]
		}
		n++
	}
	return user
}

// ProxyLogin 為受信任 proxy 帶來的值建立 proxy session（AUTH-R14）；值不在 allowed_users 中時回傳
// ErrProxyUserNotAllowed，不建立 session。
func (s *Service) ProxyLogin(ctx context.Context, user, ip, userAgent string) (Credentials, error) {
	if !s.ProxyEnabled() || !validProxyValue(user) {
		return Credentials{}, ErrUnauthenticated
	}
	if !s.ProxyUserAllowed(user) {
		return Credentials{}, ErrProxyUserNotAllowed
	}
	cred, err := s.newSession(ctx, store.NewSession{IP: ip, UserAgent: userAgent, Method: store.SessionProxy, ProxyUser: user})
	if err != nil {
		return Credentials{}, err
	}
	s.log.InfoContext(ctx, "反向代理登入：建立 session", "session_id", cred.SessionID, "ip", ip)
	return cred, nil
}

// ---- log 節流 ----

const maxLogLimiterRecords = 1024

// logLimiter 讓同一個 key 每分鐘最多一筆 log；筆數有上限，滿了先清掉過期的，仍然滿就不再記錄新的 key。
type logLimiter struct {
	mu   sync.Mutex
	max  int
	last map[string]time.Time
}

func newLogLimiter(max int) *logLimiter {
	return &logLimiter{max: max, last: map[string]time.Time{}}
}

func (l *logLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < time.Minute && !now.Before(t) {
		return false
	}
	if _, ok := l.last[key]; !ok && len(l.last) >= l.max {
		for k, t := range l.last {
			if now.Sub(t) >= time.Minute || now.Before(t) {
				delete(l.last, k)
			}
		}
		if len(l.last) >= l.max {
			return false
		}
	}
	l.last[key] = now
	return true
}
