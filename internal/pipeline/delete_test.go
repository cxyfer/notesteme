package pipeline

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
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
