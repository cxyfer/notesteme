package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	idA = "01a1fb32-8a92-70e7-9269-bf411b343f8b"
	idB = "01a1fb32-8a92-70e7-9269-bf411b343f8c"
)

func shaOf(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// manifestWith 組出含 uploads 清單的 manifest.json。
func manifestWith(t *testing.T, ups ...Upload) string {
	t.Helper()
	b, err := json.Marshal(manifestFile{Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0, NotestemeVersion: "dev"}, Uploads: ups})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func upOf(path, body string) Upload {
	return Upload{Path: path, Size: int64(len(body)), SHA256: shaOf(body)}
}

// writeUploadsDir 在 dir/uploads 下建立 <id>/<name> 檔案，回傳 uploads 目錄。
func writeUploadsDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "uploads")
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// 備份含上傳檔：項目順序是 manifest、DB、設定檔、uploads；還原後檔案與 sha 相同。
func TestUploadsRoundTrip(t *testing.T) {
	files := map[string]string{idA + "/talk.m4a": "AUDIO-one", idB + "/長 名稱.mp4": strings.Repeat("V", 5000)}
	a := &Archive{
		Manifest:   Manifest{SchemaVersion: 5, CreatedAt: t0, NotestemeVersion: "dev"},
		DBPath:     writeDB(t, "db"),
		Config:     []byte("x: 1\n"),
		HasConfig:  true,
		UploadsDir: writeUploadsDir(t, files),
		Uploads: []Upload{
			upOf("uploads/"+idA+"/talk.m4a", files[idA+"/talk.m4a"]),
			upOf("uploads/"+idB+"/長 名稱.mp4", files[idB+"/長 名稱.mp4"]),
		},
	}
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}

	zr, _ := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	tr := tar.NewReader(zr)
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	want := "manifest.json,notesteme.db,config/notesteme.yaml,uploads/" + idA + "/talk.m4a,uploads/" + idB + "/長 名稱.mp4"
	if strings.Join(names, ",") != want {
		t.Fatalf("項目 = %v", names)
	}

	dir := t.TempDir()
	c, err := Extract(bytes.NewReader(buf.Bytes()), dir, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Uploads) != 2 || c.UploadsDir != filepath.Join(dir, "uploads") {
		t.Fatalf("contents = %+v", c)
	}
	for rel, body := range files {
		got, err := os.ReadFile(filepath.Join(c.UploadsDir, filepath.FromSlash(rel)))
		if err != nil || string(got) != body || shaOf(string(got)) != shaOf(body) {
			t.Errorf("%s = %q %v", rel, got, err)
		}
	}
}

// 沒有上傳檔：沒有 uploads 項目，manifest 也沒有 uploads 欄位（與舊格式相同）。
func TestNoUploadsNoEntries(t *testing.T) {
	a := &Archive{Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0, NotestemeVersion: "dev"}, DBPath: writeDB(t, "db")}
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasPrefix(h.Name, "uploads") {
			t.Errorf("不該有 %s", h.Name)
		}
		if h.Name == ManifestName {
			if b, _ := io.ReadAll(tr); strings.Contains(string(b), "uploads") {
				t.Errorf("manifest 不該有 uploads：%s", b)
			}
		}
	}
}

// 舊格式（沒有 uploads 欄位與項目）可以還原，Contents 沒有上傳檔。
func TestExtractOldFormat(t *testing.T) {
	dir := t.TempDir()
	c, err := Extract(buildTar(t, entry{name: ManifestName, body: goodManifest}, entry{name: DBName, body: "db"}), dir, Limits{})
	if err != nil || len(c.Uploads) != 0 || c.UploadsDir != "" {
		t.Fatalf("c = %+v err = %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads")); err == nil {
		t.Error("不該建立 uploads")
	}
}

