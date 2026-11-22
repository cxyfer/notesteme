// Package assstyle 是 ASS 樣式庫（OUT-R6～R8）：內建樣式（嵌入程式碼，不入資料庫）、
// 自訂樣式的驗證與儲存、依語言挑預設字型，以及從 .ass 檔匯入 [V4+ Styles]。
package assstyle

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// Style 是一個命名的樣式。ID 與 Name 相同（名稱就是網址中的識別）。
type Style struct {
	ID          string
	Name        string
	Builtin     bool
	Primary     subtitle.ASSLine
	Translation subtitle.ASSLine
	Layout      subtitle.ASSLayout
}

// ASS 回傳不含名稱資訊的樣式本體。
func (s Style) ASS() subtitle.ASSStyle {
	return subtitle.ASSStyle{Primary: s.Primary, Translation: s.Translation, Layout: s.Layout}
}

// 預設字型（OUT-R7）：依目標語言的文字系統。
const (
	FontLatin   = "Noto Sans"
	FontCJKTC   = "Noto Sans CJK TC"
	FontCJKSC   = "Noto Sans CJK SC"
	FontCJKJP   = "Noto Sans CJK JP"
	FontCJKKR   = "Noto Sans CJK KR"
	defaultName = "default"
)

// FontFor 回傳語言的預設字型：zh-Hant（含 TW、HK、MO、yue）用 Noto Sans CJK TC，zh-Hans（含沒有字形資訊的 zh）用 SC，
// 日文 JP、韓文 KR，其餘（拉丁、西里爾、希臘語系）用 Noto Sans。
func FontFor(lang string) string {
	t := langtag.Parse(lang)
	switch {
	case t.Lang == "zh" || t.Lang == "yue":
		if t.Lang == "yue" || t.Ext == "yue" {
			return FontCJKTC
		}
		if langtag.ChineseScript(lang) == "Hant" {
			return FontCJKTC
		}
		return FontCJKSC
	case t.Lang == "ja":
		return FontCJKJP
	case t.Lang == "ko":
		return FontCJKKR
	}
	return FontLatin
}

// ResolveFonts 把沒有指定字型（空字串，代表自動）的文字層補上語言的預設字型：
// 譯文行依 mainLang，原文行依 origLang。已經指定字型的不動。
func ResolveFonts(st subtitle.ASSStyle, mainLang, origLang string) subtitle.ASSStyle {
	if st.Translation.Font == "" {
		st.Translation.Font = FontFor(mainLang)
	}
	if st.Primary.Font == "" {
		st.Primary.Font = FontFor(origLang)
	}
	return st
}

func line(size int, outline float64) subtitle.ASSLine {
	return subtitle.ASSLine{Size: size, Color: "#FFFFFF", OutlineColor: "#000000", BackColor: "#00000080", Outline: outline}
}

func layout() subtitle.ASSLayout {
	return subtitle.ASSLayout{
		Show: subtitle.ShowBoth, Order: subtitle.OrderTranslationFirst, TranslationAt: subtitle.AtStacked,
		Alignment: 2, MarginV: 48, BorderStyle: 1,
	}
}

// builtinStyles 是內建樣式（OUT-R6），順序就是列出的順序。字型留空，依語言自動帶入。
// default 是底部雙語、原文小字（plan 第 11 節）；translation-top 把譯文放到畫面上方。
func builtinStyles() []Style {
	def := Style{Name: "default", Primary: line(36, 2.5), Translation: line(52, 2.5), Layout: layout()}
	def.Primary.Color = "#E6E6E6"

	top := def
	top.Name = "translation-top"
	top.Layout.TranslationAt = subtitle.AtTop

	only := def
	only.Name = "translation-only"
	only.Layout.Show = subtitle.ShowTranslation

	large := def
	large.Name = "large"
	large.Primary, large.Translation = line(46, 3), line(68, 3)
	large.Primary.Color = "#E6E6E6"
	large.Layout.MarginV = 60

	boxed := def
	boxed.Name = "boxed"
	boxed.Layout.BorderStyle = 3
	boxed.Primary.OutlineColor, boxed.Translation.OutlineColor = "#000000B3", "#000000B3"
	boxed.Primary.Outline, boxed.Translation.Outline = 6, 6

	minimal := def
	minimal.Name = "minimal"
	minimal.Primary, minimal.Translation = line(30, 1), line(40, 1)
	minimal.Primary.Color = "#E6E6E6"
	minimal.Layout.MarginV = 36

	out := []Style{def, top, only, large, boxed, minimal}
	for i := range out {
		out[i].ID, out[i].Builtin = out[i].Name, true
	}
	return out
}

