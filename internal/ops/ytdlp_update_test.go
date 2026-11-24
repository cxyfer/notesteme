package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// fakeUpdater 是假的更新來源：依序回傳 results，不連網。
type fakeUpdater struct {
	results []fakeUpdate
	reqs    []ytdlp.UpdateRequest
}

type fakeUpdate struct {
	res *ytdlp.Result
	err error
}

func (f *fakeUpdater) Update(_ context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error) {
	f.reqs = append(f.reqs, req)
	r := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return r.res, r.err
}

type ytdlpRig struct {
	s   *store.Store
	u   *YtDlpUpdate
	f   *fakeUpdater
	out *fakeEmitter
	sch *scheduler.Scheduler
}

func newYtdlpRig(t *testing.T, results ...fakeUpdate) *ytdlpRig {
	t.Helper()
	s, clk := openStore(t)
	f := &fakeUpdater{results: results}
	out := &fakeEmitter{n: 1}
	u := &YtDlpUpdate{Store: s, Clock: clk, Outbox: out, Cfg: config.YtDlpUpdate{Enabled: true, At: "04:00", Timezone: "UTC"}, Updater: f}
	sch := scheduler.New(s, clk, nil, 0)
	if err := u.Register(sch); err != nil {
		t.Fatal(err)
	}
	if err := u.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &ytdlpRig{s: s, u: u, f: f, out: out, sch: sch}
}

