package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func TestFileName(t *testing.T) {
	got := FileName(t0)
	if got != "notesteme-backup-20261112T100000Z.tar.gz" {
		t.Fatalf("FileName = %q", got)
	}
	// 非 UTC 的時間先換成 UTC。
	if got := FileName(t0.In(time.FixedZone("x", 8*3600))); got != "notesteme-backup-20261112T100000Z.tar.gz" {
		t.Errorf("非 UTC 的 FileName = %q", got)
	}
	if Stamp(t0) != "20261112T100000Z" {
		t.Errorf("Stamp = %q", Stamp(t0))
	}
	for _, bad := range []string{"", "latest.tar.gz", "../notesteme-backup-20261112T100000Z.tar.gz", "notesteme-backup-20261112T100000Z.tar.gz/x",
		"notesteme-backup-2026-11-12.tar.gz", "notesteme-backup-20261112T100000Z.tar.gz\n"} {
		if ValidFileName(bad) {
			t.Errorf("ValidFileName(%q) 應該是 false", bad)
		}
	}
	if !ValidFileName(got) {
		t.Errorf("ValidFileName(%q) 應該是 true", got)
	}
}

func writeDB(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap.db")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteToAndExtractRoundTrip(t *testing.T) {
	for _, withConfig := range []bool{true, false} {
		a := &Archive{
			Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0, NotestemeVersion: "v1.2.3"},
			DBPath:   writeDB(t, strings.Repeat("db-content ", 5000)),
		}
		if withConfig {
			a.Config, a.HasConfig = []byte("listen: 127.0.0.1:8080\n"), true
		}
		var buf bytes.Buffer
		n, err := a.WriteTo(&buf)
		if err != nil || n != int64(buf.Len()) {
			t.Fatalf("WriteTo = %d, %v（實際 %d 位元組）", n, err, buf.Len())
		}

		dir := t.TempDir()
		c, err := Extract(&buf, dir, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if c.Manifest != a.Manifest {
			t.Errorf("manifest = %+v，預期 %+v", c.Manifest, a.Manifest)
		}
		if b, _ := os.ReadFile(c.DBPath); string(b) != strings.Repeat("db-content ", 5000) {
			t.Error("資料庫內容不符")
		}
		if fi, _ := os.Stat(c.DBPath); fi.Mode().Perm() != 0o600 {
			t.Errorf("資料庫權限 = %o", fi.Mode().Perm())
		}
		if c.HasConfig != withConfig || string(c.Config) != string(a.Config) {
			t.Errorf("設定檔 = %v %q", c.HasConfig, c.Config)
		}
	}
}

// 備份只有規格列出的項目名稱，manifest 的欄位名稱照規格。
func TestArchiveEntriesAndManifestFields(t *testing.T) {
	a := &Archive{
		Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0, NotestemeVersion: "dev"},
		DBPath:   writeDB(t, "x"), Config: []byte("c"), HasConfig: true,
	}
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(zr)
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
		if h.Name == ManifestName {
			b, _ := io.ReadAll(tr)
			for _, f := range []string{`"schema_version": 5`, `"created_at": "2026-11-12T10:00:00Z"`, `"notesteme_version": "dev"`} {
				if !strings.Contains(string(b), f) {
					t.Errorf("manifest 不含 %s：%s", f, b)
				}
			}
		}
	}
	if strings.Join(names, ",") != "manifest.json,notesteme.db,config/notesteme.yaml" {
		t.Errorf("項目 = %v", names)
	}
}

func TestWriteToMissingDB(t *testing.T) {
	a := &Archive{Manifest: Manifest{SchemaVersion: 1}, DBPath: filepath.Join(t.TempDir(), "nope.db")}
	if _, err := a.WriteTo(io.Discard); err == nil {
		t.Error("資料庫快照不存在時應該失敗")
	}
}

// entry 是手工組出的 tar 項目。
type entry struct {
	name     string
	typ      byte
	body     string
	linkname string
	size     int64 // 非 0 時覆寫標頭的大小（內容仍是 body）
}

func buildTar(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		size := int64(len(e.body))
		if typ != tar.TypeReg {
			size = 0
		}
		if e.size != 0 {
			size = e.size
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: typ, Name: e.name, Linkname: e.linkname, Mode: 0o600, Size: size}); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			body := e.body
			if int64(len(body)) > size {
				body = body[:size]
			}
			if _, err := tw.Write([]byte(body)); err != nil && e.size == 0 {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return bytes.NewReader(buf.Bytes())
}

const goodManifest = `{"schema_version":5,"created_at":"2026-11-12T10:00:00Z","notesteme_version":"dev"}`

