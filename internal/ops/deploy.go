package ops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/store"
)

// 環境變數（OPS-R19～R21）。
const (
	EnvDomain   = "NOTESTEME_DOMAIN"
	EnvPassword = "NOTESTEME_PASSWORD"
)

// deploy-check vps 的訊息（OPS-R20，逐字）。
const (
	MsgVPSDomain     = "vps profile 需要在 .env 設定 NOTESTEME_DOMAIN（例如 notes.example.com）。"
	MsgVPSDeployment = "vps profile 需要在 .env 設定 NOTESTEME_DEPLOYMENT=vps。"
	MsgVPSPassword   = "vps profile 必須設定登入密碼。請在 .env 設定 NOTESTEME_PASSWORD 後重新啟動。"
	// MsgVPSTrustedProxies：沒有 trusted_proxies 時，所有登入的來源位址都是 Caddy，任何人錯 5 次就讓 owner 一起被鎖（AUTH-R6）。
	MsgVPSTrustedProxies = "vps profile 必須把 Caddy 設成 trusted_proxies。請在 .env 設定 NOTESTEME_TRUSTED_PROXIES（compose 的預設值是 " + DefaultVPSTrustedProxies + "）。"
	MsgVPSOK             = "vps 部署檢查通過。"
)

// DefaultVPSTrustedProxies 是 compose 的 vps profile 預設的 trusted_proxies：Caddy 在 compose 網路中的固定位址
// （deploy/docker/compose.yaml 的 caddy.networks.default.ipv4_address；兩邊由測試確認一致）。
const DefaultVPSTrustedProxies = "172.31.250.10/32"

// passwordSetting 是 DB 中登入密碼雜湊的 settings 鍵（internal/auth）。
const passwordSetting = "owner_password_hash"

// CheckVPS 依序檢查 vps 部署的條件（OPS-R20），遇到第一個問題就回傳它的訊息（ok 為 false）：
// NOTESTEME_DOMAIN 是可以申請憑證的網域、NOTESTEME_DEPLOYMENT 是 vps、DB 中或 NOTESTEME_PASSWORD 有登入密碼、
// trusted_proxies（設定檔或 NOTESTEME_TRUSTED_PROXIES）不是空的。
// hasPassword 回報 DB 中是否已有密碼；只有前兩項通過、而且環境變數沒有密碼時才會呼叫。
// hasTrustedProxies 回報生效的 trusted_proxies 是否不是空的；前三項通過時才會呼叫。
func CheckVPS(env map[string]string, hasPassword, hasTrustedProxies func() (bool, error)) (ok bool, msg string, err error) {
	if !ValidDomain(env[EnvDomain]) {
		return false, MsgVPSDomain, nil
	}
	if env[config.EnvDeployment] != config.DeploymentVPS {
		return false, MsgVPSDeployment, nil
	}
	if env[EnvPassword] == "" {
		has, err := hasPassword()
		if err != nil {
			return false, "", err
		}
		if !has {
			return false, MsgVPSPassword, nil
		}
	}
	has, err := hasTrustedProxies()
	if err != nil {
		return false, "", err
	}
	if !has {
		return false, MsgVPSTrustedProxies, nil
	}
	return true, MsgVPSOK, nil
}

// ValidDomain 回報 d 是不是可以讓 Caddy 申請公開憑證的網域：不是空白、IP、localhost（含 *.localhost），
// 不是網址（不含 scheme、port、路徑），而且至少有一個點。
func ValidDomain(d string) bool {
	d = strings.ToLower(strings.TrimSuffix(d, "."))
	switch {
	case d == "", d == "localhost", strings.HasSuffix(d, ".localhost"):
		return false
	case net.ParseIP(strings.Trim(d, "[]")) != nil:
		return false
	case strings.ContainsAny(d, "/:@?# \t\r\n"), !strings.Contains(d, "."):
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

// PasswordInDB 以唯讀方式檢查資料目錄的 DB 是否已有登入密碼：不取得資料目錄的鎖、不 migrate、不寫入任何檔案
// （vps-check 服務以唯讀方式掛載資料目錄，CLI-R2 的例外）。資料目錄還沒有 DB 時視為沒有密碼。
// DB 存在但打不開時回傳錯誤，不當成「沒有密碼」。
func PasswordInDB(ctx context.Context, dataDir string) (bool, error) {
	path := filepath.Join(dataDir, store.DBFileName)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("讀取資料庫 %s 失敗：%w", path, err)
	}
	has, err := passwordFrom(ctx, path, false)
	if err == nil {
		return has, nil
	}
	// 唯讀掛載時 SQLite 可能無法建立 WAL 的 -shm：改以 immutable 開啟（只讀一列設定，不需要和寫入者協調）。
	has, ierr := passwordFrom(ctx, path, true)
	if ierr != nil {
		return false, fmt.Errorf("以唯讀方式開啟資料庫 %s 失敗：%w", path, errors.Join(err, ierr))
	}
	return has, nil
}

func passwordFrom(ctx context.Context, path string, immutable bool) (bool, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	if immutable {
		q.Set("immutable", "1")
	}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(1)")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	var hash string
	err = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordSetting).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hash != "", nil
}
