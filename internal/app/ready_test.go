package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestReadyzReportsMissingTools(t *testing.T) {
	env := testEnv(t)
	env.Vars["NOTESTEME_FFMPEG"] = "/nonexistent/ffmpeg"
	srv, err := StartServer(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get("http://" + srv.Addr() + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Code   string   `json:"code"`
		Failed []string `json:"failed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 || body.Code != "not_ready" || len(body.Failed) != 1 || body.Failed[0] != "ffmpeg" {
		t.Errorf("status = %d, body = %+v", resp.StatusCode, body)
	}
	if code := get(t, srv.Addr(), "/healthz"); code != 200 {
		t.Errorf("healthz = %d", code)
	}
}

func TestLookTool(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "yt-dlp")
	_ = os.WriteFile(tool, nil, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "ffmpeg"), nil, 0o644) // 沒有執行權限
	spec := toolSpec{name: "yt-dlp", envVar: "NOTESTEME_YTDLP"}

	if p, err := lookTool(map[string]string{"PATH": "/nonexistent" + string(os.PathListSeparator) + dir}, spec); err != nil || p != tool {
		t.Errorf("PATH lookup = %q, %v", p, err)
	}
	if _, err := lookTool(map[string]string{"PATH": dir}, toolSpec{name: "ffmpeg"}); err == nil {
		t.Error("non-executable file should not count")
	}
	// 環境變數有設定時只認它，不退回 PATH。
	if _, err := lookTool(map[string]string{"PATH": dir, "NOTESTEME_YTDLP": "/nonexistent/yt-dlp"}, spec); err == nil {
		t.Error("env override should not fall back to PATH")
	}
	if p, err := lookTool(map[string]string{"NOTESTEME_YTDLP": tool}, spec); err != nil || p != tool {
		t.Errorf("env lookup = %q, %v", p, err)
	}
}
