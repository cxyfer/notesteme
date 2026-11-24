package youtube

import (
	"context"
	"errors"
	"slices"
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

func (r *recordedCalls) ToolCall(_ context.Context, tool string, failed bool) {
	if tool != "yt-dlp" {
		panic(tool)
	}
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

// SEC-R12：YouTube 帶 cookie 的失敗一律只記一次使用，不標成失效（會員限定、403、機器人驗證、影片不存在都一樣；
// 呼叫帶 --no-warnings，yt-dlp 的「cookie 已失效」警告不會出現）。沒有帶 cookie 時一律不記。
func TestCookieFailuresAreRecorded(t *testing.T) {
	rec := &recordedCalls{}
	r := &stubRunner{}
	c := newClient(t, r)
	c.Calls = rec
	c.Cookies = &fakeCookies{dir: t.TempDir(), have: true}
	ctx := context.Background()
	for _, stderr := range []string{
		"ERROR: [youtube] abc123xxxxx: Join this channel to get access to members-only content like this video, or contact the channel owner.",
		"ERROR: unable to download video data: HTTP Error 403: Forbidden",
		"ERROR: [youtube] abc123xxxxx: Sign in to confirm you're not a bot",
		"ERROR: [youtube] abc123xxxxx: Video unavailable. This video has been removed by the uploader",
	} {
		r.res = toolexec.Result{ExitCode: 1, Stderr: []byte(stderr)}
		_, _ = c.Probe(ctx, "abc123xxxxx")
	}
	r.res, r.err = toolexec.Result{}, errors.New("exec failed")
	_, _ = c.Probe(ctx, "abc123xxxxx")
	want := []string{"failed:youtube", "failed:youtube", "failed:youtube", "failed:youtube", "failed:youtube"}
	if strings.Join(rec.uses, ",") != strings.Join(want, ",") {
		t.Fatalf("uses = %v，want %v", rec.uses, want)
	}
	if len(rec.cookies) != 0 {
		t.Fatalf("不該記錄成功使用：%v", rec.cookies)
	}
	if args := r.calls[0].Args; !slices.Contains(args, "--no-warnings") {
		t.Fatalf("YouTube 的呼叫應帶 --no-warnings（失效的判斷依賴這一點）：%v", args)
	}
	// 沒有 cookie：不記。
	rec.uses = nil
	c.Cookies = &fakeCookies{}
	r.res, r.err = toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube] abc123xxxxx: Sign in to confirm you're not a bot")}, nil
	_, _ = c.Probe(ctx, "abc123xxxxx")
	if len(rec.uses) != 0 {
		t.Fatalf("沒有帶 cookie：%v", rec.uses)
	}
}