// Builtin 回傳內建樣式（每次都是新的複本）。
func Builtin() []Style { return builtinStyles() }

// BuiltinByName 以名稱查內建樣式。
func BuiltinByName(name string) (Style, bool) {
	for _, s := range builtinStyles() {
		if s.Name == name {
			return s, true
		}
	}
	return Style{}, false
}

// Default 是沒有指定樣式時用的內建樣式。
func Default() Style { s, _ := BuiltinByName(defaultName); return s }

// namePattern 是自訂樣式的名稱：小寫英文字母、數字、「.」「_」「-」，最多 64 字元（與網址、檔名都相容）。
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName 回報名稱是否合法。
func ValidName(name string) bool { return namePattern.MatchString(name) }

// LinePatch 是文字層的部分修改：沒有出現（nil）的欄位維持原值。
type LinePatch struct {
	Font         *string  `json:"font,omitempty" doc:"字型；空字串代表依語言的文字系統自動選擇"`
	Size         *int     `json:"size,omitempty" doc:"字級（1080 高的畫布上的像素），1～400"`
	Color        *string  `json:"color,omitempty" doc:"文字顏色，#RRGGBB 或 #RRGGBBAA（AA 是不透明度）"`
	OutlineColor *string  `json:"outline_color,omitempty" doc:"描邊顏色；底框樣式（border_style 3）時是底框顏色"`
	BackColor    *string  `json:"back_color,omitempty" doc:"陰影顏色"`
	Outline      *float64 `json:"outline,omitempty" doc:"描邊寬度，0～50"`
	Shadow       *float64 `json:"shadow,omitempty" doc:"陰影距離，0～50"`
	Bold         *bool    `json:"bold,omitempty"`
}

// LayoutPatch 是版面的部分修改。
type LayoutPatch struct {
	Show          *string `json:"show,omitempty" enum:"both,translation,primary" doc:"顯示哪幾行：both、translation（只有譯文）、primary（只有原文）"`
	Order         *string `json:"order,omitempty" enum:"translation-first,primary-first" doc:"堆疊時誰在上方"`
	TranslationAt *string `json:"translation_at,omitempty" enum:"stacked,top" doc:"stacked 兩行疊在一起；top 譯文放到畫面上方"`
	Alignment     *int    `json:"alignment,omitempty" doc:"位置，數字鍵盤的 1～9（2 是底部置中）"`
	MarginV       *int    `json:"margin_v,omitempty" doc:"垂直邊距，0～1080"`
	BorderStyle   *int    `json:"border_style,omitempty" doc:"1 是描邊加陰影，3 是不透明底框"`
}

// Patch 是樣式的部分修改；內建樣式不能修改，只能當成複製的來源。
type Patch struct {
	Primary     *LinePatch   `json:"primary,omitempty" doc:"原文行"`
	Translation *LinePatch   `json:"translation,omitempty" doc:"譯文行"`
	Layout      *LayoutPatch `json:"layout,omitempty" doc:"版面"`
}

func (p *LinePatch) apply(l *subtitle.ASSLine) {
	if p == nil {
		return
	}
	if p.Font != nil {
		l.Font = *p.Font
	}
	if p.Size != nil {
		l.Size = *p.Size
	}
	if p.Color != nil {
		l.Color = *p.Color
	}
	if p.OutlineColor != nil {
		l.OutlineColor = *p.OutlineColor
	}
	if p.BackColor != nil {
		l.BackColor = *p.BackColor
	}
	if p.Outline != nil {
		l.Outline = *p.Outline
	}
	if p.Shadow != nil {
		l.Shadow = *p.Shadow
	}
	if p.Bold != nil {
		l.Bold = *p.Bold
	}
}

