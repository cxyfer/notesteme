package app

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
)

// ServeOptions 是 `notesteme serve` 的旗標。
type ServeOptions struct {
	// InsecureNoAuth 是 --insecure-no-auth（AUTH-R3）。
	InsecureNoAuth bool
}

// flagInsecureNoAuth 是 AUTH-R3 的旗標。
const flagInsecureNoAuth = "--insecure-no-auth"

// ErrVPSInsecure 是 vps 部署使用 --insecure-no-auth 時的錯誤（OPS-R21，逐字）。
var ErrVPSInsecure = errors.New("vps 部署不能使用 --insecure-no-auth。請設定 NOTESTEME_PASSWORD。")

// --insecure-no-auth 的其他啟動條件（AUTH 待決問題 A23，待 owner 追認）。
var (
	// ErrInsecureWithProxy：反向代理或 OIDC 的設定代表實例會經由其他主機存取，不能關掉驗證。
	ErrInsecureWithProxy = errors.New("--insecure-no-auth 不能與 trusted_proxies、auth.proxy 或 auth.oidc 一起使用。")
	// ErrInsecureWithPassword：設定了 NOTESTEME_PASSWORD 時不默默停用驗證。
	ErrInsecureWithPassword = errors.New("設定了 NOTESTEME_PASSWORD，不能同時使用 --insecure-no-auth。請移除其中一個。")
	// ErrInsecureWithStoredPassword：DB 已經有登入密碼時不默默停用 owner 設好的驗證。
	ErrInsecureWithStoredPassword = errors.New("已經設定了登入密碼，不能使用 --insecure-no-auth。請移除這個旗標，用密碼登入。")
)

// insecureListenError 是無驗證模式的 listen 不是 loopback 時的錯誤。
func insecureListenError(listen string) error {
	return fmt.Errorf("--insecure-no-auth 只能在只監聽本機（127.0.0.1、::1 或 localhost）時使用，目前的 listen 是 %q。", listen)
}

// ParseServeArgs 解析 serve 的參數（驗收測試的 harness 也用它）；不認得的參數是錯誤。
func ParseServeArgs(args []string) (ServeOptions, error) { return parseServeArgs(args) }

// parseServeArgs 解析 serve 的參數；不認得的參數是錯誤。
func parseServeArgs(args []string) (ServeOptions, error) {
	var o ServeOptions
	for _, a := range args {
		switch a {
		case flagInsecureNoAuth:
			o.InsecureNoAuth = true
		default:
			return ServeOptions{}, fmt.Errorf("unknown argument %q", a)
		}
	}
	return o, nil
}

// check 依載入的設定檢查旗標（在密碼檢查之前），順序：
//  1. vps 部署一律拒絕（OPS-R21，AUTH-R3 的例外不適用）；
//  2. listen 必須是 loopback；
//  3. 不能有反向代理（trusted_proxies、auth.proxy）或 OIDC 的設定；
//  4. 環境變數不能有密碼（DB 裡的密碼由呼叫端在開啟 DB 之後檢查）。
func (o ServeOptions) check(cfg *config.Config, envPassword string) error {
	if !o.InsecureNoAuth {
		return nil
	}
	if cfg.M4.Deployment == config.DeploymentVPS {
		return ErrVPSInsecure
	}
	if !LoopbackListen(cfg.Listen) {
		return insecureListenError(cfg.Listen)
	}
	if len(cfg.TrustedProxies) > 0 || cfg.M4.Proxy.UserHeader != "" || cfg.M4.OIDC.Issuer != "" {
		return ErrInsecureWithProxy
	}
	if envPassword != "" {
		return ErrInsecureWithPassword
	}
	return nil
}

// LoopbackListen 回報 listen 位址（host:port）是否只綁本機：host 是 localhost 或 loopback 的 IP 字面值。
// 不做 DNS 解析；空的 host（所有介面）、0.0.0.0、:: 與其他名稱一律不是。
func LoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	return api.IsLoopbackHost(host)
}

// insecureWarnEvery 是無驗證模式重複寫警告 log 的間隔（AUTH-R3 的「持續顯示警告」）。
const insecureWarnEvery = time.Hour

// InsecureWarning 是無驗證模式的警告 log（固定文字；listen 另外以欄位記錄）。
const InsecureWarning = "目前以 --insecure-no-auth 啟動：沒有任何登入驗證，這台電腦上的任何程式都能完整操作 Notesteme。請設定 NOTESTEME_PASSWORD 後不帶這個旗標重新啟動。"

// VPSNoTrustedProxiesWarning 是 vps 部署沒有 trusted_proxies 時的啟動警告（AUTH-R6、OPS-R20）：
// 所有經 Caddy 的登入都會被當成同一個來源，任何人錯 5 次密碼就讓 owner 一起被鎖。
const VPSNoTrustedProxiesWarning = "vps 部署沒有設定 trusted_proxies：經反向代理的登入都會被當成同一個來源位址，任何人連續輸錯密碼就會讓 owner 也被鎖定。請設定 NOTESTEME_TRUSTED_PROXIES 或設定檔的 trusted_proxies。"

// warnInsecure 在啟動時寫一筆 Warn，之後每小時再寫一次，直到 server 關閉。
func (s *Server) warnInsecure(env Env, listen string) {
	s.log.WarnContext(s.root, InsecureWarning, "listen", listen)
	go func() {
		for {
			t := env.Clock.NewTimer(insecureWarnEvery)
			select {
			case <-s.root.Done():
				t.Stop()
				return
			case <-t.C():
				s.log.WarnContext(s.root, InsecureWarning, "listen", listen)
			}
		}
	}()
}