// 惡意或不合法的上傳路徑一律拒絕，而且不留下任何檔案。
func TestExtractUploadsRejectsBadPaths(t *testing.T) {
	good := "uploads/" + idA + "/a.m4a"
	for name, p := range map[string]string{
		"上層目錄":       "uploads/../evil",
		"id 後的上層目錄":  "uploads/" + idA + "/../../evil",
		"名稱是點點":      "uploads/" + idA + "/..",
		"巢狀":         "uploads/" + idA + "/x/y.m4a",
		"id 不是 UUID": "uploads/not-a-uuid/a.m4a",
		"大寫 UUID":    "uploads/" + strings.ToUpper(idA) + "/a.m4a",
		"少一層":        "uploads/a.m4a",
		"控制字元":       "uploads/" + idA + "/a\x07.m4a",
		"換行":         "uploads/" + idA + "/a\n.m4a",
		"反斜線":        "uploads/" + idA + "/a\\b.m4a",
		"名稱太長":       "uploads/" + idA + "/" + strings.Repeat("x", 256),
		"絕對路徑":       "/uploads/" + idA + "/a.m4a",
	} {
		t.Run(name, func(t *testing.T) {
			// manifest 乾淨（只列合法的檔案），惡意項目只出現在 tar。
			entries := []entry{{name: ManifestName, body: manifestWith(t, upOf(good, "x"))}, {name: DBName, body: "db"}, {name: p, body: "x"}}
			dir := t.TempDir()
			_, err := Extract(buildTar(t, entries...), dir, Limits{})
			if !errors.Is(err, ErrUnsafeEntry) {
				t.Fatalf("錯誤 = %v，預期 ErrUnsafeEntry", err)
			}
			if es, _ := os.ReadDir(dir); len(es) != 0 {
				t.Errorf("不該留下任何檔案：%v", es)
			}
		})
	}
	// manifest 列了不合法的路徑也拒絕。
	bad := manifestWith(t, Upload{Path: "uploads/" + idA + "/../x", Size: 1, SHA256: shaOf("x")})
	if _, err := Extract(buildTar(t, entry{name: ManifestName, body: bad}, entry{name: DBName, body: "db"}), t.TempDir(), Limits{}); !errors.Is(err, ErrUnsafeEntry) {
		t.Errorf("manifest 的惡意路徑：%v", err)
	}
}

// 上傳檔必須是一般檔案：symlink、目錄、hard link 拒絕。
func TestExtractUploadsRejectsNonRegular(t *testing.T) {
	p := "uploads/" + idA + "/a.m4a"
	for name, typ := range map[string]byte{"symlink": tar.TypeSymlink, "hard link": tar.TypeLink, "目錄": tar.TypeDir, "FIFO": tar.TypeFifo} {
		entries := []entry{{name: ManifestName, body: manifestWith(t, upOf(p, "x"))}, {name: DBName, body: "db"}, {name: p, typ: typ, linkname: "/etc/passwd"}}
		dir := t.TempDir()
		if _, err := Extract(buildTar(t, entries...), dir, Limits{}); !errors.Is(err, ErrBadArchive) {
			t.Errorf("%s：錯誤 = %v", name, err)
		}
		if es, _ := os.ReadDir(dir); len(es) != 0 {
			t.Errorf("%s：不該留下任何檔案：%v", name, es)
		}
	}
}

