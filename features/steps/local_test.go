package steps

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

// local: 代號在請求路徑中：整段是已知影音就換成 ID；否則從右邊依 / 縮短，找最長的已知前綴。
func TestResolveLocalRef(t *testing.T) {
	known := map[string]string{
		"local:lecture.mp4":      "ID-LECTURE",
		"local:sub/a.mp4":        "ID-SUB",
		"local:a":                "ID-A",
		"local:a/b":              "ID-AB",
		"local:/abs/dir/x.mp4":   "ID-ABS",
		"local:talk.m4a":         "ID-TALK",
		"local:with space/x.mp4": "ID-SPACE",
	}
	lookup := func(ref string) (string, error) {
		if id, ok := known[ref]; ok {
			return id, nil
		}
		return "", errors.New("不在資料庫中")
	}
	for in, want := range map[string]string{
		"local:lecture.mp4":                "ID-LECTURE",
		"local:lecture.mp4/media":          "ID-LECTURE/media",
		"local:talk.m4a/media":             "ID-TALK/media",
		"local:sub/a.mp4/media":            "ID-SUB/media",
		"local:sub/a.mp4":                  "ID-SUB",
		"local:/abs/dir/x.mp4/cues":        "ID-ABS/cues",
		"local:a/b/media":                  "ID-AB/media", // 最長的已知前綴
		"local:a/c/media":                  "ID-A/c/media",
		"local:lecture.mp4/restore/deeper": "ID-LECTURE/restore/deeper",
		"local:unknown.mp4":                "local:unknown.mp4", // 找不到：原樣
		"local:unknown.mp4/media":          "local:unknown.mp4/media",
		"local:/etc/passwd":                "local:/etc/passwd",
		"local:":                           "local:",
	} {
		if got := resolveLocalRef(in, lookup); got != want {
			t.Errorf("resolveLocalRef(%q) = %q，預期 %q", in, got, want)
		}
	}
	// refPattern 會把路徑後面的段落一起吃進 local: 代號，才需要上面的縮短。
	if got := refPattern.FindString("/api/items/local:lecture.mp4/media"); got != "local:lecture.mp4/media" {
		t.Errorf("refPattern = %q", got)
	}
}

func TestIngestBody(t *testing.T) {
	s := newTestScenario(t)
	inbox := s.World.InboxDir()
	if got := s.ingestBody("local:talk.mp4"); got["path"] != filepath.Join(inbox, "talk.mp4") || got["url"] != nil {
		t.Errorf("inbox 的檔案 = %v", got)
	}
	if got := s.ingestBody("local:sub/talk.mp4"); got["path"] != filepath.Join(inbox, "sub", "talk.mp4") {
		t.Errorf("inbox 子目錄 = %v", got)
	}
	// 絕對路徑：虛擬路徑換成實際路徑。
	if got := s.ingestBody("local:/media/talks/keynote.mp4"); got["path"] != filepath.Join(s.World.MediaDir(), "talks", "keynote.mp4") {
		t.Errorf("media_roots 的檔案 = %v", got)
	}
	if got := s.ingestBody("local:/etc/passwd"); got["path"] != "/etc/passwd" {
		t.Errorf("其他絕對路徑原樣 = %v", got)
	}
	// 其他代號與網址送 url。
	if got := s.ingestBody("yt:abc123xxxxx"); got["url"] != "https://www.youtube.com/watch?v=abc123xxxxx" || got["path"] != nil {
		t.Errorf("yt = %v", got)
	}
	if got := s.ingestBody("https://example.com/x"); got["url"] != "https://example.com/x" {
		t.Errorf("網址 = %v", got)
	}
}

func TestLocalFileInInbox(t *testing.T) {
	s := newTestScenario(t)
	if err := s.localFileInInbox("talk.mp4"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(s.World.InboxDir(), "talk.mp4")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(s.World.FixturesDir, "local", "talk.mp4"))
	if !bytes.Equal(got, want) {
		t.Error("內容和 fixture 不同")
	}
	// mtime 是假時鐘的現在，不是真實時間。
	fi, _ := os.Stat(dst)
	if !fi.ModTime().Equal(harness.DefaultNow) {
		t.Errorf("mtime = %v，預期假時鐘 %v", fi.ModTime(), harness.DefaultNow)
	}
	// 時鐘推進之後放的檔案，mtime 用當時的假時間。
	s.World.Clock.Advance(90e9)
	if err := s.localFileInInbox("embedded/lecture-en.srt"); err != nil {
		t.Fatal(err)
	}
	fi, err = os.Stat(filepath.Join(s.World.InboxDir(), "embedded", "lecture-en.srt"))
	if err != nil || !fi.ModTime().Equal(harness.DefaultNow.Add(90e9)) {
		t.Errorf("子目錄的檔案：%v，mtime=%v", err, fi)
	}
	// fixture 不存在：失敗，不是 pending；不能跳出 fixtures/local。
	for _, bad := range []string{"nope.mp4", "../sources/groq-en.json"} {
		err := s.localFileInInbox(bad)
		if err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("%q 應該失敗：%v", bad, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.World.InboxDir(), "sources", "groq-en.json")); err == nil {
		t.Error("不該把 sources/ 的 fixture 放進 inbox")
	}
}

