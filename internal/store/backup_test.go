package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// SnapshotTo：Store 開著（讀取連線是 query_only）也能產生一致的快照；0600、沒有暫存檔、內容是目前的資料。
func TestSnapshotToWhileOpen(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "sub", "snap.db")
	if err := s.SnapshotTo(ctx, dest); err != nil {
		t.Fatal(err)
	}
	// 快照之後的寫入不影響快照。
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %o，預期 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Errorf("目錄中有多餘的檔案：%v", entries)
	}

	dir := filepath.Dir(dest)
	if err := os.Rename(dest, filepath.Join(dir, DBFileName)); err != nil {
		t.Fatal(err)
	}
	snap, err := openWith(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if v, _, _ := snap.GetSetting(ctx, "k"); v != "v1" {
		t.Errorf("快照中的值 = %q，預期 v1", v)
	}
}

// 已存在的目標會被取代；寫入失敗時不留下暫存檔。
func TestSnapshotToReplacesAndCleansUp(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "snap.db")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotTo(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if v, err := VerifyFile(ctx, dest); err != nil || v == 0 {
		t.Fatalf("取代後的快照 = %d, %v", v, err)
	}

	// 目標目錄是一般檔案：建立目錄失敗。
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if err := s.SnapshotTo(ctx, filepath.Join(blocker, "x.db")); err == nil {
		t.Error("目標的上層是檔案時應該失敗")
	}
}

func TestSnapshotFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := openWith(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetSetting(ctx, "k", "v")
	_ = s.Close()

	dest := filepath.Join(t.TempDir(), "copy.db")
	if err := SnapshotFile(ctx, filepath.Join(dir, DBFileName), dest); err != nil {
		t.Fatal(err)
	}
	want, _ := LatestSchemaVersion(nil)
	if v, err := VerifyFile(ctx, dest); err != nil || v != want {
		t.Errorf("快照的版本 = %d, %v；預期 %d", v, err, want)
	}
}

func TestSchemaVersion(t *testing.T) {
	s, _ := openTest(t)
	want, err := LatestSchemaVersion(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SchemaVersion(t.Context()); err != nil || got != want || want < 5 {
		t.Fatalf("SchemaVersion = %d, %v；預期 %d（內建至少 5 支）", got, err, want)
	}
}

func TestLatestSchemaVersion(t *testing.T) {
	m := fstest.MapFS{
		"00001_a.sql": {}, "00007_b.sql": {}, "00003_c.sql": {}, "README.md": {}, "x_y.sql": {},
	}
	if v, err := LatestSchemaVersion(m); err != nil || v != 7 {
		t.Errorf("LatestSchemaVersion = %d, %v；預期 7", v, err)
	}
	if v, err := LatestSchemaVersion(fstest.MapFS{}); err != nil || v != 0 {
		t.Errorf("空的 migration = %d, %v；預期 0", v, err)
	}
}

// VerifyFile：不是 SQLite 的檔案、沒有版本表的資料庫。
func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(t.Context(), bad); err == nil {
		t.Error("不是資料庫的檔案應該被拒絕")
	}
	if _, err := VerifyFile(t.Context(), filepath.Join(dir, "missing.db")); err == nil {
		t.Error("不存在的檔案應該失敗")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	if err := WriteFileAtomic(dst, strings.NewReader("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(dst, strings.NewReader("two")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "two" {
		t.Errorf("內容 = %q", b)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %o", fi.Mode().Perm())
	}

	// 中途失敗：目標維持原樣、不留暫存檔。
	if err := WriteFileAtomic(dst, failingReader{}); err == nil {
		t.Fatal("讀取失敗時應該回傳錯誤")
	}
	if b, _ := os.ReadFile(dst); string(b) != "two" {
		t.Errorf("失敗後內容 = %q，預期維持 two", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("留下暫存檔：%v", entries)
	}
	if err := WriteFileAtomic(filepath.Join(dir, "no", "dir", "x"), strings.NewReader("x")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("上層目錄不存在的錯誤 = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
