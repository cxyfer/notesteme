package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// withoutKey 回傳沒有 master key 的環境複本。
func withoutKey(env app.Env) app.Env {
	vars := map[string]string{}
	for k, v := range env.Vars {
		if k != "NOTESTEME_MASTER_KEY" {
			vars[k] = v
		}
	}
	env.Vars = vars
	return env
}

// client 模式不需要 master key（只有持有資料目錄的程序才需要）：serve 執行中，
// CLI 的環境變數沒有 master key，仍然走本機 socket，而且不改動資料目錄的權限。
func TestClientModeNeedsNoMasterKey(t *testing.T) {
	env, data := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	// serve 把資料目錄設成 0700；CLI 只是探測鎖，不可以再動它。
	if err := os.Chmod(data, 0o750); err != nil {
		t.Fatal(err)
	}

	res, out, errOut := run(t, withoutKey(env), "items", "ls", "--json")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(out, `"items"`) {
		t.Errorf("輸出 = %q", out)
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("client 模式不應該改動資料目錄的權限：%v %v", fi, err)
	}
}
