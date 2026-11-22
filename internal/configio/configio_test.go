package configio

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/validate"
)

// paths 取出錯誤的路徑，方便比對。
func paths(errs []validate.FieldError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Path
	}
	return out
}

func TestParse_Valid(t *testing.T) {
	doc, errs := Parse([]byte(`
default_profile: standard
profiles:
  standard:
    language: { targets: [zh-Hant-TW] }
    timing: { split_timing: forbid }
    glossary: ["a", "b"]
  empty: {}
`))
	if errs != nil {
		t.Fatalf("不該有錯誤：%+v", errs)
	}
	if doc.DefaultProfile != "standard" {
		t.Errorf("DefaultProfile = %q", doc.DefaultProfile)
	}
	if len(doc.Profiles) != 2 {
		t.Fatalf("Profiles = %v", doc.Profiles)
	}
	lang := doc.Profiles["standard"]["language"].(map[string]any)
	if got := lang["targets"].([]any); len(got) != 1 || got[0] != "zh-Hant-TW" {
		t.Errorf("targets = %v", got)
	}
	if len(doc.Profiles["empty"]) != 0 {
		t.Errorf("empty = %v", doc.Profiles["empty"])
	}
}

func TestParse_OnlyProfiles(t *testing.T) {
	doc, errs := Parse([]byte("profiles: {}\n"))
	if errs != nil || doc.DefaultProfile != "" || len(doc.Profiles) != 0 {
		t.Errorf("doc=%+v errs=%+v", doc, errs)
	}
}

// 整份讀不了的情況：doc 是 nil，錯誤的 Path 是空字串。
func TestParse_WholeFileErrors(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"空檔案", "", "空的"},
		{"只有空白與註解", "  \n# 只有註解\n", "空的"},
		{"語法錯誤", "profiles: [\n", "YAML 格式不正確"},
		{"tab 縮排", "profiles:\n\tx: 1\n", "YAML 格式不正確"},
		{"最上層是陣列", "- a\n- b\n", "最上層必須是物件"},
		{"最上層是純量", "hello\n", "最上層必須是物件"},
		{"最上層是 null", "~\n", "最上層必須是物件"},
		{"多份文件", "profiles: {}\n---\nprofiles: {}\n", "只能有一份"},
		{"超過上限", "profiles: {}\n" + strings.Repeat("#", MaxBytes), "檔案太大"},
		{"NUL 字元", "profiles: {}\x00\n", "YAML 格式不正確"},
		{"無效的 UTF-8", "profiles: {x: \xff}\n", "YAML 格式不正確"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, errs := Parse([]byte(c.in))
			if doc != nil {
				t.Errorf("整份讀不了時 doc 應為 nil：%+v", doc)
			}
			if len(errs) != 1 || errs[0].Path != "" || !strings.Contains(errs[0].Message, c.want) {
				t.Errorf("errs = %+v，預期一個沒有路徑、含 %q 的錯誤", errs, c.want)
			}
		})
	}
}

// 剛好等於上限可以讀，多一個位元組就拒絕。
func TestParse_SizeLimitBoundary(t *testing.T) {
	base := "profiles: {}\n"
	atLimit := base + strings.Repeat("#", MaxBytes-len(base)) // 註解填滿到剛好上限
	if len(atLimit) != MaxBytes {
		t.Fatalf("測資長度 %d", len(atLimit))
	}
	if doc, errs := Parse([]byte(atLimit)); errs != nil || doc == nil {
		t.Errorf("剛好等於上限應該可以讀：%+v", errs)
	}
	if _, errs := Parse([]byte(atLimit + "#")); len(errs) != 1 || !strings.Contains(errs[0].Message, "檔案太大") {
		t.Errorf("多 1 位元組應該拒絕：%+v", errs)
	}
}

