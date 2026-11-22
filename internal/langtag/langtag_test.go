package langtag

import "testing"

func TestParseNormalizes(t *testing.T) {
	cases := map[string]Tag{
		"zh-Hant-TW":        {Lang: "zh", Script: "Hant", Region: "TW"},
		"ZH_hant_tw":        {Lang: "zh", Script: "Hant", Region: "TW"},
		"zh-tw":             {Lang: "zh", Region: "TW"},
		"en":                {Lang: "en"},
		"es-419":            {Lang: "es", Region: "419"},
		"zh-Hans-CN-x-foo":  {Lang: "zh", Script: "Hans", Region: "CN", Rest: "x-foo"},
		"en-US-posix":       {Lang: "en", Region: "US", Rest: "posix"},
		"sr-Latn-ekavsk":    {Lang: "sr", Script: "Latn", Rest: "ekavsk"},
		"zh-cmn-Hans":       {Lang: "zh", Ext: "cmn", Script: "Hans"},
		"zh-YUE-hk":         {Lang: "zh", Ext: "yue", Region: "HK"},
		"zh-Hant-TW-u-ca-x": {Lang: "zh", Script: "Hant", Region: "TW", Rest: "u-ca-x"},
		"":                  {},
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %+v，預期 %+v", in, got, want)
		}
	}
	if s := Parse("zh_hant_tw").String(); s != "zh-Hant-TW" {
		t.Errorf("String() = %q", s)
	}
	if s := Parse("ZH_cmn_hans_cn_X-Foo").String(); s != "zh-cmn-Hans-CN-x-foo" {
		t.Errorf("String() = %q", s)
	}
}

// variant 與 extlang 是語言的一部分：不同的變體不能被當成相同語言（Codex P2）。
func TestEqualKeepsVariantsAndExtlang(t *testing.T) {
	if Equal("sr-Latn-ekavsk", "sr-Latn-ijekavsk") || !Equal("sr-Latn-ekavsk", "SR_latn_EKAVSK") {
		t.Error("variant 要參與比較")
	}
	if Equal("zh-cmn-Hans", "zh-cmn-Hant") || Equal("zh-cmn-Hans", "zh-yue-Hans") {
		t.Error("extlang 之後的 script 要能解析，也要參與比較")
	}
	if got := ChineseScript("zh-cmn-Hant"); got != "Hant" {
		t.Errorf("ChineseScript(zh-cmn-Hant) = %q", got)
	}
	if got := ChineseScript("zh-yue-HK"); got != "Hant" {
		t.Errorf("ChineseScript(zh-yue-HK) = %q", got)
	}
}

func TestEqualAndSameBase(t *testing.T) {
	if !Equal("zh-Hant-TW", "zh_hant_tw") || Equal("zh-Hant-TW", "zh-Hant") || Equal("", "") {
		t.Error("Equal 結果不符")
	}
	if !SameBase("zh-Hans", "zh-Hant-TW") || SameBase("zh", "en") || SameBase("", "") {
		t.Error("SameBase 結果不符")
	}
}

func TestChineseScript(t *testing.T) {
	cases := map[string]string{
		"zh-Hans": "Hans", "zh-Hant-TW": "Hant", "zh-TW": "Hant", "zh-HK": "Hant", "zh-MO": "Hant",
		"zh-CN": "Hans", "zh-SG": "Hans", "zh-MY": "Hans", "zh": "", "en": "", "en-US": "", "zh-Hans-TW": "Hans",
	}
	for in, want := range cases {
		if got := ChineseScript(in); got != want {
			t.Errorf("ChineseScript(%q) = %q，預期 %q", in, got, want)
		}
	}
	if !IsChinese("zh-Hant") || IsChinese("ja") || IsChinese("") {
		t.Error("IsChinese 結果不符")
	}
}

// 明確指定漢字以外的文字（zh-Latn、zh-Bopo）不依地區推定簡繁（Codex P2）。
func TestChineseScriptExplicitNonHan(t *testing.T) {
	for _, in := range []string{"zh-Latn-TW", "zh-Bopo-TW", "zh-Latn", "zh-Latn-CN"} {
		if got := ChineseScript(in); got != "" {
			t.Errorf("ChineseScript(%q) = %q，預期 \"\"", in, got)
		}
	}
	if got := ChineseScript("zh-Hans-TW"); got != "Hans" {
		t.Errorf("zh-Hans-TW = %q", got)
	}
}
