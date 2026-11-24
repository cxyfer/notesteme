package api

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/ops"
)

// vps profile：所有網際網路的登入都經 caddy。trusted_proxies 是 compose 的預設（caddy 的固定位址）時，
// 攻擊者連續錯 5 次只鎖住自己，owner 照常登入（AUTH-R6）；沒有 trusted_proxies 時 owner 會一起被鎖，
// 這正是 deploy-check 與 serve 的警告要擋的情況（OPS-R20）。
func TestVPSDefaultTrustedProxiesKeepOwnerOut(t *testing.T) {
	caddy := netip.MustParsePrefix(ops.DefaultVPSTrustedProxies).Addr().String() + ":40000"
	login := func(s *m4Server, password, client string) int {
		return s.do("POST", "/api/auth/login", `{"password":"`+password+`"}`, func(r *http.Request) {
			r.RemoteAddr = caddy
			r.Header.Set("X-Forwarded-For", client) // caddy 附加它看到的連線位址
		}).Code
	}
	for _, c := range []struct {
		name    string
		trusted []netip.Prefix
		owner   int
	}{
		{"compose 預設的 trusted_proxies", []netip.Prefix{netip.MustParsePrefix(ops.DefaultVPSTrustedProxies)}, http.StatusOK},
		{"沒有 trusted_proxies", nil, http.StatusTooManyRequests},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newM4Server(t, auth.Config{TrustedProxies: c.trusted})
			for i := 0; i < 5; i++ {
				login(s, "wrong", "203.0.113.66")
			}
			if got := login(s, "pw", "198.51.100.7"); got != c.owner {
				t.Fatalf("owner 登入 = %d，預期 %d", got, c.owner)
			}
			if got := login(s, "pw", "203.0.113.66"); got != http.StatusTooManyRequests {
				t.Fatalf("攻擊者自己要被鎖：%d", got)
			}
		})
	}
}
