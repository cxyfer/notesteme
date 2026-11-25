package pipeline

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestCleanArtifactTemps：只刪 artifacts/<item>/ 底下的 .tmp-* 一般檔案；正式產出、其他目錄、symlink 都不動（NFR-REL-2）。
func TestCleanArtifactTemps(t *testing.T) {
	data := t.TempDir()
	if n, err := CleanArtifactTemps(data); n != 0 || err != nil {
		t.Fatalf("沒有 artifacts/ 時 = %d, %v", n, err)
	}
	item := filepath.Join(data, artifactsDir, "item-1")
	frames := filepath.Join(item, framesSubdir, "v1")
	if err := os.MkdirAll(frames, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(item, ".tmp-a1-123"))
	write(filepath.Join(item, ".tmp-a2-456"))
	write(filepath.Join(item, "a3.json"))
	write(filepath.Join(frames, ".tmp-not-ours"))
	outside := filepath.Join(t.TempDir(), ".tmp-outside")
	write(outside)
	if err := os.Symlink(outside, filepath.Join(item, ".tmp-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(data, artifactsDir, "linked")); err != nil {
		t.Fatal(err)
	}
	n, err := CleanArtifactTemps(data)
	if err != nil || n != 2 {
		t.Fatalf("CleanArtifactTemps = %d, %v；應該刪掉 2 個", n, err)
	}
	for _, p := range []string{filepath.Join(item, "a3.json"), filepath.Join(frames, ".tmp-not-ours"), outside, filepath.Join(item, ".tmp-link")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s 不應該被刪：%v", p, err)
		}
	}
	for _, p := range []string{".tmp-a1-123", ".tmp-a2-456"} {
		if _, err := os.Lstat(filepath.Join(item, p)); !os.IsNotExist(err) {
			t.Errorf("%s 應該被刪：%v", p, err)
		}
	}
}

// TestWriteArtifactFileUsesTheTmpPrefix：暫存檔的前綴與清除時比對的前綴相同，寫完之後不留暫存檔。
func TestWriteArtifactFileUsesTheTmpPrefix(t *testing.T) {
	data := t.TempDir()
	rel, _, _, err := writeArtifactFile(data, "item-1", "a1", func(w io.Writer) error {
		_, err := w.Write([]byte("{}"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(data, artifactsDir, "item-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || filepath.Join(artifactsDir, "item-1", ents[0].Name()) != filepath.FromSlash(rel) {
		t.Fatalf("產出目錄 = %v，應該只有 %s", ents, rel)
	}
}
