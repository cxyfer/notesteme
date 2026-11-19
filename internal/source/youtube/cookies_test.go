package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// fakeCookies 是 Cookies 的替身：每次 Acquire 寫出一個新檔案，並記錄 release 的次數。
type fakeCookies struct {
	dir      string
	have     bool
	err      error
	acquired []string
	released int
	platform []string
}

func (f *fakeCookies) Acquire(_ context.Context, platform string) (string, func(), error) {
	f.platform = append(f.platform, platform)
	if f.err != nil {
		return "", func() {}, f.err
	}
	if !f.have {
		return "", func() {}, nil
	}
	p := filepath.Join(f.dir, "cookies-youtube-"+string(rune('a'+len(f.acquired)))+".txt")
	if err := os.WriteFile(p, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		return "", func() {}, err
	}
	f.acquired = append(f.acquired, p)
	return p, func() { f.released++; _ = os.Remove(p) }, nil
}

// cookieArg 回傳 args 中 --cookies 之後的路徑；沒有時回傳空字串。
func cookieArg(args []string) string {
	if i := slices.Index(args, "--cookies"); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// 有 YouTube cookie 時，每一種 yt-dlp 呼叫都帶 --cookies，而且檔案在子程序執行期間存在、結束後被釋放。
func TestEveryCallPassesCookiesAndReleases(t *testing.T) {
	fc := &fakeCookies{dir: t.TempDir(), have: true}
	var existedDuringRun []bool
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	r.after = func(c toolexec.Cmd) {
		p := cookieArg(c.Args)
		_, err := os.Stat(p)
		existedDuringRun = append(existedDuringRun, p != "" && err == nil)
		// 輸出隨呼叫不同：播放清單、字幕檔、音檔。
		for i, a := range c.Args {
			if a == "-o" {
				out := filepath.Dir(c.Args[i+1])
				_ = os.WriteFile(filepath.Join(out, "sub.en.vtt"), []byte("WEBVTT\n"), 0o600)
				_ = os.WriteFile(filepath.Join(out, "audio.m4a"), []byte("A"), 0o600)
			}
		}
		if slices.Contains(c.Args, "--flat-playlist") {
			r.res = toolexec.Result{Stdout: []byte(`{"entries":[{"id":"abc123xxxxx","title":"t"}]}`)}
		}
	}
	c := newClient(t, r)
	c.Cookies = fc
	ctx := context.Background()

	if _, err := c.Probe(ctx, "abc123xxxxx"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FetchCaptions(ctx, "abc123xxxxx", Track{Lang: "en", Kind: KindManual}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	r.res = toolexec.Result{}
	if _, err := c.DownloadAudio(ctx, "abc123xxxxx", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Playlist(ctx, "PLabc"); err != nil {
		t.Fatal(err)
	}

	if len(r.calls) != 4 {
		t.Fatalf("呼叫 %d 次，預期 4", len(r.calls))
	}
	seen := map[string]bool{}
	for i, call := range r.calls {
		p := cookieArg(call.Args)
		if p == "" {
			t.Errorf("第 %d 次呼叫沒有 --cookies：%v", i, call.Args)
		}
		if seen[p] {
			t.Errorf("第 %d 次呼叫重用了 cookie 檔 %s", i, p)
		}
		seen[p] = true
		if !existedDuringRun[i] {
			t.Errorf("第 %d 次呼叫時 cookie 檔不存在", i)
		}
		// --cookies 要在 "--" 之前，否則會被當成網址。
		if sep := slices.Index(call.Args, "--"); sep >= 0 && slices.Index(call.Args, "--cookies") > sep {
			t.Errorf("--cookies 在 -- 之後：%v", call.Args)
		}
	}
	if fc.released != 4 {
		t.Errorf("release %d 次，預期 4", fc.released)
	}
	for _, p := range fc.acquired {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 應已刪除", p)
		}
	}
	for _, pl := range fc.platform {
		if pl != "youtube" {
			t.Errorf("Acquire 的平台 = %q", pl)
		}
	}
}

// yt-dlp 失敗時也要 release。
func TestCookiesReleasedWhenYtDlpFails(t *testing.T) {
	fc := &fakeCookies{dir: t.TempDir(), have: true}
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: boom")}}
	c := newClient(t, r)
	c.Cookies = fc
	if _, err := c.Probe(context.Background(), "abc123xxxxx"); err == nil {
		t.Fatal("預期失敗")
	}
	if fc.released != 1 {
		t.Errorf("release %d 次，預期 1", fc.released)
	}
}

func TestNoCookieMeansNoFlag(t *testing.T) {
	for name, cookies := range map[string]Cookies{"nil": nil, "沒有 cookie": &fakeCookies{dir: t.TempDir()}} {
		t.Run(name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
			c := newClient(t, r)
			c.Cookies = cookies
			if _, err := c.Probe(context.Background(), "abc123xxxxx"); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(r.calls[0].Args, "--cookies") {
				t.Errorf("沒有 cookie 不該帶 --cookies：%v", r.calls[0].Args)
			}
		})
	}
}

// Version 與 cookie 無關，不取用 cookie。
func TestVersionDoesNotAcquireCookies(t *testing.T) {
	fc := &fakeCookies{dir: t.TempDir(), have: true}
	r := &stubRunner{res: toolexec.Result{Stdout: []byte("2026.11.01\n")}}
	c := newClient(t, r)
	c.Cookies = fc
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fc.platform) != 0 || slices.Contains(r.calls[0].Args, "--cookies") {
		t.Errorf("Version 不該用 cookie：%v %v", fc.platform, r.calls[0].Args)
	}
}

// 取用 cookie 失敗：不執行 yt-dlp，回報錯誤，且錯誤不是 permanent（修好後重試會成功）。
func TestAcquireFailureStopsTheCall(t *testing.T) {
	fc := &fakeCookies{err: errors.New("解開 youtube 的 cookie 失敗")}
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	c := newClient(t, r)
	c.Cookies = fc
	_, err := c.Probe(context.Background(), "abc123xxxxx")
	if err == nil || len(r.calls) != 0 {
		t.Fatalf("err = %v，呼叫 %d 次", err, len(r.calls))
	}
	if class, _ := errclass.ClassOf(err); class != errclass.ClassRetryable {
		t.Errorf("class = %v，預期 retryable", class)
	}
}

// SRC-R9：403 要使用者上傳 cookie，補上後自動恢復。
func TestForbiddenResumesOnYouTubeCookie(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: unable to download video data: HTTP Error 403: Forbidden")}}
	_, err := newClient(t, r).Probe(context.Background(), "abc123xxxxx")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || ce.ResumeOn != "cookie:youtube" {
		t.Fatalf("err = %#v，預期 needs_action 且 ResumeOn=cookie:youtube", err)
	}
	if !slices.Contains(ce.Actions, "上傳 cookie") {
		t.Errorf("Actions = %v", ce.Actions)
	}
}

// 會員限定的影片：上傳 cookie 後自動繼續。
func TestMembersOnlyNeedsCookie(t *testing.T) {
	stderr := "ERROR: [youtube] abc123xxxxx: Join this channel to get access to members-only content like this video, and other exclusive perks."
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(stderr)}}
	_, err := newClient(t, r).Probe(context.Background(), "abc123xxxxx")
	var ce *errclass.Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	if ce.Class != errclass.ClassNeedsAction || ce.ResumeOn != "cookie:youtube" {
		t.Errorf("class = %v，ResumeOn = %q", ce.Class, ce.ResumeOn)
	}
	if ce.Message != "這部影片需要登入才能取得。上傳 YouTube cookie 後會自動繼續。" {
		t.Errorf("Message = %q", ce.Message)
	}
	if !slices.Equal(ce.Actions, []string{"上傳 cookie"}) {
		t.Errorf("Actions = %v", ce.Actions)
	}
}

// 不需要 cookie 的 needs_action（找不到 yt-dlp）不帶 ResumeOn。
func TestMissingToolHasNoResumeOn(t *testing.T) {
	c := &Client{Runner: &stubRunner{}, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}
	_, err := c.Probe(context.Background(), "abc123xxxxx")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.ResumeOn != "" {
		t.Errorf("err = %#v", err)
	}
}
