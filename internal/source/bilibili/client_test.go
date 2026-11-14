package bilibili

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

// stubRunner 記錄收到的命令，回傳預先設定的結果；after 可以在回傳前做事（例如寫出字幕檔）。
type stubRunner struct {
	calls []toolexec.Cmd
	res   toolexec.Result
	err   error
	after func(toolexec.Cmd)
}

func (s *stubRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	s.calls = append(s.calls, c)
	if s.after != nil {
		s.after(c)
	}
	return s.res, s.err
}

// stubCookies 是 Cookies 的替身：path 為空代表沒有 cookie。
type stubCookies struct {
	path     string
	err      error
	acquired []string
	released int
}

func (s *stubCookies) Acquire(_ context.Context, platform string) (string, func(), error) {
	s.acquired = append(s.acquired, platform)
	if s.err != nil {
		return "", nil, s.err
	}
	return s.path, func() { s.released++ }, nil
}

func newClient(t *testing.T, r *stubRunner, ck Cookies) *Client {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Runner: r, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": dir}}, Cookies: ck}
}

const sampleInfo = `{
  "id": "BV1xx411c7mD_p2", "title": "Part two", "duration": 120.6, "uploader": "UP主", "uploader_id": "12345",
  "timestamp": 1794477000, "description": "desc",
  "subtitles": {
    "zh-Hans": [{"ext": "srt", "url": "https://x/zh.srt"}],
    "ai-zh": [{"ext": "srt", "url": "https://x/ai.srt"}],
    "danmaku": [{"ext": "xml", "url": "https://x/d.xml"}],
    "en": []
  }
}`

const loginWarning = "WARNING: [BiliBili] BV1xx411c7mD: Subtitles are only available when logged in. Use --cookies-from-browser or --cookies for the authentication.\n"

func TestProbeParsesInfoAndArguments(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	info, err := newClient(t, r, nil).Probe(context.Background(), "BV1xx411c7mD", 2)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Part two" || info.Uploader != "UP主" || info.UploaderID != "12345" || info.Description != "desc" {
		t.Errorf("info = %+v", info)
	}
	if info.DurationS == nil || *info.DurationS != 121 {
		t.Errorf("duration = %v，預期四捨五入成 121", info.DurationS)
	}
	if info.PublishedAt == nil || info.PublishedAt.Unix() != 1794477000 {
		t.Errorf("published_at = %v", info.PublishedAt)
	}
	// 空的字幕軌略過；danmaku 照實保留，交給呼叫端忽略。
	if want := []string{"ai-zh", "danmaku", "zh-Hans"}; !slices.Equal(info.Tracks, want) {
		t.Errorf("tracks = %v，預期 %v", info.Tracks, want)
	}
	if info.NeedsLogin {
		t.Error("沒有 WARNING 時 NeedsLogin 應為 false")
	}

	args := r.calls[0].Args
	for _, must := range []string{"-J", "--write-subs", "--ignore-config", "--no-playlist"} {
		if !slices.Contains(args, must) {
			t.Errorf("缺少旗標 %s：%v", must, args)
		}
	}
	// 帶 --no-warnings 會吃掉「未登入」的 WARNING，Probe 無法偵測。
	if slices.Contains(args, "--no-warnings") {
		t.Errorf("Probe 不能帶 --no-warnings：%v", args)
	}
	if i := slices.Index(args, "--use-extractors"); i < 0 || args[i+1] != "BiliBili" {
		t.Errorf("--use-extractors 應只開放 BiliBili：%v", args)
	}
	if slices.Contains(args, "--cookies") {
		t.Errorf("沒有 cookie 時不帶 --cookies：%v", args)
	}
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.bilibili.com/video/BV1xx411c7mD?p=2" {
		t.Errorf("網址應在 -- 之後且為正規化網址：%v", args)
	}
}

