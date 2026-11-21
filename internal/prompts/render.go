package prompts

// Render 把內文中的 {{ name }} 換成 vars 的值（給 LLM 切片執行時使用）。內文用到 vars 沒有的變數時回傳錯誤。
//
// TODO(WP-3)：實作；契約階段回傳 ErrNotImplemented。
func Render(body string, vars map[string]string) (string, error) {
	return "", ErrNotImplemented
}
