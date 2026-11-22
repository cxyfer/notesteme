package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pathguard"
)

// publishTwo 發布 SRT 與 VTT 兩個檔案，回傳它們的路徑（srt、vtt）。
func publishTwo(t *testing.T, r *publishRig) (en, zh string) {
	t.Helper()
	if _, err := r.publish(r.it, sidecarOut("formats", []any{"srt", "vtt"})); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(r.root, "lecture.en.srt"), filepath.Join(r.root, "lecture.en.vtt")
}

// DEL-R9：預設保留輸出檔；指定 DeleteOutputs 時只刪沒被改過的，改過的列在 KeptOutputs。
func TestTrashOutputsKeptByDefaultAndDeletedOnRequest(t *testing.T) {
	r := newPublishRig(t)
	en, zh := publishTwo(t, r)
	ctx := context.Background()

	res, err := r.p.Trash(ctx, r.it.ID, TrashOptions{})
	if err != nil || len(res.KeptOutputs) != 0 {
		t.Fatalf("Trash = %+v，%v", res, err)
	}
	if !exists(en) || !exists(zh) {
		t.Fatal("預設要保留輸出檔")
	}
	if _, err := r.p.Restore(ctx, r.it.ID); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(zh, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = r.p.Trash(ctx, r.it.ID, TrashOptions{DeleteOutputs: true})
	if err != nil {
		t.Fatal(err)
	}
	if exists(en) {
		t.Error("沒被改過的輸出檔應該被刪除")
	}
	if !exists(zh) || readText(t, zh) != "edited by hand" {
		t.Error("改過的輸出檔不能刪除")
	}
	if len(res.KeptOutputs) != 1 || res.KeptOutputs[0].Path != zh || res.KeptOutputs[0].Reason != KeptModified {
		t.Errorf("KeptOutputs = %+v", res.KeptOutputs)
	}
	// 紀錄保留：還原後重新發布才認得這些檔案。
	if _, err := r.st.Read().Output(ctx, r.it.ID, en); err != nil {
		t.Errorf("移到垃圾桶不能刪除 outputs 紀錄：%v", err)
	}
}

// 已經不存在的檔案略過；symlink 不跟隨（目標不動、列為保留）。
func TestTrashDeleteOutputsSkipsMissingAndNeverFollowsSymlinks(t *testing.T) {
	r := newPublishRig(t)
	en, zh := publishTwo(t, r)
	if err := os.Remove(en); err != nil {
		t.Fatal(err)
	}
	// zh 換成指向外面同內容檔案的 symlink。
	outside := filepath.Join(t.TempDir(), "target.srt")
	if err := os.WriteFile(outside, []byte(readText(t, zh)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(zh); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, zh); err != nil {
		t.Skip("無法建立 symlink：", err)
	}

	res, err := r.p.Trash(context.Background(), r.it.ID, TrashOptions{DeleteOutputs: true})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(outside) || !exists(zh) {
		t.Error("symlink 與它的目標都不能被刪除")
	}
	if len(res.KeptOutputs) != 1 || res.KeptOutputs[0].Path != zh || res.KeptOutputs[0].Reason != KeptModified {
		t.Errorf("KeptOutputs = %+v，預期只有 symlink（不存在的略過）", res.KeptOutputs)
	}
}

// 紀錄的路徑現在不在允許的目錄內：不碰（保留，原因 outside_roots）。
func TestTrashDeleteOutputsOutsideRootsKeepsFile(t *testing.T) {
	r := newPublishRig(t)
	en, _ := publishTwo(t, r)
	r.p.files = pathguard.New([]string{t.TempDir()}) // 設定改過：原本的資料夾不在根目錄之內了

	res, err := r.p.Trash(context.Background(), r.it.ID, TrashOptions{DeleteOutputs: true})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(en) {
		t.Fatal("不在允許目錄內的檔案不能刪除")
	}
	if len(res.KeptOutputs) != 2 || res.KeptOutputs[0].Reason != KeptOutsideRoots {
		t.Errorf("KeptOutputs = %+v", res.KeptOutputs)
	}
}

// DEL-R3：預覽列出會刪除與會保留的輸出檔，唯讀；不存在的不列。
func TestDeletionPreviewListsOutputs(t *testing.T) {
	r := newPublishRig(t)
	en, zh := publishTwo(t, r)
	if err := os.WriteFile(zh, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	pv, err := r.p.DeletionPreview(context.Background(), r.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Outputs) != 2 {
		t.Fatalf("Outputs = %+v", pv.Outputs)
	}
	got := map[string]PreviewOutput{}
	for _, o := range pv.Outputs {
		got[o.Path] = o
	}
	if o := got[en]; o.Modified || o.Reason != "" {
		t.Errorf("沒改過的：%+v", o)
	}
	if o := got[zh]; !o.Modified || o.Reason != KeptModified {
		t.Errorf("改過的：%+v", o)
	}
	if !exists(en) || readText(t, zh) != "edited by hand" {
		t.Error("預覽不能改變任何檔案")
	}
	if err := os.Remove(en); err != nil {
		t.Fatal(err)
	}
	if pv, _ = r.p.DeletionPreview(context.Background(), r.it.ID); len(pv.Outputs) != 1 {
		t.Errorf("不存在的檔案不列：%+v", pv.Outputs)
	}
}
