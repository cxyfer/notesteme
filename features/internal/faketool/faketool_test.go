package faketool

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	Dispatch() // 以假工具的名稱被執行時，在這裡結束
	os.Exit(m.Run())
}

// scenario 建立一個場景根目錄：bin/ 放 symlink，fake/ 放狀態，work/ 當工作目錄。
type scenario struct {
	t    *testing.T
	root string
	bin  string
	work string
}

func newScenario(t *testing.T, st State) *scenario {
	t.Helper()
	root := t.TempDir()
	s := &scenario{t: t, root: root, bin: filepath.Join(root, "bin"), work: filepath.Join(root, "work")}
	if err := os.MkdirAll(s.work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(s.bin); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(root, st); err != nil {
		t.Fatal(err)
	}
	return s
}

// exec 透過 symlink 以子程序執行假工具；argv[0] 只有工具名稱，PATH 指向 bin/，
// 模擬 production 以 PATH 查找工具的情況。
func (s *scenario) exec(tool string, args ...string) (code int, stdout, stderr string) {
	s.t.Helper()
	var out, errb bytes.Buffer
	cmd := &exec.Cmd{
		Path:   filepath.Join(s.bin, tool),
		Args:   append([]string{tool}, args...),
		Dir:    s.work,
		Env:    []string{"PATH=" + s.bin},
		Stdout: &out,
		Stderr: &errb,
	}
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		s.t.Fatalf("exec %s: %v", tool, err)
	}
	return code, out.String(), errb.String()
}

// inproc 在程序內執行 run，用來涵蓋較多的分支。
func (s *scenario) inproc(tool string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(env{
		tool: tool, root: s.root, cwd: s.work,
		argv: append([]string{tool}, args...), stdout: &out, stderr: &errb,
	})
	return code, out.String(), errb.String()
}

func sampleState(t *testing.T) State {
	t.Helper()
	fx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fx, "captions"), 0o755); err != nil {
		t.Fatal(err)
	}
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nfixture line\n\n"
	if err := os.WriteFile(filepath.Join(fx, "captions", "en-manual.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	return State{
		FixturesDir: fx,
		Videos: map[string]*Video{
			"abc123": {
				Title: "PostgreSQL in 100 Seconds", Duration: 125, Language: "en",
				Chapters: []Chapter{{Title: "Intro", Start: 0, End: 30}},
				Tracks: []Track{
					{Kind: KindManual, Lang: "en", Fixture: "en-manual.vtt"},
					{Kind: KindAuto, Lang: "en"},
					{Kind: KindPlatformTranslated, Lang: "zh-Hans", Cues: 3},
				},
			},
			"BV1xx411c7mD": {Title: "分P", Duration: 60, Parts: 2, Tracks: []Track{{Kind: KindAuto, Lang: "zh-Hans"}}},
			"late1":        {Title: "Late", Language: "en", AutoNotReady: true, Tracks: []Track{{Kind: KindAuto, Lang: "en"}}},
			"broken": {Title: "Broken", Fail: &Failure{
				Stderr: "ERROR: unable to download video data: HTTP Error 403: Forbidden", ExitCode: 1, On: []string{OpInfo}}},
			"failmedia": {Title: "FM", Duration: 5, Fail: &Failure{Stderr: "ERROR: media 500", ExitCode: 7, On: []string{OpMedia}}},
		},
	}
}

func TestVersionViaSymlink(t *testing.T) {
	s := newScenario(t, State{})
	code, out, _ := s.exec("yt-dlp", "--version")
	if code != 0 || strings.TrimSpace(out) != "2026.11.01" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if err := WriteState(s.root, State{YtDlpVersion: "2027.01.02"}); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := s.exec("yt-dlp", "--version"); strings.TrimSpace(out) != "2027.01.02" {
		t.Fatalf("out=%q", out)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if code, out, _ := s.exec(tool, "-version"); code != 0 || !strings.Contains(out, tool+" version") {
			t.Fatalf("%s: code=%d out=%q", tool, code, out)
		}
	}
}

