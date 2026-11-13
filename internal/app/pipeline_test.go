package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// ytRunner 是 yt-dlp 的替身 Runner：-J 回傳固定的影片資訊，下載字幕時寫出 VTT，--version 回傳版本。
// block 非 nil 時，-J 會阻塞到 block 關閉或 ctx 取消，用來模擬執行到一半。
type ytRunner struct {
	mu     sync.Mutex
	calls  [][]string
	paths  []string
	block  chan struct{}
	inProg chan struct{} // 收到 -J 時關閉一次，讓測試知道已經執行到一半
	once   sync.Once
}

func (r *ytRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, c.Args)
	r.paths = append(r.paths, c.Path)
	r.mu.Unlock()
	switch {
	case slices.Contains(c.Args, "--version"):
		return toolexec.Result{Stdout: []byte("2026.11.01\n")}, nil
	case slices.Contains(c.Args, "-J"):
		if r.inProg != nil {
			r.once.Do(func() { close(r.inProg) })
		}
		if r.block != nil {
			select {
			case <-r.block:
			case <-ctx.Done():
				return toolexec.Result{}, ctx.Err()
			}
		}
		return toolexec.Result{Stdout: []byte(`{"id":"abc123xxxxx","title":"Hello","duration":60,"language":"en","availability":"public",
			"live_status":"not_live","subtitles":{"en":[{"ext":"vtt","url":"https://x/en.vtt"}]},"automatic_captions":{}}`)}, nil
	case slices.Contains(c.Args, "--skip-download"):
		i := slices.Index(c.Args, "-o")
		out := strings.Replace(c.Args[i+1], "%(ext)s", "en.vtt", 1)
		vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhello\n\n00:00:02.000 --> 00:00:04.000\nworld\n"
		return toolexec.Result{}, os.WriteFile(out, []byte(vtt), 0o600)
	}
	return toolexec.Result{ExitCode: 2, Stderr: []byte("unexpected")}, nil
}

type httpClient struct {
	t    *testing.T
	addr string
	tr   *http.Transport
	cook []*http.Cookie
	csrf string
}

func login(t *testing.T, srv *Server) *httpClient {
	t.Helper()
	c := &httpClient{t: t, addr: srv.Addr(), tr: &http.Transport{}}
	t.Cleanup(c.tr.CloseIdleConnections)
	resp := c.do("POST", "/api/auth/login", `{"password":"pw"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		c.cook = append(c.cook, ck)
		if ck.Name == "notesteme_csrf" {
			c.csrf = ck.Value
		}
	}
	return c
}

func (c *httpClient) do(method, path, body string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, "http://"+c.addr+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, ck := range c.cook {
		req.AddCookie(ck)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := (&http.Client{Transport: c.tr}).Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *httpClient) json(method, path, body string, out any) int {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if out != nil {
		if err := json.Unmarshal(buf.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s：%v\n%s", method, path, err, buf.String())
		}
	}
	return resp.StatusCode
}

type detail struct {
	ID     string `json:"id"`
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Stages []struct {
		Stage  string `json:"stage"`
		Status string `json:"status"`
	} `json:"stages"`
}

func (d detail) status(stage string) string {
	for _, s := range d.Stages {
		if s.Stage == stage {
			return s.Status
		}
	}
	return ""
}

func ingest(t *testing.T, c *httpClient) string {
	t.Helper()
	var out struct {
		Items []detail `json:"items"`
	}
	if code := c.json("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`, &out); code != 201 {
		t.Fatalf("POST /api/items = %d", code)
	}
	return out.Items[0].ID
}

// waitClosed 等 ch 關閉；逾時就讓測試失敗，避免卡死整個 package。
func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("等待 yt-dlp 開始執行逾時")
	}
}