func (r *ytdlpRig) job(t *testing.T) store.ScheduledJob {
	t.Helper()
	j, err := r.s.Read().ScheduledJob(context.Background(), YtDlpUpdateKind, YtDlpUpdateKey)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (r *ytdlpRig) alert(t *testing.T) *store.AlertState {
	t.Helper()
	states, err := r.s.Read().AlertStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stateOf(states, KindYtdlpUpdateFailed, YtDlpUpdateKey)
}

// runAt 讓排程器在 at 執行一輪。
func (r *ytdlpRig) runAt(t *testing.T, at time.Time) {
	t.Helper()
	r.u.Clock.(interface{ Set(time.Time) }).Set(at)
	if err := r.sch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

var at4 = time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)

// 回退時告警（OPS-R6 的固定文字）並通知一次；排程本身算成功，明天同一時間再試；下一次成功時解除並通知恢復。
func TestYtDlpUpdateRollbackAlertsThenRecovers(t *testing.T) {
	rolled := &ytdlp.Result{Version: "2026.11.01", RolledBack: true, Message: "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。", Reason: "probe failed"}
	ok := &ytdlp.Result{Version: "2026.11.10", Message: "yt-dlp 已更新到 2026.11.10。"}
	r := newYtdlpRig(t, fakeUpdate{res: rolled}, fakeUpdate{res: rolled}, fakeUpdate{res: ok})
	if j := r.job(t); !j.NextRunAt.Equal(at4) {
		t.Fatalf("第一次排定 = %v", j.NextRunAt)
	}

	r.runAt(t, at4)
	if len(r.f.reqs) != 1 || r.f.reqs[0] != (ytdlp.UpdateRequest{}) {
		t.Fatalf("排程更新應以「更新到最新版」呼叫一次：%+v", r.f.reqs)
	}
	a := r.alert(t)
	if a == nil || !a.Active() || a.Message != rolled.Message || r.out.count() != 1 {
		t.Fatalf("回退應告警一次：%+v，通知 %d", a, r.out.count())
	}
	if j := r.job(t); j.Failures != 0 || !j.NextRunAt.Equal(at4.Add(24*time.Hour)) {
		t.Fatalf("回退不算排程失敗，明天再試：%+v", j)
	}
	// 第二天又回退：告警持續成立，不重複通知。
	r.runAt(t, at4.Add(24*time.Hour))
	if r.out.count() != 1 {
		t.Errorf("持續回退不應重複通知：%d", r.out.count())
	}
	// 第三天成功：解除並送出恢復。
	r.runAt(t, at4.Add(48*time.Hour))
	if a := r.alert(t); a == nil || a.Active() {
		t.Fatalf("成功後應解除：%+v", a)
	}
	if r.out.count() != 2 || !r.out.last().Alert.Resolved || !strings.Contains(r.out.last().Message, "2026.11.10") {
		t.Errorf("恢復通知 = %+v", r.out.last())
	}
}

// OPS-R7：固定版本時 Manager 回報略過，排程不告警、不算失敗。
func TestYtDlpUpdatePinnedSkips(t *testing.T) {
	r := newYtdlpRig(t, fakeUpdate{res: &ytdlp.Result{Version: "2026.11.01", Skipped: true, Pinned: "2026.11.01", Message: "yt-dlp 已固定在 2026.11.01，略過更新。"}})
	r.runAt(t, at4)
	if a := r.alert(t); a != nil || r.out.count() != 0 {
		t.Errorf("固定版本不應告警：%+v %d", a, r.out.count())
	}
	if j := r.job(t); j.Failures != 0 || j.LastOKAt == nil {
		t.Errorf("略過算成功：%+v", j)
	}
}

// 安裝失敗：告警並回傳錯誤，由排程器退避（1 分鐘）；告警文字不含 pip 的輸出。
func TestYtDlpUpdateInstallFailureBacksOff(t *testing.T) {
	r := newYtdlpRig(t, fakeUpdate{err: errors.Join(ytdlp.ErrInstall, errors.New("pip: secret-index.example /data/tools"))})
	r.runAt(t, at4)
	j := r.job(t)
	if j.Failures != 1 || !j.NextRunAt.Equal(at4.Add(time.Minute)) {
		t.Fatalf("安裝失敗應退避重試：%+v", j)
	}
	a := r.alert(t)
	if a == nil || a.Message != msgYtDlpInstallFailed || strings.Contains(a.Message, "/data") {
		t.Errorf("告警 = %+v", a)
	}
}

// 自行指定路徑、平台不支援：不是失敗，什麼都不做；手動更新正在進行：延後，不算失敗。
func TestYtDlpUpdateNotManagedAndBusy(t *testing.T) {
	for _, e := range []error{ytdlp.ErrExternallyManaged, ytdlp.ErrUnsupportedPlatform} {
		r := newYtdlpRig(t, fakeUpdate{err: e})
		r.runAt(t, at4)
		if j := r.job(t); j.Failures != 0 || !j.NextRunAt.Equal(at4.Add(24*time.Hour)) || r.alert(t) != nil {
			t.Errorf("%v：%+v", e, j)
		}
	}
	r := newYtdlpRig(t, fakeUpdate{err: ytdlp.ErrBusy})
	r.runAt(t, at4)
	if j := r.job(t); j.Failures != 0 || !j.NextRunAt.Equal(at4.Add(YtDlpBusyRetry)) || r.alert(t) != nil {
		t.Errorf("ErrBusy：%+v", j)
	}
}

// 停用時刪除工作，並安靜地解除仍成立的告警（不通知）。
func TestYtDlpUpdateDisabled(t *testing.T) {
	ctx := context.Background()
	r := newYtdlpRig(t, fakeUpdate{err: ytdlp.ErrInstall})
	r.runAt(t, at4)
	if a := r.alert(t); a == nil || !a.Active() {
		t.Fatal("應有告警")
	}
	n := r.out.count()
	r.u.Cfg.Enabled = false
	if err := r.u.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Read().ScheduledJob(ctx, YtDlpUpdateKind, YtDlpUpdateKey); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("停用後工作應刪除：%v", err)
	}
	if a := r.alert(t); a == nil || a.Active() || r.out.count() != n {
		t.Errorf("停用應安靜地解除告警：%+v，通知 %d → %d", a, n, r.out.count())
	}
	r.u.Cfg = config.YtDlpUpdate{Enabled: true, At: "x"}
	if err := r.u.Schedule(ctx); err == nil {
		t.Error("時間格式錯誤要回報")
	}
}

// 時區：Asia/Taipei 的 04:00 是 UTC 20:00。
func TestYtDlpUpdateTimezone(t *testing.T) {
	s, clk := openStore(t)
	u := &YtDlpUpdate{Store: s, Clock: clk, Cfg: config.YtDlpUpdate{Enabled: true, At: "04:00", Timezone: "Asia/Taipei"}, Updater: &fakeUpdater{}}
	if err := u.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Read().ScheduledJob(context.Background(), YtDlpUpdateKind, YtDlpUpdateKey)
	if want := time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC); !j.NextRunAt.Equal(want) {
		t.Errorf("下一次 = %v，預期 %v", j.NextRunAt, want)
	}
}
