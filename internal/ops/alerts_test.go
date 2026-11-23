package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/store"
)

func defaultAlerts() config.Alerts {
	return config.Alerts{
		Enabled: true, StuckTaskHours: 6, JobStaleHours: 24, DiskUsagePercent: 90,
		CookieStaleDays: 14, YtdlpMinCalls: 5, YtdlpFailureRatio: 0.5,
	}
}

type alertRig struct {
	s    *store.Store
	clk  *clock.Fake
	out  *fakeEmitter
	a    *Alerter
	disk int
}

func newAlertRig(t *testing.T) *alertRig {
	s, clk := openStore(t)
	r := &alertRig{s: s, clk: clk, out: &fakeEmitter{n: 1}, disk: 10}
	r.a = &Alerter{
		Store: s, Clock: clk, Outbox: r.out, Cfg: defaultAlerts(), DataDir: t.TempDir(),
		DiskUsage: func(string) (int, error) { return r.disk, nil },
	}
	return r
}

func (r *alertRig) check(t *testing.T) {
	t.Helper()
	if err := r.a.Check(context.Background(), r.clk.Now()); err != nil {
		t.Fatal(err)
	}
}

func (r *alertRig) states(t *testing.T) []store.AlertState {
	t.Helper()
	st, err := r.s.Read().AlertStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDiskUsageAlertWithHysteresis(t *testing.T) {
	r := newAlertRig(t)
	r.disk = 93
	r.check(t)
	if r.out.count() != 1 {
		t.Fatalf("93%% 要告警一次：%d", r.out.count())
	}
	ev := r.out.last()
	if ev.Alert.Kind != KindDiskUsage || ev.Alert.Key != "/data" || ev.Alert.Message != "/data 的磁碟用量是 93%，超過 90%。" || ev.Alert.Resolved {
		t.Fatalf("告警 = %+v", ev.Alert)
	}
	if ev.DedupeKey != notify.DedupeAlert(KindDiskUsage, "/data", false) {
		t.Errorf("dedupe 鍵 = %q", ev.DedupeKey)
	}
	r.disk = 95
	r.check(t)
	if r.out.count() != 1 || r.states(t)[0].Message != "/data 的磁碟用量是 95%，超過 90%。" {
		t.Fatalf("持續成立不重複告警，只更新文字：%d %+v", r.out.count(), r.states(t))
	}
	r.disk = 88
	r.check(t)
	if r.out.count() != 1 || !r.states(t)[0].Active() || !strings.Contains(r.states(t)[0].Message, "95%") {
		t.Fatalf("88%% 還沒低於 85%%，維持成立：%+v", r.states(t))
	}
	r.disk = 85
	r.check(t)
	if r.out.count() != 1 {
		t.Fatal("剛好 85% 仍算成立")
	}
	r.disk = 84
	r.check(t)
	ev = r.out.last()
	if r.out.count() != 2 || !ev.Alert.Resolved || ev.Alert.Summary != "/data 的磁碟用量是 95%，超過 90%。" ||
		!strings.HasPrefix(ev.Message, "已恢復：") {
		t.Fatalf("84%% 要解除：%+v", ev.Alert)
	}
	// 解除後再超過門檻：再告警。
	r.disk = 90
	r.check(t)
	if r.out.count() != 3 || r.out.last().Alert.Resolved {
		t.Fatalf("再成立要再告警：%d", r.out.count())
	}
}

func TestDiskUsageUnknownOrDisabled(t *testing.T) {
	r := newAlertRig(t)
	r.disk = 99
	r.check(t)
	// 讀不到用量：維持原來的狀態，也不解除。
	r.a.DiskUsage = func(string) (int, error) { return 0, errors.New("statfs failed") }
	r.check(t)
	if r.out.count() != 1 || !r.states(t)[0].Active() {
		t.Fatalf("讀不到用量時維持：%+v", r.states(t))
	}
	// 門檻設成 0：安靜地解除，不送 resolved。
	r.a.Cfg.DiskUsagePercent = 0
	r.check(t)
	if r.out.count() != 1 || r.states(t)[0].Active() {
		t.Fatalf("停用後安靜解除：%d %+v", r.out.count(), r.states(t))
	}
}

func TestAlertsWithoutNotifierStillRecordState(t *testing.T) {
	r := newAlertRig(t)
	r.out.n = 0
	r.disk = 95
	r.check(t)
	st := r.states(t)
	if len(st) != 1 || !st[0].Active() || st[0].NotifiedAt != nil {
		t.Fatalf("沒有 notifier 時仍記錄狀態、但沒有通知時間：%+v", st)
	}
	r.a.Outbox = nil
	r.disk = 10
	r.check(t)
	if r.states(t)[0].Active() {
		t.Error("沒有 outbox 時仍要解除")
	}
}

func TestEmitErrorRollsBack(t *testing.T) {
	r := newAlertRig(t)
	r.out.err = errors.New("db locked")
	r.disk = 95
	if err := r.a.Check(context.Background(), r.clk.Now()); err == nil {
		t.Fatal("寫入 outbox 失敗要回報")
	}
	if len(r.states(t)) != 0 {
		t.Error("寫入 outbox 失敗時狀態也不能寫入（同一個交易）")
	}
}

// seedRun 建立一部影音（標題 title）與一個步驟、一個任務。
func seedRun(t *testing.T, s *store.Store, ext, title, stage, class string) (store.Item, store.StageRun) {
	t.Helper()
	ctx := context.Background()
	var it store.Item
	var sr store.StageRun
	err := s.Tx(ctx, func(r *store.Repo) error {
		var err error
		if it, _, err = r.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "https://www.youtube.com/watch?v=" + ext}); err != nil {
			return err
		}
		if title != "" {
			if err := r.UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: title}); err != nil {
				return err
			}
		}
		snap, err := r.EnsureSnapshot(ctx, "sha-"+ext, `{}`)
		if err != nil {
			return err
		}
		if sr, err = r.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: snap}); err != nil {
			return err
		}
		_, err = r.InsertTask(ctx, store.NewTask{StageRunID: sr.ID, Kind: stage, Executor: "builtin", ResourceClass: class, Priority: 1})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return it, sr
}

