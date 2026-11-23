package ops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

var t0 = time.Date(2026, 11, 23, 2, 30, 0, 0, time.UTC)

func openStore(t *testing.T) (*store.Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	s, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk
}

// fakeEmitter 記下送出的事件；n 是每次回報寫入的筆數（0 代表沒有 notifier）。
type fakeEmitter struct {
	mu     sync.Mutex
	events []notify.Event
	n      int
	err    error
}

func (f *fakeEmitter) Emit(_ context.Context, _ *store.Repo, ev notify.Event) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.events = append(f.events, ev)
	return f.n, nil
}

func (f *fakeEmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func (f *fakeEmitter) last() notify.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events[len(f.events)-1]
}

// fakeArchive 是假的備份內容。
type fakeArchive struct {
	name   string
	size   int64
	body   string
	err    error
	closed *bool
}

func (a *fakeArchive) Name() string { return a.name }
func (a *fakeArchive) Size() int64  { return a.size }
func (a *fakeArchive) WriteTo(w io.Writer) (int64, error) {
	if a.err != nil {
		_, _ = io.WriteString(w, "partial")
		return 0, a.err
	}
	n, err := io.WriteString(w, a.body)
	return int64(n), err
}
func (a *fakeArchive) Close() error {
	if a.closed != nil {
		*a.closed = true
	}
	return nil
}

func newDaily(t *testing.T, s *store.Store, clk clock.Clock, dir string, out Emitter) *Daily {
	return &Daily{
		Store: s, Clock: clk, Outbox: out,
		Cfg: config.DailyBackup{Enabled: true, At: "03:00", Timezone: "UTC", Dir: dir, Keep: 3},
		Begin: func(_ context.Context, now time.Time) (Archive, error) {
			return &fakeArchive{name: backup.FileName(now), size: 10, body: "archive"}, nil
		},
	}
}

func TestNextDaily(t *testing.T) {
	cases := []struct {
		now      time.Time
		at, tz   string
		want     time.Time
		wantFail bool
	}{
		{now: t0, at: "03:00", tz: "UTC", want: time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)},
		{now: time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC), at: "03:00", tz: "", want: time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)},
		{now: t0, at: "03:00", tz: "Asia/Taipei", want: time.Date(2026, 11, 23, 19, 0, 0, 0, time.UTC)},
		{now: t0, at: "3:00", tz: "UTC", wantFail: true},
		{now: t0, at: "24:00", tz: "UTC", wantFail: true},
		{now: t0, at: "03:00", tz: "Nowhere/City", wantFail: true},
	}
	for _, c := range cases {
		got, err := NextDaily(c.now, c.at, c.tz)
		if c.wantFail {
			if err == nil {
				t.Errorf("NextDaily(%v, %q, %q) 應該失敗", c.now, c.at, c.tz)
			}
			continue
		}
		if err != nil || !got.Equal(c.want) {
			t.Errorf("NextDaily(%v, %q, %q) = %v，%v；預期 %v", c.now, c.at, c.tz, got, err, c.want)
		}
	}
}

// 夏令時間開始的那天 02:30 不存在：time.Date 正規化的結果不保證是哪一邊，只要求在 now 之後、一天之內。
func TestNextDailyDSTGap(t *testing.T) {
	now := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	got, err := NextDaily(now, "02:30", "America/New_York")
	if err != nil || !got.After(now) || got.Sub(now) > 24*time.Hour {
		t.Fatalf("NextDaily = %v，%v", got, err)
	}
}

