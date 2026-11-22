package zhconv

import "testing"

func TestTableForMatrix(t *testing.T) {
	cases := []struct {
		source, target string
		convert, local Table // "" 代表 ok=false
	}{
		{"zh-Hans", "zh-Hant-TW", S2TWP, S2TW},
		{"zh-CN", "zh-TW", S2TWP, S2TW},
		{"zh-Hans", "zh-Hant-HK", S2HK, S2HK},
		{"zh-Hans", "zh-Hant-MO", S2HK, S2HK},
		{"zh-Hans", "zh-Hant", S2T, S2T},
		{"zh-Hant-TW", "zh-Hans", TW2SP, TW2S},
		{"zh-Hant-TW", "zh-Hans-CN", TW2SP, TW2S},
		{"zh-Hant-HK", "zh-Hans", HK2S, HK2S},
		{"zh-Hant-MO", "zh-Hans", HK2S, HK2S},
		{"zh-Hant", "zh-Hans", T2S, T2S},
		{"zh-Hant-HK", "zh-Hant-TW", HK2TW, HK2TW},
		{"zh-Hant-MO", "zh-Hant-TW", HK2TW, HK2TW},
		{"zh-Hant", "zh-Hant-TW", T2TW, T2TW},
		{"zh-Hant-TW", "zh-Hant-HK", TW2HK, TW2HK},
		{"zh-Hant-TW", "zh-Hant-MO", TW2HK, TW2HK},
		{"zh-Hant", "zh-Hant-HK", T2HK, T2HK},
		// 同一字形、沒有對應的地區表：不轉換。
		{"zh-Hans", "zh-Hans-CN", "", ""},
		{"zh-Hans-CN", "zh-Hans", "", ""},
		{"zh-Hant-TW", "zh-Hant", "", ""},
		{"zh-Hant-TW", "zh-Hant-TW", "", ""},
		{"zh-Hant-HK", "zh-Hant-MO", "", ""},
		// 不是中文、字形無法判斷。
		{"en", "zh-Hant-TW", "", ""},
		{"zh-Hans", "ja", "", ""},
		{"zh", "zh-Hant-TW", "", ""},
		{"zh-Hans", "zh", "", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		for _, st := range []struct {
			s    Strategy
			want Table
		}{{StrategyConvert, c.convert}, {StrategyLocalize, c.local}} {
			got, ok := TableFor(c.source, c.target, st.s)
			if got != st.want || ok != (st.want != "") {
				t.Errorf("TableFor(%q, %q, %s) = %q, %v；預期 %q", c.source, c.target, st.s, got, ok, st.want)
			}
		}
	}
}

func TestGlossaryTable(t *testing.T) {
	cases := map[string]Table{
		"zh-Hans": TW2SP, "zh-CN": TW2SP, "zh-Hant-TW": S2TWP, "zh-TW": S2TWP,
		"zh-Hant-HK": S2HK, "zh-Hant-MO": S2HK, "zh-Hant": S2T, "zh": "", "en": "", "": "",
	}
	for src, want := range cases {
		got, ok := GlossaryTable(src)
		if got != want || ok != (want != "") {
			t.Errorf("GlossaryTable(%q) = %q, %v；預期 %q", src, got, ok, want)
		}
	}
}

func TestDetectScript(t *testing.T) {
	cases := []struct{ in, want string }{
		{"打开软件的设置，这个视频的质量很好", "Hans"},
		{"打開軟件的設置，這個視頻的質量很好", "Hant"},
		{"這是一個測試", "Hant"},
		{"这是一个测试", "Hans"},
		{"", ""},
		{"Hello world", ""},
		{"我們的人", ""},     // 可區分的字少於 3 個
		{"这个測試", ""},     // 簡繁各半：差距不到 2 倍
		{"日本語のテキスト", ""}, // 沒有簡繁專用字
	}
	for _, c := range cases {
		if got := DetectScript(c.in); got != c.want {
			t.Errorf("DetectScript(%q) = %q，預期 %q", c.in, got, c.want)
		}
	}
	if got := DetectScriptOf([]string{"这个视频", "质量很好"}); got != "Hans" {
		t.Errorf("DetectScriptOf = %q", got)
	}
}
