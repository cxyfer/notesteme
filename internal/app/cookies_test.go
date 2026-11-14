package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// memberRunner 在沒有帶 --cookies 時，對 -J 回報會員限定；帶了就交給 ytRunner 正常回應。
// 並記錄 -J 執行當下的 cookie 檔是否存在、權限與內容。
type memberRunner struct {
	*ytRunner
	cookieFile string
	mode       os.FileMode
	content    string
}

func (m *memberRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if slices.Contains(c.Args, "-J") {
		i := slices.Index(c.Args, "--cookies")
		if i < 0 {
			return toolexec.Result{ExitCode: 1, Stderr: []byte(
				"ERROR: [youtube] abc123xxxxx: Join this channel to get access to members-only content like this video\n")}, nil
		}
		m.cookieFile = c.Args[i+1]
		if fi, err := os.Stat(m.cookieFile); err == nil {
			m.mode = fi.Mode().Perm()
			b, _ := os.ReadFile(m.cookieFile)
			m.content = string(b)
		}
	}
	return m.ytRunner.Run(ctx, c)
}

// standalone 的程序內 handler 也有 cookie API，上傳後 blocked 的步驟恢復並帶著 cookie 重跑（SRC-R8、PIPE-R4）。
func TestStandaloneCookieUploadResumesBlockedItem(t *testing.T) {
	env := testEnv(t)
	r := &memberRunner{ytRunner: &ytRunner{}}
	env.Runner = r
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	h := sa.Handler()

	code, body := call(t, h, "POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	if code != 201 {
		t.Fatalf("POST /api/items = %d %s", code, body)
	}
	var created struct {
		Items []detail `json:"items"`
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.Items) != 1 {
		t.Fatalf("回應 = %s, %v", body, err)
	}
	id := created.Items[0].ID
	getDetail := func() detail {
		t.Helper()
		_, b := call(t, h, "GET", "/api/items/"+id, "")
		var d detail
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}

	if err := sa.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := getDetail().status("metadata"); got != "blocked" {
		t.Fatalf("上傳前 metadata = %s，預期 blocked", got)
	}

	code, body = call(t, h, "POST", "/api/cookies", `{"platform":"youtube","content":"SID=very-secret-sid; HSID=h"}`)
	if code != 200 || !strings.Contains(string(body), `"cookies":2`) || strings.Contains(string(body), "very-secret") {
		t.Fatalf("POST /api/cookies = %d %s", code, body)
	}
	if got := getDetail().status("metadata"); got != "pending" {
		t.Fatalf("上傳後 metadata = %s，預期 pending", got)
	}

	if err := sa.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := getDetail(); d.status("metadata") != "done" || d.status("transcript") != "done" {
		t.Errorf("恢復後應跑完：%+v", d.Stages)
	}
	if r.cookieFile == "" || r.mode != 0o600 || !strings.Contains(r.content, "very-secret-sid") {
		t.Errorf("yt-dlp 收到的 cookie 檔：%q，權限 %v，內容 %q", r.cookieFile, r.mode, r.content)
	}
	// 子程序結束後沒有 cookie 檔留下。
	if left, _ := filepath.Glob(filepath.Join(env.Vars["NOTESTEME_DATA"], "tmp", "cookies-*")); len(left) != 0 {
		t.Errorf("留下 cookie 檔：%v", left)
	}
}
