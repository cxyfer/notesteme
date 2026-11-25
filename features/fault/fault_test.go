//go:build faultinject && unix

package fault

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/faultpoint"
)

// 父程序讀的環境變數。
const (
	// envIterations 是每個（點 × 時點）的執行次數；預設 3（CI 的 go job），每晚的 fault.yml 設 100（NFR-REL-1）。
	envIterations = "NOTESTEME_FAULT_ITERATIONS"
	// envParallel 是同時執行的迭代數；預設 2。
	envParallel = "NOTESTEME_FAULT_PARALLEL"
	// envOnly 只跑名稱含這個字串的點（本機除錯用）。
	envOnly = "NOTESTEME_FAULT_ONLY"
)

// childTimeout 是一個子程序的硬上限：卡住（例如 pipeline 停在某個狀態、等不到的鎖）算失敗，不拖垮整個 job。
const childTimeout = 90 * time.Second

// requiredPoints 是劇本一定要命中的故障點。基準執行沒有命中其中任何一個就失敗：
// 有人重構掉某個 Hit、或劇本不再經過某個步驟時，每晚的 100 次不會默默變成空跑。
var requiredPoints = func() []string {
	var out []string
	for _, st := range []string{"metadata", "transcript", "normalize", "correct", "translate", "sponsorblock"} {
		for _, at := range []string{faultpoint.StageStart, faultpoint.StageMid, faultpoint.StageCommit} {
			out = append(out, faultpoint.Name(at, st))
		}
	}
	return append(out,
		faultpoint.ArtifactTmpWritten, faultpoint.ArtifactRenamed,
		faultpoint.Name(faultpoint.PaidReturned, "groq"),
		faultpoint.Name(faultpoint.PaidReturned, "llm.correct"), faultpoint.Name(faultpoint.PaidLanded, "llm.correct"),
		faultpoint.Name(faultpoint.PaidReturned, "llm.translate"), faultpoint.Name(faultpoint.PaidLanded, "llm.translate"),
	)
}()

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q 應該是正整數", name, v)
	}
	return n
}

// TestFaultInjection 是 NFR-REL-1～3 的故障注入測試：每個故障點各跑 NOTESTEME_FAULT_ITERATIONS 次
// 「kill -9 → 重新啟動 → 斷言」。第 i 次在該點的第 1+(i mod 命中次數) 次命中時殺掉，同一個點的不同命中都會輪到
// （例如 translate 的 plan、chunk、assemble 三個任務）。
func TestFaultInjection(t *testing.T) {
	iterations := envInt(t, envIterations, 3)
	parallel := envInt(t, envParallel, 2)
	start := time.Now()

	// 基準：不殺，做完整條 pipeline；記下命中的點與次數、最後的結果與付費呼叫。
	base := t.TempDir()
	if res := runChild(t, base, phaseFirst, ""); res.err != nil {
		t.Fatalf("基準執行失敗：%v\n%s", res.err, res.output)
	}
	hits, err := readTrace(filepath.Join(base, traceFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range requiredPoints {
		if hits[p] == 0 {
			t.Errorf("基準執行沒有命中故障點 %s（故障點被移除，或劇本不再經過這個步驟）", p)
		}
	}
	want, err := inspect(base)
	if err != nil {
		t.Fatalf("檢查基準執行的結果：%v", err)
	}
	if err := want.complete(); err != nil {
		t.Fatalf("基準執行沒有做完：%v", err)
	}
	baseCalls, err := readPaidCalls(base)
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range baseCalls {
		if n != 1 {
			t.Errorf("基準執行中同一個付費請求送了 %d 次：%s", n, k)
		}
	}
	// 做完之後重新啟動：不應該再送出任何付費請求，結果不變（REL-3 的「已經拿到的結果不再付錢」）。
	if res := runChild(t, base, phaseRestart, ""); res.err != nil {
		t.Fatalf("基準執行重新啟動失敗：%v\n%s", res.err, res.output)
	}
	if again, err := readPaidCalls(base); err != nil || !mapsEqual(again, baseCalls) {
		t.Errorf("做完之後重新啟動又送出了付費請求：%v（err=%v）", diffCalls(baseCalls, again), err)
	}
	if t.Failed() {
		return
	}

	points := make([]string, 0, len(hits))
	for p := range hits {
		if only := os.Getenv(envOnly); only == "" || strings.Contains(p, only) {
			points = append(points, p)
		}
	}
	sort.Strings(points)

	type job struct {
		point string
		occ   int
	}
	jobs := make(chan job)
	var (
		mu       sync.Mutex
		failures []string
		perPoint = map[string]time.Duration{}
		done     int
	)
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				began := time.Now()
				err := runIteration(t, j.point, j.occ, want, baseCalls)
				mu.Lock()
				perPoint[j.point] += time.Since(began)
				done++
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s 第 %d 次命中：%v", j.point, j.occ, err))
				}
				mu.Unlock()
			}
		}()
	}
	for i := range iterations {
		for _, p := range points {
			jobs <- job{point: p, occ: 1 + i%hits[p]}
		}
	}
	close(jobs)
	wg.Wait()

	t.Logf("故障注入：%d 個點 × %d 次 = %d 次迭代，耗時 %s（並行 %d）", len(points), iterations, done, time.Since(start).Round(time.Second), parallel)
	for _, p := range points {
		t.Logf("  %-34s 命中 %d 次／執行；%d 次迭代共 %s", p, hits[p], iterations, perPoint[p].Round(time.Millisecond))
	}
	t.Logf("重新啟動後留下、沒有 DB 記錄指向的正式產出檔：共 %d 個（已知限制，不影響正確性）", orphanFinals.Load())
	for _, f := range failures {
		t.Error(f)
	}
}

