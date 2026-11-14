package faketool

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// 這個檔案模擬 yt-dlp 的 BiliBili extractor 與 cookie 檔的行為（依 yt-dlp 原始碼）。
// 重點：
//   - 字幕只在帶 --write-subs 時出現在 -J 的輸出，位置是 subtitles，鍵是 Bilibili 的 lan
//     （AI 字幕是 ai-<lang>），格式一律是 srt、內容放在 data；另有一個 danmaku（xml）；沒有 automatic_captions。
//   - 未登入時看不到字幕：不列出、結束碼 0，stderr 印 login WARNING（有 --no-warnings 時不印）。
//   - 網址沒有 p 而且有多個分P 時，回傳 url entries；有 p 時，info 的 id 是 <BV>_p<n>。
//   - --write-subs 寫出 <樣板>.<key>.srt，不論 --sub-format；--write-auto-subs 對 Bilibili 什麼都不寫。

// cookie 是 cookie 檔中的一筆。
type cookie struct{ domain, name, value string }

// cookieJar 是載入後的 cookie 檔。
type cookieJar []cookie

// has 回報是否有網域為 domain 或其子網域、名稱為 name（空字串代表任何名稱）且值不為空的 cookie。
func (j cookieJar) has(domain, name string) bool {
	for _, c := range j {
		d := strings.TrimPrefix(c.domain, ".")
		if (d == domain || strings.HasSuffix(d, "."+domain)) && (name == "" || c.name == name) && c.value != "" {
			return true
		}
	}
	return false
}

var cookieMagic = regexp.MustCompile(`^#( Netscape)? HTTP Cookie File`)

// loadCookies 載入 Netscape 格式的 cookie 檔，規則與 Python 的 MozillaCookieJar（yt-dlp 沿用）相同：
// 第一行必須是 "# Netscape HTTP Cookie File"，# 或 $ 開頭的是註解（#HttpOnly_ 開頭的 cookie 除外），
// 其餘每行必須是 tab 分隔的 7 個欄位。path 為空或檔案不存在時回傳空的 jar。
func loadCookies(path, cwd string) (cookieJar, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if !cookieMagic.MatchString(lines[0]) {
		return nil, fmt.Errorf("%s does not look like a Netscape format cookies file", path)
	}
	var jar cookieJar
	for _, line := range lines[1:] {
		const httpOnly = "#HttpOnly_"
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, httpOnly) {
			line = strings.TrimPrefix(line, httpOnly)
		} else if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "$") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("invalid Netscape format cookies file %q: %q", path, line)
		}
		jar = append(jar, cookie{domain: f[0], name: f[5], value: f[6]})
	}
	return jar, nil
}

const biliLoginWarning = "[BiliBili] Subtitles are only available when logged in. Use --cookies-from-browser or --cookies for the authentication. " +
	"See  https://github.com/yt-dlp/yt-dlp/wiki/FAQ#how-do-i-pass-cookies-to-yt-dlp  for how to manually pass cookies"

// biliSub 是 Bilibili 的一條字幕：key 是 lan，content 是 SRT。
type biliSub struct {
	key     string
	content string
}

// biliRun 處理 Bilibili 網址的 -J、字幕下載與媒體下載。v 是 BV 這部影片（分P 數、長度、上傳者）。
func biliRun(e env, st State, a ytArgs, op string, t videoURL, v *Video, cookies cookieJar) int {
	part := v.partOf(st, t)
	id := t.id
	if t.hasPart || max(v.Parts, 1) > 1 && a.noPlaylist {
		id = fmt.Sprintf("%s_p%d", t.id, t.part)
	}
	switch op {
	case OpInfo:
		// 沒有 p 而且有多個分P：展開成 url entries（與 --flat-playlist 同形）。
		if !t.hasPart && max(v.Parts, 1) > 1 && !a.noPlaylist {
			return biliPlaylist(e, st, t.id, v)
		}
		return biliInfo(e, st, a, id, t, v, part, cookies)
	case OpSubs:
		return biliSubs(e, st, a, id, part, cookies)
	default:
		return ytMedia(e, a, t.id, t.part, v)
	}
}

