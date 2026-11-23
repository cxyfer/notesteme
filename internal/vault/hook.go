package vault

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// hook 的執行（VAULT-R10、R11）：argv 是設定檔定義的字串陣列，變數只在所在的元素內展開，不經過 shell；
// 環境變數只有 env_allowlist 加上 NOTESTEME_ITEM_ID；逾時結束整個 process group；輸出只保留前 max_output。

// 預設值（設定檔載入時已補上；這裡再保險一次）。
const (
	defaultHookTimeout   = 30 * time.Second
	defaultHookMaxOutput = 64 << 10
)

// HookValues 是 argv 可以使用的變數（VAULT-R10）。
type HookValues struct {
	NotePath  string
	VaultRoot string
	ItemID    string
	Title     string
	Commit    string
}

// expand 展開一個 argv 元素中的變數。
func (v HookValues) expand(s string) string {
	return strings.NewReplacer(
		"{{note_path}}", v.NotePath, "{{vault_root}}", v.VaultRoot, "{{item_id}}", v.ItemID,
		"{{title}}", v.Title, "{{commit}}", v.Commit,
	).Replace(s)
}

// forbiddenEnv 回報 hook 不能看到的變數（同設定檔的檢查，執行時再擋一次）。
func forbiddenEnv(name string) bool {
	u := strings.ToUpper(name)
	return u == "NOTESTEME_MASTER_KEY" || u == "NOTESTEME_PASSWORD" || strings.HasSuffix(u, "_API_KEY") || strings.HasSuffix(u, "_TOKEN")
}

// HookResult 是一次 hook 的結果。
type HookResult struct {
	ExitCode int
	TimedOut bool
	// Err 是無法啟動等錯誤（不是非 0 的結束碼）。
	Err error
	// Output 是 stdout 與 stderr 合計的前 max_output 位元組。
	Output    []byte
	Truncated bool
}

// Failed 回報 hook 是否沒有成功。
func (r HookResult) Failed() bool { return r.Err != nil || r.TimedOut || r.ExitCode != 0 }

// Message 是 hook 失敗時影音顯示的提醒（VAULT-R11）；成功時是空字串。
func (r HookResult) Message(h Hook) string {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	switch {
	case r.TimedOut:
		return fmt.Sprintf("hook %s 逾時（%d 秒），已停止。檔案已經寫入。", h.ID, int(timeout.Round(time.Second)/time.Second))
	case r.Err != nil:
		return fmt.Sprintf("hook %s 無法執行。檔案已經寫入。", h.ID)
	case r.ExitCode != 0:
		return fmt.Sprintf("hook %s 失敗（結束碼 %d）。檔案已經寫入。", h.ID, r.ExitCode)
	}
	return ""
}

// HookEnv 組出 hook 的環境變數：env_allowlist 中、vars 有設定的變數，加上 NOTESTEME_ITEM_ID。
func HookEnv(h Hook, vars map[string]string, itemID string) []string {
	env := []string{}
	for _, name := range h.EnvAllowlist {
		if forbiddenEnv(name) || name == "NOTESTEME_ITEM_ID" {
			continue
		}
		if v, ok := vars[name]; ok {
			env = append(env, name+"="+v)
		}
	}
	slices.Sort(env)
	return append(env, "NOTESTEME_ITEM_ID="+itemID)
}

// RunHook 執行一個 hook；dir 是工作目錄（任務的暫存目錄）。argv[0] 不是絕對路徑時在 vars 的 PATH 中尋找。
func RunHook(ctx context.Context, runner toolexec.Runner, h Hook, vals HookValues, vars map[string]string, dir string) HookResult {
	if len(h.Argv) == 0 {
		return HookResult{Err: errors.New("hook 沒有 argv")}
	}
	prog := h.Argv[0]
	if !filepath.IsAbs(prog) {
		p, err := toolexec.LookPath(prog, vars["PATH"])
		if err != nil {
			return HookResult{Err: err}
		}
		prog = p
	}
	args := make([]string, 0, len(h.Argv)-1)
	for _, a := range h.Argv[1:] {
		args = append(args, vals.expand(a))
	}
	timeout, max := h.Timeout, int64(h.MaxOutput)
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	if max <= 0 {
		max = defaultHookMaxOutput
	}
	res, err := runner.Run(ctx, toolexec.Cmd{
		Path: prog, Args: args, Dir: dir, Timeout: timeout, Env: HookEnv(h, vars, vals.ItemID), KeepOutput: max,
	})
	out := HookResult{ExitCode: res.ExitCode, Output: res.Stdout, Truncated: res.Truncated}
	switch {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		out.TimedOut = true
	default:
		out.Err = err
	}
	return out
}
