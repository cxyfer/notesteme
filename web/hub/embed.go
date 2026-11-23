// Package webhub 嵌入 Hub UI 的 Vite 建置產物（web/hub/dist，M5-S9／UI-6）。
//
// 與 web/client 相同：dist 只 commit 一個 .gitkeep，沒有建置前端時 Go 的建置與測試照常通過，
// Hub 回應 internal/hub 的 placeholder。`npm run build` 之後重新編譯，notesteme-hub 就會包含真正的 UI。
package webhub

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS 回傳以 dist 為根目錄的檔案系統。
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist 是編譯時嵌入的固定目錄，不會失敗
	}
	return sub
}
