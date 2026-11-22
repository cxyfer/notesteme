// openapi 把 HTTP API 的 OpenAPI 3.1 文件寫到 stdout 或 -o 指定的檔案。
//
//	go run ./internal/api/cmd/openapi -o internal/api/openapi.json
//
// 不需要資料庫，也不啟動 server。快照由 internal/api 的 TestOpenAPISnapshot 檢查是否過期。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cxyfer/notesteme/internal/api"
)

func main() {
	out := flag.String("o", "", "輸出檔案（預設 stdout）")
	flag.Parse()
	b, err := api.OpenAPISpec()
	if err != nil {
		fmt.Fprintln(os.Stderr, "產生 OpenAPI 失敗：", err)
		os.Exit(1)
	}
	if *out == "" {
		_, err = os.Stdout.Write(b)
	} else {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "寫入失敗：", err)
		os.Exit(1)
	}
}
