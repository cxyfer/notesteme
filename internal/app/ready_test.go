package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
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

func TestToolChecksFollowResolveOrder(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "yt-dlp")
	_ = os.WriteFile(tool, nil, 0o755)

	checks := toolChecks(toolexec.Resolver{Vars: map[string]string{"PATH": dir}})
	for _, c := range checks {
		err := c.Check(context.Background())
		if c.Name == "yt-dlp" && err != nil {
			t.Errorf("yt-dlp 在 PATH 中，卻回報 %v", err)
		}
		if c.Name == "ffmpeg" && err == nil {
			t.Error("ffmpeg 不在 PATH 中，卻通過檢查")
		}
	}

	// 設定檔的路徑優先於環境變數與 PATH，而且指定了就不退回後面的來源。
	r := toolexec.Resolver{
		Vars:   map[string]string{"PATH": dir},
		Config: map[string]string{"yt-dlp": "/nonexistent/yt-dlp"},
	}
	for _, c := range toolChecks(r) {
		if c.Name == "yt-dlp" && c.Check(context.Background()) == nil {
			t.Error("設定檔指定了不存在的路徑，不應退回 PATH")
		}
	}
}
