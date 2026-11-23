// Package postgres 是 Hub 的 Postgres backend（ADR-0030 第 2 點的正式環境）。
//
// M5-S0 只有佔位：Open 一律回 hubstore.ErrNotImplemented，`store.driver: postgres` 的 Hub 啟動失敗並說明原因。
// 實作時：查詢檔放在這個目錄（sqlc 的 postgresql engine，sqlc.yaml 與 SQLite 的分開），用 pgx，
// 並在 CI 加 Postgres service container 跑 hubstore/storetest（目前列為後續項目，見 docs/handoff.md）。
package postgres

import (
	"context"
	"fmt"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// Open 連到 dsn 並套用 migration（尚未實作）。
func Open(ctx context.Context, dsn string) (hubstore.Store, error) {
	return nil, fmt.Errorf("store.driver: postgres %w，目前只能用 sqlite（ADR-0030）", hubstore.ErrNotImplemented)
}
