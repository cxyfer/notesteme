package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

type recordedCalls struct {
	failed  []bool
	cookies []string
}

func (r *recordedCalls) ToolCall(_ context.Context, tool string, failed bool) {
	if tool != "yt-dlp" {
		panic(tool)
	}
	r.failed = append(r.failed, failed)
}
func (r *recordedCalls) CookieOK(_ context.Context, p string) { r.cookies = append(r.cookies, p) }

// OPS-R17、R18：每次呼叫記錄一次；影片不存在（permanent）不算失敗；帶 cookie 成功時記錄 cookie 的成功使用。
func TestCallsAreRecorded(t *testing.T) {
	rec := &recordedCalls{}
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	c := newClient(t, r)
	c.Calls = rec
	c.Cookies = &fakeCookies{dir: t.TempDir(), have: true}
	ctx := context.Background()
	if _, err := c.Probe(ctx, "abc123xxxxx"); err != nil {
		t.Fatal(err)
	}
	r.res = toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube] abc123xxxxx: Video unavailable. This video has been removed by the uploader")}
	_, _ = c.Probe(ctx, "abc123xxxxx")
	r.res = toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube] abc123xxxxx: Sign in to confirm you're not a bot")}
	_, _ = c.Probe(ctx, "abc123xxxxx")
	r.res, r.err = toolexec.Result{}, errors.New("exec failed")
	_, _ = c.Probe(ctx, "abc123xxxxx")
	if len(rec.failed) != 4 || rec.failed[0] || rec.failed[1] || !rec.failed[2] || !rec.failed[3] {
		t.Fatalf("failed = %v", rec.failed)
	}
	if len(rec.cookies) != 1 || rec.cookies[0] != "youtube" {
		t.Fatalf("cookie 的成功使用 = %v", rec.cookies)
	}
	// 沒有 cookie 時成功也不記錄 cookie。
	c.Cookies = &fakeCookies{}
	r.res, r.err = toolexec.Result{Stdout: []byte(sampleInfo)}, nil
	if _, err := c.Probe(ctx, "abc123xxxxx"); err != nil {
		t.Fatal(err)
	}
	if len(rec.cookies) != 1 {
		t.Errorf("沒有帶 cookie：%v", rec.cookies)
	}
}
