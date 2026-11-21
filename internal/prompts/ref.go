package prompts

// Ref 是解析後的 prompt 引用。
type Ref struct {
	ID string
	// Version 為 0 代表 default（跟隨「設為預設」，D11）。
	Version int
}

// ParseRef 解析 "<id>"、"<id>@default"、"<id>@N"（N ≥ 1）。
// "x@0"、"x@"、"@1"、"x@abc" 是 *InvalidError。
//
// TODO(WP-3)：實作；契約階段回傳 ErrNotImplemented。
func ParseRef(s string) (Ref, error) {
	return Ref{}, ErrNotImplemented
}
