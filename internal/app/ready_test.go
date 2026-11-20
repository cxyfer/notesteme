package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	if resp.StatusCode != 503 || body.Code != "not_ready" || !slices.Equal(body.Failed, []string{"ffmpeg"}) {
		t.Errorf("status = %d, body = %+v；failed 必須正好是 [ffmpeg]", resp.StatusCode, body)
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

// readyzResult 以獨立模式的 handler 取得 /readyz，回傳狀態碼與 failed 的完整內容。
func readyzResult(t *testing.T, env Env) (int, []string) {
	t.Helper()
	rec := standaloneGet(t, env, "/readyz")
	var body struct {
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("回應不是 JSON：%v（%s）", err, rec.Body)
	}
	return rec.Code, body.Failed
}

// failed 陣列要正好列出壞掉的項目（完整內容與順序），不能只比前綴或只看包含：
// 只有 ffmpeg 壞了就是 [ffmpeg]，不可以多列、漏列。
func TestReadyzFailedListsExactlyTheBrokenChecks(t *testing.T) {
	for _, c := range []struct {
		name   string
		breaks []string // 要弄壞的環境變數
		want   []string
	}{
		{"都正常", nil, nil},
		{"只有 ffmpeg", []string{"NOTESTEME_FFMPEG"}, []string{"ffmpeg"}},
		{"只有 yt-dlp", []string{"NOTESTEME_YTDLP"}, []string{"yt-dlp"}},
		{"兩個都壞", []string{"NOTESTEME_FFMPEG", "NOTESTEME_YTDLP"}, []string{"ffmpeg", "yt-dlp"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := testEnv(t)
			for _, v := range c.breaks {
				env.Vars[v] = "/nonexistent/tool"
			}
			code, failed := readyzResult(t, env)
			wantCode := 200
			if c.want != nil {
				wantCode = 503
			}
			if code != wantCode || !slices.Equal(failed, c.want) {
				t.Errorf("status = %d, failed = %v；預期 %d, %v", code, failed, wantCode, c.want)
			}
		})
	}
}