// 兩個內容相同的 fixture：talk.mp4 與 talk-copy.mp4 完全相同（ING-R10 的去重）。
func TestLocalFixturesShape(t *testing.T) {
	fx := harness.FixturesDir()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(fx, "local", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Equal(read("talk.mp4"), read("talk-copy.mp4")) {
		t.Error("talk.mp4 與 talk-copy.mp4 要完全相同")
	}
	if len(read("lecture.mp4")) < 4096 {
		t.Error("lecture.mp4 要 4 KiB 以上（Range 0-1023 要有內容）")
	}
	if _, ok := faketool.ParseMedia(read("notes.pdf")); ok {
		t.Error("notes.pdf 不能是標記")
	}
	cases := map[string]struct {
		video, audio, subtitle bool
	}{
		"talk.mp4": {true, true, false}, "lecture.mp4": {true, true, true}, "meetup.mp4": {true, true, false},
		"long-talk.m4a": {false, true, false}, "movie-with-subs.mkv": {true, true, true},
		"lecture.m4a": {false, true, false}, "talk.m4a": {false, true, false},
	}
	for name, want := range cases {
		m, ok := faketool.ParseMedia(read(name))
		if !ok || m.Item != "local:"+name {
			t.Errorf("%s：%+v ok=%v", name, m, ok)
			continue
		}
		var v, a bool
		for _, st := range m.EffectiveStreams() {
			v = v || st.Type == faketool.StreamVideo
			a = a || st.Type == faketool.StreamAudio
		}
		if v != want.video || a != want.audio || m.HasTextSubtitle() != want.subtitle {
			t.Errorf("%s：video=%v audio=%v subtitle=%v", name, v, a, m.HasTextSubtitle())
		}
		for _, st := range m.Streams {
			if st.Fixture != "" {
				if _, err := os.Stat(filepath.Join(fx, "local", st.Fixture)); err != nil {
					t.Errorf("%s 的內嵌字幕 fixture：%v", name, err)
				}
			}
		}
	}
	// media/ 的 fixture：沒有 item，帶 compressed 與 silences。
	raw, err := os.ReadFile(filepath.Join(fx, "media", "audio-40mb-after-compression.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := faketool.ParseMedia(raw)
	if !ok || m.Item != "" || m.Duration != 3004 || m.Compressed != 40<<20 || len(m.Silences) != 1 {
		t.Errorf("audio-40mb-after-compression.m4a = %+v ok=%v", m, ok)
	}
}

// fixture 與假工具寫出的檔案不含測試用的 secret（After hook 掃描 /data 與 log 時不會因為 inbox、uploads 的檔案誤判）。
func TestFixturesContainNoSecrets(t *testing.T) {
	secrets := []string{harness.TestPassword, harness.TestMasterKey, harness.TestSupadataKey, harness.TestGroqKey}
	err := filepath.WalkDir(harness.FixturesDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, sec := range secrets {
			if bytes.Contains(b, []byte(sec)) {
				t.Errorf("%s 含有 secret %q", path, sec)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAudioOfIsLocal(t *testing.T) {
	s := newTestScenario(t)
	if err := s.localFileInInbox("long-talk.m4a"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.World.InboxDir(), "long-talk.m4a")
	before, _ := os.ReadFile(path)
	s.World.Clock.Advance(30e9)
	if err := s.audioOfIs("local:long-talk.m4a", "audio-40mb-after-compression.m4a"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	m, ok := faketool.ParseMedia(after)
	if !ok || m.Item != "local:long-talk.m4a" || m.Duration != 3004 || m.Compressed != 41943040 ||
		len(m.Silences) != 1 || m.Silences[0] != (faketool.MediaSilence{Start: 1501.8, End: 1502.2}) {
		t.Fatalf("併入屬性後 = %+v", m)
	}
	// fixture 沒有 streams，所以原檔的串流保留；標記之後的填充原樣保留。
	if len(m.Streams) != 1 || m.Streams[0].Type != faketool.StreamAudio {
		t.Errorf("串流 = %+v", m.Streams)
	}
	if len(after) != len(before)+len(faketool.FormatMedia(m))-len(strings.SplitAfterN(string(before), "\n", 2)[0]) {
		t.Errorf("填充長度改變：%d → %d", len(before), len(after))
	}
	// mtime 是假時鐘的現在。
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(harness.DefaultNow.Add(30e9)) {
		t.Errorf("mtime = %v", fi.ModTime())
	}
	// 失敗要明確：檔案不在 inbox、不是假媒體檔、fixture 不存在，都不是 pending。
	for what, err := range map[string]error{
		"檔案不在 inbox":  s.audioOfIs("local:missing.m4a", "audio-40mb-after-compression.m4a"),
		"fixture 不存在": s.audioOfIs("local:long-talk.m4a", "nope.m4a"),
	} {
		if err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("%s：%v", what, err)
		}
	}
	if err := s.localFileInInbox("notes.pdf"); err != nil {
		t.Fatal(err)
	}
	wantFail(t, "不是假媒體檔", s.audioOfIs("local:notes.pdf", "audio-40mb-after-compression.m4a"), "不是假媒體檔")
}

func TestAudioOfIsYouTube(t *testing.T) {
	s := newTestScenario(t)
	if err := s.audioOfIs("yt:abc123xxxxx", "audio-40mb-after-compression.m4a"); err != nil {
		t.Fatal(err)
	}
	v := s.World.Tools.Videos["abc123xxxxx"]
	if v == nil || v.Duration != 3004 || v.Compressed != 41943040 || len(v.Silences) != 1 {
		t.Fatalf("video = %+v", v)
	}
	// 狀態檔已同步：假 yt-dlp 讀得到。
	st, err := faketool.ReadState(s.World.Root)
	if err != nil || st.Videos["abc123xxxxx"].Compressed != 41943040 {
		t.Fatalf("狀態檔 = %+v err=%v", st, err)
	}
	wantFail(t, "不是影音代號", s.audioOfIs("pod:show/ep-1", "audio-40mb-after-compression.m4a"), "不是影音的代號")
}

func TestMultipartFileAndUploadErrors(t *testing.T) {
	body, ct, err := multipartFile("file", "talk.m4a", []byte("NOTESTEME-FAKE-MEDIA item=x duration=1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type = %q", ct)
	}
	_, params, _ := strings.Cut(ct, "boundary=")
	mr := multipart.NewReader(bytes.NewReader(body), params)
	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(part)
	if part.FormName() != "file" || part.FileName() != "talk.m4a" || !strings.HasPrefix(string(b), "NOTESTEME-FAKE-MEDIA ") {
		t.Errorf("part = %q %q %q", part.FormName(), part.FileName(), b)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("應該只有一個 part：%v", err)
	}

	// 上傳的檔案不存在：失敗（不是 pending），而且不會送出任何請求。
	s := newTestScenario(t)
	wantFail(t, "fixture 不存在", s.ownerUploads("nope.m4a"), "nope.m4a")
	wantFail(t, "/tmp 的檔案不存在", s.ownerUploads("/tmp/missing.m4a"), "/tmp/missing.m4a")
	if s.last != nil || s.server != nil {
		t.Error("檔案不存在時不該啟動 server 或送出請求")
	}
}

func TestHasLocalTranscriptSource(t *testing.T) {
	s := newTestScenario(t)
	put := func(name string) {
		t.Helper()
		if err := s.localFileInInbox(name); err != nil {
			t.Fatal(err)
		}
	}
	inbox := s.World.InboxDir()

	if s.hasTranscriptSource("local:missing.mp4") {
		t.Error("檔案不存在")
	}
	// 標記有文字字幕串流：內嵌字幕。
	put("lecture.mp4")
	put("movie-with-subs.mkv")
	if !s.hasTranscriptSource("local:lecture.mp4") || !s.hasTranscriptSource("local:movie-with-subs.mkv") {
		t.Error("內嵌文字字幕")
	}
	// 沒有字幕串流、沒有 sidecar：沒有來源（要靠 ASR）。
	put("meetup.mp4")
	put("long-talk.m4a")
	if s.hasTranscriptSource("local:meetup.mp4") || s.hasTranscriptSource("local:long-talk.m4a") {
		t.Error("沒有字幕來源")
	}
	// sidecar：<stem>.<lang>.srt／.vtt 才算；沒有語言碼的 <stem>.srt 不採用；別的影音的 sidecar 不算。
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(inbox, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("meetup.srt")
	write("other.en.srt")
	write("meetup-notes.en.srt")
	if s.hasTranscriptSource("local:meetup.mp4") {
		t.Error("沒有語言碼的 .srt、別的影音的 sidecar 不算")
	}
	write("meetup.zh-Hant.vtt")
	if !s.hasTranscriptSource("local:meetup.mp4") {
		t.Error("meetup.zh-Hant.vtt 是 sidecar")
	}
	put("lecture.en.srt")
	put("talk.mp4")
	if s.hasTranscriptSource("local:talk.mp4") {
		t.Error("talk.mp4 沒有字幕來源")
	}
	// 腳本化的 ASR 來源與 seed 仍然算（既有規則優先）。
	s.World.Sources.Returns("supadata", "local:talk.mp4", "supadata-en.json")
	if !s.hasTranscriptSource("local:talk.mp4") {
		t.Error("腳本化了已實作的來源")
	}
}

// ---- transcriptIs：完整表格與部分表格 ----

// longCues 是 n 句的字幕（第 i 句：文字 "line 01"，時間 (i-1)*4 到 i*4 秒）。
func longCues(n int) cuesJSON {
	c := cuesJSON{Variant: "source", Lang: "en"}
	for i := 1; i <= n; i++ {
		var cue cueJSON
		cue.Index, cue.Text = i, fmt.Sprintf("line %02d", i)
		cue.Start, cue.End = fmt.Sprintf("00:00:%02d", (i-1)*4), fmt.Sprintf("00:00:%02d", i*4)
		c.Cues = append(c.Cues, cue)
	}
	return c
}

func rowsOf(t *testing.T, tbl ...[]string) []transcriptRow {
	t.Helper()
	rows, err := tableRows(table(tbl...))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIsPartialTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []transcriptRow
		want bool
	}{
		{"從 1 起連號", rowsOf(t, []string{"#", "text"}, []string{"1", "a"}, []string{"2", "b"}), false},
		{"沒有 # 欄", rowsOf(t, []string{"text"}, []string{"a"}, []string{"b"}), false},
		{"從 41 起", rowsOf(t, []string{"#", "text"}, []string{"41", "a"}, []string{"42", "b"}), true},
		{"不連續", rowsOf(t, []string{"#", "text"}, []string{"1", "a"}, []string{"3", "b"}), true},
		{"只有第 1 句", rowsOf(t, []string{"#", "text"}, []string{"1", "a"}), false},
	} {
		if got, err := isPartialTable(tc.rows); err != nil || got != tc.want {
			t.Errorf("%s：partial = %v, err = %v，預期 %v", tc.name, got, err, tc.want)
		}
	}
	_, err := isPartialTable(rowsOf(t, []string{"#", "text"}, []string{"x", "a"}))
	wantFail(t, "# 不是整數", err, "不是整數")
}

func TestCheckTranscript(t *testing.T) {
	got := longCues(12)

	// 完整表格（# 從 1 起連號）：句數必須相同，依序比對。
	full := rowsOf(t, []string{"#", "text"}, []string{"1", "line 01"}, []string{"2", "line 02"})
	wantFail(t, "完整表格句數不同", checkTranscript("local:x", "source", got, full), "有 12 句，預期 2 句")
	whole := longCues(2)
	if err := checkTranscript("local:x", "source", whole, full); err != nil {
		t.Errorf("完整表格：%v", err)
	}
	// 沒有 # 欄：同樣要求句數相同。
	noIdx := rowsOf(t, []string{"text"}, []string{"line 01"})
	wantFail(t, "沒有 # 欄句數不同", checkTranscript("local:x", "source", got, noIdx), "預期 1 句")

	// 部分表格：依 # 對應第 N 個 cue，不檢查總句數。
	partial := rowsOf(t, []string{"#", "start", "end", "text"},
		[]string{"11", "00:00:40", "00:00:44", "line 11"},
		[]string{"12", "00:00:44", "00:00:48", "line 12"})
	if err := checkTranscript("local:x", "source", got, partial); err != nil {
		t.Errorf("部分表格：%v", err)
	}
	// 內容不符要失敗，而且訊息指出是第幾句。
	badText := rowsOf(t, []string{"#", "text"}, []string{"11", "line 11"}, []string{"12", "something else"})
	wantFail(t, "第 12 句文字不同", checkTranscript("local:x", "source", got, badText), "第 12 句：text")
	badTime := rowsOf(t, []string{"#", "start", "end"}, []string{"11", "00:00:40", "00:00:45"})
	wantFail(t, "結束時間不同", checkTranscript("local:x", "source", got, badTime), "end")
	// 字幕沒有那麼多句：失敗。
	tooFar := rowsOf(t, []string{"#", "text"}, []string{"41", "x"}, []string{"42", "y"})
	wantFail(t, "沒有第 41 句", checkTranscript("local:x", "source", got, tooFar), "沒有第 41 句")
	// # 必須對應 cue 自己的編號：cue 的 index 不是 N 就失敗。
	shifted := longCues(12)
	shifted.Cues[10].Index = 99
	wantFail(t, "cue 編號不同", checkTranscript("local:x", "source", shifted, partial), "#")
}
