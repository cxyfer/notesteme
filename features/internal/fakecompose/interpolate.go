package fakecompose

import (
	"fmt"
	"strings"
)

// interpolateValue 對文件中所有字串值做變數代入（key 不代入，與 compose 相同）。path 只用於錯誤訊息。
func interpolateValue(v any, path string, env map[string]string) (any, error) {
	switch x := v.(type) {
	case string:
		s, err := interpolate(x, env)
		if err != nil {
			return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("error while interpolating %s: %v", path, err)}
		}
		return s, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			r, err := interpolateValue(e, sub, env)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := interpolateValue(e, fmt.Sprintf("%s[%d]", path, i), env)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// interpolate 代入一個字串中的變數。語法見套件說明；`$` 後面不是變數名稱、`{` 或 `$` 時是格式錯誤，與 compose 相同。
// 必要變數（`:?`、`?`）沒有值時回傳 compose 風格的錯誤：required variable X is missing a value: msg。
func interpolate(s string, env map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("invalid interpolation format for %q: 結尾的 $ 要寫成 $$", s)
		}
		switch {
		case s[i] == '$':
			b.WriteByte('$')
			i++
		case s[i] == '{':
			end, err := closingBrace(s, i)
			if err != nil {
				return "", err
			}
			val, err := expandBraced(s[i+1:end], env)
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i = end + 1
		case isNameStart(s[i]):
			j := i
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			b.WriteString(env[s[i:j]])
			i = j
		default:
			return "", fmt.Errorf("invalid interpolation format for %q: 字面上的 $ 要寫成 $$", s)
		}
	}
	return b.String(), nil
}

// closingBrace 回傳 s[open]（`{`）對應的 `}` 的位置，巢狀的 ${…} 要成對。
func closingBrace(s string, open int) (int, error) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch {
		case s[i] == '{':
			depth++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("invalid interpolation format for %q: 缺少 }", s)
}

// expandBraced 展開 `${…}` 的內容 body（不含 `${` 與 `}`）。
func expandBraced(body string, env map[string]string) (string, error) {
	j := 0
	if len(body) > 0 && isNameStart(body[0]) {
		for j = 1; j < len(body) && isNameChar(body[j]); j++ {
		}
	}
	name, rest := body[:j], body[j:]
	if name == "" {
		return "", fmt.Errorf("invalid interpolation format for %q: 缺少變數名稱", "${"+body+"}")
	}
	val, set := env[name]
	if rest == "" {
		return val, nil
	}
	op, arg := "", ""
	for _, cand := range []string{":-", ":?", ":+", "-", "?", "+"} {
		if strings.HasPrefix(rest, cand) {
			op, arg = cand, rest[len(cand):]
			break
		}
	}
	if op == "" {
		return "", fmt.Errorf("invalid interpolation format for %q", "${"+body+"}")
	}
	// 有冒號的版本把空字串也視為沒有設定。
	missing := !set || (strings.HasPrefix(op, ":") && val == "")
	switch op {
	case ":-", "-":
		if missing {
			return interpolate(arg, env)
		}
		return val, nil
	case ":+", "+":
		if missing {
			return "", nil
		}
		return interpolate(arg, env)
	default: // ":?", "?"
		if missing {
			msg, err := interpolate(arg, env)
			if err != nil {
				return "", err
			}
			return "", fmt.Errorf("required variable %s is missing a value: %s", name, msg)
		}
		return val, nil
	}
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }
