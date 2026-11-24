package faketool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const biliURL = "https://www.bilibili.com/video/BV1ai411c7mD"

const biliCaption = `{"body":[{"from":1,"to":3.5,"content":"你好"},{"from":3.5,"to":6,"content":"世界"}]}`

// biliState 是一部 2 分P 的 Bilibili 影片；第 1 分P 有 AI 字幕與 CC 字幕。
// gated 為 true 時，AI 字幕（auto）需要 Bilibili 登入。
func biliState(t *testing.T, gated bool) State {
	t.Helper()
	fx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fx, "captions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx, "captions", "ai.json"), []byte(biliCaption), 0o644); err != nil {
		t.Fatal(err)
	}
	st := State{FixturesDir: fx, Videos: map[string]*Video{
		"BV1ai411c7mD": {Title: "BV1ai411c7mD", Duration: 60, Parts: 2},
		"BV1ai411c7mD/p1": {Title: "BV1ai411c7mD", Tracks: []Track{
			{Kind: KindAuto, Lang: "zh", Fixture: "ai.json"}, {Kind: KindManual, Lang: "en-US", Cues: 2}}},
		"BV1ai411c7mD/p2": {Title: "第二集"},
	}}
	if gated {
		st.CookieKinds = map[string]string{KindAuto: "bilibili"}
	}
	return st
}

