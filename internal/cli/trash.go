package cli

// 這個檔案是刪除與垃圾桶的子命令（DEL-R13），由 DEL-B3 實作。
// 骨架先回報「還沒實作」，格式與 tokens、secrets 等尚未實作的子命令相同。

// parseItemsRm 解析 `items rm <ref>... [--delete-outputs] [--purge] [--yes]`。
func parseItemsRm(args []string) (*plan, error) {
	return unimplemented("items rm"), nil
}

// parseItemsRestore 解析 `items restore <ref>...`。
func parseItemsRestore(args []string) (*plan, error) {
	return unimplemented("items restore"), nil
}

// parseTrash 解析 `trash ls` 與 `trash empty [--yes]`。
func parseTrash(args []string) (*plan, error) {
	return unimplemented("trash"), nil
}
