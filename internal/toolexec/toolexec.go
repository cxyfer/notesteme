// Package toolexec 以子程序執行外部工具（yt-dlp、ffmpeg；ADR-0009）。
//
// production 實作 Exec 的保證：
//   - 子程序放在獨立的 process group，取消時整組一起結束，不留下孤兒程序；
//   - stdout 與 stderr 有大小上限，超過就結束子程序並回傳 ErrOutputTooLarge；
//   - 子程序的環境變數只有 allowlist 內的項目，值取自注入的 Vars，不讀程序自己的環境；
//   - 參數以 argv 傳入，不經過 shell。呼叫端不得把 secret 放進參數（cookie 等用暫存檔傳遞）。
package toolexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 預設的輸出上限與取消後的等待時間。
const (
	DefaultMaxStdout = 32 << 20
	DefaultMaxStderr = 1 << 20
	// killGrace 是 context 取消後，等待子程序的輸出管線關閉的上限。
	killGrace = 5 * time.Second
)

// DefaultEnvAllowlist 是子程序可以繼承的環境變數名稱。
var DefaultEnvAllowlist = []string{"PATH", "HOME", "LANG"}

// ErrOutputTooLarge 表示子程序的輸出超過上限，已被結束。
var ErrOutputTooLarge = errors.New("toolexec: 子程序的輸出超過上限")

// Cmd 描述一次工具呼叫。
type Cmd struct {
	// Path 是工具的絕對路徑（見 Resolver.Resolve）。
	Path string
	// Args 不含程式名稱。
	Args []string
	// Dir 是工作目錄；空字串代表呼叫者目前的目錄。
	Dir string
	// Timeout 大於 0 時，超過就結束子程序。
	Timeout time.Duration
	// Env 不是 nil 時取代 allowlist 組出的環境變數（呼叫端自己決定完整的環境，例如 git 與 hook，VAULT-R8、R11）。
	Env []string
	// KeepOutput 大於 0 時，stdout 與 stderr 合併到 Result.Stdout，只保留前 KeepOutput 位元組，其餘丟棄：
	// 輸出再多也不結束子程序（hook 的 max_output，VAULT-R11）。Result.Truncated 回報是否丟棄過。
	KeepOutput int64
}

// Result 是子程序結束後的輸出。非 0 的結束碼不是錯誤：由呼叫端依工具與 stderr 分類。
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	// Truncated 是 KeepOutput 模式下有輸出被丟棄。
	Truncated bool
}

// Runner 執行外部工具。測試可以注入替身。
type Runner interface {
	// Run 等到子程序結束。無法啟動、被取消、逾時或輸出過大時回傳錯誤；
	// 子程序自己以非 0 結束碼結束時，錯誤為 nil，結束碼在 Result.ExitCode。
	Run(ctx context.Context, c Cmd) (Result, error)
}

// Exec 是 production 的 Runner。零值不可用，請用 New。
type Exec struct {
	vars      map[string]string
	allow     []string
	maxStdout int64
	maxStderr int64
}

// New 建立 Exec；vars 是環境變數（通常是 app.Env.Vars），只有 allowlist 內的名稱會傳給子程序。
func New(vars map[string]string) *Exec {
	return &Exec{
		vars:      vars,
		allow:     DefaultEnvAllowlist,
		maxStdout: DefaultMaxStdout,
		maxStderr: DefaultMaxStderr,
	}
}

// WithLimits 回傳套用輸出上限的複本；小於等於 0 的值維持原設定。
func (e *Exec) WithLimits(maxStdout, maxStderr int64) *Exec {
	c := *e
	if maxStdout > 0 {
		c.maxStdout = maxStdout
	}
	if maxStderr > 0 {
		c.maxStderr = maxStderr
	}
	return &c
}

// env 組出子程序的環境變數：只含 allowlist 內、且 vars 有設定的項目，順序固定。
func (e *Exec) env() []string {
	names := append([]string(nil), e.allow...)
	sort.Strings(names)
	var out []string
	for _, n := range names {
		if v, ok := e.vars[n]; ok {
			out = append(out, n+"="+v)
		}
	}
	return out
}

// Run 實作 Runner。
func (e *Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Path == "" {
		return Result{}, errors.New("toolexec: 沒有指定工具的路徑")
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = e.env() // 即使是空的也要明確設定，否則會繼承程序的環境
	if c.Env != nil {
		cmd.Env = append([]string{}, c.Env...)
	}
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = killGrace

	var exceeded atomic.Bool
	kill := func() {
		exceeded.Store(true)
		_ = killProcessGroup(cmd)
	}
	var stdout, stderr bytes.Buffer
	var keep *keepWriter
	if c.KeepOutput > 0 {
		keep = &keepWriter{buf: &stdout, max: c.KeepOutput}
		cmd.Stdout, cmd.Stderr = keep, keep
	} else {
		cmd.Stdout = &limitWriter{buf: &stdout, max: e.maxStdout, onExceed: kill}
		cmd.Stderr = &limitWriter{buf: &stderr, max: e.maxStderr, onExceed: kill}
	}

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if keep != nil {
		res.Truncated = keep.dropped
	}
	if exceeded.Load() {
		return res, ErrOutputTooLarge
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		// 取消或逾時：子程序是被我們結束的，結束碼沒有意義。
		return res, fmt.Errorf("toolexec: %s：%w", c.Path, ctx.Err())
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return res, fmt.Errorf("toolexec: 執行 %s 失敗：%w", c.Path, err)
	}
}

// limitWriter 收集輸出，超過上限時呼叫 onExceed（結束子程序）並讓寫入失敗。
type limitWriter struct {
	buf      *bytes.Buffer
	max      int64
	onExceed func()
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if int64(w.buf.Len()+len(p)) > w.max {
		w.onExceed()
		return 0, ErrOutputTooLarge
	}
	return w.buf.Write(p)
}

// keepWriter 保留前 max 位元組，其餘丟棄但回報寫入成功（子程序不會因為管線關閉而失敗）。stdout 與 stderr 共用，所以加鎖。
type keepWriter struct {
	mu      sync.Mutex
	buf     *bytes.Buffer
	max     int64
	dropped bool
}

func (w *keepWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - int64(w.buf.Len())
	switch {
	case room <= 0:
		w.dropped = w.dropped || len(p) > 0
	case int64(len(p)) > room:
		w.buf.Write(p[:room])
		w.dropped = true
	default:
		w.buf.Write(p)
	}
	return len(p), nil
}
