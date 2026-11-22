package output

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// memRecords 是記憶體中的 Records。
type memRecords struct {
	rows    map[string]store.Output
	failRec error
}

func newMem() *memRecords { return &memRecords{rows: map[string]store.Output{}} }

func (m *memRecords) Lookup(_ context.Context, itemID, path string) (string, bool, error) {
	o, ok := m.rows[itemID+"|"+path]
	return o.SHA256, ok, nil
}

func (m *memRecords) Record(_ context.Context, o store.Output) error {
	if m.failRec != nil {
		return m.failRec
	}
	m.rows[o.ItemID+"|"+o.Path] = o
	return nil
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var day = time.Date(2026, 11, 12, 23, 30, 0, 0, time.UTC)

func newWriter(m *memRecords) *Writer {
	return &Writer{Records: m, Now: func() time.Time { return day }}
}

func file(dir, name, data, policy string) File {
	return File{ItemID: "it1", Dir: dir, Name: name, Data: []byte(data), Target: TargetSidecar, Format: "srt", Lang: "en", Policy: policy}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	res, err := newWriter(m).Write(context.Background(), file(dir, "a.en.srt", "v1", PolicySaveAsNew))
	if err != nil || res.Outcome != Written || res.Path != filepath.Join(dir, "a.en.srt") {
		t.Fatalf("Write = %+v，%v", res, err)
	}
	if got := read(t, res.Path); got != "v1" {
		t.Errorf("內容 = %q", got)
	}
	if fi, _ := os.Stat(res.Path); fi.Mode().Perm() != 0o644 {
		t.Errorf("權限 = %v，預期 0644", fi.Mode().Perm())
	}
	rec := m.rows["it1|"+res.Path]
	if rec.SHA256 != sha("v1") || rec.Target != TargetSidecar || rec.Lang != "en" || rec.Format != "srt" || rec.Size != 2 {
		t.Errorf("紀錄 = %+v", rec)
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("資料夾有暫存檔殘留：%v", names)
	}
}

// 核心安全性質：雜湊與紀錄相同才覆蓋。
func TestWriteOverwritesOnlyWhenHashMatchesRecord(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	w := newWriter(m)
	ctx := context.Background()
	if _, err := w.Write(ctx, file(dir, "a.en.srt", "v1", PolicySaveAsNew)); err != nil {
		t.Fatal(err)
	}
	// 沒人改過：覆蓋成新內容，紀錄跟著更新。
	res, err := w.Write(ctx, file(dir, "a.en.srt", "v2", PolicySaveAsNew))
	if err != nil || res.Outcome != Written || read(t, res.Path) != "v2" {
		t.Fatalf("沒改過時應覆蓋：%+v，%v", res, err)
	}
	if m.rows["it1|"+res.Path].SHA256 != sha("v2") {
		t.Error("覆蓋後紀錄應更新為新內容的雜湊")
	}
	// 使用者改過：不動它，另存新檔。
	if err := os.WriteFile(res.Path, []byte("hand edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = w.Write(ctx, file(dir, "a.en.srt", "v3", PolicySaveAsNew))
	if err != nil || res.Outcome != SavedNew || res.Path != filepath.Join(dir, "a.en (notesteme 2026-11-12).srt") {
		t.Fatalf("改過時應另存：%+v，%v", res, err)
	}
	if read(t, filepath.Join(dir, "a.en.srt")) != "hand edit" {
		t.Error("使用者改過的檔案被覆蓋了")
	}
	if read(t, res.Path) != "v3" {
		t.Error("另存的內容不對")
	}
	if m.rows["it1|"+filepath.Join(dir, "a.en.srt")].SHA256 != sha("v2") {
		t.Error("原檔的紀錄不該被改動")
	}
	if names := entries(t, dir); len(names) != 2 {
		t.Errorf("預期 2 個檔案，實際 %v", names)
	}
}

// 沒有紀錄的既有檔案不可能是我們寫的：一律視為使用者的檔案。
func TestWriteExistingFileWithoutRecordIsTheUsers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.en.srt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := newWriter(newMem()).Write(context.Background(), file(dir, "a.en.srt", "new", PolicySaveAsNew))
	if err != nil || res.Outcome != SavedNew {
		t.Fatalf("Write = %+v，%v", res, err)
	}
	if read(t, filepath.Join(dir, "a.en.srt")) != "mine" {
		t.Error("沒有紀錄的既有檔案被覆蓋了")
	}
}

func TestWriteSkipPolicyBlocks(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	if err := os.WriteFile(filepath.Join(dir, "a.en.srt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := newWriter(m).Write(context.Background(), file(dir, "a.en.srt", "new", PolicySkip))
	if err != nil || res.Outcome != Blocked || res.Path != filepath.Join(dir, "a.en.srt") {
		t.Fatalf("Write = %+v，%v", res, err)
	}
	if names := entries(t, dir); len(names) != 1 || read(t, filepath.Join(dir, "a.en.srt")) != "mine" {
		t.Errorf("skip 不該寫入任何東西：%v", names)
	}
	if len(m.rows) != 0 {
		t.Errorf("skip 不該留下紀錄：%v", m.rows)
	}
}

// 同一天再次另存：另存的檔案沒被改過就覆蓋，被改過就換下一個檔名。
func TestWriteSavedAsNewTwiceOnTheSameDay(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	w := newWriter(m)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "a.en.srt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	r1, _ := w.Write(ctx, file(dir, "a.en.srt", "n1", PolicySaveAsNew))
	r2, err := w.Write(ctx, file(dir, "a.en.srt", "n2", PolicySaveAsNew))
	if err != nil || r2.Path != r1.Path || r2.Outcome != SavedNew || read(t, r1.Path) != "n2" {
		t.Fatalf("另存的檔案沒被改過，應該覆蓋同一個：%+v / %+v，%v", r1, r2, err)
	}
	if err := os.WriteFile(r1.Path, []byte("edited too"), 0o644); err != nil {
		t.Fatal(err)
	}
	r3, err := w.Write(ctx, file(dir, "a.en.srt", "n3", PolicySaveAsNew))
	want := filepath.Join(dir, "a.en (notesteme 2026-11-12 2).srt")
	if err != nil || r3.Path != want || read(t, r1.Path) != "edited too" || read(t, want) != "n3" {
		t.Fatalf("另存的檔案也被改過，應換檔名：%+v，%v", r3, err)
	}
}

func TestWriteIdenticalContentIsAdopted(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	if err := os.WriteFile(filepath.Join(dir, "a.en.srt"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := newWriter(m).Write(context.Background(), file(dir, "a.en.srt", "same", PolicySaveAsNew))
	if err != nil || res.Outcome != Unchanged {
		t.Fatalf("Write = %+v，%v", res, err)
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("內容相同不該另存：%v", names)
	}
	if m.rows["it1|"+res.Path].SHA256 != sha("same") {
		t.Error("內容相同時應補上紀錄")
	}
}

// symlink 與目錄不是一般檔案：不跟隨、不覆蓋，改另存新檔。
func TestWriteNeverFollowsSymlinksOrReplacesDirectories(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "a.en.srt")); err != nil {
		t.Skip("不能建立 symlink：", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "b.en.srt"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWriter(newMem())
	for _, name := range []string{"a.en.srt", "b.en.srt"} {
		res, err := w.Write(context.Background(), file(dir, name, "new", PolicySaveAsNew))
		if err != nil || res.Outcome != SavedNew {
			t.Fatalf("%s：Write = %+v，%v", name, res, err)
		}
	}
	if read(t, secret) != "secret" {
		t.Error("寫出時跟著 symlink 改到了資料夾外的檔案")
	}
	if fi, err := os.Lstat(filepath.Join(dir, "a.en.srt")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink 被換掉了")
	}
}

func TestWriteRejectsNamesThatEscape(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", ".", "..", "../x.srt", "a/b.srt", `a\b.srt`, "a\x00b"} {
		if _, err := newWriter(newMem()).Write(context.Background(), file(dir, name, "x", PolicySaveAsNew)); err == nil {
			t.Errorf("檔名 %q 應被拒絕", name)
		}
	}
	if names := entries(t, dir); len(names) != 0 {
		t.Errorf("被拒絕的檔名不該寫出任何東西：%v", names)
	}
}

// 影音在寫出途中被永久刪除：新建的檔案要移除，不留下沒有紀錄的檔案。
func TestWriteRemovesNewFileWhenRecordingFails(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	m.failRec = store.ErrNotFound
	if _, err := newWriter(m).Write(context.Background(), file(dir, "a.en.srt", "x", PolicySaveAsNew)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v，預期 ErrNotFound", err)
	}
	if names := entries(t, dir); len(names) != 0 {
		t.Errorf("紀錄失敗後不該留下檔案：%v", names)
	}
}

func TestAlternateName(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"lecture.en.srt", 1, "lecture.en (notesteme 2026-11-12).srt"},
		{"lecture.en.srt", 2, "lecture.en (notesteme 2026-11-12 2).srt"},
		{"Kafka in 100 seconds.zh-Hant-TW.ass", 1, "Kafka in 100 seconds.zh-Hant-TW (notesteme 2026-11-12).ass"},
	}
	for _, c := range cases {
		if got := alternateName(c.name, "2026-11-12", c.n); got != c.want {
			t.Errorf("alternateName(%q, %d) = %q，預期 %q", c.name, c.n, got, c.want)
		}
	}
	if strings.Contains(alternateName("a.srt", "2026-11-12", 1), "/") {
		t.Error("檔名不可含目錄分隔")
	}
}

// createNew 絕不覆蓋已存在的檔案：競態中別人先建立了目標時，回傳 created=false，內容不變，也不留下暫存檔。
func TestCreateNewNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "a.srt"), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := createNew(root, "a.srt", []byte("ours"))
	if err != nil || created {
		t.Fatalf("createNew = %v，%v，預期 false", created, err)
	}
	if read(t, filepath.Join(dir, "a.srt")) != "theirs" {
		t.Error("既有的檔案被覆蓋了")
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("暫存檔殘留：%v", names)
	}
	if created, err := createNew(root, "b.srt", []byte("ours")); err != nil || !created || read(t, filepath.Join(dir, "b.srt")) != "ours" {
		t.Errorf("新檔案應建立：%v，%v", created, err)
	}
}
