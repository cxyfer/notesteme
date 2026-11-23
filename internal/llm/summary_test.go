package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummarySpecIsStrictAndAcceptsNull(t *testing.T) {
	spec := SummarySpec()
	if err := ValidateStrictSchema(spec.Schema); err != nil {
		t.Fatalf("summary 的 schema 不符合 OpenAI strict：%v", err)
	}
	if spec.Stage != StageSummary || !strings.Contains(spec.OutputRules, "null") {
		t.Errorf("Spec = %+v", spec)
	}
	in := ChunkInput{}
	ok := []string{
		`{"summary":"一段摘要","key_points":[],"title_translation":null}`,
		`{"summary":"一段\n摘要","key_points":["一","二"],"title_translation":"標題"}`,
	}
	for _, s := range ok {
		if _, verr := spec.Validate(in, []byte(s)); verr != nil {
			t.Errorf("%s：%+v", s, verr)
		}
	}
	bad := map[string]string{
		"缺 title_translation":   `{"summary":"a","key_points":[]}`,
		"title_translation 是數字": `{"summary":"a","key_points":[],"title_translation":3}`,
		"key_points 是 null":     `{"summary":"a","key_points":null,"title_translation":null}`,
		"summary 是 null":        `{"summary":null,"key_points":[],"title_translation":null}`,
		"多出欄位":                  `{"summary":"a","key_points":[],"title_translation":null,"start_s":1}`,
		"不是 JSON":               `摘要`,
		"key_points 項目不是字串":     `{"summary":"a","key_points":[1],"title_translation":null}`,
	}
	for name, s := range bad {
		if _, verr := spec.Validate(in, []byte(s)); verr == nil {
			t.Errorf("%s：應該退回", name)
		}
	}
}

func TestStrictRejectsNullableContainers(t *testing.T) {
	for name, s := range map[string]map[string]any{
		"nullable object": {"type": []any{"object", "null"}, "properties": map[string]any{}, "additionalProperties": false},
		"nullable array":  {"type": []any{"null", "array"}, "items": map[string]any{"type": "string"}},
		"兩個都是 null":       {"type": []any{"null", "null"}},
		"三個 type":         {"type": []any{"string", "null", "integer"}},
		"type 不是字串":       {"type": 3},
	} {
		if err := ValidateStrictSchema(s); err == nil {
			t.Errorf("%s：應該不符合 strict", name)
		}
	}
	if typ, nullable := schemaType(map[string]any{"type": []any{"null", "string"}}); typ != "string" || !nullable {
		t.Errorf("null 在前：%q %v", typ, nullable)
	}
}

