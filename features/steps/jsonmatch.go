package steps

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// anyValue 是 JSON 子集比對中「欄位存在即可」的標記值。
const anyValue = "*"

// jsonSubset 確認 got 的 JSON 包含 want 描述的全部內容（steps.md：子集比對）：
//   - 物件：want 的每個欄位都要出現在 got，且值符合；got 可以有多餘的欄位。
//   - 陣列：依序比對 want 的每個元素與 got 的前 N 個元素；got 可以有更多元素。
//   - 字串 "*"：欄位存在即可（值可以是任何東西，包括 null）。
//   - 其他純量：必須相等。
func jsonSubset(want, got []byte) error {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return fmt.Errorf("預期內容不是合法的 JSON：%w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return fmt.Errorf("回應不是合法的 JSON：%w\n回應：%s", err, snippet(got))
	}
	return subset("$", w, g)
}

// configSubset 比對生效設定（steps.md 4.11）：規則與 jsonSubset 相同，但陣列必須完全相等（長度與每個元素），
// 因為設定的陣列是整個取代，不是合併；允許 got 有多餘的元素，就驗證不到「陣列整個取代」。
func configSubset(want, got []byte) error {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return fmt.Errorf("預期內容不是合法的 JSON：%w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return fmt.Errorf("生效設定不是合法的 JSON：%w\n內容：%s", err, snippet(got))
	}
	return subsetMode("$", w, g, true)
}

func subset(path string, want, got any) error { return subsetMode(path, want, got, false) }

// subsetMode 是子集比對的本體；exactArrays 為 true 時，陣列長度必須相等。
func subsetMode(path string, want, got any, exactArrays bool) error {
	if s, ok := want.(string); ok && s == anyValue {
		return nil
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s：預期是物件，實際是 %s", path, describe(got))
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, ok := g[k]
			if !ok {
				return fmt.Errorf("%s.%s：回應沒有這個欄位", path, k)
			}
			if err := subsetMode(path+"."+k, w[k], gv, exactArrays); err != nil {
				return err
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Errorf("%s：預期是陣列，實際是 %s", path, describe(got))
		}
		if exactArrays && len(g) != len(w) {
			return fmt.Errorf("%s：預期剛好 %d 個元素，實際有 %d 個", path, len(w), len(g))
		}
		if len(g) < len(w) {
			return fmt.Errorf("%s：預期至少 %d 個元素，實際只有 %d 個", path, len(w), len(g))
		}
		for i := range w {
			if err := subsetMode(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], exactArrays); err != nil {
				return err
			}
		}
		return nil
	default:
		if !reflect.DeepEqual(want, got) {
			return fmt.Errorf("%s：預期 %s，實際 %s", path, describe(want), describe(got))
		}
		return nil
	}
}

// describe 回傳值的簡短描述，用於錯誤訊息。
func describe(v any) string {
	switch v.(type) {
	case map[string]any:
		return "物件"
	case []any:
		return "陣列"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(string(b))
}