func TestExtractRejects(t *testing.T) {
	good := []entry{{name: ManifestName, body: goodManifest}, {name: DBName, body: "db"}}
	cases := []struct {
		name    string
		entries []entry
		lim     Limits
		is      error
		want    string
	}{
		{"上層目錄", append(append([]entry{}, good...), entry{name: "../evil", body: "x"}), Limits{}, ErrUnsafeEntry, "../evil"},
		{"巢狀的上層目錄", append(append([]entry{}, good...), entry{name: "config/../../evil", body: "x"}), Limits{}, ErrUnsafeEntry, ""},
		{"絕對路徑", append(append([]entry{}, good...), entry{name: "/etc/passwd", body: "x"}), Limits{}, ErrUnsafeEntry, ""},
		{"反斜線", append(append([]entry{}, good...), entry{name: `config\notesteme.yaml`, body: "x"}), Limits{}, ErrUnsafeEntry, ""},
		{"symlink 指向外面", []entry{{name: DBName, typ: tar.TypeSymlink, linkname: "/etc/passwd"}, {name: ManifestName, body: goodManifest}}, Limits{}, ErrBadArchive, "一般檔案"},
		{"hard link", []entry{{name: ManifestName, body: goodManifest}, {name: DBName, typ: tar.TypeLink, linkname: "manifest.json"}}, Limits{}, ErrBadArchive, "一般檔案"},
		{"目錄", []entry{{name: "config/", typ: tar.TypeDir}}, Limits{}, ErrBadArchive, ""},
		{"未知名稱", append(append([]entry{}, good...), entry{name: "extra.txt", body: "x"}), Limits{}, ErrBadArchive, "未知"},
		{"重複的項目", append(append([]entry{}, good...), entry{name: DBName, body: "again"}), Limits{}, ErrBadArchive, "重複"},
		{"缺 manifest", []entry{{name: DBName, body: "db"}}, Limits{}, ErrBadArchive, "manifest.json"},
		{"缺資料庫", []entry{{name: ManifestName, body: goodManifest}}, Limits{}, ErrBadArchive, "notesteme.db"},
		{"manifest 不是 JSON", []entry{{name: ManifestName, body: "nope"}, {name: DBName, body: "db"}}, Limits{}, ErrBadArchive, "JSON"},
		{"manifest 沒有版本", []entry{{name: ManifestName, body: `{}`}, {name: DBName, body: "db"}}, Limits{}, ErrBadArchive, "schema_version"},
		{"資料庫太大", good, Limits{DB: 1}, ErrBadArchive, "太大"},
		{"設定檔太大", append(append([]entry{}, good...), entry{name: ConfigName, body: "0123456789"}), Limits{Config: 5}, ErrBadArchive, "太大"},
		{"manifest 太大", good, Limits{Manifest: 10}, ErrBadArchive, "太大"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := Extract(buildTar(t, c.entries...), dir, c.lim)
			if !errors.Is(err, c.is) {
				t.Fatalf("Extract 的錯誤 = %v，預期 %v", err, c.is)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("訊息 %q 不含 %q", err, c.want)
			}
			if _, err := os.Stat(filepath.Join(dir, DBName)); err == nil {
				t.Error("失敗時不該留下資料庫檔")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("目的目錄不該有任何檔案：%v", entries)
			}
		})
	}
}

// 不是 gzip、被截斷、gzip 損毀。
func TestExtractGarbage(t *testing.T) {
	if _, err := Extract(strings.NewReader("fixture: backup-schema-3"), t.TempDir(), Limits{}); !errors.Is(err, ErrBadArchive) {
		t.Errorf("不是 gzip 的錯誤 = %v", err)
	}
	a := &Archive{Manifest: Manifest{SchemaVersion: 5, CreatedAt: t0}, DBPath: writeDB(t, strings.Repeat("0123456789", 10000))}
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Extract(bytes.NewReader(buf.Bytes()[:buf.Len()/2]), dir, Limits{}); !errors.Is(err, ErrBadArchive) {
		t.Errorf("被截斷的錯誤 = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("截斷時不該留下檔案：%v", entries)
	}
}

func TestSaveToWritesFileAndSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	name := FileName(t0)
	path, err := SaveTo(dir, name, strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, name) {
		t.Errorf("path = %q", path)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("目錄權限 = %o", fi.Mode().Perm())
	}
	latest := filepath.Join(dir, LatestName)
	if target, err := os.Readlink(latest); err != nil || target != name {
		t.Fatalf("latest 指向 %q, %v；預期相對路徑 %q", target, err, name)
	}

	// 第二份備份：latest 改指向它，第一份還在。
	name2 := FileName(t0.Add(time.Hour))
	if _, err := SaveTo(dir, name2, strings.NewReader("two")); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(latest); target != name2 {
		t.Errorf("latest 指向 %q，預期 %q", target, name2)
	}
	if b, _ := os.ReadFile(latest); string(b) != "two" {
		t.Errorf("透過 latest 讀到 %q", b)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("第一份備份不見了：%v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("目錄中有多餘的檔案：%v", entries)
	}
}

func TestSaveToRejectsBadNameAndFailedStream(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"x.tar.gz", "../notesteme-backup-20261112T100000Z.tar.gz", LatestName} {
		if _, err := SaveTo(dir, bad, strings.NewReader("x")); err == nil {
			t.Errorf("檔名 %q 應該被拒絕", bad)
		}
	}
	// 串流中斷：不留下部分檔案，latest 也不建立。
	if _, err := SaveTo(dir, FileName(t0), io.MultiReader(strings.NewReader("partial"), errReader{})); err == nil {
		t.Fatal("串流中斷應該回傳錯誤")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("串流中斷後不該留下檔案：%v", entries)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// Windows 沒有 symlink：改複製一份。
func TestUpdateLatestWindowsCopies(t *testing.T) {
	dir := t.TempDir()
	name := FileName(t0)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateLatest(dir, name, "windows"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dir, LatestName))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Windows 應該是複本而不是 symlink：%v, %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, LatestName)); string(b) != "data" {
		t.Errorf("內容 = %q", b)
	}
}
