package bilibili

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

func (r *recordedCalls) ToolCall(_ context.Context, _ string, failed bool) {
	r.failed = append(r.failed, failed)
}
func (r *recordedCalls) CookieOK(_ context.Context, p string) { r.cookies = append(r.cookies, p) }

// OPS-R17、R18：每次呼叫記錄一次；影片不存在（permanent）不算失敗；帶 cookie 成功時記錄 cookie 的成功使用。
func TestCallsAreRecorded(t *testing.T) {
	rec := &recordedCalls{}
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	c := newClient(t, r, &stubCookies{path: "/tmp/cookies.txt"})
	c.Calls = rec
	ctx := context.Background()
	if _, err := c.Probe(ctx, "BV1xx411c7mD", 2); err != nil {
		t.Fatal(err)
	}
	r.res = toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [BiliBili] BV1xx411c7mD: code -404: 啥都木有")}
	_, _ = c.Probe(ctx, "BV1xx411c7mD", 2)
	r.res = toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: HTTP Error 412: Precondition Failed")}
	_, _ = c.Probe(ctx, "BV1xx411c7mD", 2)
	r.res, r.err = toolexec.Result{}, errors.New("exec failed")
	_, _ = c.Probe(ctx, "BV1xx411c7mD", 2)
	if len(rec.failed) != 4 || rec.failed[0] || rec.failed[1] || !rec.failed[2] || !rec.failed[3] {
		t.Fatalf("failed = %v", rec.failed)
	}
	if len(rec.cookies) != 1 || rec.cookies[0] != Platform {
		t.Fatalf("cookie 的成功使用 = %v", rec.cookies)
	}
}
