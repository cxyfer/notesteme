package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// Archive 是準備好、還沒寫出的一份備份（內容同 notesteme backup，OPS-R1）。
type Archive interface {
	Name() string
	// Size 是寫出後大約的大小（位元組，用來檢查剩餘空間）。
	Size() int64
	WriteTo(w io.Writer) (int64, error)
	Close() error
}

// Daily 是每日備份（OPS-R11、R12）。
type Daily struct {
	Store  *store.Store
	Clock  clock.Clock
	Outbox Emitter
	Log    *slog.Logger
	Cfg    config.DailyBackup
	// Begin 產生資料庫快照並準備備份的內容；檔名的時間是 now。
	Begin func(ctx context.Context, now time.Time) (Archive, error)
	// FreeSpace 回傳目錄的剩餘位元組數；nil 時用 backup.FreeSpace。
	FreeSpace func(dir string) (uint64, error)
}

// Register 登記排程工作 backup.daily（worker 開始之前呼叫）。
func (d *Daily) Register(sch *scheduler.Scheduler) error {
	return sch.Register(scheduler.Kind{Name: BackupKind, Run: d.run})
}

// Schedule 在啟動時排定下一次：停用時刪除工作；已經過了排定的時間（停機錯過）時保留，只補跑一次；
// 失敗後的重試也保留。其他情況依目前的設定（時間或時區可能改了）重排到下一個時刻。
func (d *Daily) Schedule(ctx context.Context) error {
	w := d.Store.Write()
	if !d.Cfg.Enabled {
		_, err := w.DeleteScheduledJob(ctx, BackupKind, BackupKey)
		return err
	}
	now := d.Clock.Now()
	next, err := d.next(now)
	if err != nil {
		return err
	}
	job, err := d.Store.Read().ScheduledJob(ctx, BackupKind, BackupKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return w.ScheduleJob(ctx, BackupKind, BackupKey, next)
	case err != nil:
		return err
	case !job.NextRunAt.After(now) || job.Failures > 0 || job.NextRunAt.Equal(next):
		return nil
	}
	return w.ScheduleJob(ctx, BackupKind, BackupKey, next)
}

// next 回傳 now 之後（不含 now）設定時區中下一個 backup.daily.at。
func (d *Daily) next(now time.Time) (time.Time, error) {
	return NextDaily(now, d.Cfg.At, d.Cfg.Timezone)
}

// NextDaily 回傳 now 之後（不含 now）時區 tz 中下一個 at（HH:MM）。夏令時間跳過的時刻依 time.Date 正規化。
func NextDaily(now time.Time, at, tz string) (time.Time, error) {
	h, m, ok := parseHHMM(at)
	if !ok {
		return time.Time{}, fmt.Errorf("每日備份的時間 %q 不是 HH:MM", at)
	}
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("每日備份的時區 %q 不正確：%w", tz, err)
	}
	local := now.In(loc)
	for i := 0; i < 3; i++ {
		t := time.Date(local.Year(), local.Month(), local.Day()+i, h, m, 0, 0, loc)
		if t.After(now) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("算不出 %s 之後的每日備份時間", now)
}

func parseHHMM(s string) (h, m int, ok bool) {
	hs, ms, found := strings.Cut(s, ":")
	if !found || len(hs) != 2 || len(ms) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	return h, m, err1 == nil && err2 == nil && h >= 0 && h < 24 && m >= 0 && m < 60
}

// run 執行一次每日備份：成功時刪除超出保留份數的舊備份並解除 backup_failed；失敗時不刪除任何檔案，
// 告警（同一串連續失敗只告警一次）後回傳錯誤，由排程器退避重試。
func (d *Daily) run(ctx context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
	path, err := d.backup(ctx, now)
	if err != nil {
		if ctx.Err() != nil {
			return scheduler.Result{}, ctx.Err()
		}
		reason := failureReason(err)
		d.log().ErrorContext(ctx, "每日備份失敗", "dir", d.Cfg.Dir, "reason", reason, "error", err)
		if aerr := d.alert(ctx, now, "每日備份失敗："+reason+"。", false); aerr != nil {
			return scheduler.Result{}, errors.Join(errors.New(reason), aerr)
		}
		return scheduler.Result{}, errors.New(reason)
	}
	removed, perr := Prune(d.Cfg.Dir, d.Cfg.Keep, backupName(path))
	if perr != nil {
		// 備份本身已經完成：清理舊備份失敗只記 log，下一次成功時會再清理。
		d.log().WarnContext(ctx, "清理舊的每日備份失敗", "dir", d.Cfg.Dir, "error", perr)
	}
	d.log().InfoContext(ctx, "每日備份完成", "file", path, "removed", len(removed))
	if err := d.alert(ctx, now, "", true); err != nil {
		return scheduler.Result{}, err
	}
	next, err := d.next(now)
	if err != nil {
		return scheduler.Result{}, err
	}
	return scheduler.Result{Next: next}, nil
}

