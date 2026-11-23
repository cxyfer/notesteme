package harness

import (
	"bytes"
	"net"
	"sync"

	"github.com/cxyfer/notesteme/internal/app"
)

// LogBuffer 是 serve 的 log 輸出（並行安全）。每個場景一份，供 NFR-SEC-3 掃描 secret，
// 也供 `Then the logs do not contain` 使用。
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 實作 io.Writer。
func (l *LogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String 回傳目前累積的全部內容。
func (l *LogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// ListenRecord 記錄設定的監聽位址與實際綁定的位址（ADR-0015：listener 一律綁 127.0.0.1:0）。
type ListenRecord struct {
	Configured string
	Actual     string
}

// Listen 是 app.Env.Listen 的測試版：不論設定的位址為何，一律綁 127.0.0.1:0，
// 並記錄設定值與實際位址的對照，避免場景之間搶同一個 port。
//
// "unix" 照原樣 listen、也不記錄：路徑由 serve 決定（資料目錄下的 notesteme.sock），
// 場景之間本來就不會重複，而 World.Root 刻意用很短的暫存路徑，讓它不會超過 sockaddr_un 的長度上限。
func (w *World) Listen(network, addr string) (net.Listener, error) {
	if network == "unix" {
		return net.Listen(network, addr)
	}
	ln, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w.listenMu.Lock()
	w.listens = append(w.listens, ListenRecord{Configured: addr, Actual: ln.Addr().String()})
	w.listenMu.Unlock()
	return ln, nil
}

// Listens 回傳到目前為止的監聽位址對照。
func (w *World) Listens() []ListenRecord {
	w.listenMu.Lock()
	defer w.listenMu.Unlock()
	return append([]ListenRecord(nil), w.listens...)
}

// AppEnv 以場景目前的設定組出 app.Env：環境變數取當下 Env 的複本（之後的修改不影響已啟動的 server）、
// fake clock、fakenet、場景的 listener 與 log buffer。密碼雜湊使用低成本參數。
func (w *World) AppEnv() app.Env {
	vars := make(map[string]string, len(w.Env))
	for k, v := range w.Env {
		vars[k] = v
	}
	var impl map[string]int
	if len(w.ImplVersions) > 0 {
		impl = make(map[string]int, len(w.ImplVersions))
		for k, v := range w.ImplVersions {
			impl[k] = v
		}
	}
	// migration 的覆寫（OPS-R3、R4）；組合不合理的步驟在設定當下就會失敗，這裡不會有錯誤。
	var est map[string]float64
	if len(w.CostEstimates) > 0 {
		est = make(map[string]float64, len(w.CostEstimates))
		for k, v := range w.CostEstimates {
			est[k] = v
		}
	}
	migs, _ := w.Migrations()
	return app.Env{
		Vars:   vars,
		Clock:  w.Clock,
		Net:    w.Net.Deps(),
		Listen: w.Listen,
		Log:    w.Log,
		Test: &app.TestControls{LowCostHashing: true, ManualWorker: true, SkipShutdownGrace: true, SerialLLM: true, ImplVersions: impl, Migrations: migs,
			CostEstimates: est, PricesAsOf: w.PricesAsOf, DiskUsage: w.diskUsagePercent},
	}
}

// DefaultDiskUsage 是沒有 `the data volume is … percent full` 時產品讀到的 /data 用量（OPS 待決問題 O10）：
// CI 機器真實的磁碟用量不會觸發告警。
const DefaultDiskUsage = 10

// SetDiskUsage 設定產品讀到的 /data 用量百分比（0～100）。
func (w *World) SetDiskUsage(percent int) { w.diskUsage.Store(int32(percent) + 1) }

// diskUsagePercent 是交給 app 的 TestControls.DiskUsage：每次呼叫都讀目前的設定。
func (w *World) diskUsagePercent(string) (int, error) {
	if v := w.diskUsage.Load(); v > 0 {
		return int(v - 1), nil
	}
	return DefaultDiskUsage, nil
}
