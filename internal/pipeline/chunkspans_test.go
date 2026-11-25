package pipeline

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func TestChunkSpanStatus(t *testing.T) {
	cases := []struct{ chunk, task, want string }{
		{store.ChunkDone, store.TaskFailed, ChunkSpanDone},
		{store.ChunkFallback, "", ChunkSpanFallback},
		{store.ChunkPending, store.TaskLeased, ChunkSpanRunning},
		{store.ChunkPending, store.TaskWaiting, ChunkSpanRunning},
		{store.ChunkPending, store.TaskFailed, ChunkSpanFailed},
		{store.ChunkPending, store.TaskPending, ChunkSpanPending},
		{store.ChunkPending, "cancelled", ChunkSpanPending},
		{store.ChunkPending, "", ChunkSpanPending},
	}
	for _, c := range cases {
		if got := chunkSpanStatus(c.chunk, c.task); got != c.want {
			t.Errorf("chunkSpanStatus(%q, %q) = %q，預期 %q", c.chunk, c.task, got, c.want)
		}
	}
}

func TestChunkSpanOverlaps(t *testing.T) {
	s := ChunkSpan{StartMS: 600_000, EndMS: 1_199_500, HasTime: true}
	cases := []struct {
		from, to int64
		want     bool
	}{
		{0, 0, true},
		{0, 600_000, false}, // [from, to) 不含 to
		{0, 600_001, true},
		{1_199_500, 0, false}, // 結束時間等於 from 不算重疊
		{1_199_499, 0, true},
		{700_000, 800_000, true},
	}
	for _, c := range cases {
		if got := s.Overlaps(c.from, c.to); got != c.want {
			t.Errorf("Overlaps(%d, %d) = %v，預期 %v", c.from, c.to, got, c.want)
		}
	}
	if (ChunkSpan{}).Overlaps(0, 0) {
		t.Error("沒有時間的 chunk 不應重疊")
	}
}

func TestTranslateUnitsFollowTargetKind(t *testing.T) {
	tr := &subtitle.Transcript{Cues: []subtitle.Cue{{Text: "This is one"}, {Text: "sentence."}, {Text: "Another."}}}
	if got := translateUnits(tr, trnTarget{Kind: TargetLocalize}); len(got) != 3 {
		t.Fatalf("localize 應一個 cue 一個單位，得到 %d 個", len(got))
	}
	got := translateUnits(tr, trnTarget{Kind: TargetTranslate})
	if len(got) != 2 || got[0].First != 0 || got[0].Count != 2 || got[1].First != 2 {
		t.Fatalf("translate 應依句末標點組句群：%+v", got)
	}
}