func TestParse_StructureErrors(t *testing.T) {
	cases := []struct {
		name, in  string
		wantPaths []string
		wantDoc   bool
	}{
		{"不認識的頂層鍵", "profiles: {}\nzzz: 1\nsecrets: {a: b}\n", []string{"secrets", "zzz"}, true},
		{"profiles 不是物件", "profiles: [a]\n", []string{"profiles"}, true},
		{"處理方案不是物件", "profiles:\n  a: 1\n  b: [x]\n  c:\n", []string{"profiles.a", "profiles.b", "profiles.c"}, true},
		{"default_profile 不是字串", "default_profile: 3\nprofiles: {}\n", []string{"default_profile"}, true},
		{"default_profile 是空字串", "default_profile: \"\"\nprofiles: {}\n", []string{"default_profile"}, true},
		{"default_profile 是 null", "default_profile:\nprofiles: {}\n", []string{"default_profile"}, true},
		{"重複的鍵", "profiles:\n  a: {}\n  a: {}\n", []string{"profiles.a"}, false},
		{"頂層重複的鍵", "profiles: {}\nprofiles: {}\n", []string{"profiles"}, false},
		{"別名", "profiles:\n  a: &x {language: {targets: [en]}}\n  b: *x\n", []string{"profiles.b"}, false},
		{"合併鍵", "x: &m {a: 1}\nprofiles:\n  a:\n    <<: *m\n", []string{"profiles.a"}, false},
		{"數字鍵", "profiles:\n  1: {}\n", []string{"profiles"}, false},
		{"布林鍵", "profiles:\n  true: {}\n", []string{"profiles"}, false},
		{"null 鍵", "profiles:\n  ~: {}\n", []string{"profiles"}, false},
		{"NaN", "profiles:\n  a: {timing: {x: .nan}}\n", []string{"profiles.a.timing.x"}, false},
		{"無限大", "profiles:\n  a: {timing: {x: -.inf}}\n", []string{"profiles.a.timing.x"}, false},
		{"二進位標籤", "profiles:\n  a: {glossary: [!!binary aGVsbG8=]}\n", []string{"profiles.a.glossary.0"}, false},
		{"自訂標籤", "profiles:\n  a: !foo {}\n", []string{"profiles.a"}, false},
		{"set 標籤", "profiles:\n  a: !!set {x}\n", []string{"profiles.a"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, errs := Parse([]byte(c.in))
			got := paths(errs)
			// 別名與合併鍵的錯誤路徑可能還包含連帶的其他欄位，這裡只要求預期的路徑都出現而且有錯誤。
			if len(errs) == 0 {
				t.Fatal("應該有錯誤")
			}
			for _, want := range c.wantPaths {
				found := false
				for _, g := range got {
					if g == want {
						found = true
					}
				}
				if !found {
					t.Errorf("錯誤路徑 %v 缺少 %q", got, want)
				}
			}
			if (doc != nil) != c.wantDoc {
				t.Errorf("doc 是否為 nil：doc=%+v，預期有 doc = %v", doc, c.wantDoc)
			}
		})
	}
}

// 不認識的頂層鍵依字母排序，錯誤順序固定。
func TestParse_UnknownKeysSorted(t *testing.T) {
	_, errs := Parse([]byte("zzz: 1\nprofiles: {}\nabc: 2\nmmm: 3\n"))
	if got, want := paths(errs), []string{"abc", "mmm", "zzz"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v，預期 %v", got, want)
	}
}

// YAML 的日期、整數與浮點數：日期保留原文字，數字進入 JSON 時和原本相同。
func TestParse_ScalarTypes(t *testing.T) {
	doc, errs := Parse([]byte(`
profiles:
  a:
    n: 3
    f: 0.6
    big: 18446744073709551615
    hex: 0x10
    t: 2024-01-01
    s: "true"
    yes_str: yes
    b: false
    q: "2"
`))
	if errs != nil {
		t.Fatal(errs)
	}
	raw, err := json.Marshal(doc.Profiles["a"])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":false,"big":18446744073709551615,"f":0.6,"hex":16,"n":3,"q":"2","s":"true","t":"2024-01-01","yes_str":"yes"}`
	if string(raw) != want {
		t.Errorf("\n got %s\nwant %s", raw, want)
	}
}

// 巢狀太深回報錯誤，不會崩潰。
func TestParse_TooDeep(t *testing.T) {
	deep := "profiles:\n  a: " + strings.Repeat("[", maxDepth+10) + strings.Repeat("]", maxDepth+10) + "\n"
	doc, errs := Parse([]byte(deep))
	if doc != nil || len(errs) == 0 || !strings.Contains(errs[0].Message, "巢狀太深") {
		t.Errorf("doc=%+v errs=%+v", doc, errs)
	}
}

