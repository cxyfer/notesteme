package api

import (
	"os"
	"path/filepath"
	"testing"
)

// 副檔名白名單要在解析 symlink 之後才檢查，不能讓 x.srt 連到其他檔案繞過。
func TestReadImportInput_SymlinkCannotBypassExtension(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("not a subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.srt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("無法建立 symlink：", err)
	}
	exts := []string{".srt", ".vtt", ".ass", ".ssa"}
	if _, _, ierr := readImportInput(link, "", 1<<20, exts); ierr == nil {
		t.Fatal("x.srt -> secret.txt 應該被拒絕")
	}
	// 連到字幕副檔名的一般檔案仍可讀。
	real := filepath.Join(dir, "real.srt")
	if err := os.WriteFile(real, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(dir, "ok.srt")
	if err := os.Symlink(real, ok); err != nil {
		t.Fatal(err)
	}
	if _, _, ierr := readImportInput(ok, "", 1<<20, exts); ierr != nil {
		t.Fatalf("字幕檔的 symlink 應該可讀：%v", ierr)
	}
}
