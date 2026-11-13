package steps

import (
	"os"
	"path/filepath"
	"testing"
)

// the file {string} exists：虛擬路徑改寫成場景根目錄下的路徑，檔案不存在時失敗（不是 pending）。
func TestFileExists(t *testing.T) {
	s := newTestScenario(t)
	if err := s.fileExists("/data/tmp/x.part"); err == nil {
		t.Fatal("檔案不存在應該失敗")
	}
	p := filepath.Join(s.World.DataDir(), "tmp", "x.part")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.fileExists("/data/tmp/x.part"); err != nil {
		t.Fatal(err)
	}
	if err := s.fileDoesNotExist("/data/tmp/x.part"); err == nil {
		t.Fatal("檔案存在時 does not exist 應該失敗")
	}
}
