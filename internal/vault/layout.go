// Package vault 是 Obsidian vault 輸出（VAULT）的共用部分：設定檔定義的 vault 與 hook、
// 檔案版面與檔名（VAULT-R2、R3）、寫入前的 symlink 檢查（VAULT-R4）、git adapter（VAULT-R7～R9）與 hook（VAULT-R10、R11）。
// 寫入本身沿用 internal/output 的 Writer（hash guard、原子寫入）。
package vault

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// adapter 的種類（VAULT-R1）。
const (
	AdapterFS  = "fs"
	AdapterGit = "git"
)

// Def 是設定檔定義的一個 vault（vaults.<id>，VAULT-R1）。
type Def struct {
	ID      string
	Adapter string
	// Path 是 fs vault 的根目錄（絕對路徑）。
	Path string
	// Remote、Branch、Author 只用於 git vault。
	Remote string
	Branch string
	Author string
	// Base 是 vault 內的根資料夾（例如 Video Notes）。
	Base string
}

// Hook 是設定檔定義的一個 hook（hooks.<id>，VAULT-R10）。
type Hook struct {
	ID           string
	Argv         []string
	Timeout      time.Duration
	MaxOutput    int
	EnvAllowlist []string
}

// 長度上限（VAULT-R3）：每一段的位元組數與相對於 vault 根目錄的整個路徑的字元數。
const (
	MaxSegmentBytes = 200
	MaxPathChars    = 240
)

// ChannelFolder 是日期在前的版面所在的第一段資料夾（VAULT-R2）。
const ChannelFolder = "Channel"

// DefaultFolder 是沒有指定 folder 時依平台的資料夾（VAULT-R2）。
func DefaultFolder(platform string) string {
	switch platform {
	case "youtube":
		return "YouTube"
	case "bilibili":
		return "Bilibili"
	case "podcast":
		return "Podcasts"
	}
	return "Local"
}

// Item 是決定版面需要的影音資訊。
type Item struct {
	Platform string
	Title    string
	Channel  string
	// Date 是平台的發布日；沒有時是影音的新增日（UTC）。
	Date time.Time
}

// Layout 是一部影音在 vault 中的檔案位置；路徑都相對於 vault 根目錄、以 / 分隔。
type Layout struct {
	// Dir 是筆記所在的資料夾（<base>/<folder>）。
	Dir string
	// Note 是筆記的路徑。
	Note string
	// Assets 是圖片的資料夾。
	Assets string
	// SubDir 是字幕的資料夾，SubStem 是字幕檔名去掉副檔名的部分。
	SubDir  string
	SubStem string
}

// ErrPathTooLong 是 base 與 folder 本身就超過長度上限，沒有辦法靠截短標題解決。
var ErrPathTooLong = errors.New("vault 內的路徑超過長度上限")

// Plan 依 VAULT-R2、R3 決定檔案位置。base 是設定檔的 base（已驗證是相對路徑、沒有 ..）；folder 是處理方案的
// folder（空字串時依平台），可以含 {channel}、{platform}。標題、頻道與每一段資料夾都經過檔名處理；
// 超過長度上限時截短標題。
func Plan(base, folder string, it Item) (Layout, error) {
	if folder == "" {
		folder = DefaultFolder(it.Platform)
	}
	var segs []string
	for _, s := range splitPath(base) {
		segs = append(segs, Segment(s))
	}
	folderSegs := splitPath(folder)
	channelLayout := len(folderSegs) > 0 && folderSegs[0] == ChannelFolder
	for _, s := range folderSegs {
		s = strings.ReplaceAll(s, "{channel}", it.Channel)
		s = strings.ReplaceAll(s, "{platform}", DefaultFolder(it.Platform))
		segs = append(segs, Segment(s))
	}
	dir := strings.Join(segs, "/")
	date := it.Date.UTC().Format("2006-01-02")
	title := subtitle.DownloadFileName(it.Title, "", "")

	for {
		l := layoutFor(dir, title, date, channelLayout)
		if l.fits() {
			return l, nil
		}
		shorter := trimLastRune(title)
		if shorter == "" {
			return Layout{}, fmt.Errorf("%w：%s", ErrPathTooLong, dir)
		}
		title = shorter
	}
}

func layoutFor(dir, title, date string, channel bool) Layout {
	if channel {
		stem := date + "_" + title
		return Layout{
			Dir: dir, Note: path.Join(dir, stem+".md"), Assets: path.Join(dir, "_Assets", stem),
			SubDir: path.Join(dir, "_Subtitles"), SubStem: stem,
		}
	}
	assets := path.Join(dir, "_Assets", title)
	return Layout{
		Dir: dir, Note: path.Join(dir, title+" - "+date+".md"), Assets: assets, SubDir: assets, SubStem: title,
	}
}

// fits 回報版面中最長的路徑（筆記與最長的字幕檔名）是否在上限內。字幕的副檔名以
// `.<語言>.bilingual.srt` 估算（保留 40 字元，語言標記最長約 25 字元）。
func (l Layout) fits() bool {
	const subReserve = 40
	for _, p := range []string{l.Note, path.Join(l.SubDir, l.SubStem) + strings.Repeat("x", subReserve)} {
		if utf8.RuneCountInString(p) > MaxPathChars {
			return false
		}
		for _, s := range strings.Split(p, "/") {
			if len(s) > MaxSegmentBytes {
				return false
			}
		}
	}
	return true
}

// Subtitle 回傳字幕檔的路徑：`<SubStem>.<ext>`；多個語言時加上語言（`<SubStem>.<lang>.<ext>`），
// 雙語 SRT 加上 `.bilingual`，避免互相覆蓋。
func (l Layout) Subtitle(format, lang string, multiLang bool) string {
	name := l.SubStem
	if multiLang && lang != "" {
		name += "." + Segment(lang)
	}
	if format == subtitle.FormatBilingualSRT {
		name += ".bilingual"
	}
	return path.Join(l.SubDir, name+"."+subtitle.FileExt(format))
}

// Segment 處理資料夾的一段或標題（VAULT-R3，同下載檔名）：移除 NTFS 不允許的字元、控制字元與雙向文字控制字元，
// 合併空白、去掉尾端的空白與句點，Windows 保留名稱加上 _ 前綴，空的名稱是 untitled，
// 處理後是 . 或 .. 的段落改成 _；最多 MaxSegmentBytes 位元組。
func Segment(s string) string {
	t := strings.TrimSpace(s)
	if t == "." || t == ".." {
		return "_"
	}
	t = subtitle.DownloadFileName(s, "", "")
	if t == "." || t == ".." {
		return "_"
	}
	for len(t) > MaxSegmentBytes {
		t = trimLastRune(t)
	}
	return t
}

func splitPath(s string) []string {
	var out []string
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func trimLastRune(s string) string {
	if s == "" {
		return ""
	}
	_, n := utf8.DecodeLastRuneInString(s)
	return strings.TrimRight(s[:len(s)-n], ". ")
}
