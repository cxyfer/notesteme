package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), clock.NewFake(time.Unix(1_700_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenCreatesThenUnwraps(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	first, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatalf("same master key: %v", err)
	}
	if !bytes.Equal(first.dataKey, again.dataKey) || len(first.dataKey) != keyLen {
		t.Error("同一把 master key 應該解出同一把資料金鑰")
	}
}

func TestWrongMasterKey(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	if _, err := Open(ctx, st, "key-one", TestKDF); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, st, "key-two", TestKDF)
	if !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"master key 不正確，無法解開資料金鑰。", "notesteme secrets reset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %s", want, err)
		}
	}
}

func TestBase64MasterKeyUsedDirectly(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	mk := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if _, err := Open(ctx, st, mk, TestKDF); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, st, mk, TestKDF); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, st, "something else", TestKDF); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestWrappedKeyHidesPlaintext(t *testing.T) {
	dk := bytes.Repeat([]byte{0xAB}, keyLen)
	w, err := wrap("key-one", dk, TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w, base64.RawStdEncoding.EncodeToString(dk)) {
		t.Error("wrapped key contains the plaintext data key")
	}
}

func TestUnwrapRejectsTamperedParameters(t *testing.T) {
	w, _ := wrap("key-one", bytes.Repeat([]byte{1}, keyLen), TestKDF)
	bad := strings.Replace(w, "m=8,", "m=999999999,", 1)
	if _, err := unwrap("key-one", bad); err == nil || errors.Is(err, ErrWrongMasterKey) {
		t.Errorf("err = %v, want a format error", err)
	}
	if _, err := unwrap("key-one", "garbage"); err == nil {
		t.Error("garbage should fail")
	}
}

func TestProductionKDFNotLowered(t *testing.T) {
	if ProductionKDF.Memory < 19*1024 || ProductionKDF.Time < 2 {
		t.Errorf("ProductionKDF too weak: %+v", ProductionKDF)
	}
}
