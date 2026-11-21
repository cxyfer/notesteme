package prompts

import (
	"fmt"
	"strings"
)

// Render 把內文中的 {{ name }} 換成 vars 的值（給 LLM 切片執行時使用）。內文用到 vars 沒有的變數時回傳錯誤。
// 只做一次替換：變數的值裡出現的 "{{" 不會再被展開。
func Render(body string, vars map[string]string) (string, error) {
	parts, err := tokenize(body)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if !p.isVar {
			b.WriteString(p.text)
			continue
		}
		v, ok := vars[p.text]
		if !ok {
			return "", fmt.Errorf("變數 %s 沒有提供值", p.text)
		}
		b.WriteString(v)
	}
	return b.String(), nil
}