func (p *LayoutPatch) apply(l *subtitle.ASSLayout) {
	if p == nil {
		return
	}
	if p.Show != nil {
		l.Show = *p.Show
	}
	if p.Order != nil {
		l.Order = *p.Order
	}
	if p.TranslationAt != nil {
		l.TranslationAt = *p.TranslationAt
	}
	if p.Alignment != nil {
		l.Alignment = *p.Alignment
	}
	if p.MarginV != nil {
		l.MarginV = *p.MarginV
	}
	if p.BorderStyle != nil {
		l.BorderStyle = *p.BorderStyle
	}
}

// Apply 把修改套用到樣式本體，回傳新的樣式；不修改輸入。
func (p Patch) Apply(st subtitle.ASSStyle) subtitle.ASSStyle {
	p.Primary.apply(&st.Primary)
	p.Translation.apply(&st.Translation)
	p.Layout.apply(&st.Layout)
	return st
}

// badFont 回報字型名稱是不是會破壞 ASS 欄位（逗號、控制字元）或過長。
func badFont(s string) bool {
	if len(s) > 100 {
		return true
	}
	return strings.IndexFunc(s, func(r rune) bool { return r == ',' || unicode.IsControl(r) }) >= 0
}

func validateLine(prefix string, l subtitle.ASSLine, errs *[]validate.FieldError) {
	add := func(field, msg string) {
		*errs = append(*errs, validate.FieldError{Path: prefix + "." + field, Message: msg})
	}
	if badFont(l.Font) {
		add("font", "字型名稱不可以含逗號或控制字元，最多 100 個位元組。")
	}
	if l.Size < 1 || l.Size > 400 {
		add("size", "字級必須在 1 到 400 之間。")
	}
	for field, v := range map[string]string{"color": l.Color, "outline_color": l.OutlineColor, "back_color": l.BackColor} {
		if !subtitle.ValidColour(v) {
			add(field, "顏色必須是 #RRGGBB 或 #RRGGBBAA。")
		}
	}
	if l.Outline < 0 || l.Outline > 50 {
		add("outline", "描邊寬度必須在 0 到 50 之間。")
	}
	if l.Shadow < 0 || l.Shadow > 50 {
		add("shadow", "陰影距離必須在 0 到 50 之間。")
	}
}

// Validate 檢查樣式本體；回傳逐欄位的錯誤（依固定順序），沒有錯誤時為 nil。
func Validate(st subtitle.ASSStyle) []validate.FieldError {
	var errs []validate.FieldError
	validateLine("primary", st.Primary, &errs)
	validateLine("translation", st.Translation, &errs)
	l := st.Layout
	add := func(field, msg string) {
		errs = append(errs, validate.FieldError{Path: "layout." + field, Message: msg})
	}
	if l.Show != subtitle.ShowBoth && l.Show != subtitle.ShowTranslation && l.Show != subtitle.ShowPrimary {
		add("show", "必須是 both、translation 或 primary。")
	}
	if l.Order != subtitle.OrderTranslationFirst && l.Order != subtitle.OrderPrimaryFirst {
		add("order", "必須是 translation-first 或 primary-first。")
	}
	if l.TranslationAt != subtitle.AtStacked && l.TranslationAt != subtitle.AtTop {
		add("translation_at", "必須是 stacked 或 top。")
	}
	if l.Alignment < 1 || l.Alignment > 9 {
		add("alignment", "必須是 1 到 9（數字鍵盤的位置）。")
	}
	if l.MarginV < 0 || l.MarginV > 1080 {
		add("margin_v", "必須在 0 到 1080 之間。")
	}
	if l.BorderStyle != 1 && l.BorderStyle != 3 {
		add("border_style", "必須是 1（描邊加陰影）或 3（不透明底框）。")
	}
	return errs
}

// InvalidError 是輸入不正確（API 回 422）；Fields 是逐欄位的錯誤，可以是空的。
type InvalidError struct {
	Msg    string
	Fields []validate.FieldError
}

func (e *InvalidError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if len(e.Fields) > 0 {
		return fmt.Sprintf("樣式不正確：%s（%s）。", e.Fields[0].Path, e.Fields[0].Message)
	}
	return "樣式不正確。"
}
