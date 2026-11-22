// Package webclient 嵌入 Client UI 的 Vite 建置產物（web/client/dist，ADR-0008）。
//
// dist 只 commit 一個 .gitkeep：沒有 Node、沒有建置前端時，Go 的建置與測試照常通過，
// UI 回應 internal/webui 的 Placeholder。`npm run build` 之後重新編譯，binary 就會包含真正的 UI。
// go:embed 不能指向上層目錄，所以這個檔案放在 web/client 裡。
package webclient

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS 回傳以 dist 為根目錄的檔案系統（含 index.html 與 assets/；沒有建置時只有 .gitkeep）。
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist 是編譯時嵌入的固定目錄，不會失敗
	}
	return sub
}
