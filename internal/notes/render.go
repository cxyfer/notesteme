package notes

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 平台（store.Item.Platform 的值）。
const (
	PlatformYouTube  = "youtube"
	PlatformBilibili = "bilibili"
	PlatformPodcast  = "podcast"
	PlatformLocal    = "local"
)

// Clock 把毫秒寫成筆記的時間（NOTE-R4）：不足 1 小時 MM:SS，否則 H:MM:SS；秒數無條件捨去。負數視為 0。
func Clock(ms int64) string {
	s := max(ms, 0) / 1000
	if s < 3600 {
		return fmt.Sprintf("%02d:%02d", s/60, s%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// Linker 產生跳到某個時間的連結（NOTE-R4）。
type Linker struct {
	Platform string
	// ID 是平台上的 ID（YouTube 影片 ID、Bilibili 的 BV 號）。
	ID string
	// Part 是 Bilibili 的分 P（從 1 起算；0 視為 1）。
	Part int
	// AudioURL 是 Podcast 的音檔網址。
	AudioURL string
}

// URL 回傳 ms 這個時間的連結；本機檔案與沒有可用網址時回傳空字串（寫成純文字）。
func (l Linker) URL(ms int64) string {
	sec := max(ms, 0) / 1000
	switch l.Platform {
	case PlatformYouTube:
		if l.ID == "" {
			return ""
		}
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(l.ID) + "&t=" + strconv.FormatInt(sec, 10) + "s"
	case PlatformBilibili:
		if l.ID == "" {
			return ""
		}
		return fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d&t=%d", url.PathEscape(l.ID), max(l.Part, 1), sec)
	case PlatformPodcast:
		u, err := url.Parse(l.AudioURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ""
		}
		u.Fragment, u.RawFragment = "", ""
		return markdownURL(u.String()) + "#t=" + strconv.FormatInt(sec, 10)
	}
	return ""
}

// markdownURL 跳脫網址中會提前結束 Markdown 連結的字元。
func markdownURL(s string) string {
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E").Replace(s)
}

// Anchor 是標題結尾的時間（NOTE-R4）：有連結時 ` · [MM:SS](<連結>)`，沒有時 ` · MM:SS`。
func (l Linker) Anchor(ms int64) string {
	if u := l.URL(ms); u != "" {
		return " · [" + Clock(ms) + "](" + u + ")"
	}
	return " · " + Clock(ms)
}

// Ref 是內文中 [[cue:N]] 換成的時間（NOTE-R4）：有連結時 `[MM:SS](<連結>)`，沒有時 `MM:SS`。
func (l Linker) Ref(ms int64) string {
	if u := l.URL(ms); u != "" {
		return "[" + Clock(ms) + "](" + u + ")"
	}
	return Clock(ms)
}

// FileTitle 是圖片檔名中的筆記標題（VAULT-R3 的檔名處理，同 subtitle.DownloadFileName）。
func FileTitle(title string) string { return subtitle.DownloadFileName(title, "", "") }

// FrameFileName 是嵌入的畫面檔名（NOTE-R9）：`<筆記標題> - MM-SS - frame_NNNN.jpg`。
func FrameFileName(title string, atMS int64, n int) string {
	return fmt.Sprintf("%s - %s - frame_%04d.jpg", FileTitle(title), strings.ReplaceAll(Clock(atMS), ":", "-"), n)
}

// QuoteYAML 把字串寫成 YAML 的雙引號字串（NOTE-R2）：引號、反斜線、換行與控制字元都跳脫，
// 內容不能改變 frontmatter 的結構。不是合法 UTF-8 的位元組換成 U+FFFD。
func QuoteYAML(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029 || r == 0xfeff || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// 筆記狀態（NOTE-R2、N3）。
const (
	StatusComplete = "complete"
	StatusPartial  = "partial"
)

// Frontmatter 是筆記的 frontmatter（NOTE-R2），欄位依序輸出。
type Frontmatter struct {
	Title          string
	Source         string
	Channel        string
	VideoPublished string // YYYY-MM-DD；不知道時是空字串
	ItemID         string
	Status         string // StatusComplete 或 StatusPartial
	// Engine、Timing 只有 Gemini 直接生成的筆記才有（GEM-R6：notesteme_engine: gemini、notesteme_timing: model）；
	// 空字串時不輸出這兩個欄位。
	Engine string
	Timing string
}

// extraFrontmatter 是只在有值時輸出的欄位，接在 FrontmatterFields 之後。
var extraFrontmatter = []string{"notesteme_engine", "notesteme_timing"}

// FrontmatterFields 是 frontmatter 的欄位，依輸出順序（NOTE-R2）。
var FrontmatterFields = []string{
	"title", "source", "channel", "video_published", "source_kind", "status", "read_at",
	"reading_status", "notesteme_id", "notesteme_status",
}

// Map 回傳欄位與值（給 API 的 frontmatter）。
func (f Frontmatter) Map() map[string]string {
	m := map[string]string{
		"title": f.Title, "source": f.Source, "channel": f.Channel, "video_published": f.VideoPublished,
		"source_kind": "derived", "status": "not-started", "read_at": "", "reading_status": "⚪ 待閱讀",
		"notesteme_id": f.ItemID, "notesteme_status": f.Status,
	}
	if f.Engine != "" {
		m["notesteme_engine"] = f.Engine
	}
	if f.Timing != "" {
		m["notesteme_timing"] = f.Timing
	}
	return m
}

// YAML 回傳含開頭與結尾 `---` 的 frontmatter（結尾沒有換行）。
func (f Frontmatter) YAML() string {
	m := f.Map()
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range FrontmatterFields {
		b.WriteString(k + ": " + QuoteYAML(m[k]) + "\n")
	}
	for _, k := range extraFrontmatter {
		if v, ok := m[k]; ok {
			b.WriteString(k + ": " + QuoteYAML(v) + "\n")
		}
	}
	b.WriteString("---")
	return b.String()
}

// Section 是模型寫的一節（已通過驗證，NOTE-R3）。
type Section struct {
	Level  int    `json:"level"`
	Title  string `json:"title"`
	Cue    int    `json:"cue"`
	Body   string `json:"body"`
	Frames []int  `json:"frames"`
}

// Frame 是一張選中的畫面。
type Frame struct {
	AtMS    int64
	Caption string
}

// InfoRow 是影片資訊表的一列。
type InfoRow struct{ Label, Value string }

// Input 是渲染一份筆記需要的全部資料。字串都是不可信的原始值，渲染時才處理（NOTE-R8）。
type Input struct {
	Front Frontmatter
	// Title 是筆記標題（影音標題），也用於圖片檔名。
	Title    string
	Channel  string
	URL      string // 原文的網址；沒有時資訊表不列「原文」的連結
	Platform string // 平台的顯示名稱（YouTube、Bilibili…）
	Lang     string
	// DurationMS 是片長；0 代表不知道。
	DurationMS int64
	Linker     Linker
	// CueStart 是每個句號的開始時間（毫秒）。
	CueStart map[int]int64
	Frames   map[int]Frame
	// Extra 是資訊表最後的附加列（畫面不足、畫質）；值會經過 TableCell。
	Extra []InfoRow
	// SubtitleLink 是字幕在 vault 中的完整路徑（VAULT-R2）；空字串時資訊表不列字幕。
	SubtitleLink string
	Sections     []Section
	SelfCheck    []string
	ReadButton   string
}

// ItemData 是模板的 .Item。
type ItemData struct {
	URL, Channel, Published, Duration string
}

// Data 是模板可以用的變數（NOTE-R11）：都是已經處理過的 Markdown。
type Data struct {
	Frontmatter string
	Title       string
	ReadButton  string
	InfoTable   string
	Sections    string
	SelfCheck   string
	Lang        string
	Item        ItemData
}

// Images 回傳這份筆記會嵌入的畫面檔名（依出現順序、不重複），給 images_exist 檢查與 VAULT 寫入圖片。
func (in Input) Images() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in.Sections {
		for _, n := range s.Frames {
			f, ok := in.Frames[n]
			if !ok {
				continue
			}
			name := FrameFileName(in.Title, f.AtMS, n)
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// BuildData 把 Input 處理成模板的變數。
func BuildData(in Input) Data {
	d := Data{
		Frontmatter: in.Front.YAML(),
		Title:       SanitizeInline(in.Title),
		ReadButton:  in.ReadButton,
		Lang:        SanitizeInline(in.Lang),
		Item: ItemData{
			URL: in.URL, Channel: SanitizeInline(in.Channel), Published: in.Front.VideoPublished,
		},
	}
	if in.DurationMS > 0 {
		d.Item.Duration = Clock(in.DurationMS)
	}
	if d.Title == "" {
		d.Title = "untitled"
	}
	d.InfoTable = infoTable(in, d)
	d.Sections = renderSections(in)
	d.SelfCheck = renderSelfCheck(in.SelfCheck)
	return d
}

func infoTable(in Input, d Data) string {
	rows := []InfoRow{}
	if d.Item.Channel != "" {
		rows = append(rows, InfoRow{"頻道", TableCell(in.Channel)})
	}
	if d.Item.Published != "" {
		rows = append(rows, InfoRow{"發布", d.Item.Published})
	}
	if d.Item.Duration != "" {
		rows = append(rows, InfoRow{"長度", d.Item.Duration})
	}
	if in.URL != "" && allowedScheme(in.URL) {
		label := TableCell(in.Platform)
		if label == "" {
			label = "連結"
		}
		rows = append(rows, InfoRow{"原文", "[" + label + "](" + markdownURL(in.URL) + ")"})
	}
	if in.SubtitleLink != "" {
		rows = append(rows, InfoRow{"字幕", "[[" + strings.NewReplacer("[", "", "]", "", "|", "").Replace(in.SubtitleLink) + "]]"})
	}
	for _, r := range in.Extra {
		rows = append(rows, InfoRow{TableCell(r.Label), TableCell(r.Value)})
	}
	var b strings.Builder
	b.WriteString("> [!info] 影片資訊\n> | 項目 | 內容 |\n> |---|---|")
	for _, r := range rows {
		b.WriteString("\n> | " + r.Label + " | " + r.Value + " |")
	}
	return b.String()
}

func renderSections(in Input) string {
	keep := func(n int) bool { _, ok := in.CueStart[n]; return ok }
	parts := make([]string, 0, len(in.Sections))
	for _, s := range in.Sections {
		var b strings.Builder
		b.WriteString(strings.Repeat("#", min(max(s.Level, 2), 3)) + " ")
		title := SanitizeInline(s.Title)
		if title == "" {
			title = "untitled"
		}
		b.WriteString(title)
		b.WriteString(in.Linker.Anchor(in.CueStart[s.Cue]))
		body := SanitizeBody(s.Body, keep)
		body = CueRefRe.ReplaceAllStringFunc(body, func(m string) string {
			n, _ := strconv.Atoi(CueRefRe.FindStringSubmatch(m)[1])
			return in.Linker.Ref(in.CueStart[n])
		})
		if body != "" {
			b.WriteString("\n" + body)
		}
		for _, n := range s.Frames {
			f, ok := in.Frames[n]
			if !ok {
				continue
			}
			b.WriteString("\n\n![[" + FrameFileName(in.Title, f.AtMS, n) + "]]\n*" + Clock(f.AtMS))
			if c := Emphasis(f.Caption); c != "" {
				b.WriteString(" — " + c)
			}
			b.WriteString("*")
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

// SelfCheckHeading 是自我檢驗段落的標題（NOTE-R7）。
const SelfCheckHeading = "自我檢驗"

func renderSelfCheck(items []string) string {
	var b strings.Builder
	b.WriteString("## " + SelfCheckHeading)
	for _, it := range items {
		if t := SanitizeInline(it); t != "" {
			b.WriteString("\n- " + t)
		}
	}
	return b.String()
}