// runIteration 跑一次「在 point 的第 occ 次命中 kill -9 → 斷言 → 重新啟動 → 斷言」。
// 成功時清掉根目錄；失敗時保留，錯誤訊息附上路徑與可以重現的 (point, occ)。
// orphanFinals 是重新啟動後仍留著、沒有 DB 記錄指向的正式產出檔的總數（rename 之後、DB 記錄之前被殺；
// 不影響正確性，PIPE 待決問題記為已知限制），只統計、不讓測試失敗。
var orphanFinals atomic.Int64

func runIteration(t *testing.T, point string, occ int, want *outcome, baseCalls map[string]int) (err error) {
	root, err := os.MkdirTemp("", "nstfault")
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			_ = removeRoot(root)
		} else {
			err = fmt.Errorf("%w（保留 %s）", err, root)
		}
	}()

	first := runChild(t, root, phaseFirst, point+":"+strconv.Itoa(occ))
	if !first.killed {
		return fmt.Errorf("子程序應該在故障點被 SIGKILL，實際：%v\n%s", first.err, first.output)
	}
	if last, err := lastTraceLine(filepath.Join(root, traceFile)); err != nil || last != fmt.Sprintf("KILL %s %d", point, occ) {
		return fmt.Errorf("trace 的最後一行應該是 KILL %s %d，實際 %q（err=%v）", point, occ, last, err)
	}
	// 重新啟動之前：被殺當下的 DB＋WAL（複製一份檢查，不讓檢查本身先做 recovery 與 checkpoint）。
	if err := integrityOfCopy(root); err != nil {
		return fmt.Errorf("kill 之後、重新啟動之前：%w", err)
	}

	second := runChild(t, root, phaseRestart, "")
	if second.err != nil {
		return fmt.Errorf("重新啟動後沒有做完：%v\n%s", second.err, second.output)
	}
	got, err := inspect(root)
	if err != nil {
		return fmt.Errorf("重新啟動後：%w", err)
	}
	if err := got.complete(); err != nil {
		return fmt.Errorf("重新啟動後：%w", err)
	}
	orphanFinals.Add(int64(len(got.orphanFiles)))
	if diff := want.diff(got); diff != "" {
		return fmt.Errorf("重新啟動後的結果與沒中斷時不同：\n%s", diff)
	}
	calls, err := readPaidCalls(root)
	if err != nil {
		return err
	}
	return checkPaid(point, baseCalls, calls)
}

