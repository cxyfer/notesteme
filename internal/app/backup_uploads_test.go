package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

func sumHex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// addUpload 在資料目錄建立一個上傳影音（影音列與 uploads/<id>/<name> 檔案），回傳 id。
func addUpload(t *testing.T, st *store.Store, data, name, content string) string {
	t.Helper()
	id := store.NewID()
	dir := filepath.Join(data, "uploads", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Write().InsertItemIfAbsent(t.Context(), store.NewItem{
		ID: id, Platform: "local", ExternalID: name, SourcePath: "uploads/" + id + "/" + name,
		ContentSHA256: sumHex(content), Origin: store.OriginUpload,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// makeBackupWithUploads 啟動 serve、建立上傳影音、備份成檔案後關閉；回傳備份檔與 (id → 內容)。
func makeBackupWithUploads(t *testing.T, env Env, uploads map[string]string) (string, map[string]string) {
	t.Helper()
	srv := startBackupServer(t, env)
	data := env.Vars["NOTESTEME_DATA"]
	byID := map[string]string{}
	for name, content := range uploads {
		id := addUpload(t, srv.store, data, name, content)
		byID[id+"/"+name] = content
	}
	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), b.Name())
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_ = b.Close()
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	return path, byID
}

// 備份含上傳的原始檔：還原後檔案內容與 sha 相同；原本的 uploads/ 只保留備份中的檔案，其餘移到 pre-restore-uploads。
func TestBackupAndRestoreUploads(t *testing.T) {
	env := testEnv(t)
	file, byID := makeBackupWithUploads(t, env, map[string]string{"talk.m4a": "AUDIO-one", "lecture.mp4": strings.Repeat("V", 9000)})

	// 備份的內容：manifest 列出兩個上傳檔，大小與 sha256 正確。
	c, err := backup.Extract(mustOpen(t, file), t.TempDir(), backup.Limits{})
	if err != nil || len(c.Uploads) != 2 {
		t.Fatalf("Extract = %+v, %v", c, err)
	}
	for _, u := range c.Uploads {
		rel := strings.TrimPrefix(u.Path, "uploads/")
		want, ok := byID[rel]
		if !ok || u.Size != int64(len(want)) || u.SHA256 != sumHex(want) {
			t.Errorf("manifest 的 %+v 與實際內容不符", u)
		}
	}

	// 模擬遺失：uploads/ 被清掉，換成一個備份中沒有的檔案。
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.RemoveAll(filepath.Join(data, "uploads")); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(data, "uploads", store.NewID(), "stray.m4a")
	if err := os.MkdirAll(filepath.Dir(stray), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("STRAY"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploads != 2 {
		t.Errorf("Uploads = %d", res.Uploads)
	}
	for rel, want := range byID {
		p := filepath.Join(data, "uploads", filepath.FromSlash(rel))
		got, err := os.ReadFile(p)
		if err != nil || string(got) != want || sumHex(string(got)) != sumHex(want) {
			t.Errorf("%s = %d 位元組 %v", rel, len(got), err)
		}
		if fi, _ := os.Stat(p); fi == nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s 的權限 = %v", rel, fi)
		}
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("備份中沒有的檔案不該留在 uploads/")
	}
	if res.PreRestoreUploads == "" {
		t.Fatal("原本的 uploads/ 應該被保留")
	}
	if b, err := os.ReadFile(filepath.Join(res.PreRestoreUploads, filepath.Base(filepath.Dir(stray)), "stray.m4a")); err != nil || string(b) != "STRAY" {
		t.Errorf("pre-restore-uploads 中的檔案 = %q %v", b, err)
	}
	// 還原後 serve 可以啟動，而且不會把還原的上傳檔當成孤兒刪掉（資料庫有對應的影音）。
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	srv.Kill()
	for rel := range byID {
		if _, err := os.Stat(filepath.Join(data, "uploads", filepath.FromSlash(rel))); err != nil {
			t.Errorf("serve 啟動後 %s 不見了：%v", rel, err)
		}
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// rewriteBackup 讀出備份的每個項目，交給 mod 改寫（回傳 nil 代表刪除），寫成新的備份檔。
func rewriteBackup(t *testing.T, file string, mod func(name string, body []byte) []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(mustOpen(t, file))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		if body = mod(h.Name, body); body == nil {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: h.Name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	out := filepath.Join(t.TempDir(), "tampered.tar.gz")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// 備份被竄改（內容、大小、多出的惡意路徑）時拒絕還原，而且現有的 uploads/ 與資料庫都維持原狀。
func TestRestoreRejectsTamperedUploads(t *testing.T) {
	env := testEnv(t)
	file, _ := makeBackupWithUploads(t, env, map[string]string{"talk.m4a": "AUDIO-one"})
	data := env.Vars["NOTESTEME_DATA"]
	isUpload := func(name string) bool { return strings.HasPrefix(name, "uploads/") }

	cases := map[string]func(name string, body []byte) []byte{
		"內容被改（大小相同）": func(name string, body []byte) []byte {
			if isUpload(name) {
				return []byte("AUDIO-ONE")
			}
			return body
		},
		"大小不符": func(name string, body []byte) []byte {
			if isUpload(name) {
				return append(body, '!')
			}
			return body
		},
		"缺少檔案": func(name string, body []byte) []byte {
			if isUpload(name) {
				return nil
			}
			return body
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			setMarker(t, env, "current")
			keep := filepath.Join(data, "uploads", "keep-me")
			if err := os.MkdirAll(keep, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(keep, "f"), []byte("KEEP"), 0o600); err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(keep)
			if _, err := Restore(t.Context(), env, rewriteBackup(t, file, mod)); !errors.Is(err, backup.ErrBadArchive) {
				t.Fatalf("錯誤 = %v，預期 ErrBadArchive", err)
			}
			if b, err := os.ReadFile(filepath.Join(keep, "f")); err != nil || string(b) != "KEEP" {
				t.Errorf("現有的 uploads/ 被動到了：%q %v", b, err)
			}
			if got := marker(t, env); got != "current" {
				t.Errorf("資料庫被換掉了：%q", got)
			}
			if left, _ := filepath.Glob(filepath.Join(data, "tmp", "restore-*")); len(left) != 0 {
				t.Errorf("暫存的解壓目錄沒有清掉：%v", left)
			}
		})
	}
}

// 舊格式（沒有上傳檔）的備份可以還原：不會建立 uploads/，原本的 uploads/ 移到 pre-restore-uploads。
func TestRestoreOldFormatKeepsExistingUploadsAside(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "from-backup")
	data := env.Vars["NOTESTEME_DATA"]
	old := filepath.Join(data, "uploads", store.NewID(), "old.m4a")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploads != 0 {
		t.Errorf("Uploads = %d", res.Uploads)
	}
	if _, err := os.Stat(filepath.Join(data, "uploads")); err == nil {
		t.Error("備份沒有上傳檔時不該有 uploads/")
	}
	if b, err := os.ReadFile(filepath.Join(res.PreRestoreUploads, filepath.Base(filepath.Dir(old)), "old.m4a")); err != nil || string(b) != "OLD" {
		t.Errorf("原本的上傳檔 = %q %v", b, err)
	}
	if got := marker(t, env); got != "from-backup" {
		t.Errorf("marker = %q", got)
	}
}

// 沒有上傳時備份不含 uploads 項目（既有的備份內容比對不受影響）。
func TestBackupWithoutUploadsHasNoUploadEntries(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "x")
	zr, err := gzip.NewReader(mustOpen(t, file))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasPrefix(h.Name, "uploads") {
			t.Errorf("不該有 %s", h.Name)
		}
	}
}

// 上傳影音的檔案不見（或是 symlink）時，備份略過它而不是失敗。
// 快照之後才被永久刪除的上傳影音仍在備份的資料庫中：它的原始檔也要在備份裡，
// 而且備份結束（Close）之前不會被刪掉，結束之後才刪。
func TestBackupKeepsUploadPurgedAfterSnapshot(t *testing.T) {
	env := testEnv(t)
	srv := startBackupServer(t, env)
	data := env.Vars["NOTESTEME_DATA"]
	id := addUpload(t, srv.store, data, "talk.m4a", "TALK")
	dir := filepath.Join(data, "uploads", id)
	afterBackupSnapshot = func() {
		if _, err := srv.pipeline.Trash(t.Context(), id, pipeline.TrashOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := srv.pipeline.Purge(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterBackupSnapshot = nil })

	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	afterBackupSnapshot = nil
	if _, err := srv.store.Read().ItemByID(t.Context(), id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("前置條件：影音應該已經被永久刪除，ItemByID = %v", err)
	}
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "talk.m4a")); err != nil {
		t.Fatalf("備份結束之前，上傳原始檔不應該被刪除：%v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("備份結束之後，延後的上傳目錄應該被刪除：%v", err)
	}
	c, err := backup.Extract(&buf, t.TempDir(), backup.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Uploads) != 1 || c.Uploads[0].Path != "uploads/"+id+"/talk.m4a" {
		t.Fatalf("備份中的上傳檔 = %+v", c.Uploads)
	}
}

func TestBackupSkipsMissingAndSymlinkedUploads(t *testing.T) {
	env := testEnv(t)
	srv := startBackupServer(t, env)
	data := env.Vars["NOTESTEME_DATA"]
	gone := addUpload(t, srv.store, data, "gone.m4a", "GONE")
	if err := os.Remove(filepath.Join(data, "uploads", gone, "gone.m4a")); err != nil {
		t.Fatal(err)
	}
	link := addUpload(t, srv.store, data, "link.m4a", "LINK")
	linkPath := filepath.Join(data, "uploads", link, "link.m4a")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("SECRET-OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, linkPath); err != nil {
		t.Fatal(err)
	}
	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := backup.Extract(&buf, t.TempDir(), backup.Limits{})
	if err != nil || len(c.Uploads) != 0 {
		t.Fatalf("Extract = %+v, %v", c, err)
	}
}
