package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/vault"
)

// vaultRig 是 vault 發布的測試環境：一部 YouTube 影片（標題 Designing a Cache，發布日 2025-08-09）、
// 一份 corrected/en 字幕、fs vault main（根目錄 root）。
type vaultRig struct {
	*rig
	it   store.Item
	root string
}

const vaultNote = "Video Notes/YouTube/Designing a Cache - 2025-08-09.md"
const vaultSRT = "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt"

func newVaultRig(t *testing.T) *vaultRig {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	r := newRig(t, func(d *Deps) {
		d.Vaults = map[string]vault.Def{
			"main": {ID: "main", Adapter: vault.AdapterFS, Path: root, Base: "Video Notes"},
			"repo": {ID: "repo", Adapter: vault.AdapterGit, Remote: "https://git.example.com/v.git", Branch: "main", Base: "Video Notes"},
		}
	})
	pub := time.Date(2025, 8, 9, 10, 0, 0, 0, time.UTC)
	r.yt.info = youtube.Info{ID: "abc123xxxxx", Title: "Designing a Cache", Language: "en", Channel: "Systems Weekly", PublishedAt: &pub}
	it := r.ingest("https://www.youtube.com/watch?v=abc123xxxxx", nil, StageMetadata)
	r.run()
	it, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "Caches trade memory for latency.")
	return &vaultRig{rig: r, it: it, root: root}
}

