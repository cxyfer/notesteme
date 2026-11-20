package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
)

// BackupsDirName 是資料目錄下存放快照與備份的子目錄（OPS-R1、R2、R3）。
const BackupsDirName = "backups"

// PreMigrationName 回傳 migration 之前的快照檔名：pre-migration-<舊版本>.db（OPS-R3）。
func PreMigrationName(version int64) string { return fmt.Sprintf("pre-migration-%d.db", version) }

// SchemaTooNewError 表示資料庫的 schema 版本比程式支援的新（OPS-R4）。不支援降級。
type SchemaTooNewError struct {
	// DB 是資料庫目前的版本，Program 是程式內建的最新版本。
	DB, Program int64
	// Pattern 是 migration 前快照的路徑樣式，放進訊息供使用者還原。
	Pattern string
}

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("資料庫的 schema 版本 %d 比程式支援的 %d 新，不支援降級；請改用新版程式，或還原 %s。", e.DB, e.Program, e.Pattern)
}

// MigrationError 表示 migration 失敗（OPS-R3）。有快照時已經還原它，資料庫回到升級前的狀態。
type MigrationError struct {
	// Version 是失敗的 migration；無法判斷時為 0。
	Version int64
	Err     error
	// Snapshot 是升級前的快照路徑；沒有快照（新資料庫）時為空。
	Snapshot string
	// RestoreErr 是還原快照時發生的錯誤；成功時為 nil。
	RestoreErr error
}

func (e *MigrationError) Error() string {
	head := "migration 失敗"
	if e.Version > 0 {
		head = fmt.Sprintf("migration %d 失敗", e.Version)
	}
	switch {
	case e.Snapshot == "":
		return fmt.Sprintf("%s：%v", head, e.Err)
	case e.RestoreErr != nil:
		return fmt.Sprintf("%s：%v；還原 %s 也失敗：%v", head, e.Err, e.Snapshot, e.RestoreErr)
	}
	return fmt.Sprintf("%s：%v；已還原 %s", head, e.Err, e.Snapshot)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// migrate 對 w 套用 sub 的 migration（OPS-R3、R4）：
//   - 資料庫的版本比 sub 的最新版本新時拒絕（goose 本身不會擋）。
//   - 有待執行的 migration 而且資料庫不是新的（版本 > 0）時，先在 dir/backups 留一份快照。
//   - migration 失敗時回傳 *MigrationError；呼叫端要關閉連線後再用 restoreSnapshot 還原（檔案不能在連線開著時被取代）。
//
// 回傳快照的路徑（沒有快照時為空）。
func migrate(ctx context.Context, w *sql.DB, dir string, sub fs.FS) (snapshot string, err error) {
	provider, err := goose.NewProvider(goose.DialectSQLite3, w, sub)
	if err != nil {
		return "", fmt.Errorf("初始化 migration 失敗：%w", err)
	}
	cur, err := provider.GetDBVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("讀取 schema 版本失敗：%w", err)
	}
	var latest int64
	for _, src := range provider.ListSources() {
		latest = max(latest, src.Version)
	}
	backups := filepath.Join(dir, BackupsDirName)
	if cur > latest {
		return "", &SchemaTooNewError{DB: cur, Program: latest, Pattern: filepath.Join(backups, "pre-migration-*.db")}
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return "", fmt.Errorf("檢查待執行的 migration 失敗：%w", err)
	}
	if !pending {
		return "", nil
	}
	if cur > 0 {
		snapshot = filepath.Join(backups, PreMigrationName(cur))
		if err := snapshotTo(ctx, w, snapshot); err != nil {
			return "", fmt.Errorf("migration 之前建立快照 %s 失敗：%w", snapshot, err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		me := &MigrationError{Err: err, Snapshot: snapshot}
		var pe *goose.PartialError
		if errors.As(err, &pe) {
			me.Err = pe.Err
			if pe.Failed != nil && pe.Failed.Source != nil {
				me.Version = pe.Failed.Source.Version
			}
		}
		return snapshot, me
	}
	return snapshot, nil
}

// restoreSnapshot 把快照還原成 path 的資料庫：先刪除 -wal、-shm，再複製到暫存名稱後 rename。
// 順序不能反過來：先換主檔的話，中途失敗會留下新的主檔配舊的 -wal（與 app.Restore 的順序一致）。
// 呼叫時資料庫的連線都必須已經關閉。
func restoreSnapshot(snapshot, path string) error {
	for _, p := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return copyPrivate(snapshot, path)
}

// copyPrivate 把 src 複製成 dst（0600）：先寫同目錄的暫存名稱，完整寫完才 rename。
func copyPrivate(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return WriteFileAtomic(dst, in)
}