// checkPaid 是 NFR-REL-3：每個付費請求最多多送一次，而且只有「被殺時還在途中」的那一個可以重送；
// 在結果已經落地之後（paid.landed）被殺時，一個都不能重送。基準執行沒有的請求也不能出現。
func checkPaid(point string, base, got map[string]int) error {
	var twice []string
	for k, n := range got {
		switch b := base[k]; {
		case b == 0:
			return fmt.Errorf("出現基準執行沒有的付費請求：%s（%d 次）", k, n)
		case n > b+1:
			return fmt.Errorf("付費請求 %s 送了 %d 次（基準 %d 次）", k, n, b)
		case n == b+1:
			twice = append(twice, k)
		}
	}
	for k := range base {
		if got[k] == 0 {
			return fmt.Errorf("少了付費請求 %s：結果應該與沒中斷時相同", k)
		}
	}
	if len(twice) > 1 {
		return fmt.Errorf("%d 個付費請求被重送（最多只有被殺時在途中的一個）：%v", len(twice), twice)
	}
	if len(twice) == 1 && strings.HasPrefix(point, faultpoint.PaidLanded+"/") {
		return fmt.Errorf("付費結果已經落地之後被殺，重新啟動又付了一次：%s", twice[0])
	}
	return nil
}

// childResult 是一個子程序的結果。
type childResult struct {
	killed bool // 被 SIGKILL 結束
	err    error
	output string
}

// traceFile 是 faultpoint 的 trace（NOTESTEME_FAULT_TRACE），放在場景根目錄。
const traceFile = "fault-trace.log"

// runChild 以子程序執行 TestFaultChild。子程序在自己的 process group 中，結束後整組 SIGKILL，
// 收掉被孤立的假工具程序。
func runChild(t *testing.T, root, phase, fault string, extraEnv ...string) childResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFaultChild$", "-test.count=1")
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "NOTESTEME_FAULT") || strings.HasPrefix(kv, "NST_FAULT_")
	})
	env = append(env, envChildRoot+"="+root, envChildPhase+"="+phase, faultpoint.EnvTrace+"="+filepath.Join(root, traceFile))
	if fault != "" {
		env = append(env, faultpoint.EnvFault+"="+fault)
	}
	env = append(env, extraEnv...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	res := childResult{err: err, output: tail(out.String(), 4000)}
	if ctx.Err() != nil {
		res.err = fmt.Errorf("子程序超過 %s 沒有結束（卡住）", childTimeout)
		return res
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL {
			res.killed = true
		}
	}
	return res
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// readTrace 讀 trace 檔，回傳每個點的命中次數（不含 KILL 那一行）。
func readTrace(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hits := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" && !strings.HasPrefix(line, "KILL ") {
			hits[line]++
		}
	}
	return hits, sc.Err()
}

func lastTraceLine(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-1], nil
}

// readPaidCalls 讀付費呼叫的計數檔，回傳每個請求 key 的次數。最後一行不完整（寫到一半被殺）時，
// 保守地算成一次呼叫：provider 可能已經收到。
func readPaidCalls(root string) (map[string]int, error) {
	b, err := os.ReadFile(filepath.Join(root, paidCallsFile))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	calls := map[string]int{}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			calls[strings.Join(f[:min(len(f), 2)], " ")+" "+lastOr(f, 2)]++
		}
	}
	return calls, nil
}

func lastOr(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return "?"
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func diffCalls(base, got map[string]int) []string {
	var out []string
	for k, n := range got {
		if base[k] != n {
			out = append(out, fmt.Sprintf("%s：基準 %d 次，實際 %d 次", k, base[k], n))
		}
	}
	sort.Strings(out)
	return out
}

// removeRoot 清掉迭代的根目錄（harness 在 media/ 放了指向 / 的 symlink，先移除它本身）。
func removeRoot(root string) error {
	link := filepath.Join(root, "media", "link-to-root")
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.RemoveAll(root)
}
