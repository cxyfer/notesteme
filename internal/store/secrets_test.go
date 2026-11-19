package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestSecretsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	if _, err := w.Secret(ctx, "cookie", "bilibili"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的 secret：err = %v，預期 ErrNotFound", err)
	}

	created := clk.Now()
	if err := w.UpsertSecret(ctx, "cookie", "bilibili", []byte("nonce-1"), []byte("cipher-1")); err != nil {
		t.Fatal(err)
	}
	got, err := w.Secret(ctx, "cookie", "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "cookie" || got.Name != "bilibili" || !bytes.Equal(got.Nonce, []byte("nonce-1")) ||
		!bytes.Equal(got.Ciphertext, []byte("cipher-1")) || !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(created) {
		t.Fatalf("Secret = %+v", got)
	}

	// 覆寫：換密文與 updated_at，created_at 不變。
	clk.Advance(time.Minute)
	if err := w.UpsertSecret(ctx, "cookie", "bilibili", []byte("nonce-2"), []byte("cipher-2")); err != nil {
		t.Fatal(err)
	}
	got, _ = w.Secret(ctx, "cookie", "bilibili")
	if !bytes.Equal(got.Ciphertext, []byte("cipher-2")) || !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(clk.Now()) {
		t.Errorf("覆寫後 Secret = %+v", got)
	}

	if err := w.UpsertSecret(ctx, "cookie", "youtube", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	list, err := s.Read().ListSecrets(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "bilibili" || list[1].Name != "youtube" || list[0].Kind != "cookie" {
		t.Fatalf("ListSecrets = %+v, %v", list, err)
	}
	// SecretInfo 沒有密文欄位（編譯期保證）；這裡確認時間有帶出來。
	if !list[0].UpdatedAt.Equal(clk.Now()) {
		t.Errorf("list[0].UpdatedAt = %v", list[0].UpdatedAt)
	}

	if ok, err := w.DeleteSecret(ctx, "cookie", "bilibili"); err != nil || !ok {
		t.Fatalf("DeleteSecret = %v, %v", ok, err)
	}
	if ok, err := w.DeleteSecret(ctx, "cookie", "bilibili"); err != nil || ok {
		t.Fatalf("重複 DeleteSecret = %v, %v，預期 false", ok, err)
	}
	if _, err := w.Secret(ctx, "cookie", "bilibili"); !errors.Is(err, ErrNotFound) {
		t.Errorf("刪除後 err = %v", err)
	}
}
