package pipeline

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

func TestNormalizeGlossaryTerms(t *testing.T) {
	got, err := NormalizeGlossaryTerms([]string{" KRaft ", "kraft", "Confluent", "微服務"})
	if err != nil || !slices.Equal(got, []string{"KRaft", "Confluent", "微服務"}) {
		t.Errorf("= %q, %v", got, err)
	}
	for name, terms := range map[string][]string{
		"空字串":    {"a", "  "},
		"控制字元":   {"a\nb"},
		"太長":     {strings.Repeat("字", MaxGlossaryTermRunes+1)},
		"太多個":    make([]string, MaxGlossaryTerms+1),
		"escape": {"\x1b[31m"},
	} {
		var inv *InvalidError
		if _, err := NormalizeGlossaryTerms(terms); !errors.As(err, &inv) {
			t.Errorf("%s：應該是 InvalidError：%v", name, err)
		}
	}
	if got, err := NormalizeGlossaryTerms(nil); err != nil || len(got) != 0 {
		t.Errorf("nil = %v, %v", got, err)
	}
}

func FuzzNormalizeGlossaryTerms(f *testing.F) {
	f.Add("KRaft", "\n")
	f.Fuzz(func(t *testing.T, a, b string) {
		out, err := NormalizeGlossaryTerms([]string{a, b})
		if err != nil {
			return
		}
		for _, s := range out {
			if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				t.Fatalf("通過檢查的詞不合法：%q", s)
			}
		}
	})
}

// COR-R4、S6-Q2：詞彙表轉成原文字形。簡體原文用 tw2sp（資料庫 → 数据库），台灣繁體用 s2twp；
// 原文標記沒有簡繁時依字幕內容判斷；不是中文時原樣使用。
func TestGlossaryForSource(t *testing.T) {
	cues := func(texts ...string) []subtitle.Cue {
		out := make([]subtitle.Cue, len(texts))
		for i, s := range texts {
			out[i] = subtitle.Cue{Text: s}
		}
		return out
	}
	terms := []string{"微服務", "資料庫", "Kafka"}
	for _, tc := range []struct {
		name, lang string
		cues       []subtitle.Cue
		want       []string
	}{
		{"簡體", "zh-Hans", nil, []string{"微服务", "数据库", "Kafka"}},
		{"台灣繁體", "zh-TW", nil, []string{"微服務", "資料庫", "Kafka"}},
		{"zh 依內容判斷：簡體", "zh", cues("这个服务拆分实践很重要", "数据库连接设置"), []string{"微服务", "数据库", "Kafka"}},
		{"zh 判斷不出來：原樣", "zh", cues("ok"), terms},
		{"英文：原樣", "en", nil, terms},
		{"空：原樣", "", nil, terms},
	} {
		if got := glossaryForSource(terms, tc.lang, tc.cues); !slices.Equal(got, tc.want) {
			t.Errorf("%s：%q，預期 %q", tc.name, got, tc.want)
		}
	}
	// 轉換後相同的詞合併。
	if got := glossaryForSource([]string{"數據庫", "数据库"}, "zh-Hans", nil); len(got) != 1 {
		t.Errorf("轉換後重複的詞要合併：%q", got)
	}
}

func TestMergeTermsKeepsFirstSpelling(t *testing.T) {
	got := mergeTerms([]string{"Kafka", "KRaft"}, nil, []string{"kafka", "ksqlDB", ""})
	if !slices.Equal(got, []string{"Kafka", "KRaft", "ksqlDB"}) {
		t.Errorf("= %q", got)
	}
}
