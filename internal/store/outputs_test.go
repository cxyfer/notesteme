package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOutputsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")
	w := s.Write()

	if _, err := s.Read().Output(ctx, it.ID, "/m/a.en.srt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有紀錄時 err = %v，預期 ErrNotFound", err)
	}
	rec := Output{ItemID: it.ID, Path: "/m/a.en.srt", Target: "sidecar", Format: "srt", Lang: "en", SHA256: "h1", Size: 10}
	if err := w.RecordOutput(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().Output(ctx, it.ID, rec.Path)
	if err != nil || got.SHA256 != "h1" || got.Target != "sidecar" || got.Size != 10 || !got.WrittenAt.Equal(clk.Now()) {
		t.Fatalf("Output = %+v，%v", got, err)
	}

	// 同一個路徑重寫：覆蓋雜湊與時間，不新增一列。
	clk.Advance(time.Hour)
	rec.SHA256, rec.Size = "h2", 20
	if err := w.RecordOutput(ctx, rec); err != nil {
		t.Fatal(err)
	}
	other := Output{ItemID: it.ID, Path: "/m/a.zh.vtt", Target: "folder", Format: "vtt", Lang: "zh-Hant", SHA256: "h3", Size: 5}
	if err := w.RecordOutput(ctx, other); err != nil {
		t.Fatal(err)
	}
	list, err := s.Read().Outputs(ctx, it.ID)
	if err != nil || len(list) != 2 || list[0].SHA256 != "h2" || list[0].Size != 20 || list[1].Path != other.Path {
		t.Fatalf("Outputs = %+v，%v", list, err)
	}
	if !list[0].WrittenAt.Equal(list[1].WrittenAt) || !list[0].WrittenAt.Equal(clk.Now()) {
		t.Errorf("重寫後的時間應是現在：%+v", list)
	}

	// 影音不存在：回傳 ErrNotFound，不留下紀錄。
	if err := w.RecordOutput(ctx, Output{ItemID: "ghost", Path: "/x", Target: "folder", Format: "srt", Lang: "en", SHA256: "h"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("影音不存在 err = %v，預期 ErrNotFound", err)
	}
}

// 永久刪除影音時，輸出紀錄一併刪除（DEL；寫出的檔案本身不動）。
func TestOutputsCascadeOnPurge(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")
	w := s.Write()
	if err := w.RecordOutput(ctx, Output{ItemID: it.ID, Path: "/m/a.en.srt", Target: "sidecar", Format: "srt", Lang: "en", SHA256: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(r *Repo) error { _, _, err := r.TrashItem(ctx, it.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(r *Repo) error { _, err := r.PurgeItem(ctx, it.ID, nil); return err }); err != nil {
		t.Fatal(err)
	}
	if list, err := s.Read().Outputs(ctx, it.ID); err != nil || len(list) != 0 {
		t.Errorf("永久刪除後 Outputs = %+v，%v", list, err)
	}
}
