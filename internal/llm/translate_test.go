package llm

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestBuildUnits(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  []Unit
	}{
		{"跨兩句", []string{"The reason we moved to Kafka was that", "our old queue could not keep up.", "It worked."},
			[]Unit{{0, 2}, {2, 1}}},
		{"全都有句點", []string{"A.", "B!", "C?"}, []Unit{{0, 1}, {1, 1}, {2, 1}}},
		{"沒有任何句末標點", []string{"a", "b", "c"}, []Unit{{0, 1}, {1, 1}, {2, 1}}},
		{"結尾沒有標點", []string{"Done.", "and then", "more"}, []Unit{{0, 1}, {1, 2}}},
		{"右引號", []string{"他說「好。」", "然後"}, []Unit{{0, 1}, {1, 1}}},
		{"含切分標記", []string{"a ⟨|⟩ b", "c."}, []Unit{{0, 1}, {1, 1}}},
		{"超過上限", []string{"1", "2", "3", "4", "5", "6", "7."}, []Unit{{0, 5}, {5, 2}}},
		{"空的", nil, []Unit{}},
	}
	for _, c := range cases {
		if got := BuildUnits(c.texts); !slices.Equal(got, c.want) {
			t.Errorf("%s：BuildUnits = %v，預期 %v", c.name, got, c.want)
		}
	}
}

func TestSplitParts(t *testing.T) {
	if p, ok := SplitParts("我們之所以改用 Kafka，⟨|⟩是因為舊的佇列跟不上。", 2); !ok || p[0] != "我們之所以改用 Kafka，" || p[1] != "是因為舊的佇列跟不上。" {
		t.Errorf("SplitParts = %q, %v", p, ok)
	}
	for _, c := range []struct {
		text string
		n    int
	}{{"沒有標記", 2}, {"a⟨|⟩b⟨|⟩c", 2}, {"a⟨|⟩ ", 2}, {"a", 0}} {
		if _, ok := SplitParts(c.text, c.n); ok {
			t.Errorf("SplitParts(%q, %d) 應該失敗", c.text, c.n)
		}
	}
	if p, ok := SplitParts("單句", 1); !ok || len(p) != 1 {
		t.Errorf("單句 = %q, %v", p, ok)
	}
}

func TestCheckUnit(t *testing.T) {
	o := TranslateOptions{Target: "zh-Hant-TW", Mode: ModeTranslate, MinRatio: 0.3, MaxRatio: 3, Glossary: []string{"KRaft mode"}}
	cases := []struct {
		src, out, want string
	}{
		{"We store events in a log.", "我們把事件存在日誌裡。", ""},
		{"We store events in a log.", "  ", ReasonEmpty},
		{"We store events in a log.", "We store events in a log.", ReasonUntranslated},
		{"We store events in a log.", "私たちはイベントをログに保存します。", ReasonWrongLanguage},
		{"We store events in a log.", strings.Repeat("我們把事件存在日誌裡", 10), ReasonLengthRatio},
		{"We store events in a log.", "日誌", ReasonLengthRatio},
		{"Kafka.", "Kafka.", ""},
		{"KRaft mode", "KRaft mode", ""},
		{"OK", "OK", ""},
		{"Okay, let's go.", "Okay, let's go.", ReasonUntranslated},
		{"a⟨|⟩b c d e f", "這是第一部分⟨|⟩第二", ""},
	}
	for _, c := range cases {
		if got, _ := o.CheckUnit(c.src, c.out); got != c.want {
			t.Errorf("CheckUnit(%q, %q) = %q，預期 %q", c.src, c.out, got, c.want)
		}
	}
	l := TranslateOptions{Target: "zh-Hant-TW", Mode: ModeLocalize, MinSimilarity: DefaultLocalizeSimilarity}
	if got, _ := l.CheckUnit("這個視頻的質量很好", "這部影片的品質很好"); got != "" {
		t.Errorf("localize 正常的改寫被拒絕：%q", got)
	}
	if got, _ := l.CheckUnit("這個視頻的質量很好", "今天天氣晴朗適合出門散步走走"); got != ReasonSimilarity {
		t.Errorf("localize 改寫太多 = %q", got)
	}
	if got, _ := l.CheckUnit("這個視頻的質量很好", "这个视频的质量很好"); got != ReasonWrongLanguage {
		t.Errorf("localize 字形錯誤 = %q", got)
	}
}

