package store

import (
	"context"
	"testing"
)

// DeleteLocalTombstonesBySHA 只移除 local 平台、雜湊相同的 tombstone；雜湊為空時什麼都不做。
func TestDeleteLocalTombstonesBySHA(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	addTombstone(t, s, "local", "a.mp4", 0, shaA)
	addTombstone(t, s, "local", "b.mp4", 0, shaA)
	addTombstone(t, s, "local", "c.mp4", 0, shaB)
	addTombstone(t, s, "local", "unhashed.mp4", 0, "")
	addTombstone(t, s, "youtube", "yt-id", 0, shaA)

	r := s.Write()
	if n, err := r.DeleteLocalTombstonesBySHA(ctx, ""); err != nil || n != 0 {
		t.Fatalf("空雜湊：%d %v", n, err)
	}
	if n, err := r.DeleteLocalTombstonesBySHA(ctx, shaA); err != nil || n != 2 {
		t.Fatalf("刪除筆數 = %d %v，要 2", n, err)
	}
	for _, c := range []struct {
		key, sha string
		want     bool
	}{
		{"x", shaA, false}, // local 的 shaA 已移除
		{"x", shaB, true},  // 其他雜湊不受影響
		{"unhashed.mp4", "", true},
	} {
		got, err := r.LocalTombstoneBlocks(ctx, c.key, c.sha)
		if err != nil || got != c.want {
			t.Errorf("LocalTombstoneBlocks(%q, %q) = %v %v，要 %v", c.key, c.sha, got, err, c.want)
		}
	}
	var yt int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_tombstones WHERE platform = 'youtube'`).Scan(&yt); err != nil || yt != 1 {
		t.Errorf("youtube 的 tombstone 不該被刪：%d %v", yt, err)
	}
}
