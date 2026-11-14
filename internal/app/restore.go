package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/store"
)

// RestoreResult 是還原完成後要告訴使用者的事。
type RestoreResult struct {
	// SchemaVersion 與 CreatedAt 來自備份的 manifest。
	SchemaVersion int64
	CreatedAt     time.Time
	// PreRestore 是還原前目前資料庫的快照；原本沒有資料庫時為空。
	PreRestore string
	// ConfigDir 是備份中的設定檔解到的目錄；備份沒有設定檔時為空。
	ConfigDir string
}

// ErrServeRunning 表示資料目錄正由 serve 使用，不能還原（OPS-R2）。
var ErrServeRunning = errors.New("請先停止 notesteme serve，再執行 restore。")

// Restore 把備份檔 file 還原成 env 指定的資料目錄（OPS-R2）。不經過 API：只取得資料目錄的鎖（CLI-R2 的例外），
// 所以 serve 執行中時失敗。順序：
//  1. 先取得鎖，再讀備份檔：serve 執行中時不碰備份檔，也不動資料目錄。
//  2. 備份檔不受信任（backup.Extract）；schema 版本比目前程式新時拒絕。
//  3. 還原前先把目前的資料庫存成 backups/pre-restore-<時間>.db，再刪除 -wal、-shm、換上備份的資料庫。
//  4. 設定檔不覆寫 /config（compose 可能以唯讀掛載），解到 backups/restored-config-<時間>/ 讓使用者自行替換。
//
// 資料金鑰是用備份當時的 master key 加密的；master key 輪替過的話，之後 serve 會以 SEC-R5 的訊息拒絕啟動，
// 呼叫端要把這一點告訴使用者。
func Restore(ctx context.Context, env Env, file string) (*RestoreResult, error) {
	if env.Clock == nil {
		return nil, errors.New("app: Env.Clock 不可為 nil")
	}
	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return nil, err
	}
	dataDir := cfg.Data
	if err := os.MkdirAll(dataDir, dataDirPerm); err != nil {
		return nil, fmt.Errorf("建立資料目錄 %s 失敗：%w", dataDir, err)
	}
	lock, err := store.LockDir(dataDir)
	if err != nil {
		return nil, restoreLockError(dataDir, err)
	}
	defer func() { _ = lock.Release() }()
	if err := os.Chmod(dataDir, dataDirPerm); err != nil {
		return nil, fmt.Errorf("設定資料目錄 %s 的權限失敗：%w", dataDir, err)
	}
	if err := lock.WriteInfo(store.LockInfo{Holder: holderCLI, PID: os.Getpid()}); err != nil {
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}

	in, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("無法讀取備份檔 %s：%w", file, err)
	}
	defer in.Close()

	// 解開的內容放在資料目錄的 tmp/（同一個磁碟，最後用 rename 換上）；結束時一律清掉。
	tmpRoot := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dataDirPerm); err != nil {
		return nil, fmt.Errorf("建立 %s 失敗：%w", tmpRoot, err)
	}
	work, err := os.MkdirTemp(tmpRoot, "restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	contents, err := backup.Extract(in, work, backup.Limits{})
	if err != nil {
		return nil, fmt.Errorf("備份檔 %s：%w", file, err)
	}
	var migrations fs.FS
	if env.Test != nil {
		migrations = env.Test.Migrations
	}
	if err := checkRestorable(ctx, contents, migrations); err != nil {
		return nil, fmt.Errorf("備份檔 %s：%w", file, err)
	}

	res := &RestoreResult{SchemaVersion: contents.Manifest.SchemaVersion, CreatedAt: contents.Manifest.CreatedAt}
	stamp := backup.Stamp(env.Clock.Now())
	backups := filepath.Join(dataDir, store.BackupsDirName)
	dbPath := filepath.Join(dataDir, store.DBFileName)

	if _, err := os.Stat(dbPath); err == nil {
		res.PreRestore = filepath.Join(backups, "pre-restore-"+stamp+".db")
		if err := savePreRestore(ctx, dbPath, res.PreRestore); err != nil {
			return nil, fmt.Errorf("還原前保留目前的資料庫失敗：%w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// -wal、-shm 屬於被換掉的資料庫；留著的話 SQLite 會把它們套到新的資料庫上。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if err := os.Rename(contents.DBPath, dbPath); err != nil {
		return nil, fmt.Errorf("換上備份的資料庫失敗：%w", err)
	}

	if contents.HasConfig {
		dir := filepath.Join(backups, "restored-config-"+stamp)
		if err := os.MkdirAll(dir, dataDirPerm); err != nil {
			return nil, fmt.Errorf("建立 %s 失敗：%w", dir, err)
		}
		if err := writeBytes(filepath.Join(dir, filepath.Base(backup.ConfigName)), contents.Config); err != nil {
			return nil, fmt.Errorf("寫出備份中的設定檔失敗：%w", err)
		}
		res.ConfigDir = dir
	}
	return res, nil
}

// restoreLockError 說明資料目錄為什麼不能還原：serve 持有（或持有者不明）是 ErrServeRunning，
// 另一個 CLI 持有時說明是哪個程序；其他錯誤原樣回傳。
func restoreLockError(dir string, err error) error {
	if !errors.Is(err, store.ErrLocked) {
		return err
	}
	if info, ierr := store.ReadLockInfo(dir); ierr == nil && info != nil && info.Holder == holderCLI {
		return &lockedByCLIError{pid: info.PID}
	}
	return ErrServeRunning
}

// checkRestorable 確認備份的資料庫可以由目前的程式使用：完整、schema 版本與 manifest 一致，而且不比程式新（不支援降級）。
func checkRestorable(ctx context.Context, c *backup.Contents, migrations fs.FS) error {
	latest, err := store.LatestSchemaVersion(migrations)
	if err != nil {
		return err
	}
	if c.Manifest.SchemaVersion > latest {
		return tooNewError(c.Manifest.SchemaVersion, latest)
	}
	dbVersion, err := store.VerifyFile(ctx, c.DBPath)
	if err != nil {
		return fmt.Errorf("%w：%s", backup.ErrBadArchive, err)
	}
	if dbVersion > latest {
		return tooNewError(dbVersion, latest)
	}
	if dbVersion != c.Manifest.SchemaVersion {
		return fmt.Errorf("%w：manifest 的 schema 版本 %d 與資料庫的 %d 不一致", backup.ErrBadArchive, c.Manifest.SchemaVersion, dbVersion)
	}
	return nil
}

func tooNewError(backupVersion, latest int64) error {
	return fmt.Errorf("備份的 schema 版本 %d 比程式支援的 %d 新，不支援還原；請改用新版程式。", backupVersion, latest)
}

// savePreRestore 把目前的資料庫存成 dest：優先用 VACUUM INTO（一致，也會處理遺留的 WAL）；
// 資料庫本身壞了、開不起來時（常見的還原理由）退而複製原始的檔案，不讓還原因此卡住。
func savePreRestore(ctx context.Context, dbPath, dest string) error {
	err := store.SnapshotFile(ctx, dbPath, dest)
	if err == nil {
		return nil
	}
	if merr := os.MkdirAll(filepath.Dir(dest), dataDirPerm); merr != nil {
		return errors.Join(err, merr)
	}
	in, oerr := os.Open(dbPath)
	if oerr != nil {
		return errors.Join(err, oerr)
	}
	defer in.Close()
	if werr := store.WriteFileAtomic(dest, in); werr != nil {
		return errors.Join(err, werr)
	}
	return nil
}

// writeBytes 以 0600 寫出 content（先寫暫存名稱再 rename）。
func writeBytes(path string, content []byte) error {
	return store.WriteFileAtomic(path, bytes.NewReader(content))
}
