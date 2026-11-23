package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestNormalizeQuery(t *testing.T) {
	cases := []struct {
		in, want, msg string
	}{
		{"  kafka  ", "kafka", ""},
		{"\tkafka\n", "kafka", ""},
		{"a\tb", "a\tb", ""}, // Tab 可以
		{"", "", MsgEmpty},
		{" \t\n ", "", MsgEmpty},
		{"a\x01b", "", MsgBadChars},
		{"a\nb", "", MsgBadChars},
		{"a\u0085b", "", MsgBadChars}, // C1 控制字元
		{"a\x7fb", "", MsgBadChars},
		{"a\xffb", "", MsgBadChars}, // 不是 UTF-8
		{strings.Repeat("a", 200), strings.Repeat("a", 200), ""},
		{strings.Repeat("a", 201), "", MsgTooLong},
		{strings.Repeat("𠮷", 200), strings.Repeat("𠮷", 200), ""}, // 以 code point 計算
		{strings.Repeat("𠮷", 201), "", MsgTooLong},
		{`kafka" OR "spark`, `kafka" OR "spark`, ""},
	}
	for _, c := range cases {
		got, err := NormalizeQuery(c.in)
		if c.msg == "" {
			if err != nil || got != c.want {
				t.Errorf("NormalizeQuery(%q) = %q, %v；want %q", c.in, got, err, c.want)
			}
			continue
		}
		var qe *QueryError
		if !errors.As(err, &qe) || qe.Error() != c.msg {
			t.Errorf("NormalizeQuery(%q) err = %v；want %q", c.in, err, c.msg)
		}
	}
}

func TestPhraseQuotesEverything(t *testing.T) {
	cases := map[string]string{
		`kafka or spark`:    `"kafka or spark"`,
		`"kafka`:            `"""kafka"`,
		`kafka" or "spark`:  `"kafka"" or ""spark"`,
		`near(kafka spark)`: `"near(kafka spark)"`,
		`text_fold:kafka`:   `"text_fold:kafka"`,
		`kafka*`:            `"kafka*"`,
		`""`:                `""""""`,
	}
	for in, want := range cases {
		if got := Phrase(in); got != want {
			t.Errorf("Phrase(%q) = %q；want %q", in, got, want)
		}
	}
}

func TestUseIndex(t *testing.T) {
	for in, want := range map[string]bool{"索引": false, "ab": false, "資料庫": true, "abc": true, "𠮷野": false, "𠮷野家": true} {
		if got := UseIndex(in); got != want {
			t.Errorf("UseIndex(%q) = %v", in, got)
		}
	}
}

func TestHighlights(t *testing.T) {
	cases := []struct {
		text, q string
		want    []Span
	}{
		{"Kafka keeps an ordered log.", "ordered log", []Span{{15, 26}}},
		{"今天介紹資料庫的索引。", "索引", []Span{{8, 10}}},
		// 𠮷 是 surrogate pair（2 個 UTF-16 code unit），之後的位移要加一。
		{"我在𠮷野家討論索引。", "索引", []Span{{8, 10}}},
		{"我在𠮷野家討論索引。", "𠮷野", []Span{{2, 5}}},
		{"Today we compare Kafka or Spark for streaming.", "kafka", []Span{{17, 22}}},
		{"KAFKA kafka Kafka", "kafka", []Span{{0, 5}, {6, 11}, {12, 17}}},
		// 不重疊：aaaa 中的 aa 是 [0,2) 與 [2,4)。
		{"aaaa", "aa", []Span{{0, 2}, {2, 4}}},
		{"aaa", "aa", []Span{{0, 2}}},
		// İ 摺疊成 i，仍以原文的一個字元標示。
		{"İstanbul", "istanbul", []Span{{0, 8}}},
		{"no match here", "kafka", []Span{}},
		{"anything", "", []Span{}},
		{"", "a", []Span{}},
	}
	for _, c := range cases {
		got := Highlights(c.text, c.q)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Highlights(%q, %q) = %v；want %v", c.text, c.q, got, c.want)
		}
	}
}

func TestVariantOrder(t *testing.T) {
	order := []string{"edited", "corrected", "segmented", "normalized", "translated", "source"}
	for i := range order {
		if !Indexable(order[i]) {
			t.Errorf("%s 應可索引", order[i])
		}
		for j := i + 1; j < len(order); j++ {
			if !Better(order[i], order[j]) || Better(order[j], order[i]) {
				t.Errorf("%s 應優先於 %s", order[i], order[j])
			}
		}
		if !Better(order[i], "localized") {
			t.Errorf("%s 應優先於不可索引的變體", order[i])
		}
	}
	if Indexable("localized") || Better("localized", "source") || Better("x", "y") {
		t.Error("不可索引的變體不應被選中")
	}
}

// FuzzHighlights：任何輸入都不 panic，範圍依序、不重疊、落在 UTF-16 長度之內，
// 而且每個範圍在原文中摺疊後等於查詢。
func FuzzHighlights(f *testing.F) {
	for _, s := range [][2]string{{"Kafka keeps", "kafka"}, {"我在𠮷野家", "𠮷"}, {"İİi", "i"}, {"aaaa", "aa"}, {"\xff\xfe", "a"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, text, q string) {
		if !utf8.ValidString(text) || !utf8.ValidString(q) {
			return // 索引與查詢都是合法的 UTF-8（JSON 解碼、NormalizeQuery）
		}
		folded := Fold(q)
		spans := Highlights(text, folded)
		u := utf16.Encode([]rune(text))
		prev := 0
		for _, sp := range spans {
			if sp.Start < prev || sp.End <= sp.Start || sp.End > len(u) {
				t.Fatalf("範圍不正確：%v（%q, %q）", spans, text, q)
			}
			prev = sp.End
			if got := Fold(string(utf16.Decode(u[sp.Start:sp.End]))); got != folded {
				t.Fatalf("範圍 %v 的內容 %q 不等於 %q", sp, got, folded)
			}
		}
		if folded != "" && strings.Contains(Fold(text), folded) != (len(spans) > 0) {
			t.Fatalf("有沒有命中與 strings.Contains 不一致：%q, %q", text, q)
		}
	})
}

// FuzzNormalizeQuery：任何輸入都不 panic；接受的查詢沒有前後空白、長度與字元都在限制內。
func FuzzNormalizeQuery(f *testing.F) {
	for _, s := range []string{"kafka", " ", "a\x01", strings.Repeat("a", 201), `"`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := NormalizeQuery(s)
		if err != nil {
			return
		}
		if q == "" || q != strings.TrimSpace(q) || !utf8.ValidString(q) || utf8.RuneCountInString(q) > MaxQueryRunes {
			t.Fatalf("NormalizeQuery(%q) 接受了不合法的查詢 %q", s, q)
		}
		p := Phrase(Fold(q))
		inner := p[1 : len(p)-1]
		if strings.Count(strings.ReplaceAll(inner, `""`, ""), `"`) != 0 {
			t.Fatalf("Phrase 留下了沒有跳脫的引號：%q", p)
		}
	})
}
