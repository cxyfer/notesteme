package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReader 先給一段內容，再回傳錯誤（模擬下載到一半連線中斷）。
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("連線中斷")
}

// 寫入中途失敗時，原有的檔案維持原樣，目錄裡也不留下暫存檔。
func TestWriteFileKeepsOriginalOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.srt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, &failingReader{}); err == nil || !strings.Contains(err.Error(), "連線中斷") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "original" {
		t.Errorf("原檔被破壞：%q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("目錄裡多了暫存檔：%v", entries)
	}
}

// 成功時蓋過原檔，不留下暫存檔。
func TestWriteFileReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.srt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("內容 = %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("目錄裡多了暫存檔：%v", entries)
	}
}

// 目錄不存在時回報錯誤。
func TestWriteFileMissingDir(t *testing.T) {
	if err := writeFile(filepath.Join(t.TempDir(), "no", "out.srt"), strings.NewReader("x")); err == nil {
		t.Error("預期錯誤")
	}
}