func TestManualWorkerRunsPipelineWithInjectedRunner(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	r := &ytRunner{}
	env.Runner = r
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	c := login(t, srv)
	id := ingest(t, c)

	// 手動模式：新增之後任務留在佇列，server 不會自己執行。
	var d detail
	c.json("GET", "/api/items/"+id, "", &d)
	if d.status("metadata") != "pending" || len(r.calls) != 0 {
		t.Fatalf("手動模式不應自己執行：%+v（呼叫 %d 次）", d, len(r.calls))
	}

	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.json("GET", "/api/items/"+id, "", &d)
	if d.Title != "Hello" || d.status("metadata") != "done" || d.status("transcript") != "done" || d.status("normalize") != "done" {
		t.Fatalf("detail = %+v", d)
	}
	// 工具以絕對路徑（取自注入的 PATH）執行。
	for _, p := range r.paths {
		if !filepath.IsAbs(p) || filepath.Base(p) != "yt-dlp" {
			t.Errorf("工具路徑 = %q", p)
		}
	}
	// 產出檔寫在資料目錄的 artifacts/<item-id>/ 底下；暫存目錄用完就清除。
	entries, err := os.ReadDir(filepath.Join(env.Vars["NOTESTEME_DATA"], "artifacts", id))
	if err != nil || len(entries) != 2 {
		t.Errorf("artifacts = %v, %v", entries, err)
	}
	if tmp, _ := os.ReadDir(filepath.Join(env.Vars["NOTESTEME_DATA"], "tmp")); len(tmp) != 0 {
		t.Errorf("tmp 應該是空的：%v", tmp)
	}
}

func TestProductionWorkerRunsInBackground(t *testing.T) {
	env := testEnv(t) // 沒有 ManualWorker：server 自己執行 worker
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	c := login(t, srv)
	id := ingest(t, c)

	deadline := time.Now().Add(5 * time.Second)
	var d detail
	for time.Now().Before(deadline) {
		c.json("GET", "/api/items/"+id, "", &d)
		if d.status("normalize") == "done" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker 沒有在背景完成處理：%+v", d)
}

func TestRestartPutsLeasesBackAndFinishes(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	block := &ytRunner{block: make(chan struct{}), inProg: make(chan struct{})}
	env.Runner = block
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, srv)
	id := ingest(t, c)

	// 執行到一半（yt-dlp 卡住）時程序被中斷。
	if n, err := srv.RunRound(t.Context()); err != nil || n != 1 {
		t.Fatalf("RunRound = %d, %v", n, err)
	}
	waitClosed(t, block.inProg)
	var d detail
	c.json("GET", "/api/items/"+id, "", &d)
	if d.status("metadata") != "running" {
		t.Fatalf("執行中的步驟應為 running：%+v", d)
	}
	srv.Kill()

	// 重新啟動：上一次留下的租約放回佇列（PIPE-R11），接著可以完成。
	env.Runner = &ytRunner{}
	srv2, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Kill()
	c2 := login(t, srv2)
	c2.json("GET", "/api/items/"+id, "", &d)
	if d.status("metadata") != "pending" {
		t.Fatalf("重啟後 running 的步驟應回到 pending：%+v", d)
	}
	if err := srv2.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	c2.json("GET", "/api/items/"+id, "", &d)
	if d.status("normalize") != "done" {
		t.Errorf("重啟後應完成：%+v", d)
	}
}

func TestGracefulShutdownReleasesLeases(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Test.SkipShutdownGrace = true // 寬限期為 0：執行中的任務立刻被取消；寬限期本身見 shutdown_test.go
	block := &ytRunner{block: make(chan struct{}), inProg: make(chan struct{})}
	env.Runner = block
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, srv)
	id := ingest(t, c)
	if n, err := srv.RunRound(t.Context()); err != nil || n != 1 {
		t.Fatalf("RunRound = %d, %v", n, err)
	}
	waitClosed(t, block.inProg)
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	// 正常關閉：租約已經主動放回，重新啟動不需要靠「別人的租約」回收就能領取。
	env.Runner = &ytRunner{}
	srv2, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Kill()
	if err := srv2.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	var d detail
	login(t, srv2).json("GET", "/api/items/"+id, "", &d)
	if d.status("normalize") != "done" {
		t.Errorf("%+v", d)
	}
}
