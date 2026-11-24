package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// LIB-R27：同名（不分大小寫）回傳 ErrViewExists、到上限回傳 ErrViewLimit；列表依建立順序；刪除不存在的回傳 ErrNotFound。
func TestSavedViews(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC))
	st, err := Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	q := json.RawMessage(`{"platform":"youtube"}`)
	a, err := st.InsertSavedView(ctx, "Needs me", q, 2)
	if err != nil || a.ID == "" || !a.CreatedAt.Equal(clk.Now()) {
		t.Fatalf("Insert = %+v, %v", a, err)
	}
	if _, err := st.InsertSavedView(ctx, "NEEDS ME", q, 2); !errors.Is(err, ErrViewExists) {
		t.Fatalf("同名不分大小寫：%v", err)
	}
	clk.Advance(time.Second)
	b, err := st.InsertSavedView(ctx, "Bilibili", json.RawMessage(`{"platform":"bilibili"}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertSavedView(ctx, "third", q, 2); !errors.Is(err, ErrViewLimit) {
		t.Fatalf("上限：%v", err)
	}
	list, err := st.Read().SavedViews(ctx)
	if err != nil || len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID || string(list[1].Query) != `{"platform":"bilibili"}` {
		t.Fatalf("SavedViews = %+v, %v", list, err)
	}
	if err := st.Write().DeleteSavedView(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Write().DeleteSavedView(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("刪除不存在的：%v", err)
	}
	// 刪除後名稱可以再用。
	if _, err := st.InsertSavedView(ctx, "needs me", q, 2); err != nil {
		t.Fatal(err)
	}
}
