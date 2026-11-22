package langtag

import "testing"

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "zh-Hant-TW", "zh_yue", "en-US-x-private", "-", "---", "ZH-hans-cn-u-ca-x", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = Parse(s)
		_ = IsChinese(s)
		_ = ChineseScript(s)
		_ = Equal(s, "zh-TW")
		_ = SameBase(s, "zh")
	})
}