func backupName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// alert 依結果更新 backup_failed：失敗時成立（已成立時不重複通知），成功時解除並送「已恢復：每日備份成功。」。
func (d *Daily) alert(ctx context.Context, now time.Time, message string, ok bool) error {
	return d.Store.Tx(ctx, func(r *store.Repo) error {
		states, err := r.AlertStates(ctx)
		if err != nil {
			return err
		}
		st := stateOf(states, KindBackupFailed, BackupKey)
		if ok {
			if st == nil || !st.Active() {
				return nil
			}
			return resolve(ctx, r, d.Outbox, *st, "每日備份成功。", true, now)
		}
		return raise(ctx, r, d.Outbox, st, KindBackupFailed, BackupKey, message, now)
	})
}

// errNoSpace 表示備份目錄的剩餘空間不夠放這份備份。
var errNoSpace = errors.New("備份目錄的剩餘空間不足")

// errSnapshot 包住產生資料庫快照或準備備份內容時的錯誤。
type errSnapshot struct{ err error }

func (e *errSnapshot) Error() string { return "準備備份失敗：" + e.err.Error() }
func (e *errSnapshot) Unwrap() error { return e.err }

// backup 寫出一份備份到 backup.daily.dir，回傳檔案的路徑。暫存的快照由 Archive.Close 清掉，
// 寫到一半失敗時 SaveWith 刪除暫存檔，不留下看似完整的檔案。
func (d *Daily) backup(ctx context.Context, now time.Time) (string, error) {
	dir := d.Cfg.Dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if fi, err := os.Stat(dir); err != nil {
		return "", err
	} else if !fi.IsDir() {
		return "", &fs.PathError{Op: "stat", Path: dir, Err: syscall.ENOTDIR}
	}
	arc, err := d.Begin(ctx, now)
	if err != nil {
		return "", &errSnapshot{err}
	}
	defer arc.Close()
	free := d.FreeSpace
	if free == nil {
		free = backup.FreeSpace
	}
	if n, err := free(dir); err == nil && n < uint64(max(arc.Size(), 0)) {
		return "", errNoSpace
	}
	return backup.SaveWith(dir, arc.Name(), func(w io.Writer) error {
		_, err := arc.WriteTo(w)
		return err
	})
}

// failureReason 把備份的錯誤分成固定的幾種說明：送進通知與 last_error 的文字不含路徑或系統訊息
// （NOTIFY-R9），完整的錯誤只寫進 log。
func failureReason(err error) string {
	var snap *errSnapshot
	switch {
	case errors.Is(err, errNoSpace), errors.Is(err, syscall.ENOSPC):
		return "備份目錄的磁碟空間不足"
	case errors.As(err, &snap):
		return "無法產生資料庫快照（詳見 server log）"
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.EEXIST):
		return "備份目錄的路徑中有不是目錄的項目"
	case errors.Is(err, fs.ErrPermission):
		return "沒有寫入備份目錄的權限"
	case errors.Is(err, syscall.EROFS):
		return "備份目錄所在的檔案系統是唯讀的"
	}
	return "寫入備份時發生錯誤（詳見 server log）"
}

// Prune 只保留 dir 中名稱完全符合 notesteme-backup-<時間>.tar.gz 的最新 keep 份一般檔案（依名稱中的時間排序），
// 刪除更舊的，回傳刪掉的名稱（OPS-R12）。其他檔案（pre-migration-*、latest、使用者自己的檔案）、symlink 與子目錄
// 一律不動，也不進子目錄；刪除經 os.Root，不會跟隨 symlink 離開 dir。
// current 是剛寫好的那一份：即使目錄中有名稱時間更晚的檔案，它也不會被刪掉；名稱比它晚的檔案視為最舊、最先刪除。
func Prune(dir string, keep int, current string) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("保留份數必須至少 1：%d", keep)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		// Type() 是 Lstat 的種類：0 才是一般檔案（symlink、目錄、裝置都不算）。
		if e.Type() == 0 && backup.ValidFileName(e.Name()) && e.Name() != current {
			names = append(names, e.Name())
		}
	}
	// 時間在名稱中是固定寬度的 UTC，字典序就是時間順序。
	slices.Sort(names)
	others := keep
	if backup.ValidFileName(current) {
		others--
		// 名稱時間比剛寫好的那一份還晚（時鐘曾經錯誤或手動放入）的檔案視為最舊，最先刪除，
		// 否則它們會永遠佔住保留名額，真正的備份反而被刪掉。
		i, _ := slices.BinarySearch(names, current)
		names = append(slices.Clone(names[i:]), names[:i]...)
	}
	if len(names) <= others {
		return nil, nil
	}
	var removed []string
	var errs []error
	for _, name := range names[:len(names)-others] {
		// 刪除前再確認一次仍是一般檔案（ReadDir 之後可能被換成 symlink）。
		if fi, err := root.Lstat(name); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if err := root.Remove(name); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, errors.Join(errs...)
}

func (d *Daily) log() *slog.Logger {
	if d.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return d.Log
}
