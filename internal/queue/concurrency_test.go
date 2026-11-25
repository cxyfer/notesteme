package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// busyError 回報 err 是不是 SQLite 的鎖定錯誤（SQLITE_BUSY、SQLITE_LOCKED、BUSY_SNAPSHOT）。
func busyError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "sqlite_busy") || strings.Contains(s, "database is locked") ||
		strings.Contains(s, "sqlite_locked") || strings.Contains(s, "busy_snapshot") || strings.Contains(s, "(5)") || strings.Contains(s, "(517)")
}

// TestConcurrentWritesNeverBusy 是 NFR-REL-4 的並行寫入：同時有
//   - 8 個寫入者各建立 25 部影音（影音、stage run、任務在同一個交易），一半用 TxDurable；
//   - 4 個 worker（不同 owner）反覆領取、完成任務，直到 200 個任務都完成；
//   - 一個寫入者反覆改佇列的並行上限與暫停狀態；
//   - 3 個讀取者反覆列出佇列與讀設定；
//   - 一個備份者反覆以 VACUUM INTO 做快照（另一條可寫的連線，OPS-R1）；
//   - 4 個計數者在交易中「先讀再寫」同一個設定（read-modify-write）各 50 次。WAL 中先讀後寫的 deferred 交易
//     遇到另一條寫入連線先 commit 時會得到 SQLITE_BUSY_SNAPSHOT，busy_timeout 救不了；只有所有寫入都經過
//     同一個寫入連線時才不會出現，也不會遺失更新（最後的值剛好是 200）。
//
// 斷言沒有任何 SQLITE_BUSY／database is locked，每個任務剛好被完成一次（沒有被兩個 worker 同時領到），
// 而且整個過程沒有卡住（寫入連線只有一條，交易中誤用連線池會讓它耗盡而卡住，不是報錯）。
func TestConcurrentWritesNeverBusy(t *testing.T) {
	const (
		writers   = 8
		perWriter = 25
		total     = writers * perWriter
		workers   = 4
		counters  = 4
		perCount  = 50
	)
	s, clk := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var (
		mu        sync.Mutex
		errs      []error
		completed = map[string]int{}
	)
	report := func(who string, err error) {
		if err == nil || ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, fmt.Errorf("%s：%w", who, err))
	}
	var created sync.WaitGroup
	var bg sync.WaitGroup
	stop := make(chan struct{})

	for w := range writers {
		created.Add(1)
		go func() {
			defer created.Done()
			for i := range perWriter {
				tx := s.Tx
				if i%2 == 0 {
					tx = s.TxDurable
				}
				err := tx(ctx, func(r *store.Repo) error {
					it, _, err := r.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: fmt.Sprintf("w%02di%03dxxxx", w, i), URL: "u"})
					if err != nil {
						return err
					}
					snap, err := r.EnsureSnapshot(ctx, "sha", "{}")
					if err != nil {
						return err
					}
					sr, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: "metadata", Status: store.StatusPending, ConfigSnapshotID: snap})
					if err != nil {
						return err
					}
					_, err = r.InsertTask(ctx, store.NewTask{StageRunID: sr.ID, Kind: "metadata", Executor: ExecutorBuiltin, ResourceClass: ResourceDownload, Priority: PriorityManual})
					return err
				})
				report(fmt.Sprintf("寫入者 %d", w), err)
			}
		}()
	}

	var done sync.WaitGroup
	for k := range workers {
		done.Add(1)
		q := New(s, clk, fmt.Sprintf("worker-%d", k))
		go func() {
			defer done.Done()
			for ctx.Err() == nil {
				tasks, err := q.Claim(ctx, 3)
				if err != nil {
					report(q.Owner()+" 領取", err)
					return
				}
				for _, tk := range tasks {
					err := s.Tx(ctx, func(r *store.Repo) error { return r.CompleteTask(ctx, tk.ID, tk.LeaseToken) })
					if err != nil {
						report(q.Owner()+" 完成", err)
						return
					}
					mu.Lock()
					completed[tk.ID]++
					mu.Unlock()
				}
				mu.Lock()
				n := len(completed)
				mu.Unlock()
				if n >= total {
					return
				}
				if len(tasks) == 0 {
					time.Sleep(time.Millisecond) // 寫入者還在建立任務：讓出 CPU，不是在等時間
				}
			}
		}()
	}

	for c := range counters {
		created.Add(1)
		go func() {
			defer created.Done()
			for range perCount {
				err := s.Tx(ctx, func(r *store.Repo) error {
					v, _, err := r.Setting(ctx, "rel4.counter")
					if err != nil {
						return err
					}
					n := 0
					if v != "" {
						if _, err := fmt.Sscan(v, &n); err != nil {
							return err
						}
					}
					return r.SetSetting(ctx, "rel4.counter", fmt.Sprint(n+1))
				})
				report(fmt.Sprintf("計數者 %d", c), err)
			}
		}()
	}

	ctl := New(s, clk, "control").Control()
	bg.Add(1)
	go func() {
		defer bg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			report("佇列設定", ctl.SetLimits(ctx, map[string]int{ResourceLLM: 1 + i%4}))
			// 只暫停沒有任務的資源類別，不影響 worker 領取 download。
			report("暫停", ctl.Pause(ctx, ResourceASR))
			report("繼續", ctl.Resume(ctx, ResourceASR))
		}
	}()
	reader := New(s, clk, "reader")
	for k := range 3 {
		bg.Add(1)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, err := reader.Entries(ctx)
				report(fmt.Sprintf("讀取者 %d", k), err)
				_, _, err = s.GetSetting(ctx, "queue")
				report(fmt.Sprintf("讀取者 %d", k), err)
			}
		}()
	}
	bg.Add(1)
	go func() {
		defer bg.Done()
		dir := t.TempDir()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			report("備份", s.SnapshotTo(ctx, filepath.Join(dir, fmt.Sprintf("snap-%d.db", i%2))))
		}
	}()

	created.Wait()
	done.Wait()
	close(stop)
	bg.Wait()
	if ctx.Err() != nil {
		t.Fatalf("並行寫入在 2 分鐘內沒有做完（疑似卡住）：完成 %d／%d", len(completed), total)
	}
	for _, err := range errs {
		if busyError(err) {
			t.Errorf("出現 SQLITE_BUSY（NFR-REL-4）：%v", err)
		} else {
			t.Errorf("寫入失敗：%v", err)
		}
	}
	if len(completed) != total {
		t.Errorf("完成 %d 個任務，應該是 %d 個", len(completed), total)
	}
	for id, n := range completed {
		if n != 1 {
			t.Errorf("任務 %s 被完成 %d 次（被兩個 worker 同時領到）", id, n)
		}
	}
	if v, _, err := s.GetSetting(context.Background(), "rel4.counter"); err != nil || v != fmt.Sprint(counters*perCount) {
		t.Errorf("計數器 = %q（%v），應該是 %d：先讀後寫的交易遺失了更新", v, err, counters*perCount)
	}
	entries, err := reader.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.State == store.TaskPending || e.State == store.TaskLeased {
			t.Errorf("任務 %s 仍是 %s", e.ID, e.State)
		}
	}
}

func TestBusyError(t *testing.T) {
	for _, s := range []string{"database is locked (5) (SQLITE_BUSY)", "SQLITE_BUSY_SNAPSHOT", "x (517)"} {
		if !busyError(errors.New(s)) {
			t.Errorf("busyError(%q) = false", s)
		}
	}
	if busyError(nil) || busyError(errors.New("not found")) {
		t.Error("busyError 誤判")
	}
}
