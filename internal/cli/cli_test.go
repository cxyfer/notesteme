package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/clock"
)

// shortTempDir 建立很短的暫存目錄：t.TempDir 含測試名稱，可能超過 Unix socket 的路徑長度上限。
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// testEnv 組出獨立模式與 serve 都能啟動的環境：空的 PATH、低成本的雜湊、fake clock。
func testEnv(t *testing.T) (app.Env, string) {
	t.Helper()
	root := shortTempDir(t)
	data := filepath.Join(root, "data")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return app.Env{
		Vars: map[string]string{
			"PATH":                 bin,
			"NOTESTEME_CONFIG":     filepath.Join(root, "none.yaml"),
			"NOTESTEME_DATA":       data,
			"NOTESTEME_PASSWORD":   "pw",
			"NOTESTEME_MASTER_KEY": "mk",
		},
		Clock: clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)),
		Listen: func(network, addr string) (net.Listener, error) {
			if network == "tcp" {
				addr = "127.0.0.1:0"
			}
			return net.Listen(network, addr)
		},
		Test: &app.TestControls{LowCostHashing: true, ManualWorker: true},
	}, data
}

// run 執行一次 Execute 並收集輸出。
func run(t *testing.T, env app.Env, args ...string) (Result, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := Execute(ctx, args, app.IO{Stdout: &out, Stderr: &errOut}, env)
	return res, out.String(), errOut.String()
}

func TestStandaloneMode(t *testing.T) {
	env, _ := testEnv(t)
	res, out, errOut := run(t, env, "items", "ls", "--json")
	if res.Code != 0 || res.Mode != ModeStandalone {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	var list struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || list.Items == nil {
		t.Fatalf("輸出不是 API 的清單格式：%q（%v）", out, err)
	}
	// 結束後鎖已經釋放：serve 可以接著啟動。
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("獨立模式結束後 serve 無法啟動：%v", err)
	}
	_ = srv.Shutdown(t.Context())
}

func TestClientModeThroughServeSocket(t *testing.T) {
	env, _ := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())

	// 不需要 token：本機 socket 以 owner 身分執行。
	res, out, errOut := run(t, env, "items", "ls", "--json")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(out, `"items"`) {
		t.Errorf("輸出 = %q", out)
	}
}

func TestServerRequiresTokenAndSkipsData(t *testing.T) {
	env, data := testEnv(t)
	res, _, errOut := run(t, env, "items", "ls", "--server", "http://127.0.0.1:1", "--data", data)
	if res.Code != 2 || res.Mode != "" || !strings.Contains(errOut, "--token") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("參數錯誤不應該碰資料目錄")
	}
}

// 參數錯誤一律在判斷模式之前檢查（CLI-R6）：結束碼 2、沒有模式、沒有碰資料目錄。
func TestUsageErrorsComeBeforeMode(t *testing.T) {
	cases := map[string][]string{
		"不認得的子命令":               {"frobnicate"},
		"不認得的旗標":                {"items", "ls", "--bogus"},
		"ingest 缺少位置參數":         {"ingest"},
		"ingest 太多位置參數":         {"ingest", "yt:abc123xxxxx", "yt:def456xxxxx"},
		"items 缺少子命令":           {"items"},
		"items 不認得的子命令":         {"items", "frob"},
		"items show 缺少 <item>":  {"items", "show"},
		"items rerun 缺少 --from": {"items", "rerun", "yt:abc123xxxxx"},
		"items rerun 步驟錯誤":      {"items", "rerun", "yt:abc123xxxxx", "--from", "nope"},
		"export 缺少 <item>":      {"export", "--format", "srt"},
		"export 格式錯誤":           {"export", "yt:abc123xxxxx", "--format", "docx"},
		"--until 不是步驟":          {"ingest", "yt:abc123xxxxx", "--until", "bogus"},
		"--timeout 沒有 --wait":   {"ingest", "yt:abc123xxxxx", "--timeout", "1m"},
		"--timeout 不是時間":        {"ingest", "yt:abc123xxxxx", "--wait", "--timeout", "soon"},
		"代號缺少分P":                {"ingest", "bili:BV1ai411c7mD"},
		"代號格式錯誤":                {"ingest", "yt:short"},
		"--server 不是網址":         {"items", "ls", "--server", "nope", "--token", "t"},
		"--status 格式錯誤":         {"items", "ls", "--status", "transcript"},
		"--status 步驟錯誤":         {"items", "ls", "--status", "nope=done"},
		"--status 狀態錯誤":         {"items", "ls", "--status", "transcript=nope"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 {
				t.Fatalf("結束碼 = %d，預期 2（stderr：%s）", res.Code, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
	t.Run("沒有參數", func(t *testing.T) {
		env, _ := testEnv(t)
		if res, _, _ := run(t, env); res.Code != 2 {
			t.Errorf("結束碼 = %d，預期 2", res.Code)
		}
	})
}

func TestVersionAndHelp(t *testing.T) {
	env, _ := testEnv(t)
	if res, out, _ := run(t, env, "--version"); res.Code != 0 || !strings.HasPrefix(out, "notesteme ") {
		t.Errorf("--version：%+v %q", res, out)
	}
	if res, out, _ := run(t, env, "--help"); res.Code != 0 || !strings.Contains(out, "用法") {
		t.Errorf("--help：%+v %q", res, out)
	}
}

func TestDoctorFailsWithoutTools(t *testing.T) {
	env, data := testEnv(t) // PATH 是空的資料夾：找不到任何工具
	res, out, errOut := run(t, env, "doctor", "--json")
	if res.Code != 1 || res.Mode != "" {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	var report struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Fix    string `json:"fix"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Checks) == 0 {
		t.Fatalf("輸出 = %q（%v）", out, err)
	}
	if c := report.Checks[0]; c.Name != "ffmpeg" || c.Status != "fail" || c.Fix == "" {
		t.Errorf("第一項 = %+v", c)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("doctor 不需要資料目錄")
	}

	res, out, _ = run(t, env, "doctor")
	if res.Code != 1 || !strings.Contains(out, "[fail] ffmpeg") || !strings.Contains(out, "修正方式") {
		t.Errorf("文字輸出：%+v %q", res, out)
	}
}

// 全域旗標也可以放在子命令之前，效果與放在之後相同；放在之後的優先。
func TestGlobalFlagsBeforeSubcommand(t *testing.T) {
	env, _ := testEnv(t)
	root := shortTempDir(t)
	other := filepath.Join(root, "other")

	res, out, errOut := run(t, env, "--data", other, "--json", "items", "ls")
	if res.Code != 0 || res.Mode != ModeStandalone || !strings.Contains(out, `"items"`) {
		t.Fatalf("結果 = %+v，out = %q，stderr：%s", res, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(other, "notesteme.db")); err != nil {
		t.Errorf("--data 放在子命令之前沒有生效：%v", err)
	}
	// 等號形式；子命令之後的 --data 優先。
	third := filepath.Join(root, "third")
	res, _, errOut = run(t, env, "--data="+other, "items", "ls", "--data", third)
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if _, err := os.Stat(filepath.Join(third, "notesteme.db")); err != nil {
		t.Errorf("子命令之後的 --data 應該優先：%v", err)
	}
	// 之前的 --server 缺少 --token：與放在之後一樣是參數錯誤。
	if res, _, errOut := run(t, env, "--server", "http://127.0.0.1:1", "items", "ls"); res.Code != 2 || !strings.Contains(errOut, "--token") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
	// 只有旗標沒有子命令。
	if res, _, _ := run(t, env, "--json"); res.Code != 2 {
		t.Errorf("只有全域旗標：%+v", res)
	}
}
