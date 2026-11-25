package nfrbench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/benchdata"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// ---- 取樣與報告 ----

// stats 是一組耗時樣本的統計。
type stats struct {
	n             int
	p50, p95, max time.Duration
}

func (s stats) String() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s max=%s", s.n, s.p50.Round(time.Microsecond*10), s.p95.Round(time.Microsecond*10), s.max.Round(time.Microsecond*10))
}

// summarize 以 nearest-rank 計算百分位數。
func summarize(ds []time.Duration) stats {
	s := slices.Clone(ds)
	slices.Sort(s)
	rank := func(p float64) time.Duration {
		i := int(math.Ceil(p*float64(len(s)))) - 1
		return s[max(0, min(i, len(s)-1))]
	}
	return stats{n: len(s), p50: rank(0.50), p95: rank(0.95), max: s[len(s)-1]}
}

// sample 先執行 warmup 次（不計），再執行 n 次並計時。
func sample(b *testing.B, warmup, n int, fn func() error) stats {
	b.Helper()
	for range warmup {
		if err := fn(); err != nil {
			b.Fatal(err)
		}
	}
	ds := make([]time.Duration, 0, n)
	for range n {
		start := time.Now()
		if err := fn(); err != nil {
			b.Fatal(err)
		}
		ds = append(ds, time.Since(start))
	}
	return summarize(ds)
}

// result 是寫進報告的一列。
type result struct {
	ID        string  `json:"id"`
	Case      string  `json:"case"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"`
	Data      string  `json:"data"`
	Samples   int     `json:"samples,omitempty"`
	Pass      bool    `json:"pass"`
	Note      string  `json:"note,omitempty"`
}

// checker 收集一個 Benchmark 內的所有結果，最後一次判定：先把每一項都記下來，再讓超標的項目失敗。
type checker struct {
	b      *testing.B
	failed []string
}

func newChecker(b *testing.B) *checker {
	c := &checker{b: b}
	b.Cleanup(func() {
		if len(c.failed) > 0 && !b.Failed() {
			b.Errorf("超過 nfr.md 的門檻：\n  %s", strings.Join(c.failed, "\n  "))
		}
	})
	return c
}

// check 記錄 value（越小越好）並與門檻比較。
func (c *checker) check(r result) {
	c.b.Helper()
	r.Pass = r.Value < r.Threshold
	c.b.ReportMetric(r.Value, metricName(r))
	// 直接寫 stderr：go test 會截斷 benchmark 的 b.Log 輸出。
	verdict := "ok"
	if !r.Pass {
		verdict = "OVER"
	}
	fmt.Fprintf(os.Stderr, "%s %s %s %s = %.3f%s（門檻 < %.3f%s）%s\n", verdict, r.ID, r.Case, r.Metric, r.Value, r.Unit, r.Threshold, r.Unit, r.Note)
	writeReport(c.b, r)
	if !r.Pass {
		c.failed = append(c.failed, fmt.Sprintf("%s %s %s = %.3f%s ≥ %.3f%s", r.ID, r.Case, r.Metric, r.Value, r.Unit, r.Threshold, r.Unit))
	}
}

// checkLatency 記錄 p50（只報告）與 p95 或 max（判定）。
func (c *checker) checkLatency(id, cse, data, judged string, s stats, limit time.Duration) {
	c.b.Helper()
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	c.b.ReportMetric(ms(s.p50), cse+"_p50_ms")
	v := s.p95
	if judged == "max" {
		v = s.max
	}
	c.check(result{ID: id, Case: cse, Metric: judged, Value: ms(v), Threshold: ms(limit), Unit: "ms", Data: data, Samples: s.n,
		Note: fmt.Sprintf("（%s）", s)})
}

func metricName(r result) string {
	unit := r.Unit
	if unit == "" {
		unit = "x"
	}
	return strings.NewReplacer(" ", "_", "/", "_").Replace(r.Case+"_"+r.Metric) + "_" + unit
}

var reportMu sync.Mutex

func writeReport(b *testing.B, r result) {
	path := os.Getenv("NOTESTEME_BENCH_REPORT")
	if path == "" {
		return
	}
	reportMu.Lock()
	defer reportMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	line, _ := json.Marshal(r)
	_, _ = f.Write(append(line, '\n'))
}

// ---- 資料 ----

var (
	libOnce sync.Once
	libDir  string
	libName string
	libErr  error
)

