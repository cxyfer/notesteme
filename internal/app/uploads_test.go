package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func mustMkdirFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// 孤兒的 uploads/<uuid>（資料庫中沒有這個 id 的影音）被刪除；有影音的（含垃圾桶中的）與非 UUID 名稱的保留。
func TestCleanOrphanUploads(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(ctx, data, clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	live, _, err := st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: "local", ExternalID: "a.m4a", SourcePath: "x", ContentSHA256: "aa", Origin: store.OriginUpload})
	if err != nil {
		t.Fatal(err)
	}
	trashed, _, err := st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: "local", ExternalID: "b.m4a", SourcePath: "x", ContentSHA256: "bb", Origin: store.OriginUpload})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Write().TrashItem(ctx, trashed.ID); err != nil {
		t.Fatal(err)
	}
	orphan := store.NewID()
	uploads := filepath.Join(data, "uploads")
	mustMkdirFile(t, filepath.Join(uploads, live.ID), "a.m4a")
	mustMkdirFile(t, filepath.Join(uploads, trashed.ID), "b.m4a")
	mustMkdirFile(t, filepath.Join(uploads, orphan), "c.m4a")
	mustMkdirFile(t, filepath.Join(uploads, "not-a-uuid"), "d.m4a")
	// 非目錄的項目、指向目錄的 symlink 都不處理（不跟隨 symlink）。
	other := t.TempDir()
	mustMkdirFile(t, other, "keep.txt")
	linkID := store.NewID()
	if err := os.Symlink(other, filepath.Join(uploads, linkID)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, store.NewID()), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanOrphanUploads(ctx, st, data, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	if exists(filepath.Join(uploads, orphan)) {
		t.Error("孤兒目錄應該被刪除")
	}
	for _, keep := range []string{live.ID, trashed.ID, "not-a-uuid"} {
		if !exists(filepath.Join(uploads, keep, map[string]string{live.ID: "a.m4a", trashed.ID: "b.m4a", "not-a-uuid": "d.m4a"}[keep])) {
			t.Errorf("%s 應該保留", keep)
		}
	}
	if !exists(filepath.Join(uploads, linkID)) || !exists(filepath.Join(other, "keep.txt")) {
		t.Error("symlink 不可跟隨或刪除")
	}
}

// 沒有 uploads 目錄時不報錯、也不建立它。
func TestCleanOrphanUploadsMissingDir(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(ctx, data, clock.NewFake(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cleanOrphanUploads(ctx, st, data, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if exists(filepath.Join(data, "uploads")) {
		t.Error("不該建立 uploads")
	}
}

// standalone 開啟資料目錄（取得鎖）之後會清掉孤兒。
func TestStandaloneOpenRemovesOrphanUploads(t *testing.T) {
	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	orphan := filepath.Join(data, "uploads", store.NewID())
	mustMkdirFile(t, orphan, "talk.m4a")
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	if exists(orphan) {
		t.Error("開啟之後孤兒目錄應該被刪除")
	}
}
