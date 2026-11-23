package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

func TestLimiter(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	l := newLimiter(clk)
	// 容量等於每分鐘的額度：一開始可以連取 3 次。
	for i := range 3 {
		if ok, _ := l.Take("youtube", 3); !ok {
			t.Fatalf("第 %d 次應取得額度", i+1)
		}
	}
	ok, at := l.Take("youtube", 3)
	if ok || !at.Equal(t0.Add(20*time.Second)) {
		t.Fatalf("額度用完：ok=%v retryAt=%v", ok, at)
	}
	// 不同的 key 各自計算。
	if ok, _ := l.Take("feed:a.example.com", 3); !ok {
		t.Fatal("其他 key 不受影響")
	}
	clk.Advance(19 * time.Second)
	if ok, _ := l.Take("youtube", 3); ok {
		t.Fatal("差 1 秒還沒有額度")
	}
	clk.Advance(time.Second)
	if ok, _ := l.Take("youtube", 3); !ok {
		t.Fatal("20 秒後補到一顆")
	}
	// 閒置很久也不超過容量。
	clk.Advance(time.Hour)
	for i := range 3 {
		if ok, _ := l.Take("youtube", 3); !ok {
			t.Fatalf("閒置後第 %d 次應取得額度", i+1)
		}
	}
	if ok, _ := l.Take("youtube", 3); ok {
		t.Fatal("閒置後也只補到容量")
	}

	// 429：暫停 15 分鐘，恢復時只有一顆。
	resume := l.Pause("bilibili", 2)
	if !resume.Equal(clk.Now().Add(throttlePause)) {
		t.Fatalf("恢復時間 = %v", resume)
	}
	if ok, at := l.Take("bilibili", 2); ok || !at.Equal(resume) {
		t.Fatalf("暫停中：ok=%v at=%v", ok, at)
	}
	clk.Advance(throttlePause)
	if ok, _ := l.Take("bilibili", 2); !ok {
		t.Fatal("恢復時應有一顆")
	}
	if ok, at := l.Take("bilibili", 2); ok || !at.Equal(resume.Add(30*time.Second)) {
		t.Fatalf("恢復後第二顆：ok=%v at=%v", ok, at)
	}

	// 額度設定改小時，現有額度不超過新容量；0 以下當作 1。
	l2 := newLimiter(clk)
	l2.Take("k", 10)
	if ok, _ := l2.Take("k", 0); !ok {
		t.Fatal("perMinute 0 視為 1，仍有一顆")
	}
	if ok, _ := l2.Take("k", 1); ok {
		t.Fatal("容量 1 已用完")
	}
	// 時鐘倒退不補額度、也不 panic。
	clk.Set(t0)
	if ok, _ := l2.Take("k", 1); ok {
		t.Fatal("時鐘倒退不應補額度")
	}
}

type fakeRunner struct {
	res  toolexec.Result
	err  error
	got  toolexec.Cmd
	call int
}

func (f *fakeRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.got, f.call = c, f.call+1
	return f.res, f.err
}

func fakeYtDlp(t *testing.T) toolexec.Resolver {
	t.Helper()
	p := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return toolexec.Resolver{Config: map[string]string{"yt-dlp": p}}
}

