// Package hubstore 是 Hub 的儲存介面（ADR-0030 第 2 點）：SQLite 是參考 backend（驗收測試、開發、小型自架），
// Postgres 是正式環境。兩種實作共用這個介面與 storetest 的單元測試；domain 型別放在這裡。
//
// 實作在子套件：sqlite（goose migration 與 sqlc，從 00001 起獨立編號，不和 Client 的 internal/store 共用）、
// postgres（M5-S0 只有佔位，CI 加 service container 之後由後續切片實作）。
// 資料表的方向見 ADR-0031 第 5 點；M5-S0 的 00001 只建 users 與 identities，其餘由各切片加 migration。
package hubstore

import (
	"context"
	"errors"
)

// ErrNotFound 表示查不到資料。
var ErrNotFound = errors.New("not found")

// ErrNotImplemented 表示這個 backend 還沒有實作（M5-S0 的 postgres）。
var ErrNotImplemented = errors.New("尚未實作")

// Store 是 Hub 的儲存層；方法由各切片依需要加入，兩種 backend 都要實作。
type Store interface {
	// CheckWritable 確認可以取得寫入鎖（/readyz），不寫入任何資料。
	CheckWritable(ctx context.Context) error
	// CountUsers 回傳帳號總數（doctor 與驗收的 `the number of Hub accounts is`）。
	CountUsers(ctx context.Context) (int64, error)
	// SchemaVersion 回傳已套用的 migration 版本（doctor 與 migrate 顯示）。
	SchemaVersion(ctx context.Context) (int64, error)
	Close() error
}
