package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestPasswordUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":                 {"password"},
		"不認得的子命令":               {"password", "reset"},
		"新密碼不能放在命令列":            {"password", "set", "hunter2"},
		"兩種來源擇一":                {"password", "set", "--password-env", "NEW_PW", "--password-stdin"},
		"--password-env 空名稱":    {"password", "set", "--password-env", "  "},
		"沒有來源也不在終端機":            {"password", "set"},
		"拒絕 --server":           {"password", "set", "--password-env", "NEW_PW", "--server", "http://127.0.0.1:1", "--token", "t"},
		"拒絕前置 --server":         {"--server", "http://127.0.0.1:1", "--token", "t", "password", "set", "--password-env", "NEW_PW"},
		"--password-env 的變數沒設定": {"password", "set", "--password-env", "NEW_PW"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2 且無模式（stderr：%s）", res, errOut)
			}
		})
	}
}

// loginWith 以密碼啟動一次 server 並嘗試登入，回傳登入是否成功；結束後關閉 server。
func loginWith(t *testing.T, env app.Env, password string) bool {
	t.Helper()
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(t.Context()) }()
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post("http://"+srv.Addr()+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func runWithStdinEnv(t *testing.T, env app.Env, stdin io.Reader, args ...string) (Result, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := Execute(ctx, args, app.IO{Stdin: stdin, Stdout: &out, Stderr: &errOut}, env)
	return res, out.String(), errOut.String()
}

func TestPasswordSetFromEnv(t *testing.T) {
	env, _ := testEnv(t)
	// 第一次啟動以 NOTESTEME_PASSWORD 設定密碼「pw」。
	if !loginWith(t, env, "pw") {
		t.Fatal("初始密碼應該可以登入")
	}

	env.Vars["NEW_PW"] = "battery staple"
	res, out, errOut := run(t, env, "password", "set", "--password-env", "NEW_PW")
	if res.Code != 0 || res.Mode != "" {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if strings.Contains(out+errOut, "battery staple") {
		t.Errorf("輸出不應該包含新密碼：%q %q", out, errOut)
	}
	if !strings.Contains(out, "已更新登入密碼") {
		t.Errorf("輸出 = %q", out)
	}
	// 新密碼生效；舊密碼（含 NOTESTEME_PASSWORD）失效。
	if loginWith(t, env, "pw") {
		t.Error("舊密碼不應該還能登入")
	}
	if !loginWith(t, env, "battery staple") {
		t.Error("新密碼應該可以登入")
	}
}

func TestPasswordSetJSONAndStdin(t *testing.T) {
	env, _ := testEnv(t)
	res, out, errOut := runWithStdinEnv(t, env, strings.NewReader("from stdin\r\n"), "password", "set", "--password-stdin", "--json")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	var body struct {
		Changed         bool `json:"changed"`
		RevokedSessions int  `json:"revoked_sessions"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil || !body.Changed {
		t.Fatalf("輸出 = %q（%v）", out, err)
	}
	if strings.Contains(out+errOut, "from stdin") {
		t.Errorf("輸出不應該包含新密碼")
	}
	// 資料目錄原本不存在：password set 建立它，之後啟動不再使用 NOTESTEME_PASSWORD（AUTH-R2）。
	if loginWith(t, env, "pw") {
		t.Error("NOTESTEME_PASSWORD 不應該覆寫已設定的密碼")
	}
	if !loginWith(t, env, "from stdin") {
		t.Error("stdin 的密碼（去掉換行）應該可以登入")
	}

	// stdin 第一行是空的。
	res, _, _ = runWithStdinEnv(t, env, strings.NewReader("\n"), "password", "set", "--password-stdin")
	if res.Code != 2 {
		t.Errorf("空密碼應回傳 2：%+v", res)
	}
	res, _, _ = runWithStdinEnv(t, env, strings.NewReader(""), "password", "set", "--password-stdin")
	if res.Code != 2 {
		t.Errorf("沒有輸入應回傳 2：%+v", res)
	}
}

func TestPasswordSetPromptOnTerminal(t *testing.T) {
	fakeTerminal(t)
	answers := []string{"one two", "one two"}
	old := readSecret
	readSecret = func(io.Reader) (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	t.Cleanup(func() { readSecret = old })

	env, _ := testEnv(t)
	res, out, errOut := runWithStdinEnv(t, env, strings.NewReader(""), "password", "set")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if strings.Contains(out+errOut, "one two") || !strings.Contains(errOut, "新密碼：") || !strings.Contains(errOut, "再輸入一次：") {
		t.Errorf("提示與輸出不符：out=%q err=%q", out, errOut)
	}
	if !loginWith(t, env, "one two") {
		t.Error("終端機輸入的密碼應該可以登入")
	}

	// 兩次不一致：沒有變更。
	answers = []string{"aaa", "bbb"}
	res, _, errOut = runWithStdinEnv(t, env, strings.NewReader(""), "password", "set")
	if res.Code != 2 || !strings.Contains(errOut, "不一致") {
		t.Errorf("不一致應回傳 2：%+v %q", res, errOut)
	}
	if !loginWith(t, env, "one two") {
		t.Error("兩次不一致時密碼不應該改變")
	}
}

func TestPasswordSetRefusesWhileServing(t *testing.T) {
	env, data := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(t.Context()) }()
	env.Vars["NEW_PW"] = "x"
	res, _, errOut := run(t, env, "password", "set", "--password-env", "NEW_PW")
	if res.Code != 1 || !strings.Contains(errOut, "serve") {
		t.Errorf("serve 執行中應回傳 1 並提示停止 serve：%+v %q", res, errOut)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal(err)
	}
}
