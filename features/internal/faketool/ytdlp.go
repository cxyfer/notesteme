package faketool

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ytArgs 是解析後的 yt-dlp 參數。
type ytArgs struct {
	version      bool
	dumpJSON     bool
	skipDownload bool
	writeSubs    bool
	writeAuto    bool
	noPlaylist   bool
	subLangs     []string
	subFormat    string
	output       string
	format       string
	cookies      string
	url          string
}

// 沒有值、直接接受的旗標。
var ytBoolFlags = map[string]bool{
	"--no-playlist": true, "--newline": true, "--no-progress": true,
	"--quiet": true, "-q": true, "--no-warnings": true, "--no-colors": true,
	"--ignore-config": true,
}

// 帶一個值、直接接受並忽略的旗標。
var ytIgnoredValueFlags = map[string]bool{
	"--use-extractors": true, "--progress-template": true,
}

func parseYtDlp(args []string) (ytArgs, error) {
	var a ytArgs
	valueOf := func(i *int, flag, inline string, hasInline bool) (string, error) {
		if hasInline {
			return inline, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("option %s requires an argument", flag)
		}
		*i++
		return args[*i], nil
	}
	positional := false // 遇到 "--" 之後，其餘參數一律是位置參數
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positional && arg == "--" {
			positional = true
			continue
		}
		flag, inline, hasInline := arg, "", false
		if !positional && strings.HasPrefix(arg, "--") {
			if k, v, ok := strings.Cut(arg, "="); ok {
				flag, inline, hasInline = k, v, true
			}
		}
		switch {
		case positional:
			if a.url != "" {
				return a, fmt.Errorf("unexpected extra argument %q", arg)
			}
			a.url = arg
		case flag == "--version":
			a.version = true
		case flag == "-J" || flag == "--dump-single-json":
			a.dumpJSON = true
		case flag == "--skip-download":
			a.skipDownload = true
		case flag == "--write-subs":
			a.writeSubs = true
		case flag == "--write-auto-subs":
			a.writeAuto = true
		case ytBoolFlags[flag]:
			if flag == "--no-playlist" {
				a.noPlaylist = true
			}
		case flag == "--sub-langs", flag == "--sub-format", flag == "-o", flag == "--output",
			flag == "-f", flag == "--format", flag == "--cookies", ytIgnoredValueFlags[flag]:
			v, err := valueOf(&i, flag, inline, hasInline)
			if err != nil {
				return a, err
			}
			switch flag {
			case "--sub-langs":
				a.subLangs = nil
				for _, l := range strings.Split(v, ",") {
					if l = strings.TrimSpace(l); l != "" {
						a.subLangs = append(a.subLangs, l)
					}
				}
			case "--sub-format":
				a.subFormat = v
			case "-o", "--output":
				a.output = v
			case "-f", "--format":
				a.format = v
			case "--cookies":
				a.cookies = v
			}
		case strings.HasPrefix(arg, "-"):
			return a, fmt.Errorf("unrecognized option %q", arg)
		default:
			if a.url != "" {
				return a, fmt.Errorf("unexpected extra argument %q", arg)
			}
			a.url = arg
		}
	}
	return a, nil
}

func runYtDlp(e env, args []string) int {
	a, err := parseYtDlp(args)
	logCall(e, a.cookies) // 即使參數不合法也記錄，方便診斷
	if err != nil {
		return usageError(e, "%v", err)
	}

	if a.version {
		st, err := ReadState(e.root)
		if err != nil {
			fmt.Fprintln(e.stderr, "ERROR:", err)
			return 1
		}
		v := st.YtDlpVersion
		if v == "" {
			v = DefaultYtDlpVersion
		}
		fmt.Fprintln(e.stdout, v)
		return 0
	}

	var op string
	switch {
	case a.dumpJSON:
		op = OpInfo
	case a.skipDownload && (a.writeSubs || a.writeAuto):
		op = OpSubs
	case a.format != "" && a.output != "" && !a.skipDownload:
		op = OpMedia
	default:
		return usageError(e, "unsupported combination of options %q", strings.Join(args, " "))
	}
	if a.url == "" {
		return usageError(e, "a URL is required for this operation")
	}
	if op == OpSubs && a.output == "" {
		return usageError(e, "subtitle download requires -o <template>")
	}
	if op == OpSubs && !validSubFormat(a.subFormat) {
		return usageError(e, "unsupported --sub-format %q (only vtt)", a.subFormat)
	}

	st, err := ReadState(e.root)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	waitGate(e.root, st.Gate)

	id, part, platform, err := parseVideoURL(a.url)
	if err != nil {
		fmt.Fprintf(e.stderr, "ERROR: %v\n", err)
		return 1
	}
	v := st.Videos[id]
	if v != nil && v.Fail.applies(op) {
		fmt.Fprintln(e.stderr, v.Fail.Stderr)
		if v.Fail.ExitCode != 0 {
			return v.Fail.ExitCode
		}
		return 1
	}
	if v == nil {
		fmt.Fprintf(e.stderr, "ERROR: [%s] %s: Video unavailable\n", platform, id)
		return 1
	}
	if part > max(v.Parts, 1) {
		fmt.Fprintf(e.stderr, "ERROR: [%s] %s: part %d does not exist (video has %d parts)\n", platform, id, part, max(v.Parts, 1))
		return 1
	}

	switch op {
	case OpInfo:
		return ytInfo(e, id, platform, part, v)
	case OpSubs:
		return ytSubs(e, st, a, id, v)
	default:
		return ytMedia(e, a, id, part, v)
	}
}

