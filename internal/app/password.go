package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
)

// SetPassword 直接設定登入密碼並撤銷所有 session（`notesteme password set`，AUTH-R2）。
//
// 和 secrets 子命令一樣，在資料目錄的鎖下直接開啟 DB：serve 執行中時失敗（store.ErrLocked），
// 不需要目前的密碼、也不需要 master key。資料目錄還不存在時會建立，之後第一次啟動不再使用 NOTESTEME_PASSWORD。
// 回傳撤銷的 session 數。
func SetPassword(ctx context.Context, env Env, newPassword string) (int64, error) {
	if env.Clock == nil {
		return 0, errors.New("app: Env.Clock 不可為 nil")
	}
	if newPassword == "" {
		return 0, auth.ErrEmptyNewPassword
	}
	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return 0, err
	}
	dataDir := cfg.Data
	if err := os.MkdirAll(dataDir, dataDirPerm); err != nil {
		return 0, fmt.Errorf("建立資料目錄 %s 失敗：%w", dataDir, err)
	}
	lock, err := lockDirForSecrets(dataDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = lock.Release() }()
	if err := os.Chmod(dataDir, dataDirPerm); err != nil {
		return 0, fmt.Errorf("設定資料目錄 %s 的權限失敗：%w", dataDir, err)
	}

	st, err := openStoreForSecrets(ctx, dataDir, env)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()

	params := auth.ProductionParams
	if env.Test != nil && env.Test.LowCostHashing {
		params = auth.TestParams
	}
	svc := auth.New(auth.Config{Store: st, Clock: env.Clock, Params: params})
	return svc.SetPassword(ctx, newPassword)
}