func TestStuckTaskAlert(t *testing.T) {
	ctx := context.Background()
	r := newAlertRig(t)
	it, _ := seedRun(t, r.s, "aaaaaaaaaaa", "Kafka talk", "segment", "llm")
	seedRun(t, r.s, "bbbbbbbbbbb", "", "metadata", "download")

	r.clk.Advance(6 * time.Hour)
	r.check(t)
	if r.out.count() != 0 {
		t.Fatal("剛好 6 小時不算超過")
	}
	r.clk.Advance(time.Minute)
	r.check(t)
	if r.out.count() != 2 {
		t.Fatalf("兩部影音各一則：%d", r.out.count())
	}
	var msgs []string
	for _, ev := range r.out.events {
		msgs = append(msgs, ev.Alert.Key+"="+ev.Alert.Message)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, it.ID+"=「Kafka talk」的 segment 超過 6 小時沒有進展。") ||
		!strings.Contains(joined, "「yt:bbbbbbbbbbb」的 metadata 超過 6 小時沒有進展。") {
		t.Fatalf("訊息 = %s", joined)
	}
	r.clk.Advance(time.Hour)
	r.check(t)
	if r.out.count() != 2 {
		t.Fatal("持續卡住不重複告警")
	}

	// 暫停 llm 類別：segment 那一則解除。
	r.a.ClassPaused = func(c string) bool { return c == "llm" }
	r.check(t)
	if r.out.count() != 3 || !r.out.last().Alert.Resolved || r.out.last().Alert.Key != it.ID {
		t.Fatalf("暫停的類別不算卡住：%+v", r.out.last().Alert)
	}
	// 暫停整個佇列：全部不算。
	r.a.Paused = func() bool { return true }
	r.check(t)
	if r.out.count() != 4 {
		t.Fatalf("暫停整個佇列：%d", r.out.count())
	}
	r.a.Paused, r.a.ClassPaused = nil, nil

	// 被領取（running）後從領取的時間重新計算。
	claimed, err := r.s.Write().ClaimTasks(ctx, store.ClaimOptions{Owner: "w", Max: 10, Lease: time.Hour, Executor: "builtin"})
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimTasks = %v，%v", claimed, err)
	}
	r.check(t)
	if r.out.count() != 4 {
		t.Fatalf("剛被領取的步驟不算卡住：%d", r.out.count())
	}
	r.a.Cfg.StuckTaskHours = 0
	r.clk.Advance(7 * time.Hour)
	r.check(t)
	if r.out.count() != 4 {
		t.Fatal("停用後不告警")
	}
}

