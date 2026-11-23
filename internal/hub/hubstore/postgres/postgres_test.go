package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// M5-S0：postgres 還沒有實作，Open 要明確失敗（不是回傳 nil 的 Store）。
func TestOpenNotImplemented(t *testing.T) {
	s, err := Open(context.Background(), "postgres://x")
	if s != nil || !errors.Is(err, hubstore.ErrNotImplemented) {
		t.Fatalf("Open = %v, %v", s, err)
	}
}
