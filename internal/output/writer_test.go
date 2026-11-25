package output

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
	created, err := createNew(root, "a.srt", &payload{data: []byte("ours")})
	if err != nil || created {
		t.Fatalf("createNew = %v，%v，預期 false", created, err)
	}
	if read(t, filepath.Join(dir, "a.srt")) != "theirs" {
		t.Error("既有的檔案被覆蓋了")
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("暫存檔殘留：%v", names)
	}
	if created, err := createNew(root, "b.srt", &payload{data: []byte("ours")}); err != nil || !created || read(t, filepath.Join(dir, "b.srt")) != "ours" {
		t.Errorf("新檔案應建立：%v，%v", created, err)
	}
}

// writeFrom 以 WriteFrom 寫出 f.Data（分成兩段寫，模擬逐句渲染）。
func writeFrom(w *Writer, ctx context.Context, f File) (Result, error) {
	data := f.Data
	f.Data = nil
	return w.WriteFrom(ctx, f, func(out io.Writer) error {
		half := len(data) / 2
		if _, err := out.Write(data[:half]); err != nil {
			return err
		}
		_, err := out.Write(data[half:])
		return err
	})
}

// WriteFrom 與 Write 的結果、紀錄、資料夾內容逐項相同（NFR-RES-3 只改讀寫方式，OUT-R11、R12 的語意不變）：
// 新檔、內容相同、覆蓋、使用者改過後另存、另存的檔案沒改過時覆蓋、另存的也被改過時換檔名、skip 擋下，以及 vault（Root）。
func TestWriteFromMatchesWrite(t *testing.T) {
	type step struct {
		edit   string // 不是空字串時，先把這個檔案改成使用者的內容
		name   string
		data   string
		policy string
	}
	steps := []step{
		{name: "a.en.srt", data: "v1", policy: PolicySaveAsNew},
		{name: "a.en.srt", data: "v1", policy: PolicySaveAsNew},
		{name: "a.en.srt", data: "v2", policy: PolicySaveAsNew},
		{edit: "a.en.srt", name: "a.en.srt", data: "v3", policy: PolicySaveAsNew},
		{name: "a.en.srt", data: "v4", policy: PolicySaveAsNew},
		{edit: "a.en (notesteme 2026-11-12).srt", name: "a.en.srt", data: "v5", policy: PolicySaveAsNew},
		{name: "a.en.srt", data: "v6", policy: PolicySkip},
		{name: "b.en.srt", data: "", policy: PolicySkip},
		{name: "b.en.srt", data: strings.Repeat("長", 50000), policy: PolicySkip},
	}
	type snap struct {
		res   []string
		rows  map[string]store.Output
		files map[string]string
	}
	run := func(t *testing.T, vault bool, write func(*Writer, context.Context, File) (Result, error)) snap {
		dir := t.TempDir()
		m := newMem()
		w := newWriter(m)
		var root *os.Root
		sub := dir
		if vault {
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			root, sub = r, filepath.Join(dir, "Notes", "x")
		}
		var out snap
		for i, s := range steps {
			if s.edit != "" {
				if err := os.WriteFile(filepath.Join(sub, s.edit), []byte("hand edit"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			f := file(dir, s.name, s.data, s.policy)
			if vault {
				f.Root, f.Dir, f.Target, f.Kind, f.Vault = root, "Notes/x", TargetVault, store.OutputKindSubtitle, "main"
			}
			res, err := write(w, context.Background(), f)
			if err != nil {
				t.Fatalf("第 %d 步：%v", i+1, err)
			}
			out.res = append(out.res, string(res.Outcome)+" "+strings.TrimPrefix(res.Path, dir))
		}
		out.rows = map[string]store.Output{}
		for k, v := range m.rows {
			v.Path = strings.TrimPrefix(v.Path, dir)
			out.rows[strings.ReplaceAll(k, dir, "")] = v
		}
		out.files = map[string]string{}
		for _, n := range entries(t, sub) {
			out.files[n] = read(t, filepath.Join(sub, n))
		}
		return out
	}
	for _, vault := range []bool{false, true} {
		t.Run(map[bool]string{false: "folder", true: "vault"}[vault], func(t *testing.T) {
			want := run(t, vault, (*Writer).Write)
			got := run(t, vault, writeFrom)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("WriteFrom 與 Write 不同：\n got %+v\nwant %+v", got, want)
			}
			wantRes := []string{"written", "unchanged", "written", "saved_new", "saved_new", "saved_new", "blocked", "written", "written"}
			for i, r := range got.res {
				if !strings.HasPrefix(r, wantRes[i]+" ") {
					t.Errorf("第 %d 步 = %s，預期 %s", i+1, r, wantRes[i])
				}
			}
			for n := range got.files {
				if strings.HasPrefix(n, ".notesteme-") {
					t.Errorf("暫存檔殘留：%s", n)
				}
			}
		})
	}
}

// render 失敗時不寫任何東西：沒有目標、沒有暫存檔、沒有紀錄，錯誤原樣回傳。
func TestWriteFromRenderFailsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	m := newMem()
	boom := errors.New("boom")
	res, err := newWriter(m).WriteFrom(context.Background(), file(dir, "a.en.srt", "", PolicySaveAsNew), func(out io.Writer) error {
		_, _ = out.Write([]byte("partial"))
		return boom
	})
	if !errors.Is(err, boom) || res != (Result{}) {
		t.Fatalf("WriteFrom = %+v，%v", res, err)
	}
	if names := entries(t, dir); len(names) != 0 {
		t.Errorf("render 失敗時不該留下檔案：%v", names)
	}
	if len(m.rows) != 0 {
		t.Errorf("render 失敗時不該留下紀錄：%v", m.rows)
	}
	if _, err := newWriter(m).WriteFrom(context.Background(), file(dir, "../x", "", PolicySaveAsNew), func(io.Writer) error { return nil }); err == nil {
		t.Error("檔名含目錄分隔時應拒絕")
	}
}

// 已經寫好的暫存檔：目標存在時 createNew 不刪除它（另存新檔的下一個檔名還要用）。
func TestCreateNewKeepsPreparedTemp(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tmp, err := writeTemp(root, []byte("ours"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.srt"), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	pl := &payload{tmp: tmp}
	if created, err := createNew(root, "a.srt", pl); err != nil || created {
		t.Fatalf("createNew = %v，%v，預期 false", created, err)
	}
	if created, err := createNew(root, "b.srt", pl); err != nil || !created || read(t, filepath.Join(dir, "b.srt")) != "ours" {
		t.Fatalf("暫存檔應該還在、可以放到下一個檔名：%v，%v", created, err)
	}
}
