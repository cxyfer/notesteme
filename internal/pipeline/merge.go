package pipeline

// MergePatch 依 RFC 7396 把 patch 套到 target，回傳新的 map（不修改輸入，結果也不與輸入共用子結構）：
// patch 的值是 null 時刪除該鍵；兩邊都是物件時遞迴；其他（純量、陣列）整個取代。
//
// 這和 merge（逐層合併設定用）不同：merge 沒有 null 的語意；MergePatch 只用在 PATCH 端點
// （處理方案的 config、頻道覆寫），存好的各層不含 null（決策 D5）。
// target 為 nil 視為空物件。patch 裡新出現的巢狀物件，其中的 null 一樣會被丟掉（RFC 7396 的規定）。
func MergePatch(target, patch map[string]any) map[string]any {
	out := make(map[string]any, len(target)+len(patch))
	for k, v := range target {
		out[k] = cloneValue(v)
	}
	for k, pv := range patch {
		if pv == nil {
			delete(out, k)
			continue
		}
		if pm, ok := pv.(map[string]any); ok {
			tm, _ := out[k].(map[string]any) // target 那邊不是物件時視為 {}
			out[k] = MergePatch(tm, pm)
			continue
		}
		out[k] = cloneValue(pv)
	}
	return out
}

// cloneValue 深拷貝 JSON 形狀的值（map[string]any、[]any 以外的原樣回傳）。
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}
