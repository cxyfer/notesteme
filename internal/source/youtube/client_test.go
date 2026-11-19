package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// newClient 建立一個 yt-dlp 位於暫存目錄的 Client，工具本身不會被執行（由 stubRunner 接手）。
func newClient(t *testing.T, r *stubRunner) *Client {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Runner: r, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": dir}}}
}

const sampleInfo = `{
  "id": "abc123xxxxx", "title": "PostgreSQL in 100 Seconds", "duration": 612.4, "language": "en",
  "availability": "public", "live_status": "not_live", "channel_id": "UC1", "channel": "Fireship",
  "timestamp": 1794477000, "description": "desc",
  "chapters": [{"title": "Intro", "start_time": 0, "end_time": 30.5}],
  "subtitles": {"en": [{"ext": "vtt", "url": "https://x/en.vtt"}], "zh-Hans": [{"ext": "vtt", "url": "https://x/zh.vtt"}]},
  "automatic_captions": {
    "en": [{"ext": "vtt", "url": "https://x/auto-en.vtt"}],
    "ja": [{"ext": "vtt", "url": "https://x/auto-ja.vtt?tlang=ja"}],
    "fr": []
  }
}`

func TestProbeParsesInfoAndUsesSafeArguments(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(sampleInfo)}}
	c := newClient(t, r)
	info, err := c.Probe(context.Background(), "abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}

	if info.Title != "PostgreSQL in 100 Seconds" || info.Language != "en" || info.Availability != "public" ||
		info.LiveStatus != "not_live" || info.Channel != "Fireship" || info.ChannelID != "UC1" || info.Description != "desc" {
		t.Errorf("info = %+v", info)
	}
	if info.DurationS == nil || *info.DurationS != 612 {
		t.Errorf("duration = %v，預期四捨五入成 612", info.DurationS)
	}
	if info.PublishedAt == nil || info.PublishedAt.Unix() != 1794477000 {
		t.Errorf("published_at = %v", info.PublishedAt)
	}
	if len(info.Chapters) != 1 || info.Chapters[0].End != 30.5 {
		t.Errorf("chapters = %+v", info.Chapters)
	}
	want := []Track{{"en", KindAuto}, {"en", KindManual}, {"ja", KindPlatformTranslated}, {"zh-Hans", KindManual}}
	if !slices.Equal(info.Tracks, want) {
		t.Errorf("tracks = %+v，預期 %+v", info.Tracks, want)
	}

	args := r.calls[0].Args
	for _, must := range []string{"-J", "--use-extractors", "--ignore-config", "--no-playlist"} {
		if !slices.Contains(args, must) {
			t.Errorf("缺少旗標 %s：%v", must, args)
		}
	}
	if i := slices.Index(args, "--use-extractors"); args[i+1] != "youtube" {
		t.Errorf("--use-extractors 應只開放 youtube：%v", args)
	}
	// 網址一律放在 -- 之後，而且是正規化的網址。
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.youtube.com/watch?v=abc123xxxxx" {
		t.Errorf("網址應在 -- 之後：%v", args)
	}
	if !filepath.IsAbs(r.calls[0].Path) {
		t.Errorf("工具路徑應是絕對路徑：%q", r.calls[0].Path)
	}
}

// 只有 upload_date（日期精度）時，PublishedAt 是當天 00:00 UTC，並標示為近似；有 timestamp 時是精確的。
func TestProbePublishedAtPrecision(t *testing.T) {
	for name, tt := range map[string]struct {
		json   string
		approx bool
		want   time.Time
	}{
		"timestamp":         {`{"id":"x","timestamp":1794477000,"upload_date":"20261111"}`, false, time.Unix(1794477000, 0).UTC()},
		"只有 upload_date":    {`{"id":"x","upload_date":"20261111"}`, true, time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)},
		"upload_date 格式不正確": {`{"id":"x","upload_date":"bad"}`, false, time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{Stdout: []byte(tt.json)}}
			info, err := newClient(t, r).Probe(context.Background(), "x")
			if err != nil {
				t.Fatal(err)
			}
			if info.PublishedAtApprox != tt.approx || (tt.want.IsZero() != (info.PublishedAt == nil)) ||
				(info.PublishedAt != nil && !info.PublishedAt.Equal(tt.want)) {
				t.Errorf("published_at = %v，approx = %v", info.PublishedAt, info.PublishedAtApprox)
			}
		})
	}
}

