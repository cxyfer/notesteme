package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

type serveLockedSecretsError struct{}

func (serveLockedSecretsError) Error() string {
	return "請先停止 notesteme serve，再執行此操作。"
}
func (serveLockedSecretsError) Is(target error) bool { return target == store.ErrLocked }

// RotateMasterKey 以 NOTESTEME_MASTER_KEY 解開資料金鑰，再用 env.Vars[newKeyEnv] 重新包裝後寫回 DB（SEC-R8）。
// 在資料目錄的鎖下直接開啟 DB，不呼叫 openCore／secrets.Open；serve 執行中時失敗。
func RotateMasterKey(ctx context.Context, env Env, newKeyEnv string) error {
	if env.Clock == nil {
		return errors.New("app: Env.Clock 不可為 nil")
	}
	currentMaster := env.Vars[envMasterKey]
	if currentMaster == "" {
		return ErrNoMasterKey
	}
	if strings.TrimSpace(newKeyEnv) == "" {
		return errors.New("請指定新 master key 的環境變數名稱。")
	}
	newMaster := env.Vars[newKeyEnv]
	if newMaster == "" {
		return fmt.Errorf("環境變數 %s 未設定或為空。", newKeyEnv)
	}

	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return err
	}
	dataDir := cfg.Data
	if _, err := os.Stat(dataDir); errors.Is(err, fs.ErrNotExist) {
		return secrets.ErrNoDataKey
	} else if err != nil {
		return err
	}
	lock, err := lockDirForSecrets(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	if _, err := os.Stat(filepath.Join(dataDir, store.DBFileName)); errors.Is(err, fs.ErrNotExist) {
		return secrets.ErrNoDataKey
	} else if err != nil {
		return err
	}
	st, err := openStoreForSecrets(ctx, dataDir, env)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	lowCost := env.Test != nil && env.Test.LowCostHashing
	return secrets.RotateMasterKey(ctx, st, currentMaster, newMaster, lowCost)
}

// ResetSecrets 刪除所有加密 secrets 與資料金鑰，並以 NOTESTEME_MASTER_KEY 建立新的資料金鑰（SEC-R9）。
// 不呼叫 openCore／secrets.Open，因此目前的 master key 遺失或錯誤時仍可執行。
// 影音、產出、設定、session、API token 等非 secret 資料皆保留。
func ResetSecrets(ctx context.Context, env Env) error {
	if env.Clock == nil {
		return errors.New("app: Env.Clock 不可為 nil")
	}
	master := env.Vars[envMasterKey]
	if master == "" {
		return ErrNoMasterKey
	}

	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return err
	}
	dataDir := cfg.Data
	if err := os.MkdirAll(dataDir, dataDirPerm); err != nil {
		return fmt.Errorf("建立資料目錄 %s 失敗：%w", dataDir, err)
	}
	lock, err := lockDirForSecrets(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if err := os.Chmod(dataDir, dataDirPerm); err != nil {
		return fmt.Errorf("設定資料目錄 %s 的權限失敗：%w", dataDir, err)
	}

	st, err := openStoreForSecrets(ctx, dataDir, env)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	lowCost := env.Test != nil && env.Test.LowCostHashing
	return secrets.ResetSecrets(ctx, st, master, lowCost)
}

func lockDirForSecrets(dataDir string) (*store.DirLock, error) {
	lock, err := store.LockDir(dataDir)
	if err != nil {
		return nil, secretsLockError(dataDir, err)
	}
	if err := lock.WriteInfo(store.LockInfo{Holder: holderCLI, PID: os.Getpid()}); err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}
	return lock, nil
}

func openStoreForSecrets(ctx context.Context, dataDir string, env Env) (*store.Store, error) {
	var storeOpts store.Options
	if env.Test != nil {
		storeOpts.Migrations = env.Test.Migrations
	}
	return store.Open(ctx, dataDir, env.Clock, storeOpts)
}

func secretsLockError(dir string, err error) error {
	if !errors.Is(err, store.ErrLocked) {
		return err
	}
	if info, ierr := store.ReadLockInfo(dir); ierr == nil && info != nil && info.Holder == holderCLI {
		return &lockedByCLIError{pid: info.PID}
	}
	return serveLockedSecretsError{}
}
