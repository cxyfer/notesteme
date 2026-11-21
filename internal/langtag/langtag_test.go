package langtag

import "testing"

func TestParseNormalizes(t *testing.T) {
	cases := map[string]Tag{
		"zh-Hant-TW":       {"zh", "Hant", "TW"},
		"ZH_hant_tw":       {"zh", "Hant", "TW"},
		"zh-tw":            {"zh", "", "TW"},
		"en":               {"en", "", ""},
		"es-419":           {"es", "", "419"},
		"zh-Hans-CN-x-foo": {"zh", "Hans", "CN"},
		"en-US-posix":      {"en", "", "US"},
		"":                 {},
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %+v，預期 %+v", in, got, want)
		}
	}
	if s := Parse("zh_hant_tw").String(); s != "zh-Hant-TW" {
		t.Errorf("String() = %q", s)
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
