package cli

// parseBackup 解析 `notesteme backup [--out <dir>]`（OPS-R1）。
// 骨架：還沒有實作，參數不檢查，一律回報 not_implemented；由 OPS-A 實作。
func parseBackup(_ []string) (*plan, error) {
	return unimplemented("backup"), nil
}
