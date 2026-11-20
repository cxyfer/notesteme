package store

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

func TestAPITokenLifecycle(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)

	exp := clk.Now().Add(time.Hour)
	created, err := s.CreateAPIToken(ctx, "agent", "nst_abcd", []string{"read", "agent"}, "hash-1", &exp)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID[14] != '7' {
		t.Errorf("id = %q, want UUIDv7", created.ID)
	}
	if created.Prefix != "nst_abcd" || !slices.Equal(created.Scopes, []string{"read", "agent"}) {
		t.Errorf("created = %+v", created)
	}

	// 同名有效 token 不可重複建立。
	if _, err := s.CreateAPIToken(ctx, "agent", "nst_efgh", []string{"read"}, "hash-2", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active name err = %v, want ErrConflict", err)
	}

	got, err := s.APITokenByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("APITokenByHash: %v", err)
	}
	if got.ID != created.ID || got.LastUsedAt != nil || !slices.Equal(got.Scopes, []string{"read", "agent"}) {
		t.Errorf("got = %+v", got)
	}

	// TouchAPIToken 更新 last_used_at。
	clk.Advance(10 * time.Minute)
	if err := s.TouchAPIToken(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.APITokenByHash(ctx, "hash-1")
	if err != nil || got.LastUsedAt == nil || !got.LastUsedAt.Equal(clk.Now()) {
		t.Fatalf("after touch = %+v, %v", got, err)
	}

	// 到期後查詢回傳 ErrNotFound。
	clk.Advance(time.Hour)
	if _, err := s.APITokenByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token err = %v, want ErrNotFound", err)
	}

	// 到期的 token 不佔用名稱：可以直接用同名重建，不需要先撤銷（AUTH-R9）。
	// 依名稱撤銷只比對有效的那一筆，到期的那筆維持「到期」而不是「撤銷」。
	renewed, err := s.CreateAPIToken(ctx, "agent", "nst_abce", []string{"read"}, "hash-renewed", nil)
	if err != nil {
		t.Fatalf("reuse name after expiry: %v", err)
	}
	if _, err := s.CreateAPIToken(ctx, "agent", "nst_abcf", []string{"read"}, "hash-dup", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate valid name err = %v, want ErrConflict", err)
	}
	if ok, err := s.RevokeAPIToken(ctx, "agent"); err != nil || !ok {
		t.Fatalf("RevokeAPIToken(agent) = %v, %v", ok, err)
	}
	all, err := s.ListAPITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range all {
		switch tok.ID {
		case created.ID:
			if tok.RevokedAt != nil {
				t.Errorf("到期的 token 不該被依名稱撤銷：%+v", tok)
			}
		case renewed.ID:
			if tok.RevokedAt == nil {
				t.Errorf("有效的同名 token 應該被撤銷：%+v", tok)
			}
		}
	}

	// 建立不過期的 token，測試撤銷與名稱重用。
	t2, err := s.CreateAPIToken(ctx, "reusable", "nst_1111", []string{"ingest"}, "hash-3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RevokeAPIToken(ctx, "reusable"); err != nil || !ok {
		t.Fatalf("RevokeAPIToken by name = %v, %v", ok, err)
	}
	if ok, err := s.RevokeAPIToken(ctx, "reusable"); err != nil || ok {
		t.Fatalf("second RevokeAPIToken = %v, %v; want false", ok, err)
	}
	if _, err := s.APITokenByHash(ctx, "hash-3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token err = %v, want ErrNotFound", err)
	}

	// 撤銷後可用相同名稱重建。
	t3, err := s.CreateAPIToken(ctx, "reusable", "nst_2222", []string{"admin"}, "hash-4", nil)
	if err != nil {
		t.Fatalf("reuse name after revoke: %v", err)
	}
	if ok, err := s.RevokeAPIToken(ctx, t3.ID); err != nil || !ok {
		t.Fatalf("RevokeAPIToken by id = %v, %v", ok, err)
	}

	// ExpireAPITokenByName 測試輔助。
	t4, err := s.CreateAPIToken(ctx, "temp", "nst_3333", []string{"read"}, "hash-5", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ExpireAPITokenByName(ctx, "temp", clk.Now()); err != nil || !ok {
		t.Fatalf("ExpireAPITokenByName = %v, %v", ok, err)
	}
	if _, err := s.APITokenByHash(ctx, "hash-5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired-by-name token err = %v, want ErrNotFound", err)
	}

	// ListAPITokens 依建立時間由新到舊列出全部。
	list, err := s.ListAPITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("len(list) = %d, want 5", len(list))
	}
	if list[0].ID != t4.ID || list[1].ID != t3.ID || list[2].ID != t2.ID || list[3].ID != renewed.ID || list[4].ID != created.ID {
		t.Errorf("unexpected list order: %+v", list)
	}
}

