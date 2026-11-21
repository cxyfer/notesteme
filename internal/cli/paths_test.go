package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

const pathRefusal = "只能新增媒體資料夾中的檔案。"

// GRD-R8、CLI-R6：client 模式（本機 socket）的路徑由 server 端檢查，403 印出訊息並以 1 結束。
func TestClientModeIngestPathOutsideRoots(t *testing.T) {
	env, _ := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())

	res, _, errOut := run(t, env, "ingest", "/etc/passwd")
	if res.Mode != ModeClient || res.Code != 1 || res.ErrorCode != "path_outside_roots" {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(errOut, pathRefusal) {
		t.Errorf("stderr 應包含拒絕訊息：%q", errOut)
	}
}

// 獨立模式不受路徑限制（GRD-R8）：/etc/passwd 通過檢查並新增（ING-05），不是 path_outside_roots。
func TestStandaloneModeIngestPathUnrestricted(t *testing.T) {
	env, _ := testEnv(t)
	res, out, errOut := run(t, env, "ingest", "/etc/passwd")
	if res.Mode != ModeStandalone || res.Code != 0 || res.ErrorCode != "" {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if strings.Contains(errOut, pathRefusal) {
		t.Errorf("獨立模式不應該回報路徑限制：%q", errOut)
	}
	if !strings.Contains(out, "local:/etc/passwd") {
		t.Errorf("stdout 應包含影音代號 local:/etc/passwd：%q", out)
	}
}

// --server 模式下，server 回 403 path_outside_roots 時也是結束碼 1，訊息原樣印出；
// 路徑以絕對路徑送出，由 server 檢查。
func TestServerModeIngestPathRefused(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, `{"code":"path_outside_roots","message":"`+pathRefusal+`"}`)
	})
	res, _, errOut := run(t, app.Env{}, "ingest", "/etc/passwd", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "path_outside_roots" || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(errOut, pathRefusal) {
		t.Errorf("stderr = %q", errOut)
	}
	if len(api.bodies) != 1 || api.bodies[0] != `{"path":"/etc/passwd"}` {
		t.Errorf("請求內容 = %v", api.bodies)
	}
}
