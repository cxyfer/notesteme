package app

import (
	"context"
	"strings"
	"testing"
)

// vps 部署沒有 trusted_proxies 時，啟動記一筆 warn（AUTH-R6：經 Caddy 的登入都是同一個來源，
// 任何人連續輸錯密碼就會讓 owner 一起被鎖）。local 部署或已設定 trusted_proxies 時不記。
func TestServeWarnsVPSWithoutTrustedProxies(t *testing.T) {
	for _, c := range []struct {
		name, deployment, proxies string
		warn                      bool
	}{
		{"vps 沒有 trusted_proxies", "vps", "", true},
		{"vps 有 trusted_proxies", "vps", "172.31.250.10/32", false},
		{"local 沒有 trusted_proxies", "local", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := testEnv(t)
			env.Vars["NOTESTEME_DEPLOYMENT"] = c.deployment
			env.Vars["NOTESTEME_TRUSTED_PROXIES"] = c.proxies
			logs := &lockedBuffer{}
			env.Log = logs
			srv, err := StartServerWith(t.Context(), env, ServeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
			if got := strings.Contains(logs.String(), VPSNoTrustedProxiesWarning); got != c.warn {
				t.Errorf("警告 = %v，預期 %v：%s", got, c.warn, logs)
			}
		})
	}
}