// lib 回傳 lib-10k 的資料目錄與名稱。NOTESTEME_BENCH_LIB 沒設定時產生縮小版（整個程序共用一份）。
func lib(b *testing.B) (string, string) {
	b.Helper()
	libOnce.Do(func() {
		if d := os.Getenv("NOTESTEME_BENCH_LIB"); d != "" {
			libDir, libName = d, benchdata.Lib10k.Name
			if n := os.Getenv("NOTESTEME_BENCH_LIB_NAME"); n != "" {
				libName = n
			}
			return
		}
		scale := 0.01
		if s := os.Getenv("NOTESTEME_BENCH_SCALE"); s != "" {
			if scale, libErr = strconv.ParseFloat(s, 64); libErr != nil {
				return
			}
		}
		p := benchdata.Lib10k.Scaled(scale)
		libDir, libErr = os.MkdirTemp("", "nst-lib")
		if libErr != nil {
			return
		}
		libName = p.Name
		_, libErr = benchdata.Generate(context.Background(), libDir, p, benchdata.Options{})
	})
	if libErr != nil {
		b.Fatal(libErr)
	}
	return libDir, libName
}

// itemLong 在新的暫存目錄產生 item-long（一部 3 小時、3,000 句，約 1 秒），回傳目錄與影音 ID。
func itemLong(b *testing.B) (string, string) {
	b.Helper()
	dir := shortDir(b)
	data := filepath.Join(dir, "data")
	res, err := benchdata.Generate(context.Background(), data, benchdata.ItemLong, benchdata.Options{})
	if err != nil {
		b.Fatal(err)
	}
	return data, res.ItemIDs[0]
}

// shortDir 建立很短的暫存目錄（Unix socket 的路徑長度上限），測試結束時刪除。
func shortDir(b testing.TB) string {
	b.Helper()
	dir, err := os.MkdirTemp("", "nsb")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// ---- in-process 的 server ----

// fakeTools 在 dir/bin 放假的 ffmpeg 與 yt-dlp（/readyz 要求可執行），回傳 bin 目錄。
func fakeTools(b testing.TB, dir string) string {
	b.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		b.Fatal(err)
	}
	for name, body := range map[string]string{
		"ffmpeg": "#!/bin/sh\necho 'ffmpeg version 7.0'\n",
		"yt-dlp": "#!/bin/sh\necho 2026.11.01\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	return bin
}

// server 是 in-process 啟動的 serve（真正的 HTTP handler、SQLite 與 worker），以 --insecure-no-auth 在 loopback 監聽。
type server struct {
	srv  *app.Server
	base string
	hc   *http.Client
}

// startServer 對 data 啟動 serve。runner 為 nil 時用只回應 --version 的替身（不會真的執行工具）。
func startServer(b testing.TB, data string, runner toolexec.Runner) *server {
	b.Helper()
	root := shortDir(b)
	bin := fakeTools(b, root)
	if runner == nil {
		runner = &ytRunner{}
	}
	env := app.Env{
		Vars: map[string]string{
			"PATH":                 bin,
			"NOTESTEME_CONFIG":     filepath.Join(root, "none.yaml"),
			"NOTESTEME_DATA":       data,
			"NOTESTEME_MASTER_KEY": "bench-master-key",
		},
		Clock: clock.Real{},
		// 零值的 netguard.Deps 拒絕所有對外連線：bench 不連網路（SponsorBlock 依 SPB-R5 存成空的產出）。
		Listen: func(network, addr string) (net.Listener, error) {
			if network == "unix" {
				return net.Listen(network, addr)
			}
			return net.Listen("tcp", "127.0.0.1:0")
		},
		Runner: runner,
	}
	srv, err := app.StartServerWith(context.Background(), env, app.ServeOptions{InsecureNoAuth: true})
	if err != nil {
		b.Fatal(err)
	}
	s := &server{srv: srv, base: "http://" + srv.Addr(), hc: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 16}}}
	b.Cleanup(func() { s.stop() })
	return s
}

func (s *server) stop() {
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	s.srv = nil
	s.hc.CloseIdleConnections()
}

// get 送出 GET，讀完整個回應；狀態不是 want 時回傳錯誤。
func (s *server) get(path string, want int) ([]byte, error) {
	resp, err := s.hc.Get(s.base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return body, fmt.Errorf("GET %s = %d：%s", path, resp.StatusCode, truncate(body))
	}
	return body, nil
}