func vaultOut(extra ...any) map[string]any {
	m := map[string]any{"target": "vault", "vault": "main"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

func (r *vaultRig) publish(outs ...map[string]any) (result, error) {
	r.t.Helper()
	list := make([]any, len(outs))
	for i, o := range outs {
		list[i] = o
	}
	eff, err := BuildEffective(map[string]any{"outputs": list})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.p.runPublish(context.Background(), r.it, eff)
}

func (r *vaultRig) seedNote(md string) {
	r.t.Helper()
	if err := r.p.SeedNote(context.Background(), r.it.ID, md); err != nil {
		r.t.Fatal(err)
	}
}

func (r *vaultRig) file(rel string) string {
	r.t.Helper()
	return readText(r.t, filepath.Join(r.root, filepath.FromSlash(rel)))
}

// insertNoteDoc 直接寫入一份筆記產出（自訂來源紀錄的 checks.passed 與 render）。
func (r *vaultRig) insertNoteDoc(doc noteDoc, passed bool) {
	r.t.Helper()
	body, _ := json.Marshal(doc)
	prov, _ := json.Marshal(map[string]any{"stage": StageNote, "checks": map[string]any{"passed": passed}})
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(r.dir, r.it.ID, id, func(w io.Writer) error { _, e := w.Write(body); return e })
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.Tx(context.Background(), func(tx *store.Repo) error {
		_, err := tx.InsertArtifact(context.Background(), store.NewArtifact{ID: id, ItemID: r.it.ID, Kind: KindNote, Variant: KindNote,
			Lang: doc.Lang, Format: noteFmt, Path: rel, SHA256: sha, Size: size, Provenance: prov})
		return err
	}); err != nil {
		r.t.Fatal(err)
	}
}

// VAULT-R2、R5：筆記與字幕寫進 vault，紀錄的路徑相對於 vault 根目錄，筆記排在前面。
func TestVaultPublishWritesNoteAndSubtitles(t *testing.T) {
	r := newVaultRig(t)
	r.seedNote("# Designing a Cache\nCaches trade memory for latency.\n")
	res, err := r.publish(vaultOut())
	if err != nil || res.state.Status != store.StatusDone || res.state.Message != "" {
		t.Fatalf("runPublish = %+v，%v", res, err)
	}
	if got := r.file(vaultNote); !strings.Contains(got, "Caches trade memory") {
		t.Errorf("筆記 = %q", got)
	}
	if got := r.file(vaultSRT); !strings.Contains(got, "00:00:00,000 -->") {
		t.Errorf("字幕 = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(r.root, vaultNote)); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("權限 = %v，%v", fi, err)
	}
	outs, err := r.p.Outputs(context.Background(), r.it.ID)
	if err != nil || len(outs) != 2 {
		t.Fatalf("Outputs = %+v，%v", outs, err)
	}
	if o := outs[0]; o.Path != vaultNote || o.Kind != store.OutputKindNote || o.Vault != "main" || o.Target != "vault" || o.State != store.OutputStateWritten {
		t.Errorf("筆記紀錄 = %+v", o)
	}
	if o := outs[1]; o.Path != vaultSRT || o.Kind != store.OutputKindSubtitle || o.Format != "srt" || o.Lang != "en" {
		t.Errorf("字幕紀錄 = %+v", o)
	}
	// 沒有改變時是 unchanged。
	if _, err := r.publish(vaultOut()); err != nil {
		t.Fatal(err)
	}
	if o := recordFor(t, r.rig, r.it, vaultNote); o.State != store.OutputStateUnchanged {
		t.Errorf("第二次發布的狀態 = %q", o.State)
	}
	// NoteView 也列出 vault 的檔案（GET /api/items/:id/note 的 outputs）。
	v, err := r.p.ItemNote(context.Background(), r.it.ID, "")
	if err != nil || len(v.Outputs) != 2 || v.Outputs[0].Path != vaultNote {
		t.Errorf("ItemNote.Outputs = %+v，%v", v.Outputs, err)
	}
}

// VAULT-R6：使用者改過的筆記另存；skip 時擋下且只更新狀態，不動 sha256。
func TestVaultPublishKeepsUserEdits(t *testing.T) {
	r := newVaultRig(t)
	r.seedNote("v1\n")
	if _, err := r.publish(vaultOut()); err != nil {
		t.Fatal(err)
	}
	before := recordFor(t, r.rig, r.it, vaultNote)
	if err := os.WriteFile(filepath.Join(r.root, vaultNote), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.seedNote("v2\n")
	res, err := r.publish(vaultOut("on_user_change", "skip"))
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassNeedsAction || ec.Actions[0] != actionRepublish {
		t.Fatalf("skip 應擋下：%+v，%v", res, err)
	}
	if r.file(vaultNote) != "mine" {
		t.Error("使用者的檔案被覆蓋")
	}
	if o := recordFor(t, r.rig, r.it, vaultNote); o.State != store.OutputStateUserChanged || o.SHA256 != before.SHA256 {
		t.Errorf("skip 的紀錄 = %+v", o)
	}
	if _, err := r.publish(vaultOut()); err != nil {
		t.Fatal(err)
	}
	alt := "Video Notes/YouTube/Designing a Cache - 2025-08-09 (notesteme 2026-11-12).md"
	if r.file(alt) != "v2\n" || r.file(vaultNote) != "mine" {
		t.Error("save_as_new 沒有另存")
	}
	if o := recordFor(t, r.rig, r.it, alt); o.State != store.OutputStateSavedAsNew {
		t.Errorf("另存的狀態 = %q", o.State)
	}
}

// VAULT-R4：vault 內指向外面的連結：不寫入，permanent，訊息指出路徑。
func TestVaultPublishRefusesEscapingSymlink(t *testing.T) {
	r := newVaultRig(t)
	r.seedNote("x\n")
	outside := t.TempDir()
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.root, "Video Notes")); err != nil {
		t.Fatal(err)
	}
	_, err := r.publish(vaultOut())
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassPermanent || ec.Message != "vault 內的 Video Notes 是指向 vault 以外的連結，已拒絕寫入。" {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("寫到了 vault 外面：%v", entries)
	}
	// 最後一段是逃逸的連結：同樣拒絕。
	_ = os.Remove(filepath.Join(r.root, "Video Notes"))
	if err := os.MkdirAll(filepath.Join(r.root, "Video Notes", "YouTube"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "f"), filepath.Join(r.root, vaultNote)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.publish(vaultOut()); !errors.As(err, &ec) || !strings.Contains(ec.Message, "Designing a Cache - 2025-08-09.md 是指向 vault 以外的連結") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "f")); !errors.Is(err, os.ErrNotExist) {
		t.Error("跟隨了連結")
	}
}

// NOTE-R7、N21：沒有通過自動檢查的筆記不寫入。
func TestVaultPublishRefusesFailedNote(t *testing.T) {
	r := newVaultRig(t)
	r.insertNoteDoc(noteDoc{Lang: "en", Markdown: "bad"}, false)
	_, err := r.publish(vaultOut())
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassNeedsAction || ec.Message != msgVaultNoteChecks {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.root, vaultNote)); !errors.Is(err, os.ErrNotExist) {
		t.Error("寫入了沒通過檢查的筆記")
	}
	// 來源紀錄沒有 checks 時依產出的檢查結果。
	if noteChecksPassed(store.Artifact{}, noteDoc{Checks: []notes.Check{{ID: "frontmatter", OK: false}}}) {
		t.Error("沒有來源紀錄時應依 checks")
	}
}

