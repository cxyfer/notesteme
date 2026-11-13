package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// fakeAPI 是 --server 的對象：記錄收到的請求，依路徑回應。
type fakeAPI struct {
	mu       sync.Mutex
	requests []string // "METHOD /path"
	auth     []string
	bodies   []string
	handler  func(w http.ResponseWriter, r *http.Request)
}

func newFakeAPI(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*fakeAPI, string) {
	t.Helper()
	f := &fakeAPI{handler: h}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		f.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeAPI) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

// 沒有 --server 以外的設定也能執行：不需要 master key，也不碰 --data（CLI-R3）。
func TestServerModeUsesBearerAndIgnoresData(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"items":[{"ref":"yt:abc123xxxxx","title":"T"}]}`)
	})
	env := app.Env{Vars: map[string]string{}}
	data := filepath.Join(shortTempDir(t), "nonexistent")
	res, out, errOut := run(t, env, "items", "ls", "--server", url+"/", "--token", "tok", "--data", data, "--json")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if api.auth[0] != "Bearer tok" || api.requests[0] != "GET /api/items" {
		t.Errorf("請求 = %v，Authorization = %v", api.requests, api.auth)
	}
	// 輸出就是 API 的回應（CLI-R5）。
	if strings.TrimSpace(out) != `{"items":[{"ref":"yt:abc123xxxxx","title":"T"}]}` {
		t.Errorf("輸出 = %q", out)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("--server 不應該碰 --data")
	}
}

// API 的狀態碼對應到結束碼（CLI-R6）：400、422 是 2，其他錯誤是 1。
func TestAPIStatusMapsToExitCode(t *testing.T) {
	cases := []struct {
		status int
		code   int
		errTag string
	}{
		{400, 2, "invalid_request"}, {422, 2, "invalid_request"},
		{401, 1, "unauthorized"}, {403, 1, "forbidden"}, {404, 1, "not_found"},
		{500, 1, "internal_error"}, {501, 1, "not_implemented"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, c.status, fmt.Sprintf(`{"code":%q,"message":"說明"}`, c.errTag))
			})
			res, _, errOut := run(t, app.Env{}, "items", "show", "abc", "--server", url, "--token", "t")
			if res.Code != c.code || res.ErrorCode != c.errTag || res.Mode != ModeClient {
				t.Errorf("結果 = %+v，預期結束碼 %d、ErrorCode %s", res, c.code, c.errTag)
			}
			if !strings.Contains(errOut, "說明") {
				t.Errorf("stderr = %q", errOut)
			}
		})
	}
	t.Run("連不上", func(t *testing.T) {
		res, _, _ := run(t, app.Env{}, "items", "ls", "--server", "http://127.0.0.1:1", "--token", "t")
		if res.Code != 1 {
			t.Errorf("結果 = %+v", res)
		}
	})
}

// 位置參數可以和旗標穿插。
func TestFlagsMayBeInterspersed(t *testing.T) {
	orders := map[string][]string{
		"旗標在後":  {"ingest", "yt:abc123xxxxx", "--json", "--server", "URL", "--token", "t"},
		"旗標在前":  {"ingest", "--json", "--server", "URL", "--token", "t", "yt:abc123xxxxx"},
		"= 的寫法": {"ingest", "yt:abc123xxxxx", "--json", "--server=URL", "--token=t"},
		"夾在中間":  {"ingest", "--json", "yt:abc123xxxxx", "--server", "URL", "--token", "t"},
	}
	for name, args := range orders {
		t.Run(name, func(t *testing.T) {
			api, url := ingestAPI(t, 1)
			for i := range args {
				args[i] = strings.ReplaceAll(args[i], "URL", url)
			}
			res, _, errOut := run(t, app.Env{}, args...)
			if res.Code != 0 || res.Mode != ModeClient {
				t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
			}
			var body map[string]string
			if err := json.Unmarshal([]byte(api.bodies[0]), &body); err != nil || body["url"] != "https://www.youtube.com/watch?v=abc123xxxxx" {
				t.Errorf("POST 內容 = %s", api.bodies[0])
			}
		})
	}
}

// ingestAPI 回應 POST /api/items 與每部影音的詳情；n 部影音。
func ingestAPI(t *testing.T, n int) (*fakeAPI, string) {
	t.Helper()
	return newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/items":
			items := make([]string, 0, n)
			for i := 1; i <= n; i++ {
				items = append(items, fmt.Sprintf(`{"id":"i%d","ref":"yt:video%d","already_in_library":%v}`, i, i, i == 2))
			}
			writeJSON(w, 201, `{"items":[`+strings.Join(items, ",")+`]}`)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/items/i"):
			id := strings.TrimPrefix(r.URL.Path, "/api/items/")
			writeJSON(w, 200, fmt.Sprintf(`{"id":%q,"ref":"yt:video%s","title":"標題 %s","stages":[
				{"stage":"metadata","status":"done","actions":[]},{"stage":"transcript","status":"pending","actions":[]}],
				"sources":[{"source":"x"}]}`, id, id[1:], id))
		default:
			writeJSON(w, 404, `{"code":"not_found","message":"no"}`)
		}
	})
}

// ingest --json 每部影音一行（JSON Lines）：影音的欄位加上 stages（CLI-R5）。
func TestIngestJSONLines(t *testing.T) {
	_, url := ingestAPI(t, 2)
	res, out, errOut := run(t, app.Env{}, "ingest", "yt:abc123xxxxx", "--json", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("輸出有 %d 行，預期 2：%q", len(lines), out)
	}
	for i, line := range lines {
		var obj struct {
			ID      string            `json:"id"`
			Title   string            `json:"title"`
			Stages  map[string]string `json:"stages"`
			Already bool              `json:"already_in_library"`
			Sources json.RawMessage   `json:"sources"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("第 %d 行不是 JSON：%q", i+1, line)
		}
		if obj.ID != fmt.Sprintf("i%d", i+1) || obj.Stages["metadata"] != "done" || obj.Stages["transcript"] != "pending" || obj.Sources != nil {
			t.Errorf("第 %d 行 = %+v", i+1, obj)
		}
		if obj.Already != (i == 1) {
			t.Errorf("第 %d 行的 already_in_library = %v", i+1, obj.Already)
		}
	}
}

