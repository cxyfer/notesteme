package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/store"
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
	pb := &preparedBackup{dir: dir, release: func() {}}
	ok := false
	defer func() {
		if !ok {
			_ = pb.Close()
		}
	}()

	// 快照之前就讓永久刪除先不刪上傳原始檔，直到備份寫完（Close）：快照中的上傳影音，原始檔都還在。
	if b.s.pipeline != nil {
		pb.release = b.s.pipeline.HoldUploads()
	}
	snap := filepath.Join(dir, backup.DBName)
	if err := b.s.store.SnapshotTo(ctx, snap); err != nil {
		return nil, fmt.Errorf("資料庫快照失敗：%w", err)
	}
	if afterBackupSnapshot != nil {
		afterBackupSnapshot()
	}
	schemaVersion, err := b.s.store.SchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("讀取 schema 版本失敗：%w", err)
	}
	config, hasConfig, err := readConfigFile(cfg.ConfigFile)
	if err != nil {
		return nil, err
	}
	uploads, err := b.collectUploads(ctx, snap)
	if err != nil {
		return nil, err
	}
	now := b.env.Clock.Now().UTC()
	pb.name = backup.FileName(now)
	pb.archive = backup.Archive{
		Manifest: backup.Manifest{SchemaVersion: schemaVersion, CreatedAt: now, NotestemeVersion: version},
		DBPath:   snap, Config: config, HasConfig: hasConfig,
		UploadsDir: filepath.Join(cfg.Data, backup.UploadsPrefix), Uploads: uploads,
	}
	ok = true
	return pb, nil
}

// afterBackupSnapshot 只給測試使用：Begin 做完資料庫快照之後呼叫，用來重現快照與列出上傳檔之間的永久刪除。
var afterBackupSnapshot func()

// collectUploads 列出要放進備份的上傳原始檔：快照 snap 中每個上傳（origin upload）影音在 uploads/<id>/<name> 的一般檔案。
// 上傳檔只存在資料目錄，不放進備份就無法還原。清單取自快照而不是線上的資料庫，才會和備份中的資料庫一致。
// sha256 取自快照的 content_sha256（沒有時才讀檔計算），寫出備份時會重新計算比對。
// 檔案不見或不是一般檔案（symlink、FIFO）的影音略過並記 log，不讓備份失敗。
// 沒有 uploads 目錄時不查詢資料庫，直接回傳空清單。
func (b *backupSource) collectUploads(ctx context.Context, snap string) ([]backup.Upload, error) {
	dir := filepath.Join(b.s.cfg.Data, backup.UploadsPrefix)
	if fi, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("讀取 %s 失敗：%w", dir, err)
	} else if !fi.IsDir() {
		b.s.log.WarnContext(ctx, "uploads 不是目錄，備份略過上傳檔", "path", dir)
		return nil, nil
	}
	files, err := store.SnapshotUploadFiles(ctx, snap)
	if err != nil {
		return nil, fmt.Errorf("讀取上傳檔清單失敗：%w", err)
	}
	if len(files) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("開啟 %s 失敗：%w", dir, err)
	}
	defer root.Close()
	var out []backup.Upload
	for _, f := range files {
		id, name, ok := backup.ParseUploadPath(f.SourcePath)
		if !ok || id != f.ItemID {
			b.s.log.WarnContext(ctx, "上傳影音的檔案路徑不正確，備份略過", "item_id", f.ItemID, "source_path", f.SourcePath)
			continue
		}
		rel := id + "/" + name
		fi, err := root.Lstat(rel)
		if err != nil || !fi.Mode().IsRegular() {
			b.s.log.WarnContext(ctx, "上傳檔不存在或不是一般檔案，備份略過", "item_id", f.ItemID, "source_path", f.SourcePath)
			continue
		}
		sum := f.SHA256
		if !validSHA(sum) {
			if sum, err = hashRootFile(root, rel); err != nil {
				return nil, fmt.Errorf("計算上傳檔 %s 的 sha256 失敗：%w", f.SourcePath, err)
			}
		}
		out = append(out, backup.Upload{Path: f.SourcePath, Size: fi.Size(), SHA256: sum})
	}
	return out, nil
}

// validSHA 回報 s 是不是小寫十六進位的 sha256。
func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}

// hashRootFile 計算 root 底下 rel 的 sha256。
func hashRootFile(root *os.Root, rel string) (string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	// release 結束 Begin 對上傳原始檔的保留（見 Pipeline.HoldUploads），可以重複呼叫。
	release func()
}

func (p *preparedBackup) Name() string { return p.name }

func (p *preparedBackup) WriteTo(w io.Writer) (int64, error) { return p.archive.WriteTo(w) }

// Close 結束對上傳原始檔的保留，並刪除暫存的快照。
func (p *preparedBackup) Close() error {
	p.release()
	return os.RemoveAll(p.dir)
}