func TestRotateWrappedKeyAndResetSecrets(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)

	if err := s.RotateWrappedKey(ctx, "v1$new"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate without active key err = %v, want ErrNotFound", err)
	}

	ok, err := s.InsertWrappedKey(ctx, "v1$first")
	if err != nil || !ok {
		t.Fatalf("InsertWrappedKey = %v, %v", ok, err)
	}
	clk.Advance(time.Minute)
	if err := s.RotateWrappedKey(ctx, "v1$rotated"); err != nil {
		t.Fatalf("RotateWrappedKey: %v", err)
	}
	got, ok, err := s.ActiveWrappedKey(ctx)
	if err != nil || !ok || got != "v1$rotated" {
		t.Fatalf("ActiveWrappedKey = %q, %v, %v; want v1$rotated", got, ok, err)
	}

	var totalKeys, retiredKeys int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM encryption_keys`).Scan(&totalKeys); err != nil {
		t.Fatal(err)
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM encryption_keys WHERE retired_at IS NOT NULL`).Scan(&retiredKeys); err != nil {
		t.Fatal(err)
	}
	if totalKeys != 1 || retiredKeys != 0 {
		t.Errorf("totalKeys=%d retiredKeys=%d, want 1 and 0 (the old wrapping must not be kept)", totalKeys, retiredKeys)
	}

	// 寫入 secret 與非 secret 資料（settings、item、api_token），reset 只清 secrets 與舊金鑰。
	w := s.Write()
	if err := w.UpsertSecret(ctx, "provider_api_key", "groq", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "language", `{"targets":["zh-Hant-TW"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIToken(ctx, "keep-me", "nst_keep", []string{"read"}, "hash-keep", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.InsertItemIfAbsent(ctx, NewItem{
		Platform: "youtube", ExternalID: "abc123xxxxx", UntilStage: "normalize", URL: "https://youtube.com/watch?v=abc123xxxxx",
	}); err != nil {
		t.Fatal(err)
	}

	clk.Advance(time.Minute)
	if err := s.ResetSecretsAndKey(ctx, "v1$reset"); err != nil {
		t.Fatalf("ResetSecretsAndKey: %v", err)
	}

	got, ok, err = s.ActiveWrappedKey(ctx)
	if err != nil || !ok || got != "v1$reset" {
		t.Fatalf("ActiveWrappedKey after reset = %q, %v, %v", got, ok, err)
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM encryption_keys`).Scan(&totalKeys); err != nil {
		t.Fatal(err)
	}
	if totalKeys != 1 {
		t.Errorf("totalKeys after reset = %d, want 1", totalKeys)
	}
	if list, err := s.Read().ListSecrets(ctx); err != nil || len(list) != 0 {
		t.Errorf("secrets after reset = %+v, %v; want empty", list, err)
	}
	if v, ok, err := s.GetSetting(ctx, "language"); err != nil || !ok || v == "" {
		t.Errorf("setting lost after reset: %q, %v, %v", v, ok, err)
	}
	if _, err := s.APITokenByHash(ctx, "hash-keep"); err != nil {
		t.Errorf("api_token lost after reset: %v", err)
	}
	if items, err := s.Read().ListItems(ctx); err != nil || len(items) != 1 {
		t.Errorf("items after reset = %d, %v; want 1", len(items), err)
	}
}

// 輪替後，舊 master key 包裝的資料金鑰不能留在資料庫檔案或 WAL 的任何位元組中（SEC-R8）。
func TestRotateWrappedKeyLeavesNoOldWrappingOnDisk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const old = "v1$OLD-WRAPPING-MARKER-0123456789abcdef"
	if ok, err := s.InsertWrappedKey(ctx, old); err != nil || !ok {
		t.Fatalf("InsertWrappedKey = %v, %v", ok, err)
	}
	// 讓舊的包裝先落到主檔，再輪替：檢查的是主檔中被覆寫的位置，而不只是 WAL。
	if _, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateWrappedKey(ctx, "v1$new"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{DBFileName, DBFileName + "-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(old)) {
			t.Errorf("%s 仍含有舊的包裝", name)
		}
	}
}

// 名稱剛好是另一個 token 的 id：依 id 撤銷只撤銷那一筆，不連帶撤銷以它命名的 token。
func TestRevokeAPITokenPrefersExactID(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	target, err := s.CreateAPIToken(ctx, "target", "nst_aaaa", []string{"read"}, "hash-target", nil)
	if err != nil {
		t.Fatal(err)
	}
	namedLikeID, err := s.CreateAPIToken(ctx, target.ID, "nst_bbbb", []string{"read"}, "hash-named", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RevokeAPIToken(ctx, target.ID); err != nil || !ok {
		t.Fatalf("RevokeAPIToken = %v, %v", ok, err)
	}
	if _, err := s.APITokenByHash(ctx, "hash-target"); !errors.Is(err, ErrNotFound) {
		t.Errorf("依 id 指定的 token 應該已撤銷：%v", err)
	}
	if got, err := s.APITokenByHash(ctx, "hash-named"); err != nil || got.ID != namedLikeID.ID {
		t.Errorf("以該 id 命名的 token 不該被撤銷：%+v, %v", got, err)
	}
	// 再用同一個字串撤銷：id 已撤銷，這次才比對名稱。
	if ok, err := s.RevokeAPIToken(ctx, target.ID); err != nil || !ok {
		t.Fatalf("第二次 RevokeAPIToken = %v, %v", ok, err)
	}
	if _, err := s.APITokenByHash(ctx, "hash-named"); !errors.Is(err, ErrNotFound) {
		t.Errorf("第二次應該撤銷以該 id 命名的 token：%v", err)
	}
}
