package notes

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// 自動檢查的 ID（NOTE-R7），依執行與回報的順序。
const (
	CheckFrontmatter       = "frontmatter"
	CheckHeadingTimestamps = "heading_timestamps"
	CheckImagesExist       = "images_exist"
	CheckImageNames        = "image_names"
	CheckSelfCheck         = "self_check"
)

// CheckIDs 是全部的自動檢查（模板沒有指定時全部執行）。
var CheckIDs = []string{CheckFrontmatter, CheckHeadingTimestamps, CheckImagesExist, CheckImageNames, CheckSelfCheck}

// headingExceptions 是不需要時間戳的標題（NOTE-R7）。
var headingExceptions = []string{SelfCheckHeading, "Sources", "參考資料", "可轉用的面試與實務重點", "關鍵概念速查"}

// optionalFrontmatter 是可以是空字串的欄位：read_at（NOTE-R2），以及平台沒有提供時的 channel、video_published（N13）。
var optionalFrontmatter = []string{"read_at", "channel", "video_published"}

// Check 是一項自動檢查的結果；Message 只在不通過時有值。
type Check struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// CheckOptions 是檢查需要的資料。
type CheckOptions struct {
	// Enabled 是模板勾選的檢查；nil 代表全部。
	Enabled []string
	// Images 是這次會寫出的圖片檔名（Input.Images）。
	Images []string
	// Title 是筆記標題（圖片檔名的前綴）。
	Title string
}

var (
	atxRe       = regexp.MustCompile(`^(#{2,3})[ \t]+(.*?)[ \t]*$`)
	anchorRe    = regexp.MustCompile(` · (?:\[[0-9]+:[0-9]{2}(?::[0-9]{2})?\]\([^()\s]+\)|[0-9]+:[0-9]{2}(?::[0-9]{2})?)$`)
	embedRe     = regexp.MustCompile(`!\[\[([^\[\]]*)\]\]`)
	imageNameRe = regexp.MustCompile(` - (?:[0-9]+-)?[0-9]{2}-[0-9]{2} - frame_[0-9]{4}\.jpg$`)
)

// RunChecks 執行自動檢查（NOTE-R7），依 CheckIDs 的順序回傳勾選的項目。
func RunChecks(md string, o CheckOptions) []Check {
	front, body := splitFrontmatter(md)
	lines := outsideFences(body)
	var out []Check
	for _, id := range CheckIDs {
		if o.Enabled != nil && !slices.Contains(o.Enabled, id) {
			continue
		}
		c := Check{ID: id, OK: true}
		switch id {
		case CheckFrontmatter:
			c.Message = checkFrontmatter(front)
		case CheckHeadingTimestamps:
			c.Message = checkHeadings(lines)
		case CheckImagesExist:
			c.Message = checkImagesExist(lines, o.Images)
		case CheckImageNames:
			c.Message = checkImageNames(lines, o.Title)
		case CheckSelfCheck:
			c.Message = checkSelfCheck(lines)
		}
		c.OK = c.Message == ""
		out = append(out, c)
	}
	return out
}

// Passed 回報檢查是否全部通過。
func Passed(checks []Check) bool {
	return !slices.ContainsFunc(checks, func(c Check) bool { return !c.OK })
}

// splitFrontmatter 拆出開頭的 frontmatter（不含 ---）與其餘內容；沒有 frontmatter 時 front 是 nil。
func splitFrontmatter(md string) (front *string, body string) {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	rest, ok := strings.CutPrefix(md, "---\n")
	if !ok {
		return nil, md
	}
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return nil, md
	}
	after := rest[i+4:]
	if after != "" && after[0] != '\n' {
		return nil, md
	}
	f := rest[:i]
	return &f, strings.TrimPrefix(after, "\n")
}

// ParseFrontmatter 以 YAML 解析筆記開頭的 frontmatter，值一律轉成字串；沒有或解析失敗時 ok 為 false。
func ParseFrontmatter(md string) (map[string]string, bool) {
	front, _ := splitFrontmatter(md)
	if front == nil {
		return nil, false
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(*front), &raw); err != nil {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if v == nil {
			out[k] = ""
			continue
		}
		out[k] = fmt.Sprint(v)
	}
	return out, true
}

func checkFrontmatter(front *string) string {
	if front == nil {
		return "沒有 frontmatter"
	}
	m, ok := ParseFrontmatter("---\n" + *front + "\n---\n")
	if !ok {
		return "frontmatter 不是合法的 YAML"
	}
	for _, k := range FrontmatterFields {
		v, ok := m[k]
		if !ok {
			return "frontmatter 缺少 " + k
		}
		if strings.TrimSpace(v) == "" && !slices.Contains(optionalFrontmatter, k) {
			return "frontmatter 的 " + k + " 是空的"
		}
	}
	return ""
}

// outsideFences 回傳程式碼區塊以外的行（區塊內的行換成空字串，行號不變）。
func outsideFences(body string) []string {
	lines := strings.Split(body, "\n")
	out := make([]string, len(lines))
	fence := ""
	for i, l := range lines {
		if fence != "" {
			if fenceClose(l, fence) {
				fence = ""
			}
			continue
		}
		if m := fenceOpen(l); m != nil {
			fence = m[2]
			continue
		}
		out[i] = l
	}
	return out
}

func checkHeadings(lines []string) string {
	for _, l := range lines {
		m := atxRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		title := m[2]
		if slices.Contains(headingExceptions, strings.TrimSpace(title)) {
			continue
		}
		if !anchorRe.MatchString(title) {
			return "標題沒有時間戳（" + strings.TrimSpace(title) + "）"
		}
	}
	return ""
}

func embeds(lines []string) []string {
	var out []string
	for _, l := range lines {
		for _, m := range embedRe.FindAllStringSubmatch(l, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

func checkImagesExist(lines []string, images []string) string {
	for _, e := range embeds(lines) {
		if !slices.Contains(images, e) {
			return "圖片不存在（" + e + "）"
		}
	}
	return ""
}

func checkImageNames(lines []string, title string) string {
	prefix := FileTitle(title)
	for _, e := range embeds(lines) {
		rest, ok := strings.CutPrefix(e, prefix)
		if !ok || !imageNameRe.MatchString(rest) || strings.Count(rest, " - ") != 2 {
			return "圖片檔名不符合規則（" + e + "）"
		}
	}
	return ""
}

func checkSelfCheck(lines []string) string {
	found := false
	for _, l := range lines {
		m := atxRe.FindStringSubmatch(l)
		if found {
			if m != nil || strings.HasPrefix(l, "# ") {
				break
			}
			t := strings.TrimSpace(l)
			if (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) && strings.TrimSpace(t[2:]) != "" {
				return ""
			}
			continue
		}
		if m != nil && m[1] == "##" && strings.TrimSpace(m[2]) == SelfCheckHeading {
			found = true
		}
	}
	if found {
		return "自我檢驗沒有任何項目"
	}
	return "沒有自我檢驗段落"
}

// FailureMessage 是檢查不通過時影音顯示的訊息（NOTE-R7）；全部通過時是空字串。
func FailureMessage(checks []Check) string {
	var failed []Check
	for _, c := range checks {
		if !c.OK {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	msg := "筆記沒有通過自動檢查：" + failed[0].Message + "。"
	if n := len(failed) - 1; n > 0 {
		msg += fmt.Sprintf("另有 %d 項沒有通過。", n)
	}
	return msg
}