// 別名展開炸彈不會被展開：整份直接拒絕。
func TestParse_AliasBombRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 12; i++ {
		b.WriteString("a" + strings.Repeat("1", i) + ": &a" + strings.Repeat("1", i) + " [")
		for j := 0; j < 9; j++ {
			b.WriteString("*a" + strings.Repeat("1", i-1) + ", ")
		}
		b.WriteString("]\n")
	}
	b.WriteString("profiles: {}\n")
	doc, errs := Parse([]byte(b.String()))
	if doc != nil || len(errs) == 0 {
		t.Fatalf("doc=%+v errs=%+v", doc, errs)
	}
}

func TestMarshal_Stable(t *testing.T) {
	doc := &Document{
		DefaultProfile: "b",
		Profiles: map[string]map[string]any{
			"b": {"timing": map[string]any{"split_timing": "forbid"}, "language": map[string]any{"targets": []any{"en", "zh-Hant-TW"}}},
			"a": {},
			"c": nil,
		},
	}
	want := `default_profile: b
profiles:
  a: {}
  b:
    language:
      targets:
        - en
        - zh-Hant-TW
    timing:
      split_timing: forbid
  c: {}
`
	for i := 0; i < 5; i++ { // map 的走訪順序是隨機的，重複幾次確認輸出固定
		got, err := Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("第 %d 次輸出不同：\n%s\n預期：\n%s", i, got, want)
		}
	}
}

func TestMarshal_NoDefaultProfileKey(t *testing.T) {
	got, err := Marshal(&Document{Profiles: map[string]map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "profiles: {}\n" {
		t.Errorf("got %q", got)
	}
}

// 會被 YAML 誤認成其他型別的字串，輸出時要加引號，讀回來仍是字串。
func TestMarshal_RoundTripAmbiguousStrings(t *testing.T) {
	strs := []string{"true", "false", "null", "~", "1", "0.5", "1e3", "0x10", "2024-01-01", "yes", "no", "on", "",
		" leading", "trailing ", "a: b", "- x", "#c", "line1\nline2", "tab\there", "引號\"和'", "*star", "&amp", "!bang", "@at", "%pct", "]", "{", ":"}
	list := make([]any, len(strs))
	for i, s := range strs {
		list[i] = s
	}
	doc := &Document{Profiles: map[string]map[string]any{"p": {"glossary": list}}}
	out, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, errs := Parse(out)
	if errs != nil {
		t.Fatalf("讀回失敗：%+v\n%s", errs, out)
	}
	got := back.Profiles["p"]["glossary"].([]any)
	if !reflect.DeepEqual(got, list) {
		t.Errorf("讀回的內容不同：\n got %#v\nwant %#v\nYAML：\n%s", got, list, out)
	}
}

// 數字：輸出再讀回，JSON 形式相同。
func TestMarshal_RoundTripNumbers(t *testing.T) {
	doc := &Document{Profiles: map[string]map[string]any{"p": {
		"a": float64(3), "b": 0.6, "c": 1e21, "d": 1.5e-7, "e": int64(-5), "f": uint64(18446744073709551615), "g": true, "h": nil,
	}}}
	out, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, errs := Parse(out)
	if errs != nil {
		t.Fatal(errs)
	}
	a, _ := json.Marshal(doc.Profiles)
	b, _ := json.Marshal(back.Profiles)
	if !bytes.Equal(a, b) {
		t.Errorf("\n原本 %s\n讀回 %s\nYAML：\n%s", a, b, out)
	}
}

// FuzzParse：YAML 是不可信的輸入。永遠不能 panic；能讀的內容必須能寫出來、再讀回同樣的內容（不動點）。
func FuzzParse(f *testing.F) {
	seeds := []string{
		"", "profiles: {}\n", "default_profile: a\nprofiles:\n  a: {language: {targets: [en]}}\n",
		"profiles:\n  a: &x {}\n  b: *x\n", "x: &a [*a]\n", "profiles:\n  a: {<<: {b: 1}}\n",
		"profiles:\n  1: {}\n", "profiles:\n  a: {n: .nan}\n", "profiles:\n  a: {n: .inf}\n", "profiles:\n  a: {d: 2024-01-01}\n",
		"profiles:\n  a: !!binary aGVsbG8=\n", "--- {}\n--- {}\n", "[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[",
		"profiles:\n  a: {glossary: ['', ' ', \"\\n\", \"\\u0000\"]}\n", "profiles: {a: {x: 0x7FFFFFFFFFFFFFFF}}\n",
		"profiles: {a: {x: 18446744073709551616}}\n", "profiles: {a: {x: 1e999}}\n", "\xef\xbb\xbfprofiles: {}\n", "profiles: {a: {x: \"\\ud800\"}}\n",
		"? [a]\n: b\n", "profiles: {? a : b}\n", "profiles:\n  a: |\n    block\n    text\n", "profiles:\n  a: {k: >\n    folded\n    text\n}\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, errs := Parse(data)
		if doc == nil {
			if len(errs) == 0 {
				t.Fatal("doc 為 nil 時一定要有錯誤")
			}
			return
		}
		if len(errs) > 0 {
			return
		}
		// 讀進來的值必須是 JSON 相容的（驗證層會先轉成 JSON）。
		j1, err := json.Marshal(doc.Profiles)
		if err != nil {
			t.Fatalf("讀進來的值無法轉成 JSON：%v", err)
		}
		out, err := Marshal(doc)
		if err != nil {
			t.Fatalf("Marshal 失敗：%v", err)
		}
		back, errs := Parse(out)
		if errs != nil {
			t.Fatalf("輸出的 YAML 讀不回來：%+v\n輸入：%q\n輸出：%q", errs, data, out)
		}
		j2, _ := json.Marshal(back.Profiles)
		if !bytes.Equal(j1, j2) || back.DefaultProfile != doc.DefaultProfile {
			t.Fatalf("往返後內容不同：\n輸入：%q\n輸出：%q\n之前 %s\n之後 %s", data, out, j1, j2)
		}
		out2, err := Marshal(back)
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("輸出不穩定：\n%q\n%q（%v）", out, out2, err)
		}
	})
}