func validSubFormat(f string) bool {
	switch f {
	case "", "vtt", "best", "vtt/best":
		return true
	}
	return false
}

// logCall 把這次呼叫附加到 calls.jsonl；寫入失敗不影響工具本身的行為。
func logCall(e env, cookies string) {
	c := Call{Tool: e.tool, Argv: e.argv, Cwd: e.cwd}
	if cookies != "" {
		c.CookiesFile = cookies
		p := cookies
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.cwd, p)
		}
		if fi, err := os.Stat(p); err == nil {
			c.CookiesMode = fmt.Sprintf("%04o", fi.Mode().Perm())
		} else {
			c.CookiesMode = "missing"
		}
	}
	_ = AppendCall(e.root, c)
}

// GateWaitingSuffix 是 waitGate 開始阻塞時建立的標記檔（<gate>.waiting.<pid>）的前綴：
// 每個被擋住的程序各寫一個，讓測試不必靠睡眠就能知道有幾個呼叫已經進入 gate（見 GateWaiting）。
const GateWaitingSuffix = ".waiting"

// PartialDownload 是被 gate 擋住的假 yt-dlp 在場景資料目錄寫下的部分檔（相對於 <root>/data），
// 模擬下載到一半的暫存檔：程序中斷後，重啟時清空 /data/tmp 才會把它清掉（PIPE-R11）。
const PartialDownload = "tmp/partial-download.part"

// gateTimeout 是 waitGate 的等待上限，避免測試卡死。
const gateTimeout = 30 * time.Second