func TestProbeDetectsLoginWarning(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"id":"BV1xx411c7mD","title":"T"}`), Stderr: []byte(loginWarning)}}
	info, err := newClient(t, r, nil).Probe(context.Background(), "BV1xx411c7mD", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !info.NeedsLogin {
		t.Error("stderr 有「Subtitles are only available when logged in」時 NeedsLogin 應為 true")
	}
	if len(info.Tracks) != 0 {
		t.Errorf("tracks = %v", info.Tracks)
	}
}

func TestCookiesAreAcquiredAndReleasedPerCall(t *testing.T) {
	ck := &stubCookies{path: "/tmp/cookie-abc.txt"}
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"id":"BV1xx411c7mD","title":"T"}`)}}
	c := newClient(t, r, ck)
	for range 2 {
		if _, err := c.Probe(context.Background(), "BV1xx411c7mD", 1); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(ck.acquired, []string{"bilibili", "bilibili"}) || ck.released != 2 {
		t.Errorf("acquired = %v，released = %d", ck.acquired, ck.released)
	}
	for _, call := range r.calls {
		if i := slices.Index(call.Args, "--cookies"); i < 0 || call.Args[i+1] != "/tmp/cookie-abc.txt" {
			t.Errorf("應帶 --cookies：%v", call.Args)
		}
	}
}

// 失敗時也要釋放 cookie 暫存檔；沒有 cookie 時（path 為空）不帶參數。
func TestCookiesReleasedOnFailureAndEmptyPath(t *testing.T) {
	ck := &stubCookies{path: "/tmp/c.txt"}
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: boom")}}
	if _, err := newClient(t, r, ck).Probe(context.Background(), "BV1xx411c7mD", 1); err == nil {
		t.Fatal("應該失敗")
	}
	if ck.released != 1 {
		t.Errorf("released = %d", ck.released)
	}

	empty := &stubCookies{}
	r2 := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"id":"x"}`)}}
	if _, err := newClient(t, r2, empty).Probe(context.Background(), "BV1xx411c7mD", 1); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r2.calls[0].Args, "--cookies") {
		t.Errorf("path 為空不應帶 --cookies：%v", r2.calls[0].Args)
	}
	if empty.released != 1 {
		t.Errorf("released = %d", empty.released)
	}
}

func TestCookieAcquireFailureIsReported(t *testing.T) {
	ck := &stubCookies{err: errors.New("解密失敗")}
	r := &stubRunner{}
	if _, err := newClient(t, r, ck).Probe(context.Background(), "BV1xx411c7mD", 1); err == nil || len(r.calls) != 0 {
		t.Fatalf("err = %v，calls = %d", err, len(r.calls))
	}
}

func TestPartsExpandsFlatPlaylist(t *testing.T) {
	out := `{"id":"BV1xx411c7mD","_type":"playlist","entries":[
	  {"url":"https://www.bilibili.com/video/BV1xx411c7mD?p=1","title":"One"},
	  {"url":"https://www.bilibili.com/video/BV1xx411c7mD?p=2","title":"Two"},
	  null,
	  {"url":"https://www.bilibili.com/video/BV1xx411c7mD?p=3","title":"Three"}]}`
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(out)}}
	parts, err := newClient(t, r, nil).Parts(context.Background(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	want := []PartEntry{{1, "One"}, {2, "Two"}, {3, "Three"}}
	if !slices.Equal(parts, want) {
		t.Errorf("parts = %+v", parts)
	}
	args := r.calls[0].Args
	if !slices.Contains(args, "--flat-playlist") || !slices.Contains(args, "-J") {
		t.Errorf("args = %v", args)
	}
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.bilibili.com/video/BV1xx411c7mD" {
		t.Errorf("網址 = %v", args)
	}
}

// 只有一個分P 時 yt-dlp 回傳單一影片，不是 playlist。
func TestPartsSingleVideo(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"id":"BV1xx411c7mD","title":"Only"}`)}}
	parts, err := newClient(t, r, nil).Parts(context.Background(), "BV1xx411c7mD")
	if err != nil || !slices.Equal(parts, []PartEntry{{1, "Only"}}) {
		t.Fatalf("parts = %+v, %v", parts, err)
	}
}

