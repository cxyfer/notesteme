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
	// PreRestoreUploads 是還原前原本的 uploads/ 移到的目錄；原本沒有（或是空的）上傳檔時為空。
	PreRestoreUploads string
	// Uploads 是還原的上傳檔數。
	Uploads int
}

// ErrServeRunning 表示資料目錄正由 serve 使用，不能還原（OPS-R2）。
var ErrServeRunning = errors.New("請先停止 notesteme serve，再執行 restore。")

// Restore 把備份檔 file 還原成 env 指定的資料目錄（OPS-R2）。不經過 API：只取得資料目錄的鎖（CLI-R2 的例外），
// 所以 serve 執行中時失敗。順序：
//  1. 先取得鎖，再讀備份檔：serve 執行中時不碰備份檔，也不動資料目錄。
//  2. 備份檔不受信任（backup.Extract）；schema 版本比目前程式新時拒絕。
//  3. 還原前先把目前的資料庫存成 backups/pre-restore-<時間>.db，再刪除 -wal、-shm、換上備份的資料庫。
//  4. 設定檔不覆寫 /config（compose 可能以唯讀掛載），解到 backups/restored-config-<時間>/ 讓使用者自行替換。
//  5. 上傳的原始檔先解到 tmp/，驗證大小與 sha256 之後，uploads/ 整個換成備份中的；原本的 uploads/ 移到
//     backups/pre-restore-uploads-<時間>/ 保留。換上資料庫之前失敗時，uploads/ 保持原狀。
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

	res := &RestoreResult{SchemaVersion: contents.Manifest.SchemaVersion, CreatedAt: contents.Manifest.CreatedAt, Uploads: len(contents.Uploads)}
	stamp := backup.Stamp(env.Clock.Now())
	backups := filepath.Join(dataDir, store.BackupsDirName)
	dbPath := filepath.Join(dataDir, store.DBFileName)

	if _, err := os.Stat(dbPath); err == nil {
		res.PreRestore = uniquePath(backups, "pre-restore-", stamp, ".db")
		if err := savePreRestore(ctx, dbPath, res.PreRestore); err != nil {
			return nil, fmt.Errorf("還原前保留目前的資料庫失敗：%w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// 設定檔要在換上資料庫之前寫好：換上資料庫是還原的提交點，之後不能再回報失敗。
	// 換上之前任何一步失敗，就把已經寫出的設定目錄移除。
	committed := false
	if contents.HasConfig {
		dir := uniquePath(backups, "restored-config-", stamp, "")
		defer func() {
			if !committed {
				_ = os.RemoveAll(dir)
			}
		}()
		if err := os.MkdirAll(dir, dataDirPerm); err != nil {
			return nil, fmt.Errorf("建立 %s 失敗：%w", dir, err)
		}
		if err := writeBytes(filepath.Join(dir, filepath.Base(backup.ConfigName)), contents.Config); err != nil {
			return nil, fmt.Errorf("寫出備份中的設定檔失敗：%w", err)
		}
		res.ConfigDir = dir
	}

	// 上傳的原始檔：uploads/ 整個換成備份中的（備份沒有上傳檔時，目前的 uploads/ 只是被移開）。
	// 在換上資料庫之前做，提交點之前任何一步失敗都會還原成原狀。
	swap, err := swapUploads(filepath.Join(dataDir, backup.UploadsPrefix), contents.UploadsDir,
		func() string { return uniquePath(backups, "pre-restore-uploads-", stamp, "") })
	if err != nil {
		return nil, fmt.Errorf("換上備份的上傳檔失敗：%w", err)
	}
	defer func() {
		if !committed {
			swap.undo()
		}
	}()
	res.PreRestoreUploads = swap.aside

	// -wal、-shm 屬於被換掉的資料庫；留著的話 SQLite 會把它們套到新的資料庫上。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if err := os.Rename(contents.DBPath, dbPath); err != nil {
		return nil, fmt.Errorf("換上備份的資料庫失敗：%w", err)
	}
	committed = true

	return res, nil
}

// uploadsSwap 記錄 swapUploads 做了什麼，失敗時可以還原。
type uploadsSwap struct {
	cur, next, aside string
	// moved 為 true 表示 next 已經換成 cur；asideUsed 為 true 表示原本的 cur 已經移到 aside。
	moved, asideUsed bool
}

// swapUploads 讓 cur（資料目錄的 uploads/）只剩備份中的檔案：
// 原本的 cur（有內容時）整個移到 asidePath() 回傳的路徑保留（與還原前的資料庫快照一樣，不直接刪除使用者的檔案），
// 再把已經解開並驗證過的 next 換成 cur。next 為空字串（備份沒有上傳檔）時，cur 只是被移開。
// 全部是同一個檔案系統內的 rename；任何一步失敗都回到原狀。
func swapUploads(cur, next string, asidePath func() string) (*uploadsSwap, error) {
	s := &uploadsSwap{cur: cur, next: next}
	// 空目錄直接刪掉（Remove 只刪空目錄）；沒有東西可以保留。
	if err := os.Remove(cur); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if _, serr := os.Lstat(cur); serr == nil {
			s.aside = asidePath()
			if err := os.MkdirAll(filepath.Dir(s.aside), dataDirPerm); err != nil {
				s.aside = ""
				return nil, err
			}
			if err := os.Rename(cur, s.aside); err != nil {
				s.aside = ""
				return nil, err
			}
			s.asideUsed = true
		}
	}
	if next != "" {
		if err := os.Rename(next, cur); err != nil {
			s.undo()
			return nil, err
		}
		s.moved = true
	}
	return s, nil
}

