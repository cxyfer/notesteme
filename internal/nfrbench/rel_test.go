package nfrbench

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/store"
)

// NFR-REL-5：還原（OPS-R2）在 lib-10k 上 < 5 分鐘；備份不阻擋 API 超過 1 秒。
//
//   - 備份：in-process 的 serve 執行 POST /api/backup（OPS-R1，VACUUM INTO 加 tar.gz，串流寫到檔案），
//     同時另一個 client 連續打 GET /api/items?limit=50，判定備份期間最慢的一次 < 1 秒。
//   - 還原：serve 停止後，以真正的 binary 執行 notesteme restore <file> 還原到同一個資料目錄
//     （含 OPS-R2 的「還原前先為目前的 DB 留一份快照」），判定耗時 < 5 分鐘；還原後的 DB 影音數不變。
//
// 這個 bench 會改寫 NOTESTEME_BENCH_LIB 的 DB（還原成它自己的備份，內容相同），放在 lib-10k 組的最後。
func BenchmarkRel5BackupRestore(b *testing.B) {
	requireLinux(b)
	dir, name := lib(b)
	out := shortDir(b)
	c := newChecker(b)
	for b.Loop() {
		s := startServer(b, dir, nil)
		total := s.listPage(b, "limit=1").Total

		// 備份前的基準（同一個查詢）。
		base := sample(b, 3, 30, func() error { _, err := s.get("/api/items?limit=50", http.StatusOK); return err })

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			during  []time.Duration
			stop    = make(chan struct{})
			pollErr error
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				start := time.Now()
				_, err := s.get("/api/items?limit=50", http.StatusOK)
				d := time.Since(start)
				mu.Lock()
				during = append(during, d)
				if err != nil && pollErr == nil {
					pollErr = err
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
			}
		}()
		file := filepath.Join(out, "backup.tar.gz")
		bstart := time.Now()
		size, err := s.backupTo(file)
		backupDur := time.Since(bstart)
		close(stop)
		wg.Wait()
		if err != nil {
			b.Fatal(err)
		}
		if pollErr != nil {
			b.Fatal(pollErr)
		}
		st := summarize(during)
		c.checkLatency("NFR-REL-5", "api_during_backup", name, "max", st, time.Second)
		fmt.Fprintf(os.Stderr, "    備份 %s、%.1fMB；備份前 %s\n", backupDur.Round(time.Millisecond), float64(size)/(1<<20), base)
		b.ReportMetric(backupDur.Seconds(), "backup_s")
		s.stop()

		root := shortDir(b)
		fakeTools(b, root)
		if err := writeNoNetConfig(root); err != nil {
			b.Fatal(err)
		}
		cmd := exec.Command(binary(b), "restore", file)
		cmd.Env = binEnv(root, dir, "")
		rstart := time.Now()
		if outb, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("restore：%v\n%s", err, outb)
		}
		restoreDur := time.Since(rstart)
		c.check(result{ID: "NFR-REL-5", Case: "restore", Metric: "duration", Value: restoreDur.Seconds(), Threshold: 300, Unit: "s", Data: name,
			Note: fmt.Sprintf("（備份檔 %.1fMB）", float64(size)/(1<<20))})

		db, err := sqlitedb.Open(filepath.Join(dir, store.DBFileName), true)
		if err != nil {
			b.Fatal(err)
		}
		var n int
		err = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`).Scan(&n)
		_ = db.Close()
		if err != nil || n != total {
			b.Fatalf("還原後的影音數 = %d（%v），預期 %d", n, err, total)
		}
		_ = os.Remove(file)
	}
}

// backupTo 呼叫 POST /api/backup，把 tar.gz 寫到 file。
func (s *server) backupTo(file string) (int64, error) {
	resp, err := s.hc.Post(s.base+"/api/backup", "application/json", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("POST /api/backup = %d：%s", resp.StatusCode, truncate(body))
	}
	f, err := os.Create(file)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}
