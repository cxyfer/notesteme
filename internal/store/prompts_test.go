package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestPromptsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	if _, err := w.Prompt(ctx, "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的 prompt：err = %v，預期 ErrNotFound", err)
	}

	created := clk.Now()
	p, v, err := w.CreatePrompt(ctx, "p", "correct", "body 1", "sha1", `["glossary"]`)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p" || p.Kind != "correct" || p.DefaultVersion != 1 || p.LatestVersion != 1 || !p.CreatedAt.Equal(created) {
		t.Errorf("Prompt = %+v", p)
	}
	if v.Version != 1 || v.Body != "body 1" || v.SHA256 != "sha1" || v.VariablesJSON != `["glossary"]` {
		t.Errorf("Version = %+v", v)
	}
	if _, _, err := w.CreatePrompt(ctx, "p", "correct", "x", "x", "[]"); !errors.Is(err, ErrPromptExists) {
		t.Errorf("重複建立：err = %v，預期 ErrPromptExists", err)
	}

	// 新增版本：latest 加 1，default 不動。
	clk.Advance(time.Minute)
	v2, err := w.AddPromptVersion(ctx, "p", "body 2", "sha2", "[]")
	if err != nil || v2.Version != 2 {
		t.Fatalf("AddPromptVersion = %+v, %v", v2, err)
	}
	got, _ := w.Prompt(ctx, "p")
	if got.DefaultVersion != 1 || got.LatestVersion != 2 || !got.UpdatedAt.Equal(clk.Now()) || !got.CreatedAt.Equal(created) {
		t.Errorf("新增版本後 Prompt = %+v", got)
	}
	if _, err := w.AddPromptVersion(ctx, "nope", "b", "s", "[]"); !errors.Is(err, ErrNotFound) {
		t.Errorf("對不存在的 prompt 新增版本：err = %v", err)
	}

	// 設為預設：版本必須存在。
	if err := w.SetPromptDefault(ctx, "p", 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的版本：err = %v，預期 ErrNotFound", err)
	}
	if err := w.SetPromptDefault(ctx, "p", 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Prompt(ctx, "p"); got.DefaultVersion != 2 {
		t.Errorf("DefaultVersion = %d，預期 2", got.DefaultVersion)
	}

	vs, err := s.Read().PromptVersions(ctx, "p")
	if err != nil || len(vs) != 2 || vs[0].Version != 1 || vs[1].Version != 2 {
		t.Fatalf("PromptVersions = %+v, %v", vs, err)
	}
	if _, err := s.Read().PromptVersion(ctx, "p", 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的版本：err = %v", err)
	}

	if _, _, err := w.CreatePrompt(ctx, "a", "translate", "b", "s", "[]"); err != nil {
		t.Fatal(err)
	}
	list, err := s.Read().Prompts(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "a" || list[1].ID != "p" {
		t.Fatalf("Prompts = %+v, %v", list, err)
	}

	// 刪除：版本跟著刪（ON DELETE CASCADE）。
	if ok, err := w.DeletePrompt(ctx, "p"); err != nil || !ok {
		t.Fatalf("DeletePrompt = %v, %v", ok, err)
	}
	if ok, err := w.DeletePrompt(ctx, "p"); err != nil || ok {
		t.Fatalf("重複 DeletePrompt = %v, %v，預期 false", ok, err)
	}
	if vs, _ := w.PromptVersions(ctx, "p"); len(vs) != 0 {
		t.Errorf("刪除後仍有 %d 個版本", len(vs))
	}
}

// 交易中途失敗時，新增版本一起回復：latest_version 不留下增量。
func TestAddPromptVersionRollsBack(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	if _, _, err := w.CreatePrompt(ctx, "p", "correct", "b", "s", "[]"); err != nil {
		t.Fatal(err)
	}
	err := s.Tx(ctx, func(tx *Repo) error {
		if _, err := tx.AddPromptVersion(ctx, "p", "b2", "s2", "[]"); err != nil {
			return err
		}
		return errors.New("中途失敗")
	})
	if err == nil {
		t.Fatal("預期交易失敗")
	}
	if p, _ := w.Prompt(ctx, "p"); p.LatestVersion != 1 {
		t.Errorf("交易回復後 LatestVersion = %d，預期 1", p.LatestVersion)
	}
	if vs, _ := w.PromptVersions(ctx, "p"); len(vs) != 1 {
		t.Errorf("交易回復後有 %d 個版本，預期 1", len(vs))
	}
}

// 00011 降級：兩張表都移除、其他資料保留；可以再次升級。
func TestMigration00011DownUp(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if !objectExists(t, s, "table", "prompts") || !objectExists(t, s, "table", "prompt_versions") {
		t.Fatal("升級後應有 prompts 與 prompt_versions")
	}
	if _, _, err := s.Write().CreatePrompt(ctx, "p", "correct", "b", "s", "[]"); err != nil {
		t.Fatal(err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, s.w, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	// 降到 10（不假設 00011 是最新的一支）。
	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatalf("降級：%v", err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 10 {
		t.Fatalf("降級後的版本 = %d, %v；預期 10", v, err)
	}
	if objectExists(t, s, "table", "prompts") || objectExists(t, s, "table", "prompt_versions") {
		t.Error("降級後不該留下 prompts 或 prompt_versions")
	}
	if !objectExists(t, s, "table", "items") {
		t.Error("降級不該動到 items")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("再次升級：%v", err)
	}
	if _, _, err := s.Write().CreatePrompt(ctx, "p", "correct", "b", "s", "[]"); err != nil {
		t.Errorf("再次升級後建立 prompt：%v", err)
	}
}
