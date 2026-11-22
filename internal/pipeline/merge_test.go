package pipeline

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// RFC 7396 附錄 A 的範例中，根是物件的部分（編號依附錄）。
// 另外 5 個（9～12、14 的根不是物件，或 patch 不是物件）超出 map 簽名，不適用。
func TestMergePatch_RFC7396(t *testing.T) {
	cases := []struct{ name, target, patch, want string }{
		{"A1 取代純量", `{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{"A2 新增鍵", `{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{"A3 null 刪除", `{"a":"b"}`, `{"a":null}`, `{}`},
		{"A4 null 只刪該鍵", `{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{"A5 陣列被純量取代", `{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{"A6 純量被陣列取代", `{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{"A7 巢狀物件遞迴", `{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{"A8 陣列整個取代", `{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{"A13 target 原有的 null 保留", `{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{"A15 新巢狀物件中的 null 被丟掉", `{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
		{"物件取代純量", `{"a":"x"}`, `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"空 patch", `{"a":1}`, `{}`, `{"a":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergePatch(mustMap(t, c.target), mustMap(t, c.patch))
			if want := mustMap(t, c.want); !reflect.DeepEqual(got, want) {
				t.Errorf("MergePatch = %v，預期 %v", got, want)
			}
		})
	}
	// A14 的 nil target 版本：target 不是物件時視為 {}。
	if got, want := MergePatch(nil, mustMap(t, `{"a":"b","c":null}`)), mustMap(t, `{"a":"b"}`); !reflect.DeepEqual(got, want) {
		t.Errorf("nil target：%v，預期 %v", got, want)
	}
}

// 輸入不被修改，結果也不與輸入共用子結構（之後修改結果不會動到輸入）。
func TestMergePatch_DoesNotMutate(t *testing.T) {
	target := mustMap(t, `{"a":{"b":1,"list":[1,2]},"keep":{"x":[1]}}`)
	patch := mustMap(t, `{"a":{"b":null,"c":2,"arr":[3]},"new":{"n":[4]}}`)
	wantT := mustMap(t, `{"a":{"b":1,"list":[1,2]},"keep":{"x":[1]}}`)
	wantP := mustMap(t, `{"a":{"b":null,"c":2,"arr":[3]},"new":{"n":[4]}}`)

	got := MergePatch(target, patch)
	if !reflect.DeepEqual(target, wantT) || !reflect.DeepEqual(patch, wantP) {
		t.Fatalf("輸入被修改：target=%v patch=%v", target, patch)
	}
	// 改結果，輸入不變。
	got["keep"].(map[string]any)["x"].([]any)[0] = 99
	got["a"].(map[string]any)["list"].([]any)[0] = 99
	got["new"].(map[string]any)["n"].([]any)[0] = 99
	if !reflect.DeepEqual(target, wantT) || !reflect.DeepEqual(patch, wantP) {
		t.Errorf("結果與輸入共用子結構：target=%v patch=%v", target, patch)
	}
}

// PATCH 端點的實際用法：處理方案 config 與頻道覆寫的 merge patch（D5）。
func TestMergePatch_ProfileConfigUsage(t *testing.T) {
	cases := []struct{ name, target, patch, want string }{
		{"改一個欄位，兄弟欄位保留",
			`{"stages":{"correct":{"executor":"api","provider":"anthropic","model":"m1"}}}`,
			`{"stages":{"correct":{"model":"m2"}}}`,
			`{"stages":{"correct":{"executor":"api","provider":"anthropic","model":"m2"}}}`},
		{"null 刪掉整個區段", `{"timing":{"split_timing":"forbid"},"language":{"targets":["en"]}}`, `{"timing":null}`, `{"language":{"targets":["en"]}}`},
		{"null 刪掉巢狀的鍵", `{"stages":{"correct":{"provider":"openai","model":"m"}}}`, `{"stages":{"correct":{"provider":null}}}`, `{"stages":{"correct":{"model":"m"}}}`},
		{"刪掉不存在的鍵沒有影響", `{"a":1}`, `{"b":null,"c":{"d":null}}`, `{"a":1,"c":{}}`},
		{"陣列整個取代，不以索引合併", `{"language":{"targets":["en","ja","fr"]}}`, `{"language":{"targets":["de"]}}`, `{"language":{"targets":["de"]}}`},
		{"陣列中的 null 原樣保留（由驗證器拒絕）", `{"a":[1]}`, `{"a":[null]}`, `{"a":[null]}`},
		{"空 patch 等於複製", `{"a":{"b":1}}`, `{}`, `{"a":{"b":1}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergePatch(mustMap(t, c.target), mustMap(t, c.patch))
			if want := mustMap(t, c.want); !reflect.DeepEqual(got, want) {
				t.Errorf("MergePatch = %v，預期 %v", got, want)
			}
		})
	}
	if got := MergePatch(mustMap(t, `{"a":1}`), nil); !reflect.DeepEqual(got, mustMap(t, `{"a":1}`)) {
		t.Errorf("patch 為 nil：%v", got)
	}
	if got := MergePatch(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("兩邊都是 nil 應回傳空物件：%v", got)
	}
}
