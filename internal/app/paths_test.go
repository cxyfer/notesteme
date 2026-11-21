package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMediaConfig 寫入設定檔，設定 media_roots，回傳媒體根目錄的真實路徑。
func writeMediaConfig(t *testing.T, env Env) string {
	t.Helper()
	media := filepath.Join(filepath.Dir(env.Vars["NOTESTEME_DATA"]), "media")
	if err := os.MkdirAll(filepath.Join(media, "talks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 設定檔中的路徑要寫成解析後的真實路徑，比對才與 EvalSymlinks 的結果一致。
	real, err := filepath.EvalSymlinks(media)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte("media_roots: ["+real+"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return real
}

// GRD-R7、GRD-R8：本機 socket 與 TCP 都是 server 端檢查路徑（client 模式）。
func TestServeChecksPathsOnSocket(t *testing.T) {
	env := testEnv(t)
	media := writeMediaConfig(t, env)
	env.Test.ManualWorker = true
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	uc := unixClient(t, SocketPath(env.Vars["NOTESTEME_DATA"]))
	inbox := filepath.Join(env.Vars["NOTESTEME_DATA"], "inbox")

	for _, c := range []struct {
		name, path string
		status     int
	}{
		{"根目錄之外", "/etc/passwd", 403},
		{"..", media + "/../outside.mp4", 403},
		// 通過檢查後延遲驗證：檔案不存在仍建立（ING-05）。
		{".. 回到 inbox 內仍然放行", media + "/../data/inbox/x.mp4", 201},
		{"media_roots", media + "/talks/keynote.mp4", 201},
		{"預設的 inbox", inbox + "/talk.mp4", 201},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, body := statusOf(t, uc, "POST", "http://local/api/items", `{"path":"`+c.path+`"}`)
			if code != c.status {
				t.Fatalf("POST path=%s = %d %s，預期 %d", c.path, code, body, c.status)
			}
			if c.status == 403 && !strings.Contains(body, "只能新增媒體資料夾中的檔案。") {
				t.Errorf("403 的訊息 = %s", body)
			}
		})
	}
}

// GRD-R8：獨立模式不受 GRD-R7 限制；/etc/passwd 通過檢查（延遲驗證，回 201），不是 403。
func TestStandaloneDoesNotRestrictPaths(t *testing.T) {
	env := testEnv(t)
	writeMediaConfig(t, env)
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	code, body := call(t, sa.Handler(), "POST", "/api/items", `{"path":"/etc/passwd"}`)
	if code != 201 || !strings.Contains(string(body), `"ref":"local:/etc/passwd"`) {
		t.Fatalf("standalone POST path=/etc/passwd = %d %s，預期 201 與 local:/etc/passwd", code, body)
	}
}