// 相似度函式是 correct 與 translate（localize）共用的；這裡只確認 localize 用得到的行為。
func TestSimilarityForLocalize(t *testing.T) {
	if s := Similarity("這個視頻的質量很好", "這部影片的品質很好"); math.Abs(s-4.0/9) > 1e-9 {
		t.Errorf("Similarity = %v", s)
	}
	if Similarity("", "") != 1 || Similarity("abc", "abc") != 1 || Similarity("abc", "") != 0 {
		t.Error("Similarity 的邊界不正確")
	}
}

func TestTranslateSpecCheck(t *testing.T) {
	spec := TranslateSpec(TranslateOptions{Target: "zh-Hant-TW", Mode: ModeTranslate})
	if err := spec.Ready(); err != nil {
		t.Fatal(err)
	}
	in := ChunkInput{Cues: []Cue{{I: 1, Text: "We store events in a log."}, {I: 3, Text: "It worked well today."}}}
	if _, verr := spec.Validate(in, []byte(`{"cues":[{"i":1,"text":"我們把事件存在日誌裡。"},{"i":3,"text":"今天一切都非常順利。"}]}`)); verr != nil {
		t.Fatalf("Validate = %v", verr)
	}
	_, verr := spec.Validate(in, []byte(`{"cues":[{"i":1,"text":""},{"i":3,"text":"今天一切都非常順利。"}]}`))
	if verr == nil || verr.Reason != ReasonEmpty || !strings.Contains(verr.Message, "cue 1: empty") || verr.Output == nil {
		t.Fatalf("Validate = %+v", verr)
	}
	var got CueOutput
	if err := json.Unmarshal(verr.Output, &got); err != nil || len(got.Cues) != 2 {
		t.Errorf("Output = %s", verr.Output)
	}
	if !strings.Contains(spec.OutputRules, SplitMarker) || !strings.Contains(spec.OutputRules, "目標語言：zh-Hant-TW。") {
		t.Errorf("OutputRules = %s", spec.OutputRules)
	}
}

func TestReadingSpeed(t *testing.T) {
	if ReadingSpeedLimit("zh-Hant-TW") != 9 || ReadingSpeedLimit("en") != 17 {
		t.Error("ReadingSpeedLimit")
	}
	if ReadingChars("好的，走吧！ OK 1") != 7 {
		t.Errorf("ReadingChars = %d", ReadingChars("好的，走吧！ OK 1"))
	}
}

func FuzzSplitParts(f *testing.F) {
	f.Add("a⟨|⟩b", 2)
	f.Add("⟨|⟩⟨|⟩", 3)
	f.Add("\xe2\x9f\xa8|", 2)
	f.Fuzz(func(t *testing.T, text string, n int) {
		n = n % 64
		parts, ok := SplitParts(text, n)
		if ok && (len(parts) != n || slices.Contains(parts, "")) {
			t.Fatalf("SplitParts(%q, %d) = %q", text, n, parts)
		}
	})
}

func FuzzBuildUnits(f *testing.F) {
	f.Add("a\nb.\nc", "")
	f.Add("⟨|⟩\nx.", "y")
	f.Fuzz(func(t *testing.T, joined, extra string) {
		texts := append(strings.Split(joined, "\n"), extra)
		units := BuildUnits(texts)
		next := 0
		for _, u := range units {
			if u.First != next || u.Count < 1 || u.Count > MaxGroupCues {
				t.Fatalf("BuildUnits(%q) = %v", texts, units)
			}
			next += u.Count
		}
		if next != len(texts) {
			t.Fatalf("BuildUnits(%q) 涵蓋 %d 句，預期 %d", texts, next, len(texts))
		}
	})
}

func FuzzCheckUnit(f *testing.F) {
	f.Add("We store events.", "我們儲存事件。", "zh-Hant-TW", false)
	f.Add("這個", "這部", "zh-Hant-TW", true)
	f.Fuzz(func(t *testing.T, src, out, target string, localize bool) {
		o := TranslateOptions{Target: target, Mode: ModeTranslate}
		if localize {
			o.Mode, o.MinSimilarity = ModeLocalize, DefaultLocalizeSimilarity
		}
		reason, _ := o.CheckUnit(src, out)
		switch reason {
		case "", ReasonEmpty, ReasonUntranslated, ReasonWrongLanguage, ReasonLengthRatio, ReasonSimilarity:
		default:
			t.Fatalf("CheckUnit 回傳未定義的原因 %q", reason)
		}
	})
}