func TestInfoJSONViaSymlink(t *testing.T) {
	s := newScenario(t, sampleState(t))
	code, out, errs := s.exec("yt-dlp", "-J", "--no-playlist", "--use-extractors", "youtube,bilibili",
		"--cookies", "cookies.txt", "https://www.youtube.com/watch?v=abc123")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	var info struct {
		ID, Title, Language, Availability string
		Duration                          int
		LiveStatus                        string                      `json:"live_status"`
		Chapters                          []map[string]any            `json:"chapters"`
		Subtitles                         map[string][]map[string]any `json:"subtitles"`
		Auto                              map[string][]map[string]any `json:"automatic_captions"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if info.ID != "abc123" || info.Title != "PostgreSQL in 100 Seconds" || info.Duration != 125 ||
		info.Language != "en" || info.Availability != "public" || info.LiveStatus != "not_live" {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Chapters) != 1 || info.Chapters[0]["title"] != "Intro" {
		t.Fatalf("chapters = %v", info.Chapters)
	}
	if len(info.Subtitles["en"]) != 1 || len(info.Auto["en"]) != 1 || len(info.Auto["zh-Hans"]) != 1 {
		t.Fatalf("subs=%v auto=%v", info.Subtitles, info.Auto)
	}
}

func TestSubtitlesViaSymlink(t *testing.T) {
	s := newScenario(t, sampleState(t))
	// 人工字幕來自 fixture。
	code, out, errs := s.exec("yt-dlp", "--skip-download", "--write-subs", "--sub-langs", "en",
		"--sub-format", "vtt", "-o", "subs/%(id)s.%(ext)s", "https://youtu.be/abc123")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if !strings.Contains(out, "subs/abc123.en.vtt") {
		t.Fatalf("stdout = %q", out)
	}
	b, err := os.ReadFile(filepath.Join(s.work, "subs", "abc123.en.vtt"))
	if err != nil || !strings.Contains(string(b), "fixture line") {
		t.Fatalf("content=%q err=%v", b, err)
	}

	// 自動字幕：依預設產生 120 句。
	code, _, errs = s.exec("yt-dlp", "--skip-download", "--write-auto-subs", "--sub-langs", "en",
		"-o", "%(id)s-auto.%(ext)s", "https://www.youtube.com/watch?v=abc123")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	b, err = os.ReadFile(filepath.Join(s.work, "abc123-auto.en.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), " --> "); n != DefaultCues {
		t.Fatalf("cues = %d, want %d", n, DefaultCues)
	}
}

func TestUnknownArgumentExits2AndIsLogged(t *testing.T) {
	s := newScenario(t, sampleState(t))
	code, _, errs := s.exec("yt-dlp", "--frobnicate", "https://www.youtube.com/watch?v=abc123")
	if code != 2 || !strings.Contains(errs, "--frobnicate") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if code, _, _ := s.exec("ffmpeg", "-i", "x", "y"); code != 2 {
		t.Fatalf("ffmpeg code=%d", code)
	}
	if code, _, _ := s.exec("ffprobe", "--bogus"); code != 2 {
		t.Fatalf("ffprobe code=%d", code)
	}

	calls, err := ReadCalls(s.root)
	if err != nil || len(calls) != 3 {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	if calls[0].Tool != "yt-dlp" || calls[0].Argv[0] != "yt-dlp" || calls[0].Argv[1] != "--frobnicate" {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	wantCwd, _ := filepath.EvalSymlinks(s.work)
	gotCwd, _ := filepath.EvalSymlinks(calls[0].Cwd)
	if gotCwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", calls[0].Cwd, s.work)
	}
}

func TestCookiesModeLogged(t *testing.T) {
	s := newScenario(t, sampleState(t))
	ck := filepath.Join(s.work, "cookies.txt")
	if err := os.WriteFile(ck, []byte("# Netscape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--cookies", ck, "-J", "https://www.youtube.com/watch?v=abc123"},
		{"--cookies=missing.txt", "-J", "https://www.youtube.com/watch?v=abc123"},
	} {
		if code, _, errs := s.exec("yt-dlp", args...); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
	}
	calls, _ := ReadCalls(s.root)
	if len(calls) != 2 || calls[0].CookiesMode != "0600" || calls[0].CookiesFile != ck {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[1].CookiesMode != "missing" {
		t.Fatalf("calls[1] = %+v", calls[1])
	}
}

func TestMediaAndFFprobeRoundTrip(t *testing.T) {
	s := newScenario(t, sampleState(t))
	code, _, errs := s.exec("yt-dlp", "-f", "bestaudio", "-o", "media/%(id)s.%(ext)s",
		"--newline", "--no-progress", "--quiet", "https://www.youtube.com/watch?v=abc123")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	p := filepath.Join(s.work, "media", "abc123.m4a")
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "NOTESTEME-FAKE-MEDIA item=abc123 duration=125\n" {
		t.Fatalf("media=%q err=%v", b, err)
	}
	item, dur, ok := ParseMediaMarker(b)
	if !ok || item != "abc123" || dur != 125 {
		t.Fatalf("marker = %q %d %v", item, dur, ok)
	}

	for _, flag := range []string{"-of", "-print_format"} {
		code, out, errs := s.exec("ffprobe", "-v", "error", flag, "json", "-show_format", "-show_streams", p)
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		var res struct {
			Format  struct{ Duration string }
			Streams []map[string]any
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil || res.Format.Duration != "125.000000" || len(res.Streams) != 1 {
			t.Fatalf("out=%s err=%v", out, err)
		}
	}
	// 不是假媒體檔時失敗。
	bad := filepath.Join(s.work, "bad.bin")
	_ = os.WriteFile(bad, []byte("garbage"), 0o644)
	if code, _, _ := s.exec("ffprobe", "-of", "json", bad); code != 1 {
		t.Fatalf("garbage code=%d", code)
	}
}

func TestGateBlocksUntilRemoved(t *testing.T) {
	st := sampleState(t)
	s := newScenario(t, st)
	gate := filepath.Join(s.root, "gate")
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st.Gate = gate
	if err := WriteState(s.root, st); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{
		Path: filepath.Join(s.bin, "yt-dlp"),
		Args: []string{"yt-dlp", "-J", "https://www.youtube.com/watch?v=abc123"},
		Dir:  s.work, Env: []string{"PATH=" + s.bin},
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// 等子程序寫入「已進入 gate」的標記（不是睡一段固定時間）；子程序提早結束就是失敗。
	waiting := gate + GateWaitingSuffix
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(waiting); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("子程序在進入 gate 前結束: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("子程序沒有進入 gate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// 已進入 gate 且 gate 還在：程序必須仍在執行。
	select {
	case err := <-done:
		t.Fatalf("gate 存在時不應結束: %v", err)
	default:
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("gate 移除後應結束")
	}
}

func TestInProcessBranches(t *testing.T) {
	s := newScenario(t, sampleState(t))
	yt := "https://www.youtube.com/watch?v="

	t.Run("failure applies only to configured op", func(t *testing.T) {
		code, _, errs := s.inproc("yt-dlp", "-J", yt+"broken")
		if code != 1 || !strings.Contains(errs, "HTTP Error 403") {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		code, _, errs = s.inproc("yt-dlp", "-f", "best", "-o", "%(id)s.%(ext)s", yt+"failmedia")
		if code != 7 || !strings.Contains(errs, "media 500") {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		if code, _, _ := s.inproc("yt-dlp", "-J", yt+"failmedia"); code != 0 {
			t.Fatalf("info 不應失敗: %d", code)
		}
	})

	t.Run("unknown video and urls", func(t *testing.T) {
		if code, _, errs := s.inproc("yt-dlp", "-J", yt+"nope"); code != 1 || !strings.Contains(errs, "Video unavailable") {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		for _, u := range []string{"https://example.com/x", "https://www.youtube.com/", "not a url"} {
			if code, _, _ := s.inproc("yt-dlp", "-J", u); code != 1 {
				t.Fatalf("%s: code=%d", u, code)
			}
		}
	})

	t.Run("bilibili parts", func(t *testing.T) {
		code, out, errs := s.inproc("yt-dlp", "-J", "https://www.bilibili.com/video/BV1xx411c7mD?p=2")
		if code != 0 || !strings.Contains(out, `"playlist_index":2`) {
			t.Fatalf("code=%d out=%q stderr=%q", code, out, errs)
		}
		if code, _, _ := s.inproc("yt-dlp", "-J", "https://www.bilibili.com/video/BV1xx411c7mD?p=3"); code != 1 {
			t.Fatalf("p=3 code=%d", code)
		}
		if code, _, _ := s.inproc("yt-dlp", "-J", "https://www.bilibili.com/video/BV1xx411c7mD?p=x"); code != 1 {
			t.Fatalf("p=x code=%d", code)
		}
		code, _, _ = s.inproc("yt-dlp", "-f", "bestaudio", "-o", "%(id)s.%(ext)s", "https://www.bilibili.com/video/BV1xx411c7mD?p=2")
		if code != 0 {
			t.Fatalf("download code=%d", code)
		}
		b, _ := os.ReadFile(filepath.Join(s.work, "BV1xx411c7mD.m4a"))
		if !strings.Contains(string(b), "item=BV1xx411c7mD/p2") {
			t.Fatalf("marker = %q", b)
		}
	})

	t.Run("auto captions not ready are hidden", func(t *testing.T) {
		_, out, _ := s.inproc("yt-dlp", "-J", yt+"late1")
		if strings.Contains(out, `"en":[`) {
			t.Fatalf("自動字幕尚未生成時不應列出: %s", out)
		}
		code, _, errs := s.inproc("yt-dlp", "--skip-download", "--write-auto-subs", "--sub-langs", "en", "-o", "late.%(ext)s", yt+"late1")
		if code != 0 || !strings.Contains(errs, "no subtitles") {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		if _, err := os.Stat(filepath.Join(s.work, "late.en.vtt")); err == nil {
			t.Fatal("不應寫出字幕")
		}
	})

	t.Run("translated track and all", func(t *testing.T) {
		code, _, errs := s.inproc("yt-dlp", "--skip-download", "--write-subs", "--write-auto-subs", "--sub-langs", "all",
			"-o", "subtitle:all-%(id)s.%(ext)s", yt+"abc123")
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		b, err := os.ReadFile(filepath.Join(s.work, "all-abc123.zh-Hans.vtt"))
		if err != nil || strings.Count(string(b), " --> ") != 3 {
			t.Fatalf("zh-Hans = %q err=%v", b, err)
		}
		// 同一語言有人工字幕時優先使用人工字幕。
		b, _ = os.ReadFile(filepath.Join(s.work, "all-abc123.en.vtt"))
		if !strings.Contains(string(b), "fixture line") {
			t.Fatalf("en = %q", b)
		}
	})

	t.Run("invalid usage", func(t *testing.T) {
		cases := [][]string{
			{"-J"},
			{"--sub-langs"},
			{"-o"},
			{"-J", yt + "abc123", yt + "abc123"},
			{"--skip-download", "--write-subs", "--sub-langs", "en", yt + "abc123"},
			{"--skip-download", "--write-subs", "--sub-format", "srt", "-o", "x", yt + "abc123"},
			{yt + "abc123"},
		}
		for _, args := range cases {
			if code, _, errs := s.inproc("yt-dlp", args...); code != 2 || errs == "" {
				t.Fatalf("%v: code=%d stderr=%q", args, code, errs)
			}
		}
		if code, _, _ := s.inproc("ffprobe", "-of", "xml", "x"); code != 2 {
			t.Fatalf("ffprobe xml code=%d", code)
		}
		if code, _, _ := s.inproc("ffprobe", "-v"); code != 2 {
			t.Fatalf("ffprobe -v code=%d", code)
		}
		if code, _, _ := s.inproc("ffprobe", "-of", "json"); code != 2 {
			t.Fatalf("ffprobe no input code=%d", code)
		}
		if code, _, _ := s.inproc("ffprobe", "-of", "json", "missing.bin"); code != 1 {
			t.Fatalf("ffprobe missing code=%d", code)
		}
		if code, _, _ := s.inproc("nope"); code != 2 {
			t.Fatalf("unknown tool code=%d", code)
		}
	})
}

func TestGenerateVTTAndTemplate(t *testing.T) {
	v := GenerateVTT("abc123", "en", 2)
	if !strings.HasPrefix(v, "WEBVTT") || !strings.Contains(v, "00:00:02.000 --> 00:00:04.000\nSentence 2 of abc123 (en).") {
		t.Fatalf("vtt = %q", v)
	}
	got := expandTemplate("a/%(id)s-%(title)s-%(nope)s-100%%.%(ext)s", map[string]string{"id": "i", "title": "x/y", "ext": "mp4"})
	if got != "a/i-x_y-NA-100%.mp4" {
		t.Fatalf("template = %q", got)
	}
}

func TestStateRoundTripAndMissing(t *testing.T) {
	root := t.TempDir()
	st, err := ReadState(root)
	if err != nil || st.Videos != nil {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	if calls, err := ReadCalls(root); err != nil || calls != nil {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	want := State{Gate: "/x", Videos: map[string]*Video{"a": {Title: "t", Tracks: []Track{{Kind: KindManual, Lang: "en", Cues: 5}}}}}
	if err := WriteState(root, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(root)
	if err != nil || got.Gate != "/x" || got.Videos["a"].Tracks[0].Cues != 5 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := os.WriteFile(filepath.Join(FakeDir(root), "state.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(root); err == nil {
		t.Fatal("壞掉的狀態檔應回傳錯誤")
	}
	for i := 0; i < 3; i++ {
		if err := AppendCall(root, Call{Tool: "yt-dlp", Argv: []string{"yt-dlp"}}); err != nil {
			t.Fatal(err)
		}
	}
	if calls, err := ReadCalls(root); err != nil || len(calls) != 3 {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}

func TestToolFromArg0(t *testing.T) {
	for arg0, want := range map[string]string{
		"yt-dlp": "yt-dlp", "/x/bin/ffmpeg": "ffmpeg", "./ffprobe": "ffprobe",
		"faketool.test": "", "yt-dlp2": "", "": "",
	} {
		if got := toolFromArg0(arg0); got != want {
			t.Errorf("toolFromArg0(%q) = %q, want %q", arg0, got, want)
		}
	}
}

func TestInstallReplacesExisting(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	for i := 0; i < 2; i++ {
		if err := Install(bin); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range toolNames {
		if fi, err := os.Lstat(filepath.Join(bin, n)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s: %v %v", n, fi, err)
		}
	}
}

func TestRefusesOutsideScenario(t *testing.T) {
	// 只有 bin/ 的 symlink，沒有 fake/state.json：假工具必須以 2 結束，且不建立任何東西。
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := Install(bin); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"yt-dlp", "ffmpeg", "ffprobe"} {
		var errb bytes.Buffer
		cmd := &exec.Cmd{
			Path: filepath.Join(bin, tool), Args: []string{tool, "--version"},
			Dir: root, Env: []string{"PATH=" + bin}, Stderr: &errb,
		}
		var ee *exec.ExitError
		if err := cmd.Run(); !errors.As(err, &ee) || ee.ExitCode() != 2 {
			t.Fatalf("%s: err=%v，應以結束碼 2 結束", tool, err)
		}
		if !strings.Contains(errb.String(), "state.json") {
			t.Errorf("%s: stderr 應說明找不到狀態檔: %q", tool, errb.String())
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatalf("不應建立任何檔案或目錄: %v", entries)
	}
}

func TestAppendCallDoesNotCreateDir(t *testing.T) {
	root := t.TempDir()
	if err := AppendCall(root, Call{Tool: "yt-dlp"}); err == nil {
		t.Fatal("fake/ 不存在時 AppendCall 應失敗")
	}
	if _, err := os.Stat(FakeDir(root)); err == nil {
		t.Fatal("AppendCall 不應建立 fake/")
	}
}

func TestYtDlpDoubleDashIsPositional(t *testing.T) {
	s := newScenario(t, sampleState(t))
	// "--" 之後的參數即使長得像旗標，也只當位置參數（網址），不會被當成未知選項。
	code, _, errs := s.inproc("yt-dlp", "-J", "--", "https://www.youtube.com/watch?v=abc123")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	code, _, errs = s.inproc("yt-dlp", "-J", "--", "--no-such-flag")
	if code != 1 || strings.Contains(errs, "unrecognized option") {
		t.Fatalf("應視為位置參數（網址不合法，結束碼 1）：code=%d stderr=%q", code, errs)
	}
	code, _, errs = s.inproc("yt-dlp", "-J", "--", "https://www.youtube.com/watch?v=abc123", "extra")
	if code != 2 || !strings.Contains(errs, "extra argument") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	// "--" 之前的未知旗標仍然失敗。
	if code, _, _ := s.inproc("yt-dlp", "-J", "--bogus", "--", "https://www.youtube.com/watch?v=abc123"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