func TestListerRun(t *testing.T) {
	tools := fakeYtDlp(t)
	r := &fakeRunner{res: toolexec.Result{Stdout: []byte(`{"title":"L","entries":[{"id":"abcdefghijk","title":"t","duration":61.5}]}`)}}
	l := &lister{Runner: r, Tools: tools}
	fl, err := l.run(t.Context(), extractorsYouTube, 15, "https://www.youtube.com/playlist?list=PL1")
	if err != nil || fl.Title != "L" || len(fl.Entries) != 1 || *fl.Entries[0].Duration != 61.5 {
		t.Fatalf("run = %+v, %v", fl, err)
	}
	args := r.got.Args
	want := []string{"--ignore-config", "--use-extractors", extractorsYouTube, "--no-warnings", "--flat-playlist", "-J", "--playlist-end", "15", "--", "https://www.youtube.com/playlist?list=PL1"}
	if !slices.Equal(args, want) {
		t.Errorf("參數 = %q", args)
	}
	// 網址一定在 -- 之後，避免被當成選項。
	if _, err := l.run(t.Context(), extractorsYouTube, 0, "--exec=rm"); err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(r.got.Args, "--"); i < 0 || r.got.Args[i+1] != "--exec=rm" || r.got.Args[i-1] != "1" {
		t.Errorf("參數 = %q", r.got.Args)
	}

	cases := []struct {
		name      string
		res       toolexec.Result
		err       error
		class     errclass.Class
		msg       string
		throttled bool
	}{
		{"429", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] x: HTTP Error 429: Too Many Requests")}, nil, errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 429），稍後會自動重試。", true},
		{"412", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [BiliBili] x: HTTP Error 412: Precondition Failed")}, nil, errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 412），稍後會自動重試。", true},
		{"403", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] x: HTTP Error 403: Forbidden")}, nil, errclass.ClassNeedsAction, msgForbidden, false},
		{"gone", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] PLx: The playlist does not exist.")}, nil, errclass.ClassPermanent, msgListGone, false},
		{"404", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: unable to download: HTTP Error 404: Not Found")}, nil, errclass.ClassPermanent, msgListGone, false},
		{"other", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: something <rss>secret</rss>")}, nil, errclass.ClassRetryable, msgRetryYtDlp, false},
		{"429 不在行首", toolexec.Result{ExitCode: 1, Stderr: []byte("WARNING: title HTTP Error 429 in the name\nERROR: other")}, nil, errclass.ClassRetryable, msgRetryYtDlp, false},
		{"bad json", toolexec.Result{Stdout: []byte(`{"entries":`)}, nil, errclass.ClassRetryable, msgRetryYtDlp, false},
		{"huge", toolexec.Result{Stdout: []byte(strings.Repeat(" ", int(maxToolOutput)+1))}, nil, errclass.ClassPermanent, "回應超過大小上限（10 MiB）。", false},
		{"runner", toolexec.Result{}, errors.New("boom"), errclass.ClassRetryable, msgRetryYtDlp, false},
	}
	for _, c := range cases {
		r := &fakeRunner{res: c.res, err: c.err}
		_, err := (&lister{Runner: r, Tools: tools}).run(t.Context(), extractorsBilibili, 10, "https://space.bilibili.com/1/channel/collectiondetail?sid=2")
		ce := asCheckError(err)
		if ce.Class != c.class || ce.Message != c.msg || ce.throttled != c.throttled {
			t.Errorf("%s：%+v", c.name, ce)
		}
		if strings.Contains(ce.Message, "secret") {
			t.Errorf("%s：訊息帶了 yt-dlp 的輸出", c.name)
		}
	}

	// 找不到 yt-dlp 是 needs_action，不執行任何東西。
	r = &fakeRunner{}
	_, err = (&lister{Runner: r, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}).run(t.Context(), extractorsYouTube, 1, "x")
	if ce := asCheckError(err); ce.Class != errclass.ClassNeedsAction || r.call != 0 {
		t.Errorf("找不到 yt-dlp：%+v，執行 %d 次", ce, r.call)
	}
	// context 取消時回傳 context 的錯誤（排程器據此判斷是關機）。
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = (&lister{Runner: &fakeRunner{err: errors.New("killed")}, Tools: tools}).run(ctx, extractorsYouTube, 1, "x")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("取消：%v", err)
	}
}

func TestToolURLs(t *testing.T) {
	if got := ChannelVideosURL("UCx"); got != "https://www.youtube.com/channel/UCx/videos" {
		t.Error(got)
	}
	if got := LegacyCollectionURL("9", "7&x=1"); got != "https://space.bilibili.com/9/channel/collectiondetail?sid=7%26x%3D1" {
		t.Error(got)
	}
}

// FuzzFlatCandidates：yt-dlp 的 -J 輸出是不可信的輸入（SUB-R11）。不論內容為何都不 panic、條目數有上限、
// 標題已清理、ID 合法且不重複、新增的網址一定在平台的主機上。
func FuzzFlatCandidates(f *testing.F) {
	for _, name := range []string{"sub-playlist-2-videos.json", "sub-bili-collection.json", "playlist-3-videos.json"} {
		if b, err := os.ReadFile(filepath.Join("..", "..", "features", "fixtures", "ytdlp", name)); err == nil {
			f.Add(b, false)
			f.Add(b, true)
		}
	}
	f.Add([]byte(`{"entries":[{"id":"abcdefghijk","url":"javascript:x","title":"<script>x</script>t","duration":-1},null,{"id":"abcdefghijk"}]}`), false)
	f.Add([]byte(`{"entries":[{"id":"BV1xx411c7mD","title":"‮標題","duration":1e300,"timestamp":-5}]}`), true)
	f.Fuzz(func(t *testing.T, raw []byte, bili bool) {
		var fl flatList
		if json.Unmarshal(raw, &fl) != nil {
			return
		}
		platform, host := PlatformYouTube, "https://www.youtube.com/watch?v="
		if bili {
			platform, host = PlatformBilibili, "https://www.bilibili.com/video/"
		}
		out := flatCandidates(&fl, platform)
		if len(out) > MaxEntries {
			t.Fatalf("條目 %d 筆", len(out))
		}
		seen := map[string]bool{}
		for i, c := range out {
			if c.Position != i || c.Platform != platform || seen[c.ExternalID] {
				t.Fatalf("條目 %d：%+v", i, c)
			}
			seen[c.ExternalID] = true
			if c.IngestURL != host+c.ExternalID || (bili && c.Part != 1) {
				t.Fatalf("新增網址 %q", c.IngestURL)
			}
			if (bili && !bvidRe.MatchString(c.ExternalID)) || (!bili && !videoIDRe.MatchString(c.ExternalID)) {
				t.Fatalf("ID 不合法：%q", c.ExternalID)
			}
			if c.Title != CleanText(c.Title) && utf8.RuneCountInString(c.Title) <= maxTitleRunes {
				t.Fatalf("標題沒有清理：%q", c.Title)
			}
			if utf8.RuneCountInString(c.Title) > maxTitleRunes+1 {
				t.Fatalf("標題過長")
			}
			if c.DurationS != nil && (*c.DurationS < 0 || *c.DurationS > 1e7) {
				t.Fatalf("片長 %d", *c.DurationS)
			}
		}
	})
}
