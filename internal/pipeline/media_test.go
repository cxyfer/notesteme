package pipeline

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestMediaFile(t *testing.T) {
	lr := newIngestRig(t)
	ctx := context.Background()
	path := filepath.Join(lr.root, "talk.mp4")
	if err := os.WriteFile(path, []byte("VIDEO-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := lr.ingestLocal(LocalTarget{Key: "talk.mp4", SourcePath: path, SHA256: "aa", Origin: store.OriginInbox}).Item

	f, fi, got, err := lr.p.MediaFile(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "VIDEO-bytes" || fi.Size() != int64(len(b)) || got.ID != it.ID {
		t.Errorf("內容 = %q size = %d item = %s", b, fi.Size(), got.ID)
	}

	// 上傳的檔案：source_path 相對資料目錄。
	up, _, err := lr.upload("talk.m4a", "AUDIO-up", "s1")
	if err != nil {
		t.Fatal(err)
	}
	f, _, _, err = lr.p.MediaFile(ctx, up.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(f)
	f.Close()
	if string(b) != "AUDIO-up" {
		t.Errorf("上傳檔內容 = %q", b)
	}

	// 檔案不見：ErrMediaMissing；目錄也一樣（不是一般檔案）。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := lr.p.MediaFile(ctx, it.ID); !errors.Is(err, ErrMediaMissing) {
		t.Errorf("檔案不見：%v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := lr.p.MediaFile(ctx, it.ID); !errors.Is(err, ErrMediaMissing) {
		t.Errorf("目錄：%v", err)
	}

	// 垃圾桶：InTrashError。
	if _, err := lr.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var tr *InTrashError
	if _, _, _, err := lr.p.MediaFile(ctx, it.ID); !errors.As(err, &tr) {
		t.Errorf("垃圾桶：%v", err)
	}
	// 沒有這部影音。
	if _, _, _, err := lr.p.MediaFile(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
}

// 非本機影音沒有媒體快取：ErrNoMedia。
func TestMediaFileNoMedia(t *testing.T) {
	lr := newIngestRig(t)
	yt := lr.ingest("https://www.youtube.com/watch?v=abc123xxxxx", nil, "")
	if _, _, _, err := lr.p.MediaFile(context.Background(), yt.ID); !errors.Is(err, ErrNoMedia) {
		t.Errorf("err = %v", err)
	}
	// 本機影音但沒有 source_path，同樣沒有媒體。
	local := lr.ingestLocal(LocalTarget{Key: "x.mp4", Origin: store.OriginManual}).Item
	if _, _, _, err := lr.p.MediaFile(context.Background(), local.ID); !errors.Is(err, ErrNoMedia) {
		t.Errorf("沒有 source_path：%v", err)
	}
}

// root 之外：pathguard.ErrOutsideRoots（API 回 403）。
func TestMediaFileOutsideRoots(t *testing.T) {
	lr := newIngestRig(t)
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("VIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := lr.ingestLocal(LocalTarget{Key: outside, SourcePath: outside, Origin: store.OriginManual}).Item
	if _, _, _, err := lr.p.MediaFile(context.Background(), it.ID); !errors.Is(err, pathguard.ErrOutsideRoots) {
		t.Errorf("err = %v", err)
	}
}
