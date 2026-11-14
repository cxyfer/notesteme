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

type systemResp struct {
	Listen       string `json:"listen"`
	ListenSource string `json:"listen_source"`
	Data         string `json:"data"`
	DataSource   string `json:"data_source"`
	Tools        map[string]struct {
		Path      string `json:"path"`
		Source    string `json:"source"`
		Available bool   `json:"available"`
		Error     string `json:"error"`
	} `json:"tools"`
	YtDlp struct {
		Pinned   string `json:"pinned"`
		Previous string `json:"previous"`
	} `json:"yt_dlp"`
}

// standaloneGet 以本機 owner 的身分（獨立模式的程序內 handler）送出 GET。
func standaloneGet(t *testing.T, env Env, path string) *httptest.ResponseRecorder {
	t.Helper()
	sa, err := OpenStandalone(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	sa.Handler().ServeHTTP(rec, req)
	return rec
}

func getSystem(t *testing.T, env Env) systemResp {
	t.Helper()
	rec := standaloneGet(t, env, "/api/system")
	if rec.Code != 200 {
		t.Fatalf("GET /api/system = %d %s", rec.Code, rec.Body)
	}
	var out systemResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// installManaged 在資料目錄建立受管理的 yt-dlp（可執行的空檔案）並讓 symlink 指向它。
func installManaged(t *testing.T, env Env, id string) {
	t.Helper()
	tools := ytdlp.ToolsDir(env.Vars["NOTESTEME_DATA"])
	bin := filepath.Join(ytdlp.VersionsDir(tools), id, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := ytdlp.LinkPath(tools)
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join(ytdlp.VersionsDirName, id), link); err != nil {
		t.Fatal(err)
	}
}

func TestSystemReportsConfigAndToolSources(t *testing.T) {
	env := testEnv(t)
	sys := getSystem(t, env)
	if sys.DataSource != "env" || sys.ListenSource != "default" || sys.Data != env.Vars["NOTESTEME_DATA"] || sys.Listen == "" {
		t.Errorf("設定與來源 = %+v", sys)
	}
	bin := env.Vars["PATH"]
	if y := sys.Tools["yt-dlp"]; y.Source != "path" || y.Path != filepath.Join(bin, "yt-dlp") || !y.Available || y.Error != "" {
		t.Errorf("yt-dlp = %+v", y)
	}
	// 找不到的工具：available 為 false，說明原因。
	if p := sys.Tools["ffprobe"]; p.Source != "path" || p.Available || p.Error == "" {
		t.Errorf("ffprobe = %+v", p)
	}
	for _, name := range []string{"yt-dlp", "ffmpeg", "ffprobe", "deno"} {
		if _, ok := sys.Tools[name]; !ok {
			t.Errorf("缺少工具 %s：%+v", name, sys.Tools)
		}
	}
}

// 設定檔的路徑優先於環境變數與 PATH；不能執行時仍然回報該路徑與來源，available 為 false（OPS-R8）。
func TestSystemConfigFilePathWinsAndIsReportedWhenBroken(t *testing.T) {
	env := testEnv(t)
	cfg := "tools:\n  ffmpeg: /opt/ffmpeg/bin/ffmpeg\n"
	if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Vars["NOTESTEME_FFMPEG"] = "/usr/local/bin/ffmpeg"

	f := getSystem(t, env).Tools["ffmpeg"]
	if f.Path != "/opt/ffmpeg/bin/ffmpeg" || f.Source != "config_file" || f.Available || !strings.Contains(f.Error, "tools.ffmpeg") {
		t.Errorf("ffmpeg = %+v", f)
	}

	// 環境變數優先於 PATH，來源是 env。
	if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f = getSystem(t, env).Tools["ffmpeg"]
	if f.Path != "/usr/local/bin/ffmpeg" || f.Source != "env" || f.Available {
		t.Errorf("ffmpeg = %+v", f)
	}
}

func TestSystemReportsManagedYtDlpAndState(t *testing.T) {
	env := testEnv(t)
	installManaged(t, env, "2026.11.01")
	tools := ytdlp.ToolsDir(env.Vars["NOTESTEME_DATA"])
	if err := os.WriteFile(filepath.Join(ytdlp.VersionsDir(tools), "state.json"),
		[]byte(`{"pinned":"2026.11.01","previous":"2026.10.01","previous_id":"2026.10.01"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sys := getSystem(t, env)
	y := sys.Tools["yt-dlp"]
	if y.Source != "managed" || !y.Available || y.Path != filepath.Join(ytdlp.LinkPath(tools), "bin", "yt-dlp") {
		t.Errorf("yt-dlp = %+v", y)
	}
	if sys.YtDlp.Pinned != "2026.11.01" || sys.YtDlp.Previous != "2026.10.01" {
		t.Errorf("yt_dlp = %+v", sys.YtDlp)
	}
}

// /readyz 與 /api/system、實際執行工具用同一個尋找方式：受管理的 yt-dlp 可用時，PATH 上沒有 yt-dlp 也就緒；
// 受管理的壞了，不會退回 PATH 而假裝就緒。
func TestReadyzFollowsTheSameLocator(t *testing.T) {
	ready := func(env Env) (int, string) {
		rec := standaloneGet(t, env, "/readyz")
		return rec.Code, rec.Body.String()
	}

	env := testEnv(t)
	if err := os.Remove(filepath.Join(env.Vars["PATH"], "yt-dlp")); err != nil {
		t.Fatal(err)
	}
	if code, body := ready(env); code != 503 || !strings.Contains(body, "yt-dlp") {
		t.Fatalf("沒有任何 yt-dlp：%d %s", code, body)
	}
	installManaged(t, env, "2026.11.01")
	if code, body := ready(env); code != 200 {
		t.Fatalf("受管理的 yt-dlp：%d %s", code, body)
	}

	// PATH 上有可用的 yt-dlp，但受管理的壞了（symlink 在、目標不見）。
	if err := os.WriteFile(filepath.Join(env.Vars["PATH"], "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := ytdlp.ToolsDir(env.Vars["NOTESTEME_DATA"])
	if err := os.RemoveAll(filepath.Join(ytdlp.VersionsDir(tools), "2026.11.01")); err != nil {
		t.Fatal(err)
	}
	if code, body := ready(env); code != 503 || !strings.Contains(body, "yt-dlp") {
		t.Fatalf("受管理的壞了：%d %s", code, body)
	}
}

// 更新 API 在獨立模式（本機 owner）可用；路徑由環境變數指定時回 409。
func TestUpdateRouteRefusesExternallyManagedYtDlp(t *testing.T) {
	env := testEnv(t)
	env.Vars["NOTESTEME_YTDLP"] = filepath.Join(env.Vars["PATH"], "yt-dlp")
	sa, err := OpenStandalone(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	rec := httptest.NewRecorder()
	sa.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tools/yt-dlp/update", nil))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "tool_externally_managed") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