func (s *server) post(path, body string, want int) ([]byte, error) {
	resp, err := s.hc.Post(s.base+path, "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return out, fmt.Errorf("POST %s = %d：%s", path, resp.StatusCode, truncate(out))
	}
	return out, nil
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// ---- 真正的 binary ----

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// binary 回傳 notesteme binary 的路徑：NOTESTEME_BENCH_BIN，或以 go build 建一份（與 release 相同不帶 race）。
func binary(b testing.TB) string {
	b.Helper()
	binOnce.Do(func() {
		if p := os.Getenv("NOTESTEME_BENCH_BIN"); p != "" {
			binPath = p
			return
		}
		dir, err := os.MkdirTemp("", "nst-bin")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "notesteme")
		out, err := exec.Command("go", "build", "-o", binPath, "github.com/cxyfer/notesteme/cmd/notesteme").CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build：%v\n%s", err, out)
		}
	})
	if binErr != nil {
		b.Fatal(binErr)
	}
	return binPath
}

// proc 是執行中的 notesteme 子程序。
type proc struct {
	cmd  *exec.Cmd
	base string
	log  *os.File
}

// binEnv 是執行 binary 的環境變數：只有 bench 需要的變數，PATH 先找假的工具。
func binEnv(root, data, listen string, extra ...string) []string {
	env := []string{
		"PATH=" + filepath.Join(root, "bin") + ":/usr/bin:/bin",
		"HOME=" + root,
		"NOTESTEME_DATA=" + data,
		"NOTESTEME_CONFIG=" + filepath.Join(root, "config.yaml"),
		"NOTESTEME_MASTER_KEY=bench-master-key",
	}
	if listen != "" {
		env = append(env, "NOTESTEME_LISTEN="+listen)
	}
	return append(env, extra...)
}

// freePort 找一個目前沒有使用的 loopback 埠。
func freePort(b testing.TB) string {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// startBinary 執行 notesteme serve --insecure-no-auth（不等就緒）。
func startBinary(b testing.TB, root, data string) *proc {
	b.Helper()
	addr := freePort(b)
	logf, err := os.CreateTemp(root, "serve-*.log")
	if err != nil {
		b.Fatal(err)
	}
	cmd := exec.Command(binary(b), "serve", "--insecure-no-auth")
	cmd.Env = binEnv(root, data, addr)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		b.Fatal(err)
	}
	p := &proc{cmd: cmd, base: "http://" + addr, log: logf}
	b.Cleanup(func() { p.stop(b) })
	return p
}

// waitReady 輪詢 /readyz 直到 200，回傳從 since 起算的時間。
func (p *proc) waitReady(b testing.TB, since time.Time, limit time.Duration) time.Duration {
	b.Helper()
	hc := &http.Client{Timeout: time.Second}
	for time.Since(since) < limit {
		resp, err := hc.Get(p.base + "/readyz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(since)
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	b.Fatalf("serve 在 %s 內沒有就緒；log：\n%s", limit, p.tail())
	return 0
}

func (p *proc) tail() string {
	b, _ := os.ReadFile(p.log.Name())
	if len(b) > 4000 {
		b = b[len(b)-4000:]
	}
	return string(b)
}

// stop 送 SIGTERM 並等程序結束（正常關閉會釋放資料目錄的鎖）。
func (p *proc) stop(b testing.TB) {
	if p.cmd.ProcessState != nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		b.Errorf("serve 沒有在 60 秒內結束；log：\n%s", p.tail())
	}
}

// procStatus 讀 /proc/<pid>/status 的欄位（kB）。
func procStatus(pid int, field string) (int64, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), field+":"); ok {
			fs := strings.Fields(v)
			if len(fs) == 0 {
				break
			}
			return strconv.ParseInt(fs[0], 10, 64)
		}
	}
	return 0, fmt.Errorf("/proc/%d/status 沒有 %s", pid, field)
}

// procCPUTicks 讀 /proc/<pid>/stat 的 utime＋stime（clock ticks）。
func procCPUTicks(pid int) (int64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// 第 2 欄（comm）可能有空白，從最後一個 ')' 之後算起：state 是第 3 欄，utime、stime 是第 14、15 欄。
	s := string(b)
	fs := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(fs) < 13 {
		return 0, fmt.Errorf("/proc/%d/stat 格式不對", pid)
	}
	ut, err1 := strconv.ParseInt(fs[11], 10, 64)
	st, err2 := strconv.ParseInt(fs[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("/proc/%d/stat：%v %v", pid, err1, err2)
	}
	return ut + st, nil
}

// clockTicks 是 USER_HZ；Linux 上幾乎都是 100，以 getconf 確認。
func clockTicks() float64 {
	out, err := exec.Command("getconf", "CLK_TCK").Output()
	if err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil && v > 0 {
			return v
		}
	}
	return 100
}

func requireLinux(b testing.TB) {
	if runtime.GOOS != "linux" {
		b.Skip("需要 /proc（Linux）")
	}
}