func writeCookies(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const biliCookie = "# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tfixture\n"

type subEntry struct{ Ext, Data string }

func biliInfoSubs(t *testing.T, out string) map[string][]subEntry {
	t.Helper()
	var info struct {
		Subtitles map[string][]subEntry
		Auto      any `json:"automatic_captions"`
		Channel   any
		Language  any
		Avail     any `json:"availability"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if info.Auto != nil || info.Channel != nil || info.Language != nil || info.Avail != nil {
		t.Fatalf("Bilibili 的 info 不應有 automatic_captions、channel、language、availability：%q", out)
	}
	return info.Subtitles
}

func TestBiliInfoSubtitlesNeedLogin(t *testing.T) {
	s := newScenario(t, biliState(t, true))
	p1 := biliURL + "?p=1"

	// 沒有 --write-subs：沒有字幕，也沒有 WARNING。
	code, out, errs := s.inproc("yt-dlp", "-J", p1)
	if code != 0 || len(biliInfoSubs(t, out)) != 0 || errs != "" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errs)
	}

	// 未登入：AI 字幕不列出、結束碼 0、印 WARNING；CC 字幕與 danmaku 仍在。
	code, out, errs = s.inproc("yt-dlp", "-J", "--write-subs", p1)
	subs := biliInfoSubs(t, out)
	if code != 0 || subs["ai-zh"] != nil || subs["en-US"] == nil || subs["danmaku"][0].Ext != "xml" {
		t.Fatalf("code=%d subs=%v", code, subs)
	}
	if !strings.HasPrefix(errs, "WARNING: [BiliBili] Subtitles are only available when logged in. Use --cookies-from-browser or --cookies") ||
		!strings.Contains(errs, "See  https://github.com/yt-dlp/yt-dlp/wiki/FAQ#how-do-i-pass-cookies-to-yt-dlp  for how to manually pass cookies") {
		t.Fatalf("stderr = %q", errs)
	}

	// --no-warnings 不印。
	if _, _, errs = s.inproc("yt-dlp", "-J", "--write-subs", "--no-warnings", p1); errs != "" {
		t.Fatalf("stderr = %q", errs)
	}

	// 有 SESSDATA 的 cookie 檔：列出 ai-<lang>，格式 srt，內容是轉換後的 SRT。
	ck := writeCookies(t, s.work, biliCookie)
	code, out, errs = s.inproc("yt-dlp", "-J", "--write-subs", "--cookies", ck, p1)
	subs = biliInfoSubs(t, out)
	if code != 0 || errs != "" || len(subs["ai-zh"]) != 1 || subs["ai-zh"][0].Ext != "srt" {
		t.Fatalf("code=%d stderr=%q subs=%v", code, errs, subs)
	}
	if want := "1\n00:00:01,000 --> 00:00:03,500\n你好\n\n2\n00:00:03,500 --> 00:00:06,000\n世界\n\n"; subs["ai-zh"][0].Data != want {
		t.Fatalf("data = %q", subs["ai-zh"][0].Data)
	}

	// 沒有 SESSDATA（或網域不對）等於沒登入。
	for _, body := range []string{
		"# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\t0\tbuvid3\tx\n",
		"# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tx\n",
	} {
		ck = writeCookies(t, s.work, body)
		if _, out, _ = s.inproc("yt-dlp", "-J", "--write-subs", "--cookies", ck, p1); biliInfoSubs(t, out)["ai-zh"] != nil {
			t.Fatalf("不該登入：%q", body)
		}
	}
}

func TestBiliPartsAndIDs(t *testing.T) {
	s := newScenario(t, biliState(t, false))

	// 沒有 p、多分P：url entries。
	code, out, errs := s.inproc("yt-dlp", "--flat-playlist", "-J", biliURL)
	var pl struct {
		Entries []struct{ URL string }
	}
	if err := json.Unmarshal([]byte(out), &pl); code != 0 || err != nil || len(pl.Entries) != 2 ||
		pl.Entries[1].URL != biliURL+"?p=2" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errs)
	}

	// 有 p：id 是 <BV>_p<n>，標題來自分P。
	_, out, _ = s.inproc("yt-dlp", "-J", biliURL+"?p=2")
	if !strings.Contains(out, `"id":"BV1ai411c7mD_p2"`) || !strings.Contains(out, `"title":"第二集"`) || !strings.Contains(out, `"uploader_id"`) {
		t.Fatalf("out = %q", out)
	}
	if code, _, _ := s.inproc("yt-dlp", "-J", biliURL+"?p=3"); code != 1 {
		t.Fatalf("p=3 code=%d", code)
	}
}

func TestBiliSubtitleDownload(t *testing.T) {
	s := newScenario(t, biliState(t, true))
	ck := writeCookies(t, s.work, biliCookie)
	args := []string{"--skip-download", "--write-subs", "--write-auto-subs", "--sub-langs", "all", "--sub-format", "vtt",
		"-o", "out/%(id)s.%(ext)s", "--cookies", ck, biliURL + "?p=1"}
	if code, _, errs := s.inproc("yt-dlp", args...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	// --sub-format vtt 仍寫出 srt；danmaku 另寫 xml。
	for _, name := range []string{"BV1ai411c7mD_p1.ai-zh.srt", "BV1ai411c7mD_p1.en-US.srt", "BV1ai411c7mD_p1.danmaku.xml"} {
		if _, err := os.Stat(filepath.Join(s.work, "out", name)); err != nil {
			t.Fatalf("缺少 %s：%v", name, err)
		}
	}
	// 只有 --write-auto-subs：什麼都不寫。
	if code, _, _ := s.inproc("yt-dlp", "--skip-download", "--write-auto-subs", "--sub-langs", "all", "-o", "none/%(id)s.%(ext)s", "--cookies", ck, biliURL+"?p=1"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(s.work, "none")); err == nil {
		t.Fatal("--write-auto-subs 不該寫任何檔案")
	}
	// 未登入：需要登入的字幕不下載。
	if code, _, errs := s.inproc("yt-dlp", "--skip-download", "--write-subs", "--sub-langs", "all", "-o", "anon/%(id)s.%(ext)s", biliURL+"?p=1"); code != 0 || !strings.Contains(errs, "logged in") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(s.work, "anon", "BV1ai411c7mD_p1.ai-zh.srt")); err == nil {
		t.Fatal("未登入不該下載 AI 字幕")
	}
}

func TestExtractorsRestriction(t *testing.T) {
	st := biliState(t, false)
	st.Videos["abc123xxxxx"] = &Video{Title: "T"}
	s := newScenario(t, st)
	yt := "https://www.youtube.com/watch?v=abc123xxxxx"
	for _, tt := range []struct {
		spec, url string
		ok        bool
	}{
		{"youtube", yt, true},
		{"YouTube,BiliBili", yt, true},
		{"youtube", biliURL + "?p=1", false},
		{"bilibili", yt, false},
		{"youtube:tab,youtube:playlist", yt, false},
		{"youtube:tab,youtube:playlist", "https://www.youtube.com/playlist?list=PL1", true},
		{"youtube", "https://www.youtube.com/playlist?list=PL1", false},
		{"all", biliURL + "?p=1", true},
	} {
		args := []string{"--use-extractors", tt.spec, "-J", tt.url}
		if strings.Contains(tt.url, "playlist") {
			args = append(args, "--flat-playlist")
		}
		code, _, errs := s.inproc("yt-dlp", args...)
		if tt.ok && strings.Contains(errs, "Unsupported URL") || !tt.ok && (code != 1 || errs != "ERROR: Unsupported URL: "+tt.url+"\n") {
			t.Errorf("%q %s：code=%d stderr=%q，預期 ok=%v", tt.spec, tt.url, code, errs, tt.ok)
		}
	}
}

func TestMembersOnlyNeedsYouTubeCookie(t *testing.T) {
	s := newScenario(t, State{Videos: map[string]*Video{"members1xxx": {Title: "M", Availability: "subscriber_only"}}})
	u := "https://www.youtube.com/watch?v=members1xxx"
	// 取得資訊照常回報 availability（辨識網址時的 Probe 不帶 cookie，ING-R9）。
	if code, out, _ := s.inproc("yt-dlp", "-J", u); code != 0 || !strings.Contains(out, `"availability":"subscriber_only"`) {
		t.Fatalf("info code=%d out=%q", code, out)
	}
	dl := []string{"-f", "bestaudio", "-o", "%(id)s.%(ext)s", u}
	code, _, errs := s.inproc("yt-dlp", dl...)
	if code != 1 || errs != "ERROR: [youtube] members1xxx: Join this channel to get access to members-only content like this video, or contact the channel owner.\n" {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	ck := writeCookies(t, s.work, "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsecret-sid\n")
	if code, _, errs := s.inproc("yt-dlp", append([]string{"--cookies", ck}, dl...)...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	// 別的平台的 cookie 不算。
	ck = writeCookies(t, s.work, biliCookie)
	if code, _, _ := s.inproc("yt-dlp", append([]string{"--cookies", ck}, dl...)...); code != 1 {
		t.Fatalf("bilibili cookie code=%d", code)
	}
}

func TestCookieFileFormat(t *testing.T) {
	s := newScenario(t, State{Videos: map[string]*Video{"abc123xxxxx": {Title: "T"}}})
	u := "https://www.youtube.com/watch?v=abc123xxxxx"
	for name, body := range map[string]string{
		"沒有檔頭":      "SESSDATA=fixture\n",
		"欄位數不對":     "# Netscape HTTP Cookie File\n.bilibili.com\tSESSDATA\tfixture\n",
		"header 格式": "SESSDATA=fixture; buvid3=x",
	} {
		ck := writeCookies(t, s.work, body)
		if code, _, errs := s.inproc("yt-dlp", "-J", "--cookies", ck, u); code != 1 || !strings.Contains(errs, "Netscape format cookies file") {
			t.Errorf("%s：code=%d stderr=%q", name, code, errs)
		}
	}
	// #HttpOnly_ 開頭的 cookie 要算進去，註解不算。
	ck := writeCookies(t, s.work, "# Netscape HTTP Cookie File\n# comment\n\n#HttpOnly_.bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tv\n")
	jar, err := loadCookies(ck, s.work)
	if err != nil || !jar.has("bilibili.com", "SESSDATA") {
		t.Fatalf("jar=%v err=%v", jar, err)
	}
}

func TestYouTubeWarningsHonourNoWarnings(t *testing.T) {
	s := newScenario(t, State{Videos: map[string]*Video{"abc123xxxxx": {Title: "T"}}})
	args := []string{"--skip-download", "--write-subs", "--sub-langs", "en", "-o", "%(id)s.%(ext)s", "https://www.youtube.com/watch?v=abc123xxxxx"}
	if _, _, errs := s.inproc("yt-dlp", args...); !strings.Contains(errs, "WARNING:") {
		t.Fatalf("stderr = %q", errs)
	}
	if _, _, errs := s.inproc("yt-dlp", append([]string{"--no-warnings"}, args...)...); errs != "" {
		t.Fatalf("stderr = %q", errs)
	}
}

// production 每次呼叫都帶 --cookies；沒有 cookie 時是空檔，視為沒有登入而不是格式錯誤。
func TestEmptyCookieFileIsLoggedOut(t *testing.T) {
	s := newScenario(t, biliState(t, true))
	ck := writeCookies(t, s.work, "")
	code, out, errs := s.inproc("yt-dlp", "-J", "--write-subs", "--cookies", ck, biliURL+"?p=1")
	if code != 0 || biliInfoSubs(t, out)["ai-zh"] != nil || !strings.Contains(errs, "logged in") {
		t.Fatalf("code=%d stderr=%q out=%q", code, errs, out)
	}
}

// RejectedCookies（SEC-R12）：平台不再接受上傳的 cookie 時，有 SESSDATA 也當作沒登入，照樣印出 login WARNING。
func TestBiliRejectedCookie(t *testing.T) {
	st := biliState(t, true)
	st.RejectedCookies = map[string]bool{"bilibili": true}
	s := newScenario(t, st)
	ck := writeCookies(t, s.work, biliCookie)
	code, out, errs := s.inproc("yt-dlp", "-J", "--write-subs", "--cookies", ck, biliURL+"?p=1")
	if code != 0 || biliInfoSubs(t, out)["ai-zh"] != nil || !strings.Contains(errs, "Subtitles are only available when logged in") {
		t.Fatalf("code=%d stderr=%q out=%q", code, errs, out)
	}
}
