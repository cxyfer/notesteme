package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

func writeCorruptState(t *testing.T, env Env) string {
	t.Helper()
	tools := ytdlp.ToolsDir(env.Vars["NOTESTEME_DATA"])
	if err := os.MkdirAll(ytdlp.VersionsDir(tools), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ytdlp.VersionsDir(tools), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// state.json 無法解析：/api/system 仍然回 200，yt_dlp 是 {pinned: null, previous: null, error}，其他欄位照常。
func TestSystemSurvivesCorruptYtDlpState(t *testing.T) {
	env := testEnv(t)
	path := writeCorruptState(t, env)
	rec := standaloneGet(t, env, "/api/system")
	if rec.Code != 200 {
		t.Fatalf("GET /api/system = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Data  string                     `json:"data"`
		Tools map[string]json.RawMessage `json:"tools"`
		YtDlp map[string]any             `json:"yt_dlp"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data != env.Vars["NOTESTEME_DATA"] || len(out.Tools) != 4 {
		t.Errorf("其他欄位要照常：data=%q tools=%d", out.Data, len(out.Tools))
	}
	pinned, hasPinned := out.YtDlp["pinned"]
	previous, hasPrevious := out.YtDlp["previous"]
	if !hasPinned || pinned != nil || !hasPrevious || previous != nil {
		t.Errorf("pinned 與 previous 都要是 null：%v", out.YtDlp)
	}
	msg, _ := out.YtDlp["error"].(string)
	if !strings.Contains(msg, path) {
		t.Errorf("error 要指出 state.json 的位置 %s：%q", path, msg)
	}
}

// 更新與回退遇到損毀的 state.json：409 tool_state_corrupt，訊息指出位置與可以刪除。
func TestUpdateAndRollbackReportCorruptState(t *testing.T) {
	env := testEnv(t)
	path := writeCorruptState(t, env)
	sa, err := OpenStandalone(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	for _, route := range []string{"/api/tools/yt-dlp/update", "/api/tools/yt-dlp/rollback"} {
		rec := httptest.NewRecorder()
		sa.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route, nil))
		var e struct{ Code, Message string }
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 409 || e.Code != "tool_state_corrupt" {
			t.Fatalf("%s：%d %s", route, rec.Code, rec.Body)
		}
		if !strings.Contains(e.Message, path) || !strings.Contains(e.Message, "刪除") {
			t.Errorf("%s：message = %q", route, e.Message)
		}
	}
}
