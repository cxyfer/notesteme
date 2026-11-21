package pipeline

import (
	"context"
	"path/filepath"
	"testing"
)

// 備份進行中時，永久刪除照常清掉資料庫與其他目錄，但上傳原始檔等最後一個備份結束才刪。
func TestPurgeDefersUploadRemovalWhileHeld(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch)
	uploads := filepath.Join(r.dir, "uploads", it.ID)
	media := filepath.Join(r.dir, "media", it.ID)

	first := r.p.HoldUploads()
	second := r.p.HoldUploads()
	r.trashDirect(it.ID, r.clk.Now())
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if n := r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, it.ID); n != 0 {
		t.Fatalf("影音仍在資料庫中：%d", n)
	}
	if exists(media) {
		t.Error("media 目錄不受備份影響，應該已經刪除")
	}
	if !exists(uploads) {
		t.Fatal("備份進行中，上傳原始檔不應該被刪除")
	}

	first()
	first() // 重複呼叫不影響另一個備份的保留。
	if !exists(uploads) {
		t.Fatal("還有一個備份進行中，上傳原始檔不應該被刪除")
	}
	second()
	if exists(uploads) {
		t.Error("最後一個備份結束後，延後的上傳目錄應該被刪除")
	}
}

// 沒有備份進行中時，上傳原始檔隨永久刪除立即刪除；保留結束之後的永久刪除也一樣。
func TestPurgeRemovesUploadsWithoutHold(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	release := r.p.HoldUploads()
	release()
	it := r.processed(t, watch)
	r.trashDirect(it.ID, r.clk.Now())
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(r.dir, "uploads", it.ID)) {
		t.Error("沒有備份進行中，上傳目錄應該立即刪除")
	}
}
