// Package langtag 解析 BCP-47 語言標記的前三個子標籤（主語言、文字系統、地區），
// 供 TRN（中文字形轉換）與 OUT（雙語判斷）共用。不依賴其他內部套件。
package langtag

import "strings"

// Tag 是 BCP-47 標記的前三個子標籤：Lang 小寫、Script 首字大寫、Region 大寫。其餘子標籤一律忽略。
type Tag struct{ Lang, Script, Region string }

// Parse 解析語言標記；分隔符 _ 視同 -。空字串回傳零值。
func Parse(s string) Tag {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"), "-")
	var t Tag
	t.Lang = strings.ToLower(parts[0])
	for _, p := range parts[1:] {
		switch {
		case t.Script == "" && t.Region == "" && len(p) == 4 && isAlpha(p):
			t.Script = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		case t.Region == "" && ((len(p) == 2 && isAlpha(p)) || (len(p) == 3 && isDigit(p))):
			t.Region = strings.ToUpper(p)
		default:
			// 超過 script 與 region 的子標籤（variant、extension）不參與比較。
			return t
		}
	}
	return t
}

// String 回傳正規化後的標記，例如 zh_hant_tw → zh-Hant-TW。
func (t Tag) String() string {
	parts := make([]string, 0, 3)
	for _, p := range []string{t.Lang, t.Script, t.Region} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "-")
}

// Equal 回報兩個標記正規化後是否完全相同（TRN-R10）。兩者都是空字串時為 false：沒有語言資訊不算相同。
func Equal(a, b string) bool {
	ta, tb := Parse(a), Parse(b)
	return ta.Lang != "" && ta == tb
}

// SameBase 回報主語言是否相同（zh 與 zh-Hant；OUT-R5）。
func SameBase(a, b string) bool {
	la, lb := Parse(a).Lang, Parse(b).Lang
	return la != "" && la == lb
}

// IsChinese 回報標記的主語言是不是中文（zh）。
func IsChinese(s string) bool { return Parse(s).Lang == "zh" }

// ChineseScript 回傳中文標記的字形：Hans 或 Hant。沒有 script 時依 region 推定：
// TW、HK、MO 為 Hant，CN、SG、MY 為 Hans；都沒有、或不是中文時回傳 ""。
func ChineseScript(s string) string {
	t := Parse(s)
	if t.Lang != "zh" {
		return ""
	}
	if t.Script == "Hans" || t.Script == "Hant" {
		return t.Script
	}
	switch t.Region {
	case "TW", "HK", "MO":
		return "Hant"
	case "CN", "SG", "MY":
		return "Hans"
	}
	return ""
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func isDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
