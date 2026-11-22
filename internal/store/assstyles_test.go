package store

import (
	"context"
	"errors"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestAssStylesRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	if list, err := s.Read().AssStyles(ctx); err != nil || len(list) != 0 {
		t.Fatalf("新資料庫的自訂樣式 = %v，%v", list, err)
	}
	created, err := w.CreateAssStyle(ctx, "b", `{"x":1}`)
	if err != nil || created.Name != "b" || created.StyleJSON != `{"x":1}` || !created.CreatedAt.Equal(clk.Now()) {
		t.Fatalf("CreateAssStyle = %+v，%v", created, err)
	}
	if _, err := w.CreateAssStyle(ctx, "a", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateAssStyle(ctx, "b", `{}`); !errors.Is(err, ErrConflict) {
		t.Errorf("重複名稱 err = %v，預期 ErrConflict", err)
	}
	list, _ := s.Read().AssStyles(ctx)
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Errorf("AssStyles 應依名稱排序：%+v", list)
	}
	if got, err := w.UpdateAssStyle(ctx, "b", `{"y":2}`); err != nil || got.StyleJSON != `{"y":2}` {
		t.Errorf("UpdateAssStyle = %+v，%v", got, err)
	}
	if _, err := w.UpdateAssStyle(ctx, "ghost", `{}`); !errors.Is(err, ErrNotFound) {
		t.Errorf("更新不存在的樣式 err = %v", err)
	}
	if _, err := s.Read().AssStyle(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("查詢不存在的樣式 err = %v", err)
	}
}

// 00012 降級：ass_styles 移除、其他資料保留；可以再次升級。
func TestMigration00012DownUp(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if !objectExists(t, s, "table", "ass_styles") {
		t.Fatal("升級後應有 ass_styles")
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.w, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatalf("降級：%v", err)
	}
	if objectExists(t, s, "table", "ass_styles") || !objectExists(t, s, "table", "prompts") {
		t.Error("降級只應移除 ass_styles")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("再次升級：%v", err)
	}
	if _, err := s.Write().CreateAssStyle(ctx, "x", `{}`); err != nil {
		t.Errorf("再次升級後建立樣式：%v", err)
	}
}
