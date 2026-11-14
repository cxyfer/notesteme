package cli

// parseTools 解析 `notesteme tools update [--pin <version>] [--unpin]`（OPS-R6、R7）。
// 骨架：還沒有實作，參數不檢查，一律回報 not_implemented；由 OPS-B 實作。
func parseTools(_ []string) (*plan, error) {
	return unimplemented("tools"), nil
}
