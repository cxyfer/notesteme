package vault

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

var testHook = Hook{ID: "notify", Argv: []string{"/bin/hook", "{{note_path}}", "--title={{title}}", "{{item_id}}:{{commit}}", "{{vault_root}}"},
	Timeout: 2 * time.Second, MaxOutput: 100, EnvAllowlist: []string{"LANG", "NOTESTEME_MASTER_KEY", "GH_TOKEN", "OPENAI_API_KEY", "MISSING"}}

// VAULT-R10、R11：變數只在所在的元素內展開、不經過 shell；環境只有 allowlist 與 NOTESTEME_ITEM_ID（禁用的名稱一律不傳）。
func TestRunHookArgvAndEnv(t *testing.T) {
	r := &scriptRunner{}
	vals := HookValues{NotePath: "/v/$(rm -rf ~).md", VaultRoot: "/v", ItemID: "it1", Title: `a "b"; c`, Commit: "c1"}
	vars := map[string]string{"LANG": "C.UTF-8", "NOTESTEME_MASTER_KEY": "mk", "GH_TOKEN": "t", "OPENAI_API_KEY": "k", "PATH": "/bin", "NOTESTEME_PASSWORD": "pw"}
	res := RunHook(context.Background(), r, testHook, vals, vars, "/work")
	if res.Failed() || res.Message(testHook) != "" {
		t.Fatalf("res = %+v", res)
	}
	c := r.calls[0]
	if c.Path != "/bin/hook" || c.Dir != "/work" || c.Timeout != 2*time.Second || c.KeepOutput != 100 {
		t.Errorf("cmd = %+v", c)
	}
	if !slices.Equal(c.Args, []string{"/v/$(rm -rf ~).md", `--title=a "b"; c`, "it1:c1", "/v"}) {
		t.Errorf("args = %q", c.Args)
	}
	if !slices.Equal(c.Env, []string{"LANG=C.UTF-8", "NOTESTEME_ITEM_ID=it1"}) {
		t.Errorf("env = %q", c.Env)
	}
}

func TestRunHookOutcomes(t *testing.T) {
	h := Hook{ID: "flaky", Argv: []string{"/bin/h"}, Timeout: time.Second}
	cases := []struct {
		res  toolexec.Result
		err  error
		want string
	}{
		{toolexec.Result{ExitCode: 3}, nil, "hook flaky 失敗（結束碼 3）。檔案已經寫入。"},
		{toolexec.Result{}, context.DeadlineExceeded, "hook flaky 逾時（1 秒），已停止。檔案已經寫入。"},
		{toolexec.Result{}, errors.New("exec failed"), "hook flaky 無法執行。檔案已經寫入。"},
	}
	for _, c := range cases {
		r := &scriptRunner{reply: map[string]toolexec.Result{"": c.res}, err: map[string]error{"": c.err}}
		res := RunHook(context.Background(), r, h, HookValues{}, nil, "")
		if !res.Failed() || res.Message(h) != c.want {
			t.Errorf("%+v：訊息 = %q", c, res.Message(h))
		}
		if r.calls[0].Timeout != time.Second || r.calls[0].KeepOutput != defaultHookMaxOutput {
			t.Errorf("預設值：%+v", r.calls[0])
		}
	}
	// 預設逾時 30 秒；找不到程式時無法執行。
	if m := (HookResult{TimedOut: true}).Message(Hook{ID: "x"}); m != "hook x 逾時（30 秒），已停止。檔案已經寫入。" {
		t.Error(m)
	}
	if res := RunHook(context.Background(), &scriptRunner{}, Hook{ID: "x", Argv: []string{"no-such-hook"}}, HookValues{}, map[string]string{"PATH": t.TempDir()}, ""); res.Err == nil {
		t.Error("找不到程式應失敗")
	}
	if res := RunHook(context.Background(), &scriptRunner{}, Hook{ID: "x"}, HookValues{}, nil, ""); res.Err == nil {
		t.Error("沒有 argv 應失敗")
	}
}

// 真實子程序：逾時結束、輸出只保留前 max_output，argv 經 PATH 找到。
func TestRunHookRealProcess(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("找不到 sh")
	}
	dir := t.TempDir()
	if err := os.Symlink(sh, dir+"/myhook"); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"PATH": dir}
	run := toolexec.New(vars)
	h := Hook{ID: "chatty", Argv: []string{"myhook", "-c", "yes | head -c 5000; echo END-OF-HOOK-OUTPUT; exit 0"}, MaxOutput: 64}
	res := RunHook(context.Background(), run, h, HookValues{}, vars, dir)
	if res.Failed() || len(res.Output) != 64 || !res.Truncated || strings.Contains(string(res.Output), "END") {
		t.Errorf("res = %+v", res)
	}
	h = Hook{ID: "slow", Argv: []string{"myhook", "-c", "sleep 30"}, Timeout: 200 * time.Millisecond}
	start := time.Now()
	res = RunHook(context.Background(), run, h, HookValues{}, vars, dir)
	if !res.TimedOut || time.Since(start) > 10*time.Second {
		t.Errorf("逾時：%+v", res)
	}
}
