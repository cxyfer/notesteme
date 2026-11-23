package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

func runDeployCheck(t *testing.T, vars map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	res := Execute(context.Background(), append([]string{"deploy-check"}, args...), app.IO{Stdout: &out, Stderr: &errOut}, app.Env{Vars: vars})
	return res.Code, out.String(), errOut.String()
}

func TestDeployCheckVPS(t *testing.T) {
	data := t.TempDir()
	base := map[string]string{"NOTESTEME_DATA": data, "NOTESTEME_DOMAIN": "notes.example.com", "NOTESTEME_DEPLOYMENT": "vps"}
	code, _, errOut := runDeployCheck(t, base, "vps")
	if code != 1 || !strings.Contains(errOut, "vps profile 必須設定登入密碼。") {
		t.Fatalf("沒有密碼：%d %q", code, errOut)
	}
	withPW := map[string]string{"NOTESTEME_PASSWORD": "a-long-enough-owner-password"}
	for k, v := range base {
		withPW[k] = v
	}
	code, out, _ := runDeployCheck(t, withPW, "vps")
	if code != 0 || !strings.Contains(out, "vps 部署檢查通過。") {
		t.Fatalf("通過：%d %q", code, out)
	}
	code, out, _ = runDeployCheck(t, withPW, "vps", "--json")
	if code != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("--json：%d %q", code, out)
	}
	// 不合法的部署方式在載入設定之前就回報（不是設定的錯誤）。
	bad := map[string]string{"NOTESTEME_DATA": data, "NOTESTEME_DOMAIN": "notes.example.com", "NOTESTEME_DEPLOYMENT": "cloud"}
	if code, _, errOut := runDeployCheck(t, bad, "vps"); code != 1 || !strings.Contains(errOut, "NOTESTEME_DEPLOYMENT=vps") {
		t.Fatalf("cloud：%d %q", code, errOut)
	}
	for _, args := range [][]string{nil, {"local"}, {"vps", "extra"}} {
		if code, _, _ := runDeployCheck(t, base, args...); code != 2 {
			t.Errorf("%v 的結束碼 = %d，預期 2", args, code)
		}
	}
	if code, _, _ := runDeployCheck(t, base, "vps", "--server", "http://127.0.0.1:1"); code != 2 {
		t.Errorf("--server 要拒絕：%d", code)
	}
}
