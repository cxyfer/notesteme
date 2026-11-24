package bilibili

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

type recordedCalls struct {
	failed  []bool
	cookies []string
	// uses 記錄失敗的 cookie 使用：「failed:<平台>」或「expired:<平台>」（SEC-R12）。
	uses []string
}

func (r *recordedCalls) ToolCall(_ context.Context, _ string, failed bool) {
	r.failed = append(r.failed, failed)
}
func (r *recordedCalls) CookieOK(_ context.Context, p string) { r.cookies = append(r.cookies, p) }
func (r *recordedCalls) CookieFailed(_ context.Context, p string, expired bool) {
	if expired {
		r.uses = append(r.uses, "expired:"+p)
	} else {
		r.uses = append(r.uses, "failed:"+p)
	}
}

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

// SEC-R12：帶 cookie 仍被要求登入（ERROR 的 -101，或結束碼 0 但印出「只有登入才看得到字幕」）時 cookie 標成失效；
// 其他失敗只記一次使用。沒有帶 cookie 時一律不記。
func TestCookieFailuresAreRecorded(t *testing.T) {
	const warning = "WARNING: [BiliBili] Subtitles are only available when logged in. Use --cookies-from-browser or --cookies for the authentication.\n"
	rec := &recordedCalls{}
	r := &stubRunner{}
	c := newClient(t, r, &stubCookies{path: "/tmp/cookies.txt"})
	c.Calls = rec
	ctx := context.Background()
	for _, stderr := range []string{
		"ERROR: [BiliBili] BV1xx411c7mD: code -101: 账号未登录",
		"ERROR: HTTP Error 412: Precondition Failed",
		"ERROR: [BiliBili] BV1xx411c7mD: code -404: 啥都木有",
	} {
		r.res = toolexec.Result{ExitCode: 1, Stderr: []byte(stderr)}
		_, _ = c.Probe(ctx, "BV1xx411c7mD", 2)
	}
	r.res = toolexec.Result{Stdout: []byte(sampleInfo), Stderr: []byte(warning)}
	if _, err := c.Probe(ctx, "BV1xx411c7mD", 2); err != nil {
		t.Fatal(err)
	}
	r.res, r.err = toolexec.Result{}, errors.New("exec failed")
	_, _ = c.Probe(ctx, "BV1xx411c7mD", 2)
	want := "expired:bilibili,failed:bilibili,failed:bilibili,expired:bilibili,failed:bilibili"
	if got := strings.Join(rec.uses, ","); got != want {
		t.Fatalf("uses = %s，want %s", got, want)
	}
	if len(rec.cookies) != 0 {
		t.Fatalf("不該記錄成功使用：%v", rec.cookies)
	}
	// 沒有 cookie：未登入的警告是正常的，不記。
	rec.uses = nil
	c.Cookies = &stubCookies{}
	r.res, r.err = toolexec.Result{Stdout: []byte(sampleInfo), Stderr: []byte(warning)}, nil
	if _, err := c.Probe(ctx, "BV1xx411c7mD", 2); err != nil {
		t.Fatal(err)
	}
	if len(rec.uses) != 0 || len(rec.cookies) != 0 {
		t.Fatalf("沒有帶 cookie：uses=%v cookies=%v", rec.uses, rec.cookies)
	}
}