// 大小、sha256 與 manifest 不符，或清單與項目不一致，都拒絕還原。
func TestExtractUploadsMismatch(t *testing.T) {
	p := "uploads/" + idA + "/a.m4a"
	db := entry{name: DBName, body: "db"}
	cases := map[string][]entry{
		"標頭的大小不符":         {{name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"))}, db, {name: p, body: "AUDIO-longer"}},
		"manifest 的大小錯":   {{name: ManifestName, body: manifestWith(t, Upload{Path: p, Size: 3, SHA256: shaOf("AUDIO")})}, db, {name: p, body: "AUDIO"}},
		"sha256 不符":       {{name: ManifestName, body: manifestWith(t, Upload{Path: p, Size: 5, SHA256: shaOf("OTHER")})}, db, {name: p, body: "AUDIO"}},
		"sha256 格式錯":      {{name: ManifestName, body: manifestWith(t, Upload{Path: p, Size: 5, SHA256: "zz"})}, db, {name: p, body: "AUDIO"}},
		"不在清單中":           {{name: ManifestName, body: goodManifest}, db, {name: p, body: "AUDIO"}},
		"清單中的檔案缺少":        {{name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"))}, db},
		"出現在 manifest 之前": {db, {name: p, body: "AUDIO"}, {name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"))}},
		"清單重複":            {{name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"), upOf(p, "AUDIO"))}, db, {name: p, body: "AUDIO"}},
		"項目重複":            {{name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"))}, db, {name: p, body: "AUDIO"}, {name: p, body: "AUDIO"}},
	}
	for name, entries := range cases {
		dir := t.TempDir()
		if _, err := Extract(buildTar(t, entries...), dir, Limits{}); !errors.Is(err, ErrBadArchive) {
			t.Errorf("%s：錯誤 = %v，預期 ErrBadArchive", name, err)
		}
		if es, _ := os.ReadDir(dir); len(es) != 0 {
			t.Errorf("%s：不該留下任何檔案：%v", name, es)
		}
	}
	// 單一上傳檔太大。
	entries := []entry{{name: ManifestName, body: manifestWith(t, upOf(p, "AUDIO"))}, db, {name: p, body: "AUDIO"}}
	if _, err := Extract(buildTar(t, entries...), t.TempDir(), Limits{Upload: 2}); !errors.Is(err, ErrBadArchive) {
		t.Errorf("太大：%v", err)
	}
}

// 剩餘空間檢查把上傳檔算進去：資料庫放得下、加上上傳檔放不下時，在寫出任何東西之前拒絕。
func TestExtractFreeSpaceCountsUploads(t *testing.T) {
	p := "uploads/" + idA + "/a.m4a"
	body := strings.Repeat("A", 100)
	entries := []entry{{name: ManifestName, body: manifestWith(t, upOf(p, body))}, {name: DBName, body: "0123456789"}, {name: p, body: body}}
	free := func(n uint64) Limits { return Limits{FreeSpace: func(string) (uint64, error) { return n, nil }} }
	dir := t.TempDir()
	if _, err := Extract(buildTar(t, entries...), dir, free(50)); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("錯誤 = %v，預期 ErrInsufficientSpace", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("不該留下檔案：%v", es)
	}
	// 剛好放得下（需要 110 位元組，有 111）。
	if _, err := Extract(buildTar(t, entries...), t.TempDir(), free(111)); err != nil {
		t.Errorf("空間足夠卻失敗：%v", err)
	}
}

// 備份時：symlink 不收；檔案內容在寫出時與清單不符（被換掉）就失敗。
func TestWriteToUploadsRejects(t *testing.T) {
	root := writeUploadsDir(t, map[string]string{idA + "/a.m4a": "AUDIO"})
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, idA, "link.m4a")); err != nil {
		t.Fatal(err)
	}
	base := Archive{Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0}, DBPath: writeDB(t, "db"), UploadsDir: root}

	a := base
	a.Uploads = []Upload{upOf("uploads/"+idA+"/link.m4a", "SECRET")}
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err == nil {
		t.Error("symlink 應該被拒絕")
	}
	if bytes.Contains(buf.Bytes(), []byte("SECRET")) {
		t.Error("symlink 指向的內容不該被讀出")
	}

	a = base
	a.Uploads = []Upload{{Path: "uploads/" + idA + "/a.m4a", Size: 5, SHA256: shaOf("OTHER")}}
	if _, err := a.WriteTo(io.Discard); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("內容與清單不符：%v", err)
	}
	a = base
	a.Uploads = []Upload{{Path: "uploads/" + idA + "/a.m4a", Size: 99, SHA256: shaOf("AUDIO")}}
	if _, err := a.WriteTo(io.Discard); err == nil {
		t.Error("大小與清單不符應該失敗")
	}
	a = base
	a.Uploads = []Upload{upOf("uploads/"+idA+"/../x", "x")}
	if _, err := a.WriteTo(io.Discard); err == nil {
		t.Error("不合法的路徑應該失敗")
	}
}

func TestParseUploadPath(t *testing.T) {
	if id, name, ok := ParseUploadPath("uploads/" + idA + "/" + strings.Repeat("x", 255)); !ok || id != idA || len(name) != 255 {
		t.Errorf("255 位元組：%q %q %v", id, name, ok)
	}
	if _, _, ok := ParseUploadPath("uploads/" + idA + "/" + strings.Repeat("x", 256)); ok {
		t.Error("256 位元組應該被拒絕")
	}
}
