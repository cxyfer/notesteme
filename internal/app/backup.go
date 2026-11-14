package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/backup"
)

// maxConfigFileBytes 是備份時讀取設定檔的上限（與 backup.Extract 的設定檔上限一致）。
const maxConfigFileBytes = 1 << 20

// backupSource 回傳 POST /api/backup 的備份來源（OPS-R1）。serve、本機 socket 與 standalone 共用。
func (s *Server) backupSource(env Env) api.BackupSource { return &backupSource{s: s, env: env} }

type backupSource struct {
	s   *Server
	env Env
}

// Begin 產生一致的資料庫快照（serve 執行中也可以做）並讀入設定檔；備份的內容在 WriteTo 時才以串流寫出。
// 暫存的快照放在資料目錄的 tmp/（與資料庫同一個磁碟，不佔用 /tmp），Close 時刪除。
func (b *backupSource) Begin(ctx context.Context) (api.Backup, error) {
	cfg := b.s.cfg
	tmpRoot := filepath.Join(cfg.Data, "tmp")
	if err := os.MkdirAll(tmpRoot, dataDirPerm); err != nil {
		return nil, fmt.Errorf("建立 %s 失敗：%w", tmpRoot, err)
	}
	dir, err := os.MkdirTemp(tmpRoot, "backup-")
	if err != nil {
		return nil, err
	}
	pb := &preparedBackup{dir: dir}
	ok := false
	defer func() {
		if !ok {
			_ = pb.Close()
		}
	}()

	snap := filepath.Join(dir, backup.DBName)
	if err := b.s.store.SnapshotTo(ctx, snap); err != nil {
		return nil, fmt.Errorf("資料庫快照失敗：%w", err)
	}
	schemaVersion, err := b.s.store.SchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("讀取 schema 版本失敗：%w", err)
	}
	config, hasConfig, err := readConfigFile(cfg.ConfigFile)
	if err != nil {
		return nil, err
	}
	now := b.env.Clock.Now().UTC()
	pb.name = backup.FileName(now)
	pb.archive = backup.Archive{
		Manifest: backup.Manifest{SchemaVersion: schemaVersion, CreatedAt: now, NotestemeVersion: version},
		DBPath:   snap, Config: config, HasConfig: hasConfig,
	}
	ok = true
	return pb, nil
}

// readConfigFile 讀設定檔；不存在時 ok 為 false（OPS-R1：有這個檔案才放進備份）。
func readConfigFile(path string) (content []byte, ok bool, err error) {
	if path == "" {
		return nil, false, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("讀取設定檔 %s 失敗：%w", path, err)
	}
	defer f.Close()
	content, err = io.ReadAll(io.LimitReader(f, maxConfigFileBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("讀取設定檔 %s 失敗：%w", path, err)
	}
	if len(content) > maxConfigFileBytes {
		return nil, false, fmt.Errorf("設定檔 %s 超過 %d 位元組，不放進備份。", path, maxConfigFileBytes)
	}
	return content, true, nil
}

// preparedBackup 是準備好的備份（api.Backup）。
type preparedBackup struct {
	dir     string
	name    string
	archive backup.Archive
}

func (p *preparedBackup) Name() string { return p.name }

func (p *preparedBackup) WriteTo(w io.Writer) (int64, error) { return p.archive.WriteTo(w) }

// Close 刪除暫存的快照。
func (p *preparedBackup) Close() error { return os.RemoveAll(p.dir) }
