// Package storetest 是 hubstore.Store 的共用測試：SQLite 與 Postgres 兩種 backend 都要通過（ADR-0030 第 2 點）。
// 各切片加入 Store 的方法時，在這裡加對應的測試，而不是只寫在某一個 backend 的測試中。
package storetest

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// Run 對 open 回傳的 Store 執行全部的共用測試；open 每次回傳一個新的、空的 Store。
func Run(t *testing.T, open func(t *testing.T) hubstore.Store) {
	t.Run("Empty", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		if err := s.CheckWritable(ctx); err != nil {
			t.Fatalf("CheckWritable：%v", err)
		}
		n, err := s.CountUsers(ctx)
		if err != nil || n != 0 {
			t.Fatalf("CountUsers = %d, %v，預期 0", n, err)
		}
		v, err := s.SchemaVersion(ctx)
		if err != nil || v < 1 {
			t.Fatalf("SchemaVersion = %d, %v，預期已套用 migration", v, err)
		}
	})
}
