package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestSecretsUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":                    {"secrets"},
		"不認得的子命令":                  {"secrets", "ls"},
		"rotate-key 缺少 flag":       {"secrets", "rotate-key"},
		"rotate-key 空 flag":        {"secrets", "rotate-key", "--new-key-env", "  "},
		"rotate-key 多餘位置參數":        {"secrets", "rotate-key", "--new-key-env", "NEW_KEY", "extra"},
		"rotate-key 拒絕 --server":   {"secrets", "rotate-key", "--new-key-env", "NEW_KEY", "--server", "http://127.0.0.1:1", "--token", "t"},
		"rotate-key 拒絕前置 --server": {"--server", "http://127.0.0.1:1", "--token", "t", "secrets", "rotate-key", "--new-key-env", "NEW_KEY"},
		"reset 多餘位置參數":             {"secrets", "reset", "--yes", "extra"},
		"reset 非終端機缺 --yes":        {"secrets", "reset"},
		"reset 拒絕 --server":        {"secrets", "reset", "--yes", "--server", "http://127.0.0.1:1", "--token", "t"},
		"reset 拒絕前置 --server":      {"--server", "http://127.0.0.1:1", "--token", "t", "secrets", "reset", "--yes"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2 且無模式（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func TestSecretsResetConfirmationOnTTY(t *testing.T) {
	fakeTerminal(t)

	for _, stdin := range []string{"n\n", ""} {
		res, out, errOut := runWithStdin(t, stdin, "secrets", "reset")
		if res.Code != 1 || out != "" || !strings.Contains(errOut, "已取消") {
			t.Errorf("stdin=%q：結果 = %+v，out = %q，err = %q", stdin, res, out, errOut)
		}
	}

	// 即使在終端機上，指定 --server 也要回傳結束碼 2（且不詢問）。
	res, _, errOut := runWithStdin(t, "y\n", "secrets", "reset", "--server", "http://127.0.0.1:1", "--token", "t")
	if res.Code != 2 || strings.Contains(errOut, "[y/N]") {
		t.Errorf("--server 應直接拒絕：%+v，stderr：%s", res, errOut)
	}
}

func TestSecretsRotateKeyAndReset(t *testing.T) {
	env, _ := testEnv(t)
	// 先初始化資料目錄與金鑰。
	if res, _, errOut := run(t, env, "items", "ls", "--json"); res.Code != 0 {
		t.Fatalf("初始化失敗：%+v %s", res, errOut)
	}

	// serve 執行中：rotate-key 與 reset 都必須拒絕並回傳結束碼 1。
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	env.Vars["NEW_KEY"] = "mk-two"
	if res, _, _ := run(t, env, "secrets", "rotate-key", "--new-key-env", "NEW_KEY"); res.Code != 1 || res.Mode != "" {
		t.Errorf("serve 執行中 rotate-key 應回傳 1：%+v", res)
	}
	if res, _, _ := run(t, env, "secrets", "reset", "--yes"); res.Code != 1 || res.Mode != "" {
		t.Errorf("serve 執行中 reset 應回傳 1：%+v", res)
	}
	_ = srv.Shutdown(t.Context())

	// 缺少新 key 環境變數或舊 master key 錯誤時都應失敗（結束碼 1），且不破壞既有金鑰。
	if res, _, _ := run(t, env, "secrets", "rotate-key", "--new-key-env", "MISSING_KEY"); res.Code != 1 || res.Mode != "" {
		t.Errorf("缺少環境變數應回傳 1：%+v", res)
	}
	wrongEnv := app.Env{
		Vars:  map[string]string{"NOTESTEME_DATA": env.Vars["NOTESTEME_DATA"], "NOTESTEME_CONFIG": env.Vars["NOTESTEME_CONFIG"], "NOTESTEME_MASTER_KEY": "wrong", "NEW_KEY": "mk-two"},
		Clock: env.Clock,
		Test:  env.Test,
	}
	if res, _, _ := run(t, wrongEnv, "secrets", "rotate-key", "--new-key-env", "NEW_KEY"); res.Code != 1 || res.Mode != "" {
		t.Errorf("錯誤 master key 應回傳 1：%+v", res)
	}

	// 輪替 master key 成功。
	res, out, errOut := run(t, env, "secrets", "rotate-key", "--new-key-env", "NEW_KEY", "--json")
	if res.Code != 0 || res.Mode != "" || strings.TrimSpace(out) != `{"rotated":true}` {
		t.Fatalf("rotate-key：%+v out=%q err=%s", res, out, errOut)
	}

	// 舊 key 無法啟動 serve，新 key 可以。
	if _, err := app.StartServer(t.Context(), env); err == nil {
		t.Fatal("輪替後舊 master key 不應該還能啟動")
	}
	env.Vars["NOTESTEME_MASTER_KEY"] = "mk-two"
	srv, err = app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("新 master key 無法啟動：%v", err)
	}
	_ = srv.Shutdown(t.Context())

	// 用錯的 master key 重設 secrets：應重建資料金鑰並以新 key 啟動成功。
	env.Vars["NOTESTEME_MASTER_KEY"] = "mk-reset"
	res, out, errOut = run(t, env, "secrets", "reset", "--yes", "--json")
	if res.Code != 0 || res.Mode != "" || strings.TrimSpace(out) != `{"reset":true}` {
		t.Fatalf("reset：%+v out=%q err=%s", res, out, errOut)
	}
	srv, err = app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("reset 後應能以新 master key 啟動：%v", err)
	}
	_ = srv.Shutdown(t.Context())

	// 在終端機上輸入 y 確認也可執行 reset（不需 --yes）。
	fakeTerminal(t)
	var outBuf, errBuf strings.Builder
	env.Vars["NOTESTEME_MASTER_KEY"] = "mk-tty"
	res = Execute(t.Context(), []string{"secrets", "reset"}, app.IO{
		Stdin:  strings.NewReader("y\n"),
		Stdout: &outBuf,
		Stderr: &errBuf,
	}, env)
	if res.Code != 0 || !strings.Contains(errBuf.String(), "[y/N]") {
		t.Fatalf("終端機確認 reset 應成功：%+v stderr=%s", res, errBuf.String())
	}
}