func TestProbeMissingFieldsAreEmpty(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"id":"x","title":"t","language":null,"duration":null,"live_status":"is_live"}`)}}
	info, err := newClient(t, r).Probe(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "" || info.DurationS != nil || !info.Live() || len(info.Tracks) != 0 {
		t.Errorf("info = %+v", info)
	}
}

func TestProbeClassifiesFailures(t *testing.T) {
	// 其他非 0 結束碼是 retryable；403 是 needs_action（見 TestClassify403NeedsAction）。
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: HTTP Error 500: Internal Server Error\n")}}
	_, err := newClient(t, r).Probe(context.Background(), "x")
	var e *errclass.Error
	if !errors.As(err, &e) || e.Class != errclass.ClassRetryable || e.ExitCode != 1 || !strings.Contains(e.Detail, "500") {
		t.Fatalf("err = %#v", err)
	}

	// 無法啟動工具（runner 錯誤）也是 retryable；取消則原樣回傳。
	r = &stubRunner{err: errors.New("boom")}
	if _, err := newClient(t, r).Probe(context.Background(), "x"); !errors.Is(err, r.err) {
		t.Errorf("err = %v", err)
	} else if cl, _ := errclass.ClassOf(err); cl != errclass.ClassRetryable {
		t.Errorf("class = %s", cl)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = &stubRunner{err: ctx.Err()}
	if _, err := newClient(t, r).Probe(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("取消應原樣回傳：%v", err)
	}

	// 輸出不是合法的 JSON。
	r = &stubRunner{res: toolexec.Result{Stdout: []byte("not json")}}
	if _, err := newClient(t, r).Probe(context.Background(), "x"); err == nil {
		t.Error("無法解析的輸出應回傳錯誤")
	}
}

// yt-dlp 回報影片不存在（已刪除、私人、無法取得）是 permanent，不重試；其他錯誤維持 retryable。
func TestClassifyVideoGone(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   errclass.Class
	}{
		{"影片不存在", "ERROR: [youtube] abc123xxxxx: Video unavailable\n", errclass.ClassPermanent},
		{"上傳者移除", "ERROR: [youtube] abc123xxxxx: Video unavailable. This video has been removed by the uploader\n", errclass.ClassPermanent},
		{"帳號已終止", "ERROR: [youtube] abc123xxxxx: Video unavailable. This video is no longer available because the YouTube account associated with this video has been terminated.\n", errclass.ClassPermanent},
		{"私人影片", "ERROR: [youtube] abc123xxxxx: Private video. Sign in if you've been granted access to this video\n", errclass.ClassPermanent},
		{"舊版的移除訊息", "ERROR: [youtube] abc123xxxxx: This video has been removed for violating YouTube's Terms of Service\n", errclass.ClassPermanent},
		{"舊版的無法取得訊息", "ERROR: [youtube] abc123xxxxx: This video is no longer available due to a copyright claim\n", errclass.ClassPermanent},
		{"extractor 名稱帶後綴", "ERROR: [youtube:tab] abc123xxxxx: Video unavailable\n", errclass.ClassPermanent},
		{"前面有其他輸出", "[youtube] Extracting URL: https://www.youtube.com/watch?v=abc123xxxxx\nERROR: [youtube] abc123xxxxx: Video unavailable\n", errclass.ClassPermanent},
		{"大小寫不同", "error: [YouTube] abc123xxxxx: video unavailable\n", errclass.ClassPermanent},

		{"被限流", "ERROR: [youtube] abc123xxxxx: Video unavailable. This content isn't available, try again later. The current session has been rate-limited by YouTube for up to an hour.\n", errclass.ClassRetryable},
		// 會員限定改為 needs_action（上傳 cookie 後自動繼續），見 TestMembersOnlyNeedsCookie。
		{"會員限定", "ERROR: [youtube] abc123xxxxx: Join this channel to get access to members-only content like this video\n", errclass.ClassNeedsAction},
		{"機器人驗證", "ERROR: [youtube] abc123xxxxx: Sign in to confirm you're not a bot\n", errclass.ClassRetryable},
		{"不是 youtube extractor", "ERROR: [BiliBili] BV1xx411c7mD: Video unavailable\n", errclass.ClassRetryable},
		{"只是輸出提到", "WARNING: Video unavailable is mentioned here\nERROR: unable to connect\n", errclass.ClassRetryable},
		{"沒有輸出", "", errclass.ClassRetryable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(tt.stderr)}}
			c := newClient(t, r)
			for op, call := range map[string]func() error{
				"Probe": func() error { _, err := c.Probe(context.Background(), "abc123xxxxx"); return err },
				"FetchCaptions": func() error {
					_, err := c.FetchCaptions(context.Background(), "abc123xxxxx", Track{"en", KindManual}, t.TempDir())
					return err
				},
			} {
				err := call()
				var e *errclass.Error
				if !errors.As(err, &e) || e.Class != tt.want || e.ExitCode != 1 {
					t.Errorf("%s：err = %#v，預期類別 %s", op, err, tt.want)
				}
			}
		})
	}
}

// yt-dlp 回傳 403 是 needs_action（SRC-R9），附上給使用者看的訊息與修正動作；影片不存在的判斷優先。
func TestClassify403NeedsAction(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   errclass.Class
	}{
		{"下載媒體", "ERROR: unable to download video data: HTTP Error 403: Forbidden\n", errclass.ClassNeedsAction},
		{"帶 extractor 前綴", "ERROR: [youtube] abc123xxxxx: unable to download video data: HTTP Error 403: Forbidden\n", errclass.ClassNeedsAction},
		{"下載網頁", "ERROR: [youtube] abc123xxxxx: Unable to download webpage: HTTP Error 403: Forbidden (caused by <HTTPError 403: 'Forbidden'>)\n", errclass.ClassNeedsAction},
		{"前面有其他輸出", "[youtube] Extracting URL: x\nERROR: unable to download video data: HTTP Error 403: Forbidden\n", errclass.ClassNeedsAction},
		{"影片不存在優先", "ERROR: [youtube] abc123xxxxx: Video unavailable\nERROR: unable to download video data: HTTP Error 403: Forbidden\n", errclass.ClassPermanent},
		{"不是 ERROR 行", "WARNING: HTTP Error 403: Forbidden, retrying\nERROR: unable to connect\n", errclass.ClassRetryable},
		{"不是 403", "ERROR: unable to download video data: HTTP Error 4030: odd\n", errclass.ClassRetryable},
		{"429", "ERROR: unable to download video data: HTTP Error 429: Too Many Requests\n", errclass.ClassRetryable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(tt.stderr)}}
			_, err := newClient(t, r).Probe(context.Background(), "abc123xxxxx")
			var e *errclass.Error
			if !errors.As(err, &e) || e.Class != tt.want {
				t.Fatalf("err = %#v，預期類別 %s", err, tt.want)
			}
			if tt.want == errclass.ClassNeedsAction {
				if e.Message != "YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。" ||
					!slices.Equal(e.Actions, []string{"更新 yt-dlp", "上傳 cookie"}) {
					t.Errorf("訊息與動作 = %q %q", e.Message, e.Actions)
				}
			} else if e.Message != "" || len(e.Actions) != 0 {
				t.Errorf("其他類別不該帶使用者訊息：%q %q", e.Message, e.Actions)
			}
		})
	}
}

func TestProbeWithoutToolNeedsAction(t *testing.T) {
	c := &Client{Runner: &stubRunner{}, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}
	_, err := c.Probe(context.Background(), "x")
	if cl, ok := errclass.ClassOf(err); !ok || cl != errclass.ClassNeedsAction {
		t.Errorf("找不到 yt-dlp 時應是 needs_action：%v", err)
	}
}

func TestFetchCaptionsArguments(t *testing.T) {
	for _, tt := range []struct {
		track Track
		flag  string
		other string
	}{
		{Track{"en", KindManual}, "--write-subs", "--write-auto-subs"},
		{Track{"en", KindAuto}, "--write-auto-subs", "--write-subs"},
		{Track{"zh-Hans", KindPlatformTranslated}, "--write-auto-subs", "--write-subs"},
	} {
		dir := t.TempDir()
		r := &stubRunner{after: func(c toolexec.Cmd) {
			_ = os.WriteFile(filepath.Join(dir, "sub."+tt.track.Lang+".vtt"), []byte("WEBVTT\n"), 0o644)
		}}
		data, err := newClient(t, r).FetchCaptions(context.Background(), "abc123xxxxx", tt.track, dir)
		if err != nil || string(data) != "WEBVTT\n" {
			t.Fatalf("%+v：%q, %v", tt.track, data, err)
		}
		args := r.calls[0].Args
		if !slices.Contains(args, tt.flag) || slices.Contains(args, tt.other) || !slices.Contains(args, "--skip-download") {
			t.Errorf("%+v：旗標不正確 %v", tt.track, args)
		}
		if i := slices.Index(args, "--sub-langs"); i < 0 || args[i+1] != tt.track.Lang {
			t.Errorf("--sub-langs：%v", args)
		}
		if i := slices.Index(args, "--sub-format"); i < 0 || args[i+1] != "vtt" {
			t.Errorf("--sub-format：%v", args)
		}
		if i := slices.Index(args, "-o"); i < 0 || !strings.HasPrefix(args[i+1], dir) {
			t.Errorf("-o 應指向暫存目錄：%v", args)
		}
		if n := len(args); args[n-2] != "--" {
			t.Errorf("網址應在 -- 之後：%v", args)
		}
		if !slices.Contains(args, "--use-extractors") {
			t.Errorf("缺少 --use-extractors：%v", args)
		}
	}
}

// yt-dlp 的 -o 是輸出模板：目錄裡的 % 要跳脫成 %%，否則 %(title)s 之類會被展開。
func TestFetchCaptionsEscapesPercentInOutputTemplate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "100% done %(title)s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &stubRunner{after: func(toolexec.Cmd) {
		_ = os.WriteFile(filepath.Join(dir, "sub.en.vtt"), []byte("WEBVTT\n"), 0o644)
	}}
	if _, err := newClient(t, r).FetchCaptions(context.Background(), "abc123xxxxx", Track{"en", KindManual}, dir); err != nil {
		t.Fatal(err)
	}
	args := r.calls[0].Args
	want := filepath.Join(strings.ReplaceAll(dir, "%", "%%"), "sub.%(ext)s")
	if i := slices.Index(args, "-o"); i < 0 || args[i+1] != want {
		t.Errorf("-o = %v，預期 %q", args, want)
	}
}

func TestFetchCaptionsRejectsUnsafeLanguage(t *testing.T) {
	for _, lang := range []string{"-en", "all", "en,ja", "en.*", "", "a b"} {
		r := &stubRunner{}
		_, err := newClient(t, r).FetchCaptions(context.Background(), "x", Track{lang, KindManual}, t.TempDir())
		if err == nil || len(r.calls) != 0 {
			t.Errorf("語言 %q 應在呼叫 yt-dlp 之前被拒絕（err=%v，呼叫 %d 次）", lang, err, len(r.calls))
		}
	}
}

func TestFetchCaptionsMissingFile(t *testing.T) {
	_, err := newClient(t, &stubRunner{}).FetchCaptions(context.Background(), "x", Track{"en", KindManual}, t.TempDir())
	if !errors.Is(err, ErrNoCaptionFile) {
		t.Errorf("err = %v", err)
	}
}

func TestVersion(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte("2026.11.01\n")}}
	if v, err := newClient(t, r).Version(context.Background()); err != nil || v != "2026.11.01" {
		t.Errorf("Version = %q, %v", v, err)
	}
	if r.calls[0].Args[0] != "--version" {
		t.Errorf("args = %v", r.calls[0].Args)
	}
}
