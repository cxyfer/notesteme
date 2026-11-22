package zhconv

import "testing"

func FuzzConvert(f *testing.F) {
	for _, s := range []string{"", "軟體 software 软件", "\xff\xfe", "頭髮發財"} {
		f.Add(s)
	}
	tables := []struct{ src, dst string }{{"zh-Hans", "zh-TW"}, {"zh-Hant", "zh-CN"}, {"zh-Hans", "zh-Hant"}}
	f.Fuzz(func(t *testing.T, s string) {
		_ = DetectScript(s)
		for _, p := range tables {
			if tb, ok := TableFor(p.src, p.dst, StrategyConvert); ok {
				_, _ = Convert(tb, s)
			}
		}
	})
}

func FuzzParseDict(f *testing.F) {
	f.Add([]byte("a\tb c\n\nd\te\r\n"))
	f.Add([]byte("noTab\n\tvalue\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseDict("fuzz", b)
	})
}