// partOf 回傳分P 的資料：State.Videos["<bvid>/p<n>"]；沒有登記就沿用影片本身。
func (v *Video) partOf(st State, t videoURL) *Video {
	if p := st.Videos[PartKey(t.id, t.part)]; p != nil {
		return p
	}
	return v
}

func biliPlaylist(e env, st State, bvid string, v *Video) int {
	entries := make([]map[string]any, 0, v.Parts)
	for n := 1; n <= v.Parts; n++ {
		title := bvid
		if p := st.Videos[PartKey(bvid, n)]; p != nil && p.Title != "" {
			title = p.Title
		}
		entries = append(entries, map[string]any{
			"_type": "url", "ie_key": "BiliBili", "id": fmt.Sprintf("%s_p%d", bvid, n),
			"url": fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", bvid, n), "title": title,
		})
	}
	return printJSON(e, map[string]any{
		"_type": "playlist", "id": bvid, "title": orDefault(v.Title, bvid), "extractor_key": "BiliBili",
		"webpage_url": "https://www.bilibili.com/video/" + bvid, "entries": entries,
	})
}

// biliVisibleSubs 回傳目前可見的字幕（含 danmaku）；沒有登入時，需要登入的種類不列出，並印出 login WARNING。
func biliVisibleSubs(e env, st State, a ytArgs, id string, p *Video, cookies cookieJar) ([]biliSub, error) {
	loggedIn := cookies.has("bilibili.com", "SESSDATA")
	gated := false
	var subs []biliSub
	for _, t := range p.Tracks {
		key := t.Lang
		switch t.Kind {
		case KindManual:
		case KindAuto:
			key = "ai-" + t.Lang
		default:
			continue // Bilibili 沒有平台翻譯字幕
		}
		if st.CookieKinds[t.Kind] == "bilibili" {
			gated = true
			if !loggedIn {
				continue
			}
		}
		content, err := trackSRT(st, id, &t)
		if err != nil {
			return nil, err
		}
		subs = append(subs, biliSub{key: key, content: content})
	}
	if gated && !loggedIn {
		warn(e, a, "%s", biliLoginWarning)
	}
	// danmaku 一律存在，與登入無關。
	subs = append(subs, biliSub{key: "danmaku", content: `<?xml version="1.0" encoding="UTF-8"?><i></i>`})
	return subs, nil
}

func biliInfo(e env, st State, a ytArgs, id string, t videoURL, v, p *Video, cookies cookieJar) int {
	subtitles := map[string][]map[string]string{}
	if a.writeSubs {
		subs, err := biliVisibleSubs(e, st, a, id, p, cookies)
		if err != nil {
			fmt.Fprintln(e.stderr, "ERROR:", err)
			return 1
		}
		for _, s := range subs {
			ext := "srt"
			if s.key == "danmaku" {
				ext = "xml"
			}
			subtitles[s.key] = []map[string]string{{"ext": ext, "data": s.content}}
		}
	}
	chapters := make([]map[string]any, 0, len(v.Chapters))
	for _, c := range v.Chapters {
		chapters = append(chapters, map[string]any{"title": c.Title, "start_time": c.Start, "end_time": c.End})
	}
	info := map[string]any{
		"_type":         "video",
		"id":            id,
		"title":         orDefault(p.Title, t.id),
		"duration":      v.Duration,
		"uploader":      orDefault(v.Channel, "uploader-"+t.id),
		"uploader_id":   orDefault(v.ChannelID, fmt.Sprintf("%d", 100000000+crc32.ChecksumIEEE([]byte(t.id))%900000000)),
		"live_status":   orDefault(v.LiveStatus, "not_live"),
		"extractor_key": "BiliBili",
		"webpage_url":   fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", t.id, t.part),
		"chapters":      chapters,
		"subtitles":     subtitles,
	}
	if v.Description != "" {
		info["description"] = v.Description
	}
	if v.PublishedAt != "" {
		if ts, err := time.Parse(time.RFC3339, v.PublishedAt); err == nil {
			info["timestamp"] = ts.Unix()
			info["upload_date"] = ts.UTC().Format("20060102")
		}
	}
	return printJSON(e, info)
}

