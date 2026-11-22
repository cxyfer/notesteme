package prompts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// varNameRE 是 {{ }} 之間合法的變數名稱格式；不符合的（含空白、換行或空的）視為無法解析，不是「未定義的變數」。
var varNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// part 是內文切開後的一段：純文字，或一個 {{變數}}。
type part struct {
	text  string // 純文字；isVar 時是變數名稱（已去掉前後空白）
	isVar bool
}

// tokenize 把內文切成純文字與 {{ name }} 變數。未關閉的 "{{" 與格式不正確的名稱回傳錯誤。
func tokenize(body string) ([]part, error) {
	var parts []part
	for {
		i := strings.Index(body, "{{")
		if i < 0 {
			break
		}
		end := strings.Index(body[i+2:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("有未關閉的「{{」（第 %d 個字元之後沒有對應的「}}」）", len([]rune(body[:i]))+1)
		}
		name := strings.TrimSpace(body[i+2 : i+2+end])
		if !varNameRE.MatchString(name) {
			return nil, fmt.Errorf("「{{%s}}」不是有效的變數寫法；變數名稱只能用英文字母、數字與底線", body[i+2:i+2+end])
		}
		if i > 0 {
			parts = append(parts, part{text: body[:i]})
		}
		parts = append(parts, part{text: name, isVar: true})
		body = body[i+2+end+2:]
	}
	if body != "" {
		parts = append(parts, part{text: body})
	}
	return parts, nil
}

// ParseVariables 找出內文中的 {{ name }} 變數（允許前後空白）。
// used 是依第一次出現順序、去掉重複的變數；undefined 是其中不在 Variables 內的。未關閉的 "{{" 回傳錯誤。
func ParseVariables(body string) (used, undefined []string, err error) {
	parts, err := tokenize(body)
	if err != nil {
		return nil, nil, err
	}
	used, undefined = []string{}, []string{}
	for _, p := range parts {
		if !p.isVar || slices.Contains(used, p.text) {
			continue
		}
		used = append(used, p.text)
		if !slices.Contains(Variables, p.text) {
			undefined = append(undefined, p.text)
		}
	}
	return used, undefined, nil
}