// VAULT-R5：formats: [] 只寫筆記；沒有字幕或沒有筆記時以訊息說明；不存在的 vault 是 permanent。
func TestVaultPublishFormatsAndMessages(t *testing.T) {
	r := newVaultRig(t)
	r.seedNote("x\n")
	res, err := r.publish(vaultOut("formats", []any{}))
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatal(res, err)
	}
	if _, err := os.Stat(filepath.Join(r.root, vaultSRT)); !errors.Is(err, os.ErrNotExist) {
		t.Error("formats: [] 不應寫字幕")
	}
	// 指定沒有的語言：列在訊息中；多個語言時字幕檔名帶語言。
	res, err = r.publish(vaultOut("lang", []any{"en", "ja"}))
	if err != nil || res.state.Message != msgPublishNoLang+"ja。" {
		t.Fatalf("missing = %+v，%v", res, err)
	}
	if !strings.Contains(r.file("Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.en.srt"), "Caches") {
		t.Error("多語言的字幕檔名沒有語言")
	}

	r2 := newVaultRig(t) // 沒有筆記：只寫字幕
	res, err = r2.publish(vaultOut())
	if err != nil || res.state.Message != msgVaultNoNote {
		t.Fatalf("沒有筆記 = %+v，%v", res, err)
	}
	if _, err := r2.publish(vaultOut("vault", "gone")); err == nil || !strings.Contains(err.Error(), "沒有這個 vault") {
		t.Errorf("不存在的 vault：%v", err)
	}
}

func TestVaultPublishWithoutTranscript(t *testing.T) {
	r := newRig(t, func(d *Deps) {
		d.Vaults = map[string]vault.Def{"main": {ID: "main", Adapter: vault.AdapterFS, Path: filepath.Join(t.TempDir(), "v"), Base: "B"}}
	})
	r.yt.cues = 0
	it := r.ingest("https://www.youtube.com/watch?v=abc123xxxxx", nil, StageMetadata)
	r.run()
	if err := r.p.SeedNote(context.Background(), it.ID, "n"); err != nil {
		t.Fatal(err)
	}
	eff, _ := BuildEffective(map[string]any{"outputs": []any{vaultOut()}})
	res, err := r.p.runPublish(context.Background(), it, eff)
	if err != nil || res.state.Status != store.StatusDone || res.state.Message != msgVaultNoSubtitles {
		t.Fatalf("沒有字幕 = %+v，%v", res, err)
	}
}

// VAULT-R5、ADR-0020 第 4 點：筆記嵌入的畫面寫到 _Assets，筆記與 frames 版本標成 pinned。
func TestVaultPublishWritesFramesAndPins(t *testing.T) {
	r := newVaultRig(t)
	ctx := context.Background()
	if err := r.p.SeedFrames(ctx, r.it.ID, []SeedFrame{{AtMS: 5000, Selected: true, Caption: "c"}, {AtMS: 9000}}); err != nil {
		t.Fatal(err)
	}
	name := notes.FrameFileName(r.it.Title, 5000, 1)
	r.seedNote("![[" + name + "]]\n")
	if _, err := r.publish(vaultOut("formats", []any{})); err != nil {
		t.Fatal(err)
	}
	asset := "Video Notes/YouTube/_Assets/Designing a Cache/" + name
	if b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(asset))); err != nil || len(b) < 100 {
		t.Fatalf("畫面沒有寫出：%v", err)
	}
	if o := recordFor(t, r.rig, r.it, asset); o.Kind != store.OutputKindAsset || o.Format != "jpg" {
		t.Errorf("畫面紀錄 = %+v", o)
	}
	for _, kind := range []string{KindNote, KindFrames} {
		arts, _ := r.st.Read().CurrentArtifacts(ctx, r.it.ID, kind)
		if len(arts) != 1 || !arts[0].Pinned {
			t.Errorf("%s 沒有標成 pinned：%+v", kind, arts)
		}
	}
	// 筆記引用的畫面找不到 frames 產出時：permanent。
	r.insertNoteDoc(noteDoc{Lang: "en", Markdown: "x", Images: []noteImage{{N: 1, File: name}}}, true)
	if _, err := r.publish(vaultOut()); err == nil || !strings.Contains(err.Error(), "找不到筆記引用的關鍵畫面") {
		t.Errorf("err = %v", err)
	}
}