// undo 還原 swapUploads 的變更（盡力而為）。
func (s *uploadsSwap) undo() {
	if s.moved {
		_ = os.Rename(s.cur, s.next)
		s.moved = false
	}
	if s.asideUsed {
		_ = os.Rename(s.aside, s.cur)
		s.asideUsed, s.aside = false, ""
	}
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
	if err := os.MkdirAll(filepath.Dir(dest), dataDirPerm); err != nil {
		return err
	}
	// 先把原始的檔案另存一份：嘗試開啟損毀的資料庫時，SQLite 可能丟棄看不懂的 -wal，之後就沒有東西可以保存了。
	raw, err := os.MkdirTemp(filepath.Dir(dest), ".pre-restore-raw-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(raw)
	suffixes := []string{"-wal", "-shm", ""}
	for _, suffix := range suffixes {
		if cerr := copyIfExists(dbPath+suffix, filepath.Join(raw, store.DBFileName+suffix)); cerr != nil {
			return cerr
		}
	}

	serr := store.SnapshotFile(ctx, dbPath, dest)
	if serr == nil {
		return nil
	}
	// 退而複製原始的檔案，-wal 與 -shm 要一併保存：WAL 裡已提交的資料還沒寫回主檔，
	// 之後還原會刪掉它們，不保存就會遺失。主檔放最後，有任何一個失敗都不會留下看似完整的快照。
	for _, suffix := range suffixes {
		if cerr := copyIfExists(filepath.Join(raw, store.DBFileName+suffix), dest+suffix); cerr != nil {
			return errors.Join(serr, cerr)
		}
	}
	return nil
}

// copyIfExists 把 src 複製成 dst（0600、先寫暫存名稱再 rename）；src 不存在時什麼都不做。
func copyIfExists(src, dst string) error {
	in, err := os.Open(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	return store.WriteFileAtomic(dst, in)
}

// uniquePath 回傳 dir 下 <prefix><stamp><ext> 的路徑；已存在（含 -wal、-shm 附屬檔）時依序加上 -2、-3…，不覆蓋既有的檔案。
func uniquePath(dir, prefix, stamp, ext string) string {
	for n := 1; ; n++ {
		name := prefix + stamp
		if n > 1 {
			name += fmt.Sprintf("-%d", n)
		}
		path := filepath.Join(dir, name+ext)
		taken := false
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if _, err := os.Lstat(path + suffix); err == nil {
				taken = true
				break
			}
		}
		if !taken {
			return path
		}
	}
}

// writeBytes 以 0600 寫出 content（先寫暫存名稱再 rename）。
func writeBytes(path string, content []byte) error {
	return store.WriteFileAtomic(path, bytes.NewReader(content))
}