// 最壞情況的輸入（接近上限的大小）必須很快就有結果：不能因為巢狀、別名或大量節點而卡住。
func TestParse_AdversarialInputsAreFast(t *testing.T) {
	big := MaxBytes - 64
	cases := map[string]string{
		"一堆 [":   strings.Repeat("[", big),
		"一堆 {a:": strings.Repeat("{a:", big/3),
		"巢狀區塊序列": strings.Repeat("- ", big/2),
		"巢狀區塊序列加縮排": func() string {
			var b strings.Builder
			for i := 0; i < 2000; i++ {
				b.WriteString(strings.Repeat(" ", i) + "-\n")
			}
			return b.String()
		}(),
		"大量鍵":            "profiles:\n" + strings.Repeat("  a: {}\n", 1) + strings.Repeat("x: 1\n", big/5),
		"大量元素":           "profiles: {a: {glossary: [" + strings.Repeat("a,", big/2) + "]}}\n",
		"超長純量":           "profiles: {a: {x: " + strings.Repeat("a", big) + "}}\n",
		"大量別名":           "a: &a [1]\nb: [" + strings.Repeat("*a,", big/3) + "]\n",
		"大量錨點":           strings.Repeat("- &a x\n", big/7),
		"大量 complex key": strings.Repeat("? a\n: b\n", big/8),
		"大量多份文件":         strings.Repeat("---\n", big/4),
		"大量空行與註解":        strings.Repeat("#\n", big/2),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if len(in) > MaxBytes {
				in = in[:MaxBytes]
			}
			start := time.Now()
			doc, errs := Parse([]byte(in))
			// 只擋住指數或平方級的卡死；常數倍數會被 -race 與共用的 CI 主機放大（大量元素在 -race 下約 5 秒）。
			if d := time.Since(start); d > 30*time.Second {
				t.Errorf("解析花了 %v", d)
			}
			if doc == nil && len(errs) == 0 {
				t.Error("沒有 doc 也沒有錯誤")
			}
		})
	}
}
