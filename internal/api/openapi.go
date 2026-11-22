package api

import (
	"encoding/json"
	"net/http"
)

// OpenAPISnapshotVersion 是匯出 OpenAPI 快照時的版本號：固定的值讓快照不隨 binary 版本改變。
const OpenAPISnapshotVersion = "snapshot"

// OpenAPISpec 回傳與 GET /api/openapi.json 相同內容的 OpenAPI 3.1 文件（縮排的 JSON，結尾換行）。
//
// 不需要資料庫，也不需要啟動 server：只註冊路由。Events 用 no-op handler，
// 和正式環境一樣不產生 SSE 的 stub operation。前端用它產生 TypeScript 型別（web/client 的 gen:api）。
func OpenAPISpec() ([]byte, error) {
	d := Deps{
		Version: OpenAPISnapshotVersion,
		Events:  http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
	r := newHumaAPI(http.NewServeMux(), d)
	b, err := json.MarshalIndent(r.api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