// VAULT V8：筆記的字幕連結依第一個 vault 輸出；publish 的版面不同時提醒。
func TestVaultSubtitleLink(t *testing.T) {
	r := newVaultRig(t)
	eff, _ := BuildEffective(map[string]any{"outputs": []any{
		map[string]any{"target": "folder", "path": "/x"}, vaultOut("formats", []any{"vtt", "srt"}, "folder", "Channel/{channel}"),
	}})
	if got := r.p.vaultSubtitleLink(r.it, eff); got != "Video Notes/Channel/Systems Weekly/_Subtitles/2025-08-09_Designing a Cache.srt" {
		t.Errorf("link = %q", got)
	}
	eff, _ = BuildEffective(map[string]any{"outputs": []any{vaultOut("formats", []any{"vtt"})}})
	if got := r.p.vaultSubtitleLink(r.it, eff); got != "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.vtt" {
		t.Errorf("沒有 srt 時用第一個格式：%q", got)
	}
	for _, e := range []map[string]any{{}, {"outputs": []any{vaultOut("formats", []any{})}}, {"outputs": []any{vaultOut("vault", "gone")}}} {
		eff, _ = BuildEffective(e)
		if got := r.p.vaultSubtitleLink(r.it, eff); got != "" {
			t.Errorf("%v：link = %q", e, got)
		}
	}
	r.insertNoteDoc(noteDoc{Lang: "en", Markdown: "x", Render: noteRender{SubtitleLink: "Video Notes/Old/x.srt"}}, true)
	res, err := r.publish(vaultOut())
	if err != nil || res.state.Message != msgVaultStaleLink {
		t.Errorf("過時的連結 = %+v，%v", res, err)
	}
}

// VAULT-R12、DEL-R9：刪除影音時只刪沒改過的 vault 檔案；設定檔沒有這個 vault 時保留。
func TestVaultDeleteOutputs(t *testing.T) {
	r := newVaultRig(t)
	r.seedNote("x\n")
	if _, err := r.publish(vaultOut()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, vaultSRT), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := r.p.previewOutputs(context.Background(), r.it.ID)
	if err != nil || len(preview) != 2 || preview[0].Modified || !preview[1].Modified {
		t.Fatalf("預覽 = %+v，%v", preview, err)
	}
	kept, err := r.p.deleteOutputs(context.Background(), r.it)
	if err != nil || len(kept) != 1 || kept[0].Path != vaultSRT || kept[0].Reason != KeptModified {
		t.Fatalf("kept = %+v，%v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(r.root, vaultNote)); !errors.Is(err, os.ErrNotExist) {
		t.Error("沒改過的筆記應被刪除")
	}
	// 設定檔已經沒有這個 vault：保留。
	r.p.vaults = nil
	if st := r.p.vaultOutputStatus(store.Output{Vault: "main", Path: vaultSRT}, true); st.Reason != KeptVaultMissing {
		t.Errorf("沒有 vault = %+v", st)
	}
}

func TestVaultInspectFileGuards(t *testing.T) {
	r := newVaultRig(t)
	rec := store.Output{Vault: "main", Path: "a/b.md", SHA256: "x"}
	if st, reason := r.p.inspectVaultFile(filepath.Join(r.root, "missing"), rec, false); st != output.StateMissing || reason != "" {
		t.Errorf("不存在的 vault = %v %q", st, reason)
	}
	outside := t.TempDir()
	_ = os.MkdirAll(r.root, 0o755)
	_ = os.WriteFile(filepath.Join(outside, "b.md"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(r.root, "a")); err != nil {
		t.Fatal(err)
	}
	if st, reason := r.p.inspectVaultFile(r.root, rec, true); st != output.StateModified || reason != KeptModified {
		t.Errorf("經過連結 = %v %q", st, reason)
	}
	if _, err := os.Stat(filepath.Join(outside, "b.md")); err != nil {
		t.Error("刪到了 vault 外面的檔案")
	}
}
