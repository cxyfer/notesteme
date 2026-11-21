package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pathguard"
)

func TestFileGuard(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Data:       filepath.Join(tmp, "data"),
		Inbox:      filepath.Join(tmp, "inbox"),
		MediaRoots: []string{filepath.Join(tmp, "media")},
	}
	in := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }

	serve := (&Server{cfg: cfg}).fileGuard()
	for _, p := range []string{in("inbox", "a.mp4"), in("media", "b.mp4"), in("data", "uploads", "id", "c.m4a")} {
		if err := serve.Check(p); err != nil {
			t.Errorf("serve 應允許 %s：%v", p, err)
		}
	}
	for _, p := range []string{in("elsewhere", "d.mp4"), in("data", "tmp", "x"), in("data", "app.db")} {
		if err := serve.Check(p); !errors.Is(err, pathguard.ErrOutsideRoots) {
			t.Errorf("serve 應拒絕 %s：%v", p, err)
		}
	}

	standalone := (&Server{cfg: cfg, unrestrictedFiles: true}).fileGuard()
	if _, err := standalone.Resolve(in("elsewhere", "d.mp4")); err != nil {
		t.Errorf("standalone 不受限制：%v", err)
	}
}
