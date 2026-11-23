package cli

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestNotifiersUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":     {"notifiers"},
		"不認得的子命令":   {"notifiers", "rm"},
		"ls 多餘參數":   {"notifiers", "ls", "extra"},
		"test 缺 id": {"notifiers", "test"},
		"test 壞 id": {"notifiers", "test", "../x"},
		"test 太多參數": {"notifiers", "test", "a", "b"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func TestNotifiersClientMode(t *testing.T) {
	testOK := true
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/notifiers":
			writeJSON(w, 200, `{"notifiers":[{"id":"phone","kind":"ntfy","config":{"topic":"t"},"events":["item.done","item.failed"],"enabled":true},{"id":"off","kind":"webhook","config":{},"events":["alert"],"enabled":false}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/notifiers/phone/test":
			if testOK {
				writeJSON(w, 200, `{"ok":true,"status":200}`)
			} else {
				writeJSON(w, 200, `{"ok":false,"error_class":"permanent","message":"通知端點回應 HTTP 401。"}`)
			}
		default:
			writeJSON(w, 404, `{"code":"not_found","message":"找不到這個 notifier。"}`)
		}
	})
	res, out, errOut := run(t, app.Env{}, "notifiers", "ls", "--server", url, "--token", "tok")
	if res.Code != 0 || res.Mode != ModeClient || out != "phone\tntfy\t啟用\titem.done,item.failed\noff\twebhook\t停用\talert\n" {
		t.Fatalf("ls：%+v %q %s", res, out, errOut)
	}
	if res, out, _ := run(t, app.Env{}, "notifiers", "ls", "--json", "--server", url, "--token", "tok"); res.Code != 0 || !strings.Contains(out, `"phone"`) {
		t.Errorf("ls --json：%+v %q", res, out)
	}
	if res, out, _ := run(t, app.Env{}, "notifiers", "test", "phone", "--server", url, "--token", "tok"); res.Code != 0 || !strings.Contains(out, "HTTP 200") {
		t.Errorf("test 成功：%+v %q", res, out)
	}
	testOK = false
	if res, out, _ := run(t, app.Env{}, "notifiers", "test", "phone", "--server", url, "--token", "tok"); res.Code != 1 || !strings.Contains(out, "HTTP 401") {
		t.Errorf("test 失敗要結束碼 1：%+v %q", res, out)
	}
	if res, _, errOut := run(t, app.Env{}, "notifiers", "test", "nope", "--server", url, "--token", "tok"); res.Code == 0 || !strings.Contains(errOut, "找不到") {
		t.Errorf("不存在：%+v %s", res, errOut)
	}
}
