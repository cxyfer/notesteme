package cli

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/subscription"
)

func TestSubsUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":           {"subs"},
		"不認得的子命令":         {"subs", "edit"},
		"ls 多餘參數":         {"subs", "ls", "x"},
		"add 缺網址":         {"subs", "add"},
		"add 兩個網址":        {"subs", "add", "a", "b"},
		"add 壞的 backfill": {"subs", "add", "https://x", "--backfill", "some"},
		"add recent 沒有數字": {"subs", "add", "https://x", "--backfill", "recent:x"},
		"show 缺 id":       {"subs", "show"},
		"show 壞 id":       {"subs", "show", "../x"},
		"preview 缺參數":     {"subs", "preview"},
		"check 太多參數":      {"subs", "check", "a", "b"},
		"rm 缺 id":         {"subs", "rm"},
		"rm 沒有 --yes":     {"subs", "rm", "abc"},
		"import 缺檔案":      {"subs", "import"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func TestSubsClientMode(t *testing.T) {
	var (
		lastBody   map[string]any
		lastPath   string
		importData []byte
	)
	view := `{"id":"s1","kind":"channel","platform":"youtube","external_id":"UCfixture000000000000000","url":"https://www.youtube.com/channel/UCfixture000000000000000","title":"Fixture","profile":null,"schedule":"6h","backfill":{"mode":"recent","count":2},"filters":{},"enabled":true,"next_check_at":"2026-11-20T08:00:00Z","last_checked_at":null,"last_error":"暫時無法檢查訂閱（HTTP 503），稍後會自動重試。","last_error_class":"retryable"}`
	var fa *fakeAPI
	fa, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.Method + " " + r.URL.Path
		lastBody = nil
		raw := fa.bodies[len(fa.bodies)-1] // newFakeAPI 已經讀完本體
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			_ = json.Unmarshal([]byte(raw), &lastBody)
		}
		switch lastPath {
		case "GET /api/subscriptions":
			writeJSON(w, 200, `{"subscriptions":[`+view+`]}`)
		case "POST /api/subscriptions":
			if lastBody["url"] == "https://www.youtube.com/watch?v=abc123xxxxx" {
				writeJSON(w, 422, `{"code":"not_a_collection","message":"這是單一影片，請直接新增，不需要訂閱。"}`)
				return
			}
			writeJSON(w, 201, view)
		case "GET /api/subscriptions/s1":
			writeJSON(w, 200, view)
		case "POST /api/subscriptions/s1/preview", "POST /api/subscriptions/preview":
			writeJSON(w, 200, `{"would_process":[{"ref":"yt:keep001xxxx","title":"Keep"}],"would_skip":[{"ref":"yt:long001xxxx","title":"Long","reason":"too_long"}],"truncated":true}`)
		case "POST /api/subscriptions/s1/check":
			writeJSON(w, 202, `{"next_check_at":"2026-11-20T08:00:00Z"}`)
		case "DELETE /api/subscriptions/s1":
			w.WriteHeader(204)
		case "POST /api/subscriptions/import":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(strings.NewReader(raw), params["boundary"])
			p, err := mr.NextPart()
			if err != nil || p.FormName() != "file" {
				writeJSON(w, 400, `{"code":"invalid_input","message":"bad"}`)
				return
			}
			importData, _ = io.ReadAll(p)
			writeJSON(w, 200, `{"created":1,"duplicates":["https://feeds.example.com/a.xml"],"failed":[{"line":3,"url":"ftp://x","message":"無法辨識這個網址。"}]}`)
		default:
			writeJSON(w, 404, `{"code":"not_found","message":"找不到這個訂閱。"}`)
		}
	})
	cli := func(args ...string) (Result, string, string) {
		return run(t, app.Env{}, append(args, "--server", url, "--token", "tok")...)
	}

	res, out, errOut := cli("subs", "add", "https://www.youtube.com/channel/UCfixture000000000000000", "--backfill", "recent:2", "--schedule", "2h", "--profile", "p")
	if res.Code != 0 || !strings.Contains(out, "已建立訂閱 s1") || !strings.Contains(out, "recent:2") {
		t.Fatalf("add：%+v %q %s", res, out, errOut)
	}
	bf, _ := lastBody["backfill"].(map[string]any)
	if lastBody["schedule"] != "2h" || lastBody["profile"] != "p" || bf["mode"] != "recent" || bf["count"] != float64(2) {
		t.Errorf("add 的 body：%v", lastBody)
	}
	if res, _, _ := cli("subs", "add", "https://x", "--backfill", "all", "--json"); res.Code != 0 || lastBody["backfill"].(map[string]any)["mode"] != "all" || lastBody["schedule"] != nil {
		t.Errorf("add all：%+v %v", res, lastBody)
	}
	if res, _, errOut := cli("subs", "add", "https://www.youtube.com/watch?v=abc123xxxxx"); res.Code != 2 || res.ErrorCode != "not_a_collection" || !strings.Contains(errOut, "這是單一影片") {
		t.Errorf("422 要結束碼 2：%+v %s", res, errOut)
	}
	if res, out, _ := cli("subs", "ls"); res.Code != 0 || !strings.HasPrefix(out, "s1\tchannel\t6h\t啟用\trecent:2\t下次 2026-11-20T08:00:00Z\thttps://www.youtube.com/channel/") {
		t.Errorf("ls：%+v %q", res, out)
	}
	if res, out, _ := cli("subs", "ls", "--json"); res.Code != 0 || !strings.Contains(out, `"subscriptions"`) {
		t.Errorf("ls --json：%+v %q", res, out)
	}
	if res, out, _ := cli("subs", "show", "s1"); res.Code != 0 || !strings.Contains(out, "標題：Fixture") || !strings.Contains(out, "最近的錯誤（retryable）") || !strings.Contains(out, "處理方案：（預設）") {
		t.Errorf("show：%+v %q", res, out)
	}
	if res, _, errOut := cli("subs", "show", "nope"); res.Code != 1 || !strings.Contains(errOut, "找不到這個訂閱") {
		t.Errorf("show 不存在：%+v %s", res, errOut)
	}
	if res, out, _ := cli("subs", "preview", "s1"); res.Code != 0 || lastPath != "POST /api/subscriptions/s1/preview" ||
		!strings.Contains(out, "會處理 1 部") || !strings.Contains(out, "yt:long001xxxx\ttoo_long") || !strings.Contains(out, "只列出各 200 部") {
		t.Errorf("preview id：%+v %q", res, out)
	}
	if res, _, _ := cli("subs", "preview", "https://feeds.example.com/a.xml", "--json"); res.Code != 0 || lastPath != "POST /api/subscriptions/preview" || lastBody["url"] != "https://feeds.example.com/a.xml" {
		t.Errorf("preview url：%+v %s %v", res, lastPath, lastBody)
	}
	if res, out, _ := cli("subs", "check", "s1"); res.Code != 0 || !strings.Contains(out, "2026-11-20T08:00:00Z") {
		t.Errorf("check：%+v %q", res, out)
	}
	if res, out, _ := cli("subs", "rm", "s1", "--yes"); res.Code != 0 || lastPath != "DELETE /api/subscriptions/s1" || !strings.Contains(out, "已刪除訂閱 s1") {
		t.Errorf("rm：%+v %q", res, out)
	}

	file := filepath.Join(t.TempDir(), "subs.opml")
	if err := os.WriteFile(file, []byte(`<opml/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, out, errOut := cli("subs", "import", file); res.Code != 0 || string(importData) != `<opml/>` ||
		!strings.Contains(out, "已建立 1 個訂閱，重複 1 個，失敗 1 個") || !strings.Contains(out, "第 3 行 ftp://x") {
		t.Errorf("import：%+v %q %s", res, out, errOut)
	}
	// 超過上限的檔案最多讀上限加 1 個位元組，交給 server 判斷。
	big := filepath.Join(t.TempDir(), "big.opml")
	if err := os.WriteFile(big, make([]byte, subscription.MaxImportBytes+100), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _, _ := cli("subs", "import", big, "--json"); res.Code != 0 || len(importData) != subscription.MaxImportBytes+1 {
		t.Errorf("import 大檔：%+v %d", res, len(importData))
	}
	if res, _, errOut := cli("subs", "import", filepath.Join(t.TempDir(), "missing.opml")); res.Code != 1 || !strings.Contains(errOut, "無法讀取匯入檔") {
		t.Errorf("import 不存在的檔案：%+v %s", res, errOut)
	}
}
