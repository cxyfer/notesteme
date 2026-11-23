package hubclient

import (
	"bytes"
	"encoding/json"
	"strings"
)

// redactedToken 取代 Hub 回應中出現的 token（HSHR 待決問題 S18）。
const redactedToken = "[已遮蔽]"

// scrubToken 把 Hub 回應（不可信）中出現的 token 換成 redactedToken：惡意的 Hub 可能把收到的 token
// 放進錯誤訊息、上傳者名稱等會轉給呼叫端或存進來源紀錄的文字。JSON 先解碼再比對，\u 跳脫的寫法也擋得到；
// 不是 JSON 時直接比對 bytes。
func scrubToken(raw []byte, tok string) []byte {
	if tok == "" || !mayContainToken(raw, tok) {
		return raw
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil || dec.More() {
		return bytes.ReplaceAll(raw, []byte(tok), []byte(redactedToken))
	}
	v = scrubValue(v, tok)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return bytes.ReplaceAll(raw, []byte(tok), []byte(redactedToken))
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// mayContainToken 是便宜的預先檢查：原文含 token，或含 \u 跳脫（解碼後才可能出現 token）。
func mayContainToken(raw []byte, tok string) bool {
	return bytes.Contains(raw, []byte(tok)) || bytes.Contains(raw, []byte(`\u`))
}

func scrubValue(v any, tok string) any {
	switch t := v.(type) {
	case string:
		return strings.ReplaceAll(t, tok, redactedToken)
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], tok)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[strings.ReplaceAll(k, tok, redactedToken)] = scrubValue(x, tok)
		}
		return out
	}
	return v
}

// containsToken 回報內容（解碼後的任何字串，或原始 bytes）有沒有 token。版本內容有 sha256，不能改寫，
// 含有 token 時整份拒絕（內容驗證失敗）。
func containsToken(raw []byte, tok string) bool {
	if tok == "" {
		return false
	}
	if bytes.Contains(raw, []byte(tok)) {
		return true
	}
	if !bytes.Contains(raw, []byte(`\u`)) {
		return false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return valueHasToken(v, tok)
}

func valueHasToken(v any, tok string) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, tok)
	case []any:
		for _, x := range t {
			if valueHasToken(x, tok) {
				return true
			}
		}
	case map[string]any:
		for k, x := range t {
			if strings.Contains(k, tok) || valueHasToken(x, tok) {
				return true
			}
		}
	}
	return false
}
