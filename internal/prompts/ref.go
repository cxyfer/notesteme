package prompts

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/validate"
)

// idRE 是 prompt id 的格式：不可以含 "@"（用來分隔版本）。
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)

// Ref 是解析後的 prompt 引用。
type Ref struct {
	ID string
	// Version 為 0 代表 default（跟隨「設為預設」，D11）。
	Version int
}

// refError 是引用格式錯誤；路徑是 prompt（處理方案的 stages.<s>.prompt 由呼叫端加上前綴）。
func refError(s, why string) *InvalidError {
	msg := fmt.Sprintf("prompt 引用「%s」格式不正確：%s", s, why)
	return &InvalidError{Msg: msg, Fields: []validate.FieldError{{Path: "prompt", Message: msg}}}
}

// ParseRef 解析 "<id>"、"<id>@default"、"<id>@N"（N ≥ 1）。
// "x@0"、"x@"、"@1"、"x@abc" 是 *InvalidError。
func ParseRef(s string) (Ref, error) {
	id, ver, hasVer := strings.Cut(s, "@")
	if !idRE.MatchString(id) {
		return Ref{}, refError(s, "id 要以小寫英文字母或數字開頭，只能用小寫英文字母、數字、「.」與「-」，最長 64 個字元")
	}
	if !hasVer || ver == "default" {
		return Ref{ID: id}, nil
	}
	if ver == "" || strings.Trim(ver, "0123456789") != "" || ver[0] == '0' {
		return Ref{}, refError(s, "版本要寫成 default 或 1 以上的整數")
	}
	n, err := strconv.Atoi(ver)
	if err != nil {
		return Ref{}, refError(s, "版本號太大")
	}
	return Ref{ID: id, Version: n}, nil
}
