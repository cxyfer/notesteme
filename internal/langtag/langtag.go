// Package langtag 解析 BCP-47 語言標記，供 TRN（中文字形轉換）與 OUT（雙語判斷）共用。不依賴其他內部套件。
package langtag

import "strings"

// Tag 是 BCP-47 標記：Lang 小寫、Ext（extlang，例如 zh-yue 的 yue）小寫、Script 首字大寫、Region 大寫；
// Rest 是 region 之後的其餘子標籤（variant、extension、私用），小寫、以 - 連接。
type Tag struct{ Lang, Ext, Script, Region, Rest string }

// Parse 解析語言標記；分隔符 _ 視同 -。空字串回傳零值。
func Parse(s string) Tag {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"), "-")
	var t Tag
	t.Lang = strings.ToLower(parts[0])
	i := 1
	// extlang：主語言之後 3 個字母的子標籤（zh-yue-HK、zh-cmn-Hans），要在 script、region 之前。
	if i < len(parts) && len(parts[i]) == 3 && isAlpha(parts[i]) {
		t.Ext = strings.ToLower(parts[i])
		i++
	}
	if i < len(parts) && len(parts[i]) == 4 && isAlpha(parts[i]) {
		t.Script = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
		i++
	}
	if i < len(parts) && ((len(parts[i]) == 2 && isAlpha(parts[i])) || (len(parts[i]) == 3 && isDigit(parts[i]))) {
		t.Region = strings.ToUpper(parts[i])
		i++
	}
	if i < len(parts) {
		t.Rest = strings.ToLower(strings.Join(parts[i:], "-"))
	}
	return t
}

// String 回傳正規化後的標記，例如 zh_hant_tw → zh-Hant-TW。
func (t Tag) String() string {
	parts := make([]string, 0, 5)
	for _, p := range []string{t.Lang, t.Ext, t.Script, t.Region, t.Rest} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "-")
}

// Equal 回報兩個標記正規化後是否完全相同（TRN-R10）：連 variant 與 extension 都要相同
// （sr-Latn-ekavsk 與 sr-Latn-ijekavsk 是不同的語言）。兩者都是空字串時為 false：沒有語言資訊不算相同。
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

// ChineseScript 回傳中文標記的字形：Hans 或 Hant。省略 script 時才依 region 推定（明確指定其他 script 回傳 ""）：
// TW、HK、MO 為 Hant，CN、SG、MY 為 Hans；都沒有、或不是中文時回傳 ""。
func ChineseScript(s string) string {
	t := Parse(s)
	if t.Lang != "zh" {
		return ""
	}
	if t.Script == "Hans" || t.Script == "Hant" {
		return t.Script
	}
	if t.Script != "" {
		return "" // 明確指定其他文字（zh-Latn、zh-Bopo）：不是漢字，不依地區推定簡繁
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
