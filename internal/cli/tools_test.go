package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// 在 testEnv 的資料目錄放一個受管理的假 yt-dlp：shell script 回報版本，--version 以外的參數一律失敗。
func installFakeManaged(t *testing.T, data, version string) {
	t.Helper()
	tools := ytdlp.ToolsDir(data)
	bin := filepath.Join(ytdlp.VersionsDir(tools), version, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1\" = \"--version\" ] && echo " + version + " && exit 0\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ytdlp.VersionsDirName, version), ytdlp.LinkPath(tools)); err != nil {
		t.Fatal(err)
	}
}

func TestToolsUpdateParsing(t *testing.T) {
	for name, args := range map[string][]string{
		"沒有子命令":           {"tools"},
		"旗標不是子命令":         {"tools", "--pin", "2026.11.01"},
		"不認得的子命令":         {"tools", "frob"},
		"位置參數":            {"tools", "update", "now"},
		"--pin 與 --unpin": {"tools", "update", "--pin", "2026.11.01", "--unpin"},
		"--pin 沒有值":       {"tools", "update", "--pin"},
		"--pin 空字串":       {"tools", "update", "--pin", ""},
		"版本格式不對":          {"tools", "update", "--pin", "latest"},
		"版本含 pip 旗標":      {"tools", "update", "--pin", "--index-url=https://evil.example"},
		"版本含分號":           {"tools", "update", "--pin", "2026.11.01;rm"},
	} {
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
}

// 固定之後，手動更新略過並回報、結束碼 0；--unpin 解除。獨立模式：狀態存在資料目錄，兩個指令之間不需要 server。
func TestToolsUpdatePinAndSkip(t *testing.T) {
	env, data := testEnv(t)
	installFakeManaged(t, data, "2026.11.01")

	res, out, errOut := run(t, env, "tools", "update", "--pin", "2026.11.01")
	if res.Code != 0 || res.Mode != ModeStandalone || !strings.Contains(out, "已固定在 2026.11.01") {
		t.Fatalf("pin：%+v out=%q stderr=%q", res, out, errOut)
	}
	res, out, errOut = run(t, env, "tools", "update")
	if res.Code != 0 || strings.TrimSpace(out) != "yt-dlp 已固定在 2026.11.01，略過更新。" {
		t.Fatalf("略過：%+v out=%q stderr=%q", res, out, errOut)
	}
	res, out, _ = run(t, env, "tools", "update", "--json")
	if res.Code != 0 || !strings.Contains(out, `"skipped":true`) || !strings.Contains(out, `"pinned":"2026.11.01"`) {
		t.Errorf("--json 原樣輸出 API 的回應：%+v %q", res, out)
	}
	res, out, errOut = run(t, env, "tools", "update", "--unpin")
	if res.Code != 0 || !strings.Contains(out, "已解除固定") {
		t.Fatalf("unpin：%+v out=%q stderr=%q", res, out, errOut)
	}
}

// 路徑由環境變數指定：update 回 409，結束碼 1，訊息說明原因。
func TestToolsUpdateRefusesExternallyManagedYtDlp(t *testing.T) {
	env, _ := testEnv(t)
	env.Vars["NOTESTEME_YTDLP"] = "/nonexistent/yt-dlp"
	res, _, errOut := run(t, env, "tools", "update")
	if res.Code != 1 || res.ErrorCode != "tool_externally_managed" || !strings.Contains(errOut, "NOTESTEME_YTDLP") {
		t.Fatalf("%+v stderr=%q", res, errOut)
	}
}
