package prompts

// ParseVariables 找出內文中的 {{ name }} 變數（允許前後空白）。
// used 是依第一次出現順序、去掉重複的變數；undefined 是其中不在 Variables 內的。未關閉的 "{{" 回傳錯誤。
//
// TODO(WP-3)：實作；契約階段回傳 ErrNotImplemented。
func ParseVariables(body string) (used, undefined []string, err error) {
	return nil, nil, ErrNotImplemented
}
