package steps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
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

// the file {string} contains:：內容照字面寫入，上層目錄自動建立；/backups 對應到場景根目錄；/tmp 放到場景根目錄之下。
func TestFileContainsDocWritesLiterally(t *testing.T) {
	s := newTestScenario(t)
	content := "fixture: backup-schema-3\n  indented: {x}"
	if err := s.fileContainsDoc("/backups/old.tar.gz", &godog.DocString{Content: content}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.World.Root, "backups", "old.tar.gz")); string(b) != content {
		t.Errorf("內容 = %q", b)
	}
	if err := s.fileContainsDoc("/tmp/fansub.ass", &godog.DocString{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.World.Root, "tmp", "fansub.ass")); err != nil {
		t.Errorf("/tmp 的檔案應放在場景根目錄之下：%v", err)
	}
	if _, err := os.Stat("/tmp/fansub.ass"); err == nil {
		t.Error("不該寫到真正的 /tmp")
	}
	if err := s.fileExists("/backups/old.tar.gz"); err != nil {
		t.Error(err)
	}
}

// makeTarGz 組出 tar.gz；每個項目是 名稱 → 內容。
func makeTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}

// contains／does not contain：.tar.gz 逐一比對每個項目的內容（檔名不算）；一般檔案原樣比對。
func TestFileContainsText(t *testing.T) {
	s := newTestScenario(t)
	if err := os.MkdirAll(s.World.BackupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(s.World.BackupsDir(), name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.tar.gz", makeTarGz(t, map[string]string{"one": "alpha", "two": "bravo-secret", "needle-in-name": "zzz"}))
	write("a.gz", gzipBytes(t, "plain gz secret"))
	write("a.txt", []byte("plain text secret"))
	write("bad.tar.gz", []byte("not gzip but secret"))

	for _, c := range []struct {
		file, text string
		contains   bool
	}{
		{"/backups/a.tar.gz", "bravo-secret", true},
		{"/backups/a.tar.gz", "alpha", true},
		{"/backups/a.tar.gz", "needle-in-name", false}, // 項目的檔名不算
		{"/backups/a.tar.gz", "secret-nowhere", false},
		{"/backups/a.gz", "gz secret", true},
		{"/backups/a.txt", "text secret", true},
		{"/backups/a.txt", "nope", false},
	} {
		err := s.fileContainsText(c.file, c.text)
		if (err == nil) != c.contains {
			t.Errorf("contains %s %q：錯誤 = %v，預期 contains=%v", c.file, c.text, err, c.contains)
		}
		err = s.fileDoesNotContainText(c.file, c.text)
		if (err == nil) == c.contains {
			t.Errorf("does not contain %s %q：錯誤 = %v，預期 contains=%v", c.file, c.text, err, c.contains)
		}
	}
	// 解不開的 .tar.gz 在步驟中明確失敗（不能當成「不含」而通過）；不存在的檔案也是。
	if err := s.fileDoesNotContainText("/backups/bad.tar.gz", "x"); err == nil {
		t.Error("不是 gzip 的 .tar.gz 應該失敗")
	}
	if err := s.fileDoesNotContainText("/backups/missing.tar.gz", "x"); err == nil {
		t.Error("不存在的檔案應該失敗")
	}
	if err := s.fileContainsText("/backups/a.txt", ""); err == nil {
		t.Error("空字串應該失敗")
	}
}

// 空的 .tar.gz（沒有任何一般檔案項目）不能證明任何事：contains 與 does not contain 都要失敗並說明原因。
func TestFileContainsTextFailsOnEmptyArchive(t *testing.T) {
	s := newTestScenario(t)
	if err := os.MkdirAll(s.World.BackupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.World.BackupsDir(), "empty.tar.gz"), makeTarGz(t, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"contains":         s.fileContainsText("/backups/empty.tar.gz", "x"),
		"does not contain": s.fileDoesNotContainText("/backups/empty.tar.gz", "x"),
	} {
		if err == nil || !strings.Contains(err.Error(), "沒有任何檔案") {
			t.Errorf("%s：錯誤 = %v，預期說明封存檔沒有任何檔案", name, err)
		}
	}
}

// After hook 的備份掃描：/backups 中的內容（.tar.gz 解開後）含有 master key 就失敗；
// production 產生的壓縮檔解不開時是 harness 錯誤（不退回掃原始位元組）；場景自己寫入的 fixture 照字面掃描；symlink 不重複計算。
func TestCheckBackupSecrets(t *testing.T) {
	s := newTestScenario(t)
	if err := s.checkBackupSecrets(); err != nil {
		t.Fatalf("/backups 不存在時不該失敗：%v", err)
	}
	if err := os.MkdirAll(s.World.BackupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := s.World.BackupsDir()
	clean := makeTarGz(t, map[string]string{"notesteme.db": "no secrets here"})
	_ = os.WriteFile(filepath.Join(dir, "clean.tar.gz"), clean, 0o600)
	// 場景用 `the file … contains:` 放的不合法封存檔是 fixture，照字面掃描、不算 harness 錯誤。
	if err := s.fileContainsDoc("/backups/old.tar.gz", &godog.DocString{Content: "fixture: backup-schema-3"}); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("clean.tar.gz", filepath.Join(dir, "latest.tar.gz"))
	if err := s.checkBackupSecrets(); err != nil {
		t.Fatalf("乾淨的備份不該失敗：%v", err)
	}
	// 不是 fixture 的壓縮檔解不開：harness 錯誤；即使原始位元組含有 master key，也不退回去掃。
	_ = os.WriteFile(filepath.Join(dir, "broken.tar.gz"), []byte("not gzip"), 0o600)
	if err := s.checkBackupSecrets(); err == nil || !strings.Contains(err.Error(), "harness 錯誤") || !strings.Contains(err.Error(), "/backups/broken.tar.gz") {
		t.Fatalf("解不開的備份：錯誤 = %v，預期 harness 錯誤", err)
	}
	_ = os.Remove(filepath.Join(dir, "broken.tar.gz"))
	// fixture 被改過就不再享有例外。
	_ = os.WriteFile(filepath.Join(dir, "old.tar.gz"), []byte("changed by production"), 0o600)
	if err := s.checkBackupSecrets(); err == nil || !strings.Contains(err.Error(), "harness 錯誤") {
		t.Fatalf("被改過的 fixture：錯誤 = %v", err)
	}
	if err := s.fileContainsDoc("/backups/old.tar.gz", &godog.DocString{Content: "fixture: backup-schema-3"}); err != nil {
		t.Fatal(err)
	}

	s.World.Env["NOTESTEME_MASTER_KEY"] = "ops-backup-master-key-1"
	leaky := makeTarGz(t, map[string]string{"notesteme.db": "xx ops-backup-master-key-1 xx"})
	_ = os.WriteFile(filepath.Join(dir, "leaky.tar.gz"), leaky, 0o600)
	err := s.checkBackupSecrets()
	if err == nil || !strings.Contains(err.Error(), "/backups/leaky.tar.gz") || strings.Contains(err.Error(), "ops-backup-master-key-1") {
		t.Fatalf("錯誤 = %v，預期指出 leaky.tar.gz 且不重複 master key 的值", err)
	}
	// 一般檔案也掃描原始位元組。
	_ = os.Remove(filepath.Join(dir, "leaky.tar.gz"))
	_ = os.WriteFile(filepath.Join(dir, "raw.bin"), []byte("..ops-backup-master-key-1.."), 0o600)
	if err := s.checkBackupSecrets(); err == nil {
		t.Error("一般檔案含有 master key 應該失敗")
	}
}
