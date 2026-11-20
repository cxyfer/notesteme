package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// 同一個 sha 有一筆在垃圾桶、一筆正常：上傳相同內容回 200，指向正常的那筆。
func TestUploadSameContentPrefersLiveItem(t *testing.T) {
	r := newLocalRig(t, nil)
	ctx := context.Background()
	first := decode[itemsBody](t, r.upload("talk.m4a", "AUDIO-one", nil)).Items[0]
	if _, err := r.p.Trash(ctx, first.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var live store.Item
	err := r.st.Tx(ctx, func(rp *store.Repo) error {
		var err error
		live, _, err = rp.InsertItemIfAbsent(ctx, store.NewItem{
			Platform: "local", ExternalID: "other.m4a", SourcePath: "x", ContentSHA256: sha("AUDIO-one"), Origin: store.OriginUpload,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := r.upload("again.m4a", "AUDIO-one", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if it := decode[itemsBody](t, rec).Items[0]; it.ID != live.ID || !it.AlreadyInLibrary {
		t.Errorf("item = %+v，要指向 %s", it, live.ID)
	}
}

// inbox 頂層已有同名檔案：上傳的 ref 避開它。
func TestUploadAvoidsInboxName(t *testing.T) {
	r := newLocalRig(t, nil)
	if err := os.WriteFile(filepath.Join(r.inbox, "talk.m4a"), []byte("AUDIO-inbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := r.upload("talk.m4a", "AUDIO-one", nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if ref := decode[itemsBody](t, rec).Items[0].Ref; ref != "local:talk-2.m4a" {
		t.Errorf("ref = %q", ref)
	}
}