// GateWaiting 回傳目前停在 gate 前的程序所寫的標記檔（路徑已排序）。
func GateWaiting(gate string) ([]string, error) {
	dir, prefix := filepath.Dir(gate), filepath.Base(gate)+GateWaitingSuffix+"."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// waitGate 在 gate 檔存在時阻塞，直到檔案被刪除（上限 gateTimeout）。
// 阻塞期間在 <root>/data/tmp 留下部分檔，並寫自己的標記檔 <gate>.waiting.<pid>；
// gate 解除後（或逾時）移除自己的標記檔。被 kill 時沒有機會清理，由 harness 的 OpenGate 清除。
func waitGate(root, gate string) {
	if gate == "" {
		return
	}
	if _, err := os.Stat(gate); err != nil {
		return
	}
	// 資料目錄存在才寫部分檔（單元測試的根目錄沒有 data/）；寫入失敗不影響阻塞行為。
	data := filepath.Join(root, "data")
	if fi, err := os.Stat(data); err == nil && fi.IsDir() {
		part := filepath.Join(data, filepath.FromSlash(PartialDownload))
		if os.MkdirAll(filepath.Dir(part), 0o700) == nil {
			_ = os.WriteFile(part, []byte("partial"), 0o600)
		}
	}
	// 部分檔寫好之後才寫標記：測試看到標記時，部分檔一定已經在了。
	marker := fmt.Sprintf("%s%s.%d", gate, GateWaitingSuffix, os.Getpid())
	_ = os.WriteFile(marker, nil, 0o644)
	defer os.Remove(marker)
	deadline := time.Now().Add(gateTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(gate); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// parseVideoURL 從網址取出平台 ID 與分P（Bilibili 的 ?p=n，預設 1）。
func parseVideoURL(raw string) (id string, part int, platform string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" {
		return "", 0, "", fmt.Errorf("[generic] '%s' is not a valid URL", raw)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	part = 1
	switch {
	case host == "youtu.be":
		id = strings.Trim(u.Path, "/")
		platform = "youtube"
	case host == "youtube.com" || host == "m.youtube.com" || host == "music.youtube.com":
		id = u.Query().Get("v")
		if id == "" {
			if rest, ok := strings.CutPrefix(u.Path, "/shorts/"); ok {
				id = strings.Trim(rest, "/")
			}
		}
		platform = "youtube"
	case host == "bilibili.com" || host == "m.bilibili.com":
		if rest, ok := strings.CutPrefix(u.Path, "/video/"); ok {
			id = strings.Trim(rest, "/")
		}
		if p := u.Query().Get("p"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 {
				return "", 0, "", fmt.Errorf("[BiliBili] invalid part %q", p)
			}
			part = n
		}
		platform = "BiliBili"
	default:
		return "", 0, "", fmt.Errorf("Unsupported URL: %s", raw)
	}
	if id == "" {
		return "", 0, "", fmt.Errorf("Unsupported URL: %s", raw)
	}
	return id, part, platform, nil
}

// ItemKeyFromURL 從影片網址取出假媒體標記中的 item（見 itemKey），
// 供 fake 來源依網址決定是哪部影音。
func ItemKeyFromURL(raw string) (string, error) {
	id, part, _, err := parseVideoURL(raw)
	if err != nil {
		return "", err
	}
	return itemKey(id, part), nil
}

// itemKey 是假媒體標記中的 item：Bilibili 第 2 分P 以後為 "<bvid>/p<n>"，其餘為平台 ID。
func itemKey(id string, part int) string {
	if part > 1 {
		return fmt.Sprintf("%s/p%d", id, part)
	}
	return id
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func trackName(t Track) string {
	switch t.Kind {
	case KindAuto:
		return t.Lang + " (auto-generated)"
	case KindPlatformTranslated:
		return t.Lang + " (translated)"
	}
	return t.Lang
}

// available 回報這條字幕軌是否已經可用。
func (v *Video) available(t Track) bool {
	return !(v.AutoNotReady && t.Kind != KindManual)
}

func ytInfo(e env, id, platform string, part int, v *Video) int {
	type sub struct {
		Ext  string `json:"ext"`
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	subs, autos := map[string][]sub{}, map[string][]sub{}
	for _, t := range v.Tracks {
		if !v.available(t) {
			continue
		}
		s := sub{Ext: "vtt", URL: fmt.Sprintf("https://fake.invalid/sub/%s/%s.vtt", id, t.Lang), Name: trackName(t)}
		if t.Kind == KindPlatformTranslated {
			// 與真實的 yt-dlp 相同：機器翻譯的字幕軌，網址帶有 tlang 參數。
			s.URL += "?tlang=" + t.Lang
		}
		if t.Kind == KindManual {
			subs[t.Lang] = append(subs[t.Lang], s)
		} else {
			autos[t.Lang] = append(autos[t.Lang], s)
		}
	}
	chapters := make([]map[string]any, 0, len(v.Chapters))
	for _, c := range v.Chapters {
		chapters = append(chapters, map[string]any{"title": c.Title, "start_time": c.Start, "end_time": c.End})
	}
	webpage := "https://www.youtube.com/watch?v=" + id
	if platform == "BiliBili" {
		webpage = fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", id, part)
	}
	info := map[string]any{
		"_type":              "video",
		"id":                 id,
		"title":              v.Title,
		"duration":           v.Duration,
		"language":           nilIfEmpty(v.Language),
		"availability":       orDefault(v.Availability, "public"),
		"live_status":        orDefault(v.LiveStatus, "not_live"),
		"channel_id":         nilIfEmpty(v.ChannelID),
		"channel":            nilIfEmpty(v.Channel),
		"extractor_key":      platform,
		"webpage_url":        webpage,
		"chapters":           chapters,
		"subtitles":          subs,
		"automatic_captions": autos,
	}
	if v.Description != "" {
		info["description"] = v.Description
	}
	if v.PublishedAt != "" {
		if t, err := time.Parse(time.RFC3339, v.PublishedAt); err == nil {
			info["timestamp"] = t.Unix()
			info["upload_date"] = t.UTC().Format("20060102")
		}
	}
	if part > 1 || v.Parts > 1 {
		info["playlist_index"] = part
	}
	b, err := json.Marshal(info)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	fmt.Fprintln(e.stdout, string(b))
	return 0
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var templateField = regexp.MustCompile(`%%|%\((\w+)\)s`)

// expandTemplate 展開 yt-dlp 的輸出樣板（支援 %(id)s、%(title)s、%(ext)s、%%）。
func expandTemplate(tmpl string, fields map[string]string) string {
	// 輸出樣板可以有類型前綴（例如 subtitle:），這裡一律當作同一個樣板。
	for _, p := range []string{"subtitle:", "default:"} {
		tmpl = strings.TrimPrefix(tmpl, p)
	}
	return templateField.ReplaceAllStringFunc(tmpl, func(m string) string {
		if m == "%%" {
			return "%"
		}
		name := m[2 : len(m)-2]
		if v, ok := fields[name]; ok {
			return strings.ReplaceAll(v, "/", "_")
		}
		return "NA"
	})
}

func writeFile(e env, path string, data []byte) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.cwd, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ytSubs(e env, st State, a ytArgs, id string, v *Video) int {
	langs := a.subLangs
	if len(langs) == 0 {
		fmt.Fprintln(e.stdout, "[info] no subtitle languages requested")
		return 0
	}
	// 依 yt-dlp 的行為，同一個語言有人工字幕時優先使用人工字幕。
	pick := func(lang string) *Track {
		var auto *Track
		for i := range v.Tracks {
			t := &v.Tracks[i]
			if (lang != "all" && t.Lang != lang) || !v.available(*t) {
				continue
			}
			if t.Kind == KindManual && a.writeSubs {
				return t
			}
			if t.Kind != KindManual && a.writeAuto && auto == nil {
				auto = t
			}
		}
		return auto
	}
	var chosen []*Track
	seen := map[string]bool{}
	for _, l := range langs {
		if l == "all" {
			for i := range v.Tracks {
				t := &v.Tracks[i]
				if !seen[t.Lang] {
					if p := pick(t.Lang); p != nil {
						seen[t.Lang] = true
						chosen = append(chosen, p)
					}
				}
			}
			continue
		}
		if p := pick(l); p != nil && !seen[l] {
			seen[l] = true
			chosen = append(chosen, p)
		} else if p == nil {
			fmt.Fprintf(e.stderr, "WARNING: [info] There are no subtitles for the requested languages: %s\n", l)
		}
	}
	for _, t := range chosen {
		data, err := trackContent(st, id, t)
		if err != nil {
			fmt.Fprintln(e.stderr, "ERROR:", err)
			return 1
		}
		ext := t.Lang + ".vtt"
		path := expandTemplate(a.output, map[string]string{"id": id, "title": v.Title, "ext": ext})
		if err := writeFile(e, path, data); err != nil {
			fmt.Fprintln(e.stderr, "ERROR:", err)
			return 1
		}
		fmt.Fprintf(e.stdout, "[info] Writing video subtitles to: %s\n", path)
	}
	return 0
}

// trackContent 回傳字幕軌的 VTT 內容：來自 fixture 或依句數產生。
func trackContent(st State, id string, t *Track) ([]byte, error) {
	if t.Fixture != "" {
		if st.FixturesDir == "" {
			return nil, fmt.Errorf("fake state has no fixtures_dir; cannot read caption fixture %q", t.Fixture)
		}
		b, err := os.ReadFile(filepath.Join(st.FixturesDir, "captions", filepath.Clean("/"+t.Fixture)))
		if err != nil {
			return nil, fmt.Errorf("caption fixture %q: %w", t.Fixture, err)
		}
		return b, nil
	}
	n := t.Cues
	if n <= 0 {
		n = DefaultCues
	}
	return []byte(GenerateVTT(id, t.Lang, n)), nil
}

// GenerateVTT 產生 n 句的 VTT 字幕，每句 2 秒，內容為 "Sentence i of <id> (<lang>)."。
func GenerateVTT(id, lang string, n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "WEBVTT\nKind: captions\nLanguage: %s\n\n", lang)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s --> %s\nSentence %d of %s (%s).\n\n", vttTime(i*2), vttTime(i*2+2), i+1, id, lang)
	}
	return sb.String()
}

func vttTime(sec int) string {
	return fmt.Sprintf("%02d:%02d:%02d.000", sec/3600, sec%3600/60, sec%60)
}

func ytMedia(e env, a ytArgs, id string, part int, v *Video) int {
	ext := "mp4"
	if strings.Contains(a.format, "audio") {
		ext = "m4a"
	}
	path := expandTemplate(a.output, map[string]string{"id": id, "title": v.Title, "ext": ext})
	if err := writeFile(e, path, []byte(MediaMarker(itemKey(id, part), v.Duration))); err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "[download] Destination: %s\n[download] 100%% of fake\n", path)
	return 0
}
