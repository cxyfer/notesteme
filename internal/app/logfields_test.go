package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// failingYT 是讓 metadata 失敗的 yt-dlp 替身：-J 一律以 exit 1 結束。
type failingYT struct{}

func (failingYT) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if slices.Contains(c.Args, "--version") {
		return toolexec.Result{Stdout: []byte("2026.11.01\n")}, nil
	}
	return toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube] abc123xxxxx: Video unavailable")}, nil
}

// logLines 把 JSON log 逐行解析；不是 JSON 的行讓測試失敗（NFR-OBS-1：log 是 slog 的 JSON）。
func logLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log 不是 JSON：%v\n%s", err, sc.Text())
		}
		out = append(out, m)
	}
	return out
}

// NFR-OBS-1（ci）：用 production 組出的 logger（openCore）實際執行請求與任務，解析輸出的 JSON log：
//   - 每一筆 HTTP 請求的 log 都有 request_id；請求帶合法的 X-Request-Id 時沿用，回應標頭與 log 是同一個值；
//     不合法的值不沿用，改產生新的。
//   - 每一筆帶 task 的 log 都有 item、stage 與 request_id（任務的每次執行有自己的 request ID）。
func TestLogsCarryRequestAndTaskFields(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = failingYT{}
	logs := &lockedBuffer{}
	env.Log = logs
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	c := login(t, srv)

	// 帶合法的 X-Request-Id：沿用。
	req, _ := http.NewRequest(http.MethodPost, "http://"+c.addr+"/api/items", strings.NewReader(`{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "ci-obs-1.req_A")
	req.Header.Set("X-CSRF-Token", c.csrf)
	for _, ck := range c.cook {
		req.AddCookie(ck)
	}
	resp, err := (&http.Client{Transport: c.tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Request-Id") != "ci-obs-1.req_A" {
		t.Fatalf("POST /api/items = %d，X-Request-Id = %q", resp.StatusCode, resp.Header.Get("X-Request-Id"))
	}

	// 不合法的 X-Request-Id（有空白）：改產生新的。
	bad, _ := http.NewRequest(http.MethodGet, "http://"+c.addr+"/healthz", nil)
	bad.Header.Set("X-Request-Id", "evil id\twith spaces")
	resp, err = (&http.Client{Transport: c.tr}).Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	generated := resp.Header.Get("X-Request-Id")
	if generated == "" || strings.Contains(generated, " ") {
		t.Fatalf("不合法的 X-Request-Id 應該改產生新的，實際是 %q", generated)
	}

	// 未登入的請求被 authMiddleware 擋下，回應與 log 一樣有 request ID。
	unauth, err := (&http.Client{Transport: c.tr}).Get("http://" + c.addr + "/api/items")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized || unauth.Header.Get("X-Request-Id") == "" {
		t.Fatalf("401 = %d，X-Request-Id = %q", unauth.StatusCode, unauth.Header.Get("X-Request-Id"))
	}

	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}

	lines := logLines(t, logs.String())
	var sawPost, sawGenerated, sawUnauth, sawTask bool
	for _, m := range lines {
		if m["msg"] == "http request" {
			id, _ := m["request_id"].(string)
			if id == "" {
				t.Errorf("HTTP 請求的 log 沒有 request_id：%v", m)
			}
			switch id {
			case "ci-obs-1.req_A":
				sawPost = m["path"] == "/api/items" && m["method"] == "POST"
			case generated:
				sawGenerated = m["path"] == "/healthz"
			case unauth.Header.Get("X-Request-Id"):
				sawUnauth = m["status"] == float64(http.StatusUnauthorized)
			}
		}
		if _, ok := m["task"]; ok {
			sawTask = true
			for _, k := range []string{"item", "stage", "request_id"} {
				if v, _ := m[k].(string); v == "" {
					t.Errorf("任務的 log 沒有 %s：%v", k, m)
				}
			}
		}
	}
	if !sawPost || !sawGenerated || !sawUnauth {
		t.Errorf("找不到對應回應標頭的請求 log（POST %v、產生的 %v、401 %v）\n%s", sawPost, sawGenerated, sawUnauth, logs.String())
	}
	if !sawTask {
		t.Errorf("沒有任何任務的 log（metadata 失敗應該記錄「步驟失敗」）\n%s", logs.String())
	}
}
