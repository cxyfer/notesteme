package app

import (
	"errors"
	"fmt"

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

// errInsecureNotImplemented 是 local 部署使用 --insecure-no-auth 的錯誤：AUTH-R3 的無密碼模式還沒有實作，
// 不能默默接受旗標卻照樣要求密碼。
var errInsecureNotImplemented = errors.New("--insecure-no-auth 尚未實作（AUTH-R3）。請設定 NOTESTEME_PASSWORD。")

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

// check 依載入的設定檢查旗標（在密碼檢查之前）：vps 部署一律拒絕 --insecure-no-auth（OPS-R21，AUTH-R3 的例外不適用）。
func (o ServeOptions) check(cfg *config.Config) error {
	if !o.InsecureNoAuth {
		return nil
	}
	if cfg.M4.Deployment == config.DeploymentVPS {
		return ErrVPSInsecure
	}
	return errInsecureNotImplemented
}