func TestFetchCaptionsDownloadsRawKey(t *testing.T) {
	dir := t.TempDir()
	r := &stubRunner{after: func(c toolexec.Cmd) {
		// 檔名是 <tmpl>.<原始鍵>.srt
		_ = os.WriteFile(filepath.Join(dir, "sub.ai-zh.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o600)
	}}
	data, err := newClient(t, r, nil).FetchCaptions(context.Background(), "BV1xx411c7mD", 2, "ai-zh", dir)
	if err != nil || string(data) == "" {
		t.Fatalf("data = %q, err = %v", data, err)
	}
	args := r.calls[0].Args
	for _, pair := range [][2]string{{"--sub-langs", "ai-zh"}, {"--sub-format", "srt"}, {"--use-extractors", "BiliBili"}} {
		if i := slices.Index(args, pair[0]); i < 0 || args[i+1] != pair[1] {
			t.Errorf("%s 應為 %s：%v", pair[0], pair[1], args)
		}
	}
	for _, must := range []string{"--skip-download", "--write-subs"} {
		if !slices.Contains(args, must) {
			t.Errorf("缺少 %s：%v", must, args)
		}
	}
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.bilibili.com/video/BV1xx411c7mD?p=2" {
		t.Errorf("網址 = %v", args)
	}
}

func TestFetchCaptionsRejectsUnsafeKeysAndMissingFile(t *testing.T) {
	r := &stubRunner{}
	c := newClient(t, r, nil)
	for _, key := range []string{"-all", "all", "zh,en", "", "a b"} {
		_, err := c.FetchCaptions(context.Background(), "BV1xx411c7mD", 1, key, t.TempDir())
		if cl, _ := errclass.ClassOf(err); cl != errclass.ClassPermanent {
			t.Errorf("key %q：err = %v", key, err)
		}
	}
	if len(r.calls) != 0 {
		t.Error("不合法的字幕軌鍵不應呼叫 yt-dlp")
	}
	_, err := c.FetchCaptions(context.Background(), "BV1xx411c7mD", 1, "zh-Hans", t.TempDir())
	if !errors.Is(err, ErrNoCaptionFile) {
		t.Errorf("err = %v", err)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		code   int
		class  errclass.Class
	}{
		{"影片不存在 -404", "ERROR: [BiliBili] BV1xx411c7mD: -404: 啥都木有\n", 1, errclass.ClassPermanent},
		{"地區限制 -10403", "ERROR: [BiliBili] BV1xx411c7mD: -10403: 抱歉您所在地区不可观看\n", 1, errclass.ClassPermanent},
		{"geo restriction", "ERROR: [BiliBili] BV1xx411c7mD: This video is not available from your location due to geo restriction\n", 1, errclass.ClassPermanent},
		{"需要登入 -101", "ERROR: [BiliBili] BV1xx411c7mD: -101: 账号未登录\n", 1, errclass.ClassNeedsAction},
		{"412 風控", "ERROR: [BiliBili] BV1xx411c7mD: Unable to download JSON metadata: HTTP Error 412: Precondition Failed\n", 1, errclass.ClassRetryable},
		{"其他", "ERROR: something odd\n", 1, errclass.ClassRetryable},
		// 只看 ERROR 行：WARNING 提到 -404 不代表失敗的原因。
		{"WARNING 不算", "WARNING: retry after -404\nERROR: timed out\n", 1, errclass.ClassRetryable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{ExitCode: tt.code, Stderr: []byte(tt.stderr)}}
			_, err := newClient(t, r, nil).Probe(context.Background(), "BV1xx411c7mD", 1)
			var ce *errclass.Error
			if !errors.As(err, &ce) || ce.Class != tt.class {
				t.Fatalf("err = %v，預期類別 %s", err, tt.class)
			}
			if tt.class == errclass.ClassNeedsAction && (ce.ResumeOn != "cookie:bilibili" || ce.Message == "" || len(ce.Actions) == 0) {
				t.Errorf("needs_action 要帶 ResumeOn、訊息與動作：%+v", ce)
			}
		})
	}
}

func TestMissingYtDlpNeedsAction(t *testing.T) {
	c := &Client{Runner: &stubRunner{}, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}
	_, err := c.Probe(context.Background(), "BV1xx411c7mD", 1)
	if cl, _ := errclass.ClassOf(err); cl != errclass.ClassNeedsAction {
		t.Errorf("err = %v", err)
	}
}

// R8 的文案要與場景逐字相同。
func TestLoginMessages(t *testing.T) {
	if MsgAICaptionsNeedLogin != "這部影片的 AI 字幕需要登入 Bilibili 才能取得。上傳 Bilibili cookie，或改用語音轉錄。" {
		t.Errorf("訊息 = %q", MsgAICaptionsNeedLogin)
	}
	if ActionUploadCookie != "上傳 cookie" || ActionUseASR != "改用語音轉錄" || ResumeOnCookie != "cookie:bilibili" {
		t.Error("動作或 ResumeOn 文案不符")
	}
}