// biliSubs 實作 --write-subs 的下載：寫出 <樣板>.<key>.srt（--sub-format 不影響），danmaku 寫成 .danmaku.xml。
// --write-auto-subs 對 Bilibili 什麼都不寫。
func biliSubs(e env, st State, a ytArgs, id string, p *Video, cookies cookieJar) int {
	if len(a.subLangs) == 0 {
		fmt.Fprintln(e.stdout, "[info] no subtitle languages requested")
		return 0
	}
	if !a.writeSubs {
		return 0
	}
	subs, err := biliVisibleSubs(e, st, a, id, p, cookies)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	for _, s := range subs {
		if !slices.ContainsFunc(a.subLangs, func(pat string) bool { return subLangMatches(pat, s.key) }) {
			continue
		}
		ext := s.key + ".srt"
		if s.key == "danmaku" {
			ext = "danmaku.xml"
		}
		path := expandTemplate(a.output, map[string]string{"id": id, "title": p.Title, "ext": ext})
		if err := writeFile(e, path, []byte(s.content)); err != nil {
			fmt.Fprintln(e.stderr, "ERROR:", err)
			return 1
		}
		fmt.Fprintf(e.stdout, "[info] Writing video subtitles to: %s\n", path)
	}
	return 0
}

// subLangMatches 依 yt-dlp 的規則比對 --sub-langs：all 全部，其餘是 regex 的 fullmatch。
func subLangMatches(pattern, key string) bool {
	if pattern == "all" {
		return true
	}
	re, err := regexp.Compile(`^(?:` + pattern + `)$`)
	return err == nil && re.MatchString(key)
}

func printJSON(e env, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	fmt.Fprintln(e.stdout, string(b))
	return 0
}

// trackSRT 回傳 Bilibili 字幕軌的 SRT 內容：fixture 是 Bilibili 的字幕 JSON（body[{from, to, content}]），
// 轉成 SRT（真實的 yt-dlp 也是這樣處理 json 字幕）；沒有 fixture 時依句數產生。
func trackSRT(st State, id string, t *Track) (string, error) {
	if t.Fixture == "" {
		n := t.Cues
		if n <= 0 {
			n = DefaultCues
		}
		return GenerateSRT(id, t.Lang, n), nil
	}
	if st.FixturesDir == "" {
		return "", fmt.Errorf("fake state has no fixtures_dir; cannot read caption fixture %q", t.Fixture)
	}
	b, err := os.ReadFile(filepath.Join(st.FixturesDir, "captions", filepath.Clean("/"+t.Fixture)))
	if err != nil {
		return "", fmt.Errorf("caption fixture %q: %w", t.Fixture, err)
	}
	var doc struct {
		Body []struct {
			From, To float64
			Content  string
		} `json:"body"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("caption fixture %q 不是 Bilibili 字幕 JSON（body[{from,to,content}]）：%w", t.Fixture, err)
	}
	var sb strings.Builder
	for i, c := range doc.Body {
		fmt.Fprintf(&sb, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(c.From), srtTime(c.To), c.Content)
	}
	return sb.String(), nil
}

// GenerateSRT 產生 n 句的 SRT 字幕，每句 2 秒，內容與 GenerateVTT 相同。
func GenerateSRT(id, lang string, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%d\n%s --> %s\nSentence %d of %s (%s).\n\n", i+1, srtTime(float64(i*2)), srtTime(float64(i*2+2)), i+1, id, lang)
	}
	return sb.String()
}

// srtTime 把秒數寫成 HH:MM:SS,mmm。
func srtTime(sec float64) string {
	ms := int64(sec*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
