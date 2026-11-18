package transcript

import "strings"

// langAliases 把同一語言的舊代碼或變體換成 Whisper 與多數平台使用的代碼：
// nb（書面挪威語）與 nn（新挪威語）→ no、fil（菲律賓語）→ tl、
// iw → he、in → id、jw → jv（ISO-639 已廢止的舊代碼）。
var langAliases = map[string]string{
	"nb": "no", "nn": "no", "fil": "tl", "iw": "he", "in": "id", "jw": "jv",
}

// PrimaryLang 回傳語言代碼的主要語言（小寫），用於比較兩個語言是否相同：
// 去掉 -orig 與 -、_ 後面的子標籤，別名換成同一個代碼（見 langAliases），
// 未知（空字串或 und）回傳空字串。
func PrimaryLang(l string) string {
	l = strings.ToLower(NormalizeLang(strings.TrimSpace(l)))
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	if l == "und" {
		return ""
	}
	if a, ok := langAliases[l]; ok {
		return a
	}
	return l
}
