package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaCacheDir(t *testing.T) {
	got := MediaCacheDir("/data", "0192f3a0-0000-7000-8000-000000000001")
	want := filepath.Join("/data", "media", "0192f3a0-0000-7000-8000-000000000001")
	if got != want {
		t.Errorf("MediaCacheDir = %q，預期 %q", got, want)
	}
}

func TestInTrashErrorIsErrInTrash(t *testing.T) {
	err := fmt.Errorf("包一層：%w", &InTrashError{ItemID: "x"})
	if !errors.Is(err, ErrInTrash) {
		t.Error("*InTrashError 應該等於 ErrInTrash")
	}
	var tr *InTrashError
	if !errors.As(err, &tr) || tr.ItemID != "x" {
		t.Errorf("errors.As = %v", tr)
	}
}

// DEL 的方法在實作前回傳 *NotImplementedError，API 對應成 501。
func TestDeleteMethodsAreNotImplemented(t *testing.T) {
	p := &Pipeline{}
	ctx := context.Background()
	calls := map[string]func() error{
		"Trash":           func() error { _, err := p.Trash(ctx, "x", TrashOptions{}); return err },
		"Restore":         func() error { _, err := p.Restore(ctx, "x"); return err },
		"TrashList":       func() error { _, err := p.TrashList(ctx); return err },
		"Purge":           func() error { return p.Purge(ctx, "x") },
		"PurgeAll":        func() error { _, err := p.PurgeAll(ctx); return err },
		"PurgeExpired":    func() error { _, err := p.PurgeExpired(ctx, time.Time{}, time.Hour); return err },
		"DeletionPreview": func() error { _, err := p.DeletionPreview(ctx, "x"); return err },
	}
	for name, call := range calls {
		var ni *NotImplementedError
		if err := call(); !errors.As(err, &ni) {
			t.Errorf("%s = %v，預期 *NotImplementedError", name, err)
		}
	}
}