func TestDailySchedule(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	d := newDaily(t, s, clk, t.TempDir(), nil)
	at3 := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)

	job := func() store.ScheduledJob {
		j, err := s.Read().ScheduledJob(ctx, BackupKind, BackupKey)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	if err := d.Schedule(ctx); err != nil || !job().NextRunAt.Equal(at3) {
		t.Fatalf("第一次排定：%v", err)
	}
	// 設定改成 04:00：未來的排定時間依新的設定重排。
	d.Cfg.At = "04:00"
	if err := d.Schedule(ctx); err != nil || !job().NextRunAt.Equal(at3.Add(time.Hour)) {
		t.Fatalf("改設定後重排：%v %v", job().NextRunAt, err)
	}
	// 停機錯過：已經過了排定的時間就保留，只補跑一次。
	clk.Set(at3.Add(74 * time.Hour))
	if err := d.Schedule(ctx); err != nil || !job().NextRunAt.Equal(at3.Add(time.Hour)) {
		t.Fatalf("錯過的排定要保留：%v %v", job().NextRunAt, err)
	}
	// 失敗後的重試也保留。
	retry := clk.Now().Add(time.Minute)
	if err := s.Write().RecordScheduledJobRun(ctx, BackupKind, BackupKey, store.ScheduledJobRun{NextRunAt: retry, LastRunAt: clk.Now(), LastError: "x", Failures: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.Schedule(ctx); err != nil || !job().NextRunAt.Equal(retry) {
		t.Fatalf("失敗後的重試要保留：%v %v", job().NextRunAt, err)
	}
	// 停用：刪除工作。
	d.Cfg.Enabled = false
	if err := d.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read().ScheduledJob(ctx, BackupKind, BackupKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("停用後工作應該刪除：%v", err)
	}
	d.Cfg = config.DailyBackup{Enabled: true, At: "x"}
	if err := d.Schedule(ctx); err == nil {
		t.Error("時間格式錯誤要回報")
	}
}

func TestDailyRunWritesPrunesAndRecovers(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	dir := t.TempDir()
	out := &fakeEmitter{n: 1}
	d := newDaily(t, s, clk, dir, out)
	for _, name := range []string{
		"notesteme-backup-20261119T030000Z.tar.gz", "notesteme-backup-20261120T030000Z.tar.gz",
		"notesteme-backup-20261121T030000Z.tar.gz", "notesteme-backup-20261122T030000Z.tar.gz",
		"pre-migration-3.db", "my-own.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	res, err := d.run(ctx, scheduler.Job{}, now)
	if err != nil || !res.Next.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("run = %+v，%v", res, err)
	}
	got := dirNames(t, dir)
	want := "my-own.tar.gz,notesteme-backup-20261121T030000Z.tar.gz,notesteme-backup-20261122T030000Z.tar.gz," +
		"notesteme-backup-20261123T030000Z.tar.gz,notesteme-backup-latest.tar.gz,pre-migration-3.db"
	if got != want {
		t.Fatalf("目錄 = %s\n預期 %s", got, want)
	}
	if out.count() != 0 {
		t.Errorf("沒有失敗過時成功不送通知：%+v", out.events)
	}

	// 失敗：不刪除任何檔案、只告警一次；成功後送恢復。
	before := dirNames(t, dir)
	d.Begin = func(context.Context, time.Time) (Archive, error) {
		return nil, errors.New("disk I/O error at /secret/path")
	}
	for i := range 3 {
		_, err := d.run(ctx, scheduler.Job{}, now.Add(time.Duration(i+1)*time.Hour))
		if err == nil || strings.Contains(err.Error(), "/secret/path") {
			t.Fatalf("失敗的錯誤不能含路徑：%v", err)
		}
	}
	if dirNames(t, dir) != before {
		t.Fatal("失敗時不能刪除任何檔案")
	}
	if out.count() != 1 {
		t.Fatalf("同一串連續失敗只告警一次：%d", out.count())
	}
	ev := out.last()
	if ev.Alert == nil || ev.Alert.Kind != KindBackupFailed || ev.Alert.Resolved ||
		ev.Message != "每日備份失敗：無法產生資料庫快照（詳見 server log）。" {
		t.Fatalf("告警 = %+v %+v", ev, ev.Alert)
	}
	states, _ := s.Read().AlertStates(ctx)
	if len(states) != 1 || states[0].NotifiedAt == nil {
		t.Fatalf("告警狀態 = %+v", states)
	}

	d.Begin = newDaily(t, s, clk, dir, out).Begin
	if _, err := d.run(ctx, scheduler.Job{}, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ev = out.last()
	if out.count() != 2 || !ev.Alert.Resolved || ev.Message != "已恢復：每日備份成功。" || ev.Alert.Summary != "每日備份成功。" {
		t.Fatalf("恢復 = %+v %+v", ev, ev.Alert)
	}
	if !strings.Contains(dirNames(t, dir), "notesteme-backup-20261124T030000Z.tar.gz") {
		t.Error("恢復後的備份不存在")
	}
}

func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestDailyFailureReasons(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := &fakeEmitter{n: 1}
	d := newDaily(t, s, clk, file, out)
	_, err := d.run(ctx, scheduler.Job{}, t0)
	if err == nil || err.Error() != "備份目錄的路徑中有不是目錄的項目" {
		t.Fatalf("目錄是檔案：%v", err)
	}
	if out.count() != 1 || !strings.HasPrefix(out.last().Message, "每日備份失敗：") {
		t.Fatalf("告警 = %+v", out.events)
	}

	// 空間不足：不寫出任何檔案。
	dir := t.TempDir()
	closed := false
	d = newDaily(t, s, clk, dir, out)
	d.Begin = func(_ context.Context, now time.Time) (Archive, error) {
		return &fakeArchive{name: backup.FileName(now), size: 1 << 40, closed: &closed}, nil
	}
	d.FreeSpace = func(string) (uint64, error) { return 1024, nil }
	if _, err := d.run(ctx, scheduler.Job{}, t0); err == nil || err.Error() != "備份目錄的磁碟空間不足" {
		t.Fatalf("空間不足：%v", err)
	}
	if !closed || dirNames(t, dir) != "" {
		t.Errorf("空間不足時要關閉暫存的快照、不留下檔案：closed=%v %s", closed, dirNames(t, dir))
	}
	// 寫到一半失敗（例如寫入時磁碟滿了）：清掉暫存檔。
	d.FreeSpace = nil
	d.Begin = func(_ context.Context, now time.Time) (Archive, error) {
		return &fakeArchive{name: backup.FileName(now), err: syscall.ENOSPC}, nil
	}
	if _, err := d.run(ctx, scheduler.Job{}, t0); err == nil || err.Error() != "備份目錄的磁碟空間不足" {
		t.Fatalf("寫入時空間不足：%v", err)
	}
	if dirNames(t, dir) != "" {
		t.Errorf("寫到一半失敗不能留下檔案：%s", dirNames(t, dir))
	}
	// 被取消時直接回傳 ctx 的錯誤，不告警。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	n := out.count()
	d.Begin = func(ctx context.Context, _ time.Time) (Archive, error) { return nil, ctx.Err() }
	if _, err := d.run(cctx, scheduler.Job{}, t0); !errors.Is(err, context.Canceled) || out.count() != n {
		t.Errorf("取消：%v", err)
	}
}

func TestFailureReason(t *testing.T) {
	cases := map[error]string{
		&os.PathError{Op: "open", Path: "/p", Err: syscall.EACCES}:  "沒有寫入備份目錄的權限",
		&os.PathError{Op: "open", Path: "/p", Err: syscall.EROFS}:   "備份目錄所在的檔案系統是唯讀的",
		&os.PathError{Op: "mkdir", Path: "/p", Err: syscall.EEXIST}: "備份目錄的路徑中有不是目錄的項目",
		errors.New("something else"):                                "寫入備份時發生錯誤（詳見 server log）",
	}
	for err, want := range cases {
		if got := failureReason(err); got != want {
			t.Errorf("failureReason(%v) = %q，預期 %q", err, got, want)
		}
	}
}

func TestPruneOnlyTouchesOwnRegularFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "notesteme-backup-20200101T000000Z.tar.gz")
	if err := os.WriteFile(victim, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{
		"notesteme-backup-20261101T030000Z.tar.gz", "notesteme-backup-20261102T030000Z.tar.gz",
		"notesteme-backup-20991231T030000Z.tar.gz", // 名稱時間在未來
		"notesteme-backup-20261101T030000Z.tar.gz.bak", "Notesteme-backup-20261101T030000Z.tar.gz",
		"notesteme-backup-2026110T030000Z.tar.gz", "pre-restore-20261101T030000Z.db",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 名稱符合的 symlink（指向 dir 之外）與子目錄都不動。
	if err := os.Symlink(victim, filepath.Join(dir, "notesteme-backup-20200101T000000Z.tar.gz")); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "notesteme-backup-20200102T000000Z.tar.gz")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "notesteme-backup-20200103T000000Z.tar.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := "notesteme-backup-20261123T030000Z.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, current), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(dir, 1, current)
	if err != nil {
		t.Fatal(err)
	}
	// keep 1：只留剛寫好的那一份，即使有名稱時間更晚的檔案。
	if strings.Join(removed, ",") != "notesteme-backup-20261101T030000Z.tar.gz,notesteme-backup-20261102T030000Z.tar.gz,notesteme-backup-20991231T030000Z.tar.gz" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("symlink 指向的 dir 之外的檔案被刪掉了")
	}
	for _, keep := range []string{current, "notesteme-backup-20200101T000000Z.tar.gz", "notesteme-backup-20200102T000000Z.tar.gz/notesteme-backup-20200103T000000Z.tar.gz",
		"notesteme-backup-20261101T030000Z.tar.gz.bak", "Notesteme-backup-20261101T030000Z.tar.gz", "pre-restore-20261101T030000Z.db"} {
		if _, err := os.Lstat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s 不該被刪除：%v", keep, err)
		}
	}
	if _, err := Prune(dir, 0, current); err == nil {
		t.Error("keep 0 要拒絕")
	}
	if _, err := Prune(filepath.Join(dir, "missing"), 1, current); err == nil {
		t.Error("目錄不存在要回報")
	}
	// 沒有 current（例如手動呼叫）時依 keep 計算。
	if removed, err := Prune(dir, 5, ""); err != nil || len(removed) != 0 {
		t.Errorf("份數沒超過時不刪除：%v %v", removed, err)
	}
}

func TestRegister(t *testing.T) {
	s, clk := openStore(t)
	sch := scheduler.New(s, clk, nil, 0)
	if err := (&Daily{}).Register(sch); err != nil {
		t.Fatal(err)
	}
	if err := (&Alerter{}).Register(sch); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sch.Kinds(), ","); got != "backup.daily,alerts" {
		t.Errorf("Kinds = %s", got)
	}
}

// 經排程器跑一次每日備份：錯過的工作只補跑一次，檔名用執行的時間。
func TestDailyThroughScheduler(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	dir := t.TempDir()
	d := newDaily(t, s, clk, dir, nil)
	sch := scheduler.New(s, clk, nil, 0)
	if err := d.Register(sch); err != nil {
		t.Fatal(err)
	}
	if err := d.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Set(time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC))
	if err := sch.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, dir); got != "notesteme-backup-20261126T040000Z.tar.gz,notesteme-backup-latest.tar.gz" {
		t.Fatalf("目錄 = %s", got)
	}
	j, _ := s.Read().ScheduledJob(ctx, BackupKind, BackupKey)
	if !j.NextRunAt.Equal(time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("下一次 = %v", j.NextRunAt)
	}
}
