package apierr

import (
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"
)

func TestNegotiate(t *testing.T) {
	for h, want := range map[string]string{
		"":                             LangZhHant,
		"zh-Hant":                      LangZhHant,
		"zh-TW":                        LangZhHant,
		"zh-HK":                        LangZhHant,
		"zh-hant-tw":                   LangZhHant,
		"zh-Hans":                      LangZhHans,
		"zh-CN":                        LangZhHans,
		"zh-Hans-HK":                   LangZhHans,
		"zh":                           LangZhHant,
		"en":                           LangEn,
		"en-US,en;q=0.9":               LangEn,
		"EN-gb":                        LangEn,
		"fr":                           LangZhHant,
		"fr-FR,fr;q=0.9":               LangZhHant,
		"fr, en;q=0.5":                 LangEn,
		"*":                            LangZhHant,
		"en;q=0.2, zh-CN;q=0.8":        LangZhHans,
		"zh-CN;q=0.5, en;q=0.5":        LangZhHans, // q 相同時依出現的順序
		"en;q=0, zh-Hans":              LangZhHans,
		"en;q=0":                       LangZhHant,
		"en;q=abc":                     LangZhHant,
		"en;q=1.5":                     LangZhHant,
		"  en-US ; q=0.8 ,zh-TW;q=0.9": LangZhHant,
		";;,,":                         LangZhHant,
	} {
		if got := Negotiate(h); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", h, got, want)
		}
	}
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en")
	if LangFrom(r) != LangEn {
		t.Error("LangFrom")
	}
}

func TestLocalize(t *testing.T) {
	e := New(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	e.ItemID = "item-1"
	en := Localize(e, LangEn)
	if en.Message != "This item was not found." || en.Code != CodeNotFound || en.ItemID != "item-1" || en.Status != http.StatusNotFound {
		t.Errorf("en = %+v", en)
	}
	if e.Message != "找不到這部影音。" {
		t.Error("Localize 不可以改到原本的錯誤")
	}
	if Localize(e, LangZhHant) != e || Localize(nil, LangEn) != nil {
		t.Error("繁中原樣回傳")
	}
	if got := Localize(New(400, "x", "目錄中沒有的訊息"), LangEn).Message; got != "目錄中沒有的訊息" {
		t.Errorf("不在目錄中的訊息照原樣：%q", got)
	}
	f := Newf(http.StatusForbidden, "forbidden", "forbidden_scope", "admin")
	if f.Message != "權限不足：需要 admin scope。" {
		t.Errorf("繁中 = %q", f.Message)
	}
	if got := Localize(f, LangZhHans).Message; got != "权限不足：需要 admin scope。" {
		t.Errorf("简中 = %q", got)
	}
	if got := Localize(f, LangEn).Message; got != "Permission denied: the admin scope is required." {
		t.Errorf("en = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("不存在的範本應該 panic")
		}
	}()
	Newf(400, "x", "no_such_template")
}

var placeholder = regexp.MustCompile(`\{\d+\}`)

// 目錄本身：每一則都有三種語言，範本的參數三種語言相同，同一句不會出現在兩個目錄中。
func TestCatalogComplete(t *testing.T) {
	seen := map[string]string{}
	for name, m := range map[string]map[string]translation{"messages": messages, "domainMessages": domainMessages, "hubMessages": hubMessages} {
		for k, v := range m {
			if strings.TrimSpace(v.hans) == "" || strings.TrimSpace(v.en) == "" {
				t.Errorf("%q 缺少翻譯", k)
			}
			if prev, dup := seen[k]; dup && !(name == "domainMessages" && humaFixedReasons[k] != (translation{})) {
				t.Errorf("%q 同時在 %s 與 %s", k, prev, name)
			}
			seen[k] = name
		}
	}
	verb := regexp.MustCompile(`%[sdqvg]`)
	for _, f := range compiledFormats {
		n := len(verb.FindAllString(f.hant, -1))
		for _, s := range []string{f.hans, f.en} {
			got := placeholder.FindAllString(s, -1)
			uniq := map[string]bool{}
			for _, g := range got {
				uniq[g] = true
			}
			if len(uniq) != n {
				t.Errorf("格式 %q 有 %d 個參數，翻譯 %q 用了 %d 個", f.hant, n, s, len(uniq))
			}
		}
	}
	// 每一則格式以範例參數組出的句子只符合它自己（沒有歧義），而且翻譯得出來。
	for _, f := range compiledFormats {
		sample := strings.NewReplacer("%s", "S1", "%v", "V1", "%d", "7", "%g", "2.5", "%q", `"q1"`, "%%", "%").Replace(f.hant)
		if !f.re.MatchString(sample) {
			t.Errorf("格式 %q 不符合自己的範例 %q", f.hant, sample)
			continue
		}
		for _, g := range compiledFormats {
			if g.hant != f.hant && g.re.MatchString(sample) && g.literal >= f.literal {
				t.Errorf("格式 %q 的範例 %q 也符合 %q（歧義）", f.hant, sample, g.hant)
			}
		}
		if en, ok := Translate(sample, LangEn); !ok || en == sample {
			t.Errorf("格式 %q 翻譯不出來：%q", f.hant, en)
		}
	}
	for k, v := range templates {
		want := placeholder.FindAllString(v.hant, -1)
		sort.Strings(want)
		for _, s := range []string{v.hans, v.en} {
			got := placeholder.FindAllString(s, -1)
			sort.Strings(got)
			if !slices.Equal(got, want) || s == "" {
				t.Errorf("範本 %s 的參數不一致：%q vs %q", k, v.hant, s)
			}
		}
	}
	for status, d := range DefaultMessages {
		if _, ok := messages[d.Message]; !ok {
			t.Errorf("DefaultMessages[%d] %q 不在目錄中", status, d.Message)
		}
	}
	if _, ok := messages[NotImplemented().Message]; !ok {
		t.Error("NotImplemented 的訊息不在目錄中")
	}
}

// 英文的訊息、範本與格式不含中文字（漏翻的部分會原樣出現在英文介面）。
func TestCatalogEnglishHasNoHan(t *testing.T) {
	han := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
	}
	for _, m := range []map[string]translation{messages, domainMessages, hubMessages} {
		for k, v := range m {
			if han(v.en) {
				t.Errorf("%q 的英文含中文字：%q", k, v.en)
			}
		}
	}
	for _, f := range compiledFormats {
		if han(f.en) {
			t.Errorf("格式 %q 的英文含中文字：%q", f.hant, f.en)
		}
	}
	for k, v := range templates {
		if han(v.en) {
			t.Errorf("範本 %s 的英文含中文字：%q", k, v.en)
		}
	}
}