func TestCheckSummary(t *testing.T) {
	mk := func(summary string, points []string, title any) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"summary": summary, "key_points": points, "title_translation": title})
		return b
	}
	out, verr := CheckSummary(mk("  摘要\n第二行  ", []string{" 一 ", "二"}, " 譯名 "))
	if verr != nil || out.Summary != "摘要\n第二行" || out.KeyPoints[0] != "一" || out.TitleTranslation == nil || *out.TitleTranslation != "譯名" {
		t.Fatalf("去掉頭尾空白：%+v %+v", out, verr)
	}
	if out, verr := CheckSummary(mk("a", nil, nil)); verr != nil || out.TitleTranslation != nil || len(out.KeyPoints) != 0 {
		t.Errorf("沒有重點、沒有譯名：%+v %+v", out, verr)
	}
	// 邊界：剛好在上限的長度可以接受。
	if _, verr := CheckSummary(mk(strings.Repeat("字", MaxSummaryText), []string{strings.Repeat("點", MaxSummaryPoint)}, strings.Repeat("題", MaxSummaryTitleLen))); verr != nil {
		t.Errorf("剛好在上限：%+v", verr)
	}
	if _, verr := CheckSummary(mk("a", make7(), nil)); verr != nil {
		t.Errorf("7 個重點：%+v", verr)
	}
	// ZWJ（emoji 組合）不算控制字元。
	if _, verr := CheckSummary(mk("👨‍👩‍👧", nil, "👨‍👩")); verr != nil {
		t.Errorf("ZWJ：%+v", verr)
	}
	cases := map[string]struct {
		out    json.RawMessage
		reason string
		msg    string
	}{
		"空摘要":           {mk(" \n ", nil, nil), ReasonSummaryText, "summary 是空的"},
		"摘要太長":          {mk(strings.Repeat("字", MaxSummaryText+1), nil, nil), ReasonSummaryText, "summary 有 1201 字，上限是 1200 字"},
		"摘要含 tab":       {mk("a\tb", nil, nil), ReasonSummaryText, "控制字元"},
		"摘要含 CR":        {mk("a\rb", nil, nil), ReasonSummaryText, "控制字元"},
		"摘要含 RLO":       {mk("a‮b", nil, nil), ReasonSummaryText, "控制字元"},
		"摘要含零寬空白":       {mk("a​b", nil, nil), ReasonSummaryText, "控制字元"},
		"摘要含 ESC":       {mk("a\x1b[31mb", nil, nil), ReasonSummaryText, "控制字元"},
		"8 個重點":         {mk("a", append(make7(), "八"), nil), ReasonSummaryPoints, "key_points 有 8 項，上限是 7 項"},
		"空重點":           {mk("a", []string{"一", "  "}, nil), ReasonSummaryPoints, "key_points[1] 是空的"},
		"重點太長":          {mk("a", []string{strings.Repeat("點", MaxSummaryPoint+1)}, nil), ReasonSummaryPoints, "key_points[0] 有 201 字"},
		"重點換行":          {mk("a", []string{"一\n二"}, nil), ReasonSummaryPoints, "換行"},
		"譯名空字串":         {mk("a", nil, ""), ReasonSummaryTitle, "title_translation 是空的"},
		"譯名換行":          {mk("a", nil, "上\n下"), ReasonSummaryTitle, "換行"},
		"譯名太長":          {mk("a", nil, strings.Repeat("題", MaxSummaryTitleLen+1)), ReasonSummaryTitle, "上限是 200 字"},
		"型別錯誤":          {json.RawMessage(`{"summary":1}`), ReasonSchema, "schema"},
		"key_points 型別": {json.RawMessage(`{"summary":"a","key_points":"一"}`), ReasonSchema, "schema"},
	}
	for name, c := range cases {
		_, verr := CheckSummary(c.out)
		if verr == nil || verr.Reason != c.reason || !strings.Contains(verr.Message, c.msg) {
			t.Errorf("%s：%+v", name, verr)
			continue
		}
		if c.reason != ReasonSchema && verr.Detail["error"] != verr.Message {
			t.Errorf("%s：Detail.error 要等於訊息（步驟用它說明原因）：%+v", name, verr.Detail)
		}
	}
	// 無效的 UTF-8：json.Unmarshal 會換成 U+FFFD，要退回。
	if _, verr := CheckSummary(json.RawMessage("{\"summary\":\"a\xffb\",\"key_points\":[],\"title_translation\":null}")); verr == nil {
		t.Error("無效的 UTF-8 要退回")
	}
}

func make7() []string { return []string{"一", "二", "三", "四", "五", "六", "七"} }

// FuzzCheckSummary：模型的輸出不可信；任何內容都不能 panic，通過驗證的結果一定符合 DIG-R3 的上限。
func FuzzCheckSummary(f *testing.F) {
	for _, s := range []string{
		`{"summary":"a","key_points":[],"title_translation":null}`, `null`, `[]`, `{"summary":"\u0000"}`,
		`{"summary":"a","key_points":["\n"],"title_translation":"‮"}`, `{"key_points":[null]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		out, verr := CheckSummary(b)
		if verr != nil {
			if verr.Reason == "" || verr.Message == "" {
				t.Fatalf("沒有原因或說明：%+v", verr)
			}
			return
		}
		if n := utf8.RuneCountInString(out.Summary); n == 0 || n > MaxSummaryText || strings.ContainsFunc(out.Summary, isSummaryControl) {
			t.Fatalf("summary 不符：%q", out.Summary)
		}
		if len(out.KeyPoints) > MaxSummaryPoints {
			t.Fatalf("太多重點：%d", len(out.KeyPoints))
		}
		for _, p := range out.KeyPoints {
			if checkLine(p, MaxSummaryPoint) != "" {
				t.Fatalf("重點不符：%q", p)
			}
		}
		if out.TitleTranslation != nil && checkLine(*out.TitleTranslation, MaxSummaryTitleLen) != "" {
			t.Fatalf("譯名不符：%q", *out.TitleTranslation)
		}
	})
}