func TestIngestTargets(t *testing.T) {
	cases := []struct {
		arg, field, want string
	}{
		{"https://www.youtube.com/watch?v=abc123xxxxx", "url", "https://www.youtube.com/watch?v=abc123xxxxx"},
		{"yt:abc123xxxxx", "url", "https://www.youtube.com/watch?v=abc123xxxxx"},
		{"bili:BV1ai411c7mD/p2", "url", "https://www.bilibili.com/video/BV1ai411c7mD?p=2"},
		{"/media/a.mp4", "path", "/media/a.mp4"},
	}
	for _, c := range cases {
		api, url := ingestAPI(t, 1)
		res, _, errOut := run(t, app.Env{}, "ingest", c.arg, "--profile", "fast", "--until", "metadata", "--server", url, "--token", "t")
		if res.Code != 0 {
			t.Fatalf("%s：結果 = %+v，stderr：%s", c.arg, res, errOut)
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(api.bodies[0]), &body); err != nil {
			t.Fatal(err)
		}
		if body[c.field] != c.want || body["profile"] != "fast" || body["until"] != "metadata" {
			t.Errorf("%s：POST 內容 = %s", c.arg, api.bodies[0])
		}
	}
}

// 本機路徑交給 server 檢查（GRD-R7）；server 還沒實作時，錯誤原樣回報。
func TestIngestPathNotImplementedOnServer(t *testing.T) {
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 501, `{"code":"not_implemented","message":"尚未實作"}`)
	})
	res, _, _ := run(t, app.Env{}, "ingest", "/etc/passwd", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_implemented" || res.Mode != ModeClient {
		t.Errorf("結果 = %+v", res)
	}
}

func TestItemsShowResolvesRefAndRerunPostsStage(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/items":
			writeJSON(w, 200, `{"items":[{"id":"i9","ref":"yt:abc123xxxxx"}]}`)
		case r.Method == "POST" && r.URL.Path == "/api/items/i9/rerun":
			writeJSON(w, 202, `{"id":"i9","ref":"yt:abc123xxxxx","title":"T","stages":[{"stage":"transcript","status":"pending"}]}`)
		default:
			writeJSON(w, 200, `{"id":"i9","ref":"yt:abc123xxxxx","title":"T","stages":[]}`)
		}
	})
	if res, out, errOut := run(t, app.Env{}, "items", "show", "yt:abc123xxxxx", "--json", "--server", url, "--token", "t"); res.Code != 0 || !strings.Contains(out, `"id":"i9"`) {
		t.Fatalf("show：%+v %q %s", res, out, errOut)
	}
	if res, out, errOut := run(t, app.Env{}, "items", "rerun", "yt:abc123xxxxx", "--from", "transcript", "--server", url, "--token", "t"); res.Code != 0 || !strings.Contains(out, "transcript") {
		t.Fatalf("rerun：%+v %q %s", res, out, errOut)
	}
	if api.bodies[len(api.bodies)-1] != `{"from":"transcript"}` {
		t.Errorf("rerun 的內容 = %s", api.bodies[len(api.bodies)-1])
	}
	// 找不到的代號：1。
	res, _, _ := run(t, app.Env{}, "items", "show", "yt:zzzzzzzzzzz", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" {
		t.Errorf("找不到：%+v", res)
	}
}

func TestExportStreamsToStdoutOrFile(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/items":
			writeJSON(w, 200, `{"items":[{"id":"i9","ref":"yt:abc123xxxxx"}]}`)
		default:
			w.Header().Set("Content-Type", "application/x-subrip")
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nhi\n")
		}
	})
	res, out, errOut := run(t, app.Env{}, "export", "yt:abc123xxxxx", "--format", "vtt", "--lang", "en", "--variant", "source", "--server", url, "--token", "t")
	if res.Code != 0 || out != "1\n00:00:01,000 --> 00:00:02,000\nhi\n" {
		t.Fatalf("結果 = %+v %q %s", res, out, errOut)
	}
	if got := api.requests[len(api.requests)-1]; got != "GET /api/items/i9/subtitles?format=vtt&lang=en&variant=source" {
		t.Errorf("請求 = %s", got)
	}
	path := filepath.Join(shortTempDir(t), "out.srt")
	if res, _, errOut := run(t, app.Env{}, "export", "i9", "--out", path, "--server", url, "--token", "t"); res.Code != 0 {
		t.Fatalf("--out：%+v %s", res, errOut)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "hi") {
		t.Errorf("檔案內容 = %q（%v）", b, err)
	}
	// API 回 422（例如 --variant 不正確）：2。
	_, bad := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, `{"code":"invalid_request","message":"variant 不正確"}`)
	})
	if res, _, _ := run(t, app.Env{}, "export", "i9", "--variant", "x", "--server", bad, "--token", "t"); res.Code != 2 {
		t.Errorf("422：%+v", res)
	}
}