func TestStageSince(t *testing.T) {
	created := t0
	started := t0.Add(time.Hour)
	later := t0.Add(2 * time.Hour)
	cases := []struct {
		run  store.ActiveStageRun
		want time.Time
	}{
		{store.ActiveStageRun{Status: store.StatusRunning, StartedAt: &started, CreatedAt: created}, started},
		{store.ActiveStageRun{Status: store.StatusRunning, CreatedAt: created}, created},
		{store.ActiveStageRun{Status: store.StatusPending, TaskCreatedAt: started}, started},
		{store.ActiveStageRun{Status: store.StatusAwaitingAgent, TaskCreatedAt: started, TaskNotBefore: &later}, later},
		{store.ActiveStageRun{Status: store.StatusPending, TaskCreatedAt: later, TaskNotBefore: &started}, later},
	}
	for i, c := range cases {
		if got := StageSince(c.run); !got.Equal(c.want) {
			t.Errorf("%d：StageSince = %v，預期 %v", i, got, c.want)
		}
	}
}

func TestJobStaleAlert(t *testing.T) {
	ctx := context.Background()
	r := newAlertRig(t)
	w := r.s.Write()
	now := r.clk.Now()
	for _, k := range []string{BackupKind, AlertsKind, notify.DeliverKind, "subscription.check"} {
		if err := w.ScheduleJob(ctx, k, "k", now); err != nil {
			t.Fatal(err)
		}
		if err := w.RecordScheduledJobRun(ctx, k, "k", store.ScheduledJobRun{NextRunAt: now, LastRunAt: now, LastError: "boom\n  at line", Failures: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ok := now.Add(12 * time.Hour)
	if err := w.RecordScheduledJobRun(ctx, "subscription.check", "k", store.ScheduledJobRun{NextRunAt: now, LastRunAt: now, LastOKAt: &ok, LastError: "x", Failures: 2}); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(24 * time.Hour)
	r.check(t)
	if r.out.count() != 0 {
		t.Fatal("剛好 24 小時不算")
	}
	r.clk.Advance(time.Minute)
	r.check(t)
	if r.out.count() != 1 {
		t.Fatalf("只有 backup.daily（alerts、notify.deliver 不檢查，subscription.check 12 小時前成功過）：%d", r.out.count())
	}
	ev := r.out.last().Alert
	if ev.Kind != KindJobStale || ev.Key != "backup.daily:k" || ev.Message != "排程 backup.daily 超過 24 小時沒有成功執行：boom at line" {
		t.Fatalf("告警 = %+v", ev)
	}
	// 成功後解除。
	if err := w.RecordScheduledJobRun(ctx, BackupKind, "k", store.ScheduledJobRun{NextRunAt: now, LastRunAt: now, LastOKAt: &now}); err != nil {
		t.Fatal(err)
	}
	r.check(t)
	if r.out.count() != 2 || !r.out.last().Alert.Resolved {
		t.Fatalf("成功後解除：%d", r.out.count())
	}
	if got := clip(strings.Repeat("字", 300), 200); len([]rune(got)) != 201 {
		t.Errorf("clip 的長度 = %d", len([]rune(got)))
	}
}

func TestCookieStaleAlert(t *testing.T) {
	ctx := context.Background()
	r := newAlertRig(t)
	w := r.s.Write()
	for _, p := range []string{"youtube", "bilibili"} {
		if err := w.UpsertSecret(ctx, CookieKind, p, []byte("n"), []byte("c")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.UpsertSecret(ctx, "service", "groq", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	old := r.clk.Now().Add(-15 * 24 * time.Hour)
	if err := w.SetSecretTimes(ctx, CookieKind, "youtube", old); err != nil {
		t.Fatal(err)
	}
	r.check(t)
	if r.out.count() != 1 {
		t.Fatalf("只有 YouTube 過期：%d", r.out.count())
	}
	ev := r.out.last().Alert
	if ev.Kind != KindCookieStale || ev.Key != "youtube" || ev.Message != "YouTube cookie 超過 14 天沒有成功使用，可能已經失效。請重新上傳。" {
		t.Fatalf("告警 = %+v", ev)
	}
	// 成功使用後解除。
	if err := w.MarkSecretOK(ctx, CookieKind, "youtube", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.check(t)
	if r.out.count() != 2 || !r.out.last().Alert.Resolved {
		t.Fatal("成功使用後解除")
	}
	// Bilibili 只上傳過、從沒成功：用上傳的時間。
	r.clk.Advance(15 * 24 * time.Hour)
	if err := w.MarkSecretOK(ctx, CookieKind, "youtube", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.check(t)
	if r.out.count() != 3 || r.out.last().Alert.Message != "Bilibili cookie 超過 14 天沒有成功使用，可能已經失效。請重新上傳。" {
		t.Fatalf("Bilibili：%+v", r.out.last().Alert)
	}
}

func TestYtdlpFailureRateAlert(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		fails, calls int
		alert        bool
	}{{4, 5, true}, {3, 3, false}, {2, 10, false}, {5, 10, true}}
	for _, c := range cases {
		r := newAlertRig(t)
		now := r.clk.Now()
		for i := range c.calls {
			at := now.Add(-time.Hour + time.Duration(i)*time.Hour/time.Duration(c.calls)).Add(time.Minute)
			if err := r.s.Write().AddToolCall(ctx, ToolYtDlp, at, i < c.fails); err != nil {
				t.Fatal(err)
			}
		}
		// 一小時之前的紀錄不算。
		if err := r.s.Write().SetToolCalls(ctx, ToolYtDlp, now.Add(-3*time.Hour), 100, 100); err != nil {
			t.Fatal(err)
		}
		r.check(t)
		if got := r.out.count() == 1; got != c.alert {
			t.Errorf("%d/%d：告警 = %v", c.fails, c.calls, got)
		}
		if c.alert && c.calls == 5 && r.out.last().Alert.Message != "最近 1 小時 yt-dlp 失敗 4/5 次。先更新 yt-dlp；如果仍然失敗，上傳 cookie 或確認 VPS 的 IP 是否被封鎖。" {
			t.Errorf("訊息 = %q", r.out.last().Alert.Message)
		}
		// 48 小時前的統計被清掉。
		r.clk.Advance(49 * time.Hour)
		r.check(t)
		if calls, _, _ := r.s.Read().ToolCallTotals(ctx, ToolYtDlp, time.Time{}); calls != 0 {
			t.Errorf("48 小時前的統計要清掉：%d", calls)
		}
	}
}

func TestAlerterSchedule(t *testing.T) {
	ctx := context.Background()
	r := newAlertRig(t)
	if err := r.a.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := r.s.Read().ScheduledJob(ctx, AlertsKind, AlertsKey)
	if err != nil || !j.NextRunAt.Equal(r.clk.Now()) {
		t.Fatalf("啟用時立即檢查：%v %v", j.NextRunAt, err)
	}
	r.disk = 95
	r.check(t)
	// backup_failed 由每日備份管理，停用告警時不動它。
	if err := r.s.Write().ActivateAlert(ctx, KindBackupFailed, BackupKey, "x", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.a.Cfg.Enabled = false
	if err := r.a.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Read().ScheduledJob(ctx, AlertsKind, AlertsKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("停用時刪除工作：%v", err)
	}
	for _, st := range r.states(t) {
		if st.Kind == KindDiskUsage && st.Active() {
			t.Error("停用時安靜解除告警")
		}
		if st.Kind == KindBackupFailed && !st.Active() {
			t.Error("backup_failed 不受 alerts.enabled 影響")
		}
	}
	if r.out.count() != 1 {
		t.Errorf("停用時不送 resolved：%d", r.out.count())
	}
}
