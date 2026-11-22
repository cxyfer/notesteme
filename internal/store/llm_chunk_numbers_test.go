package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// LatestLLMChunkNumbers 回傳「最近一次切分」實際存在的編號：由小到大、可以有空洞（translate 沿用的目標不產生 chunk），
// 不混入較早的 stage run、也不混入別的步驟或別的影音；沒有切分過時是空的。
func TestLatestLLMChunkNumbers(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")
	other, _ := seedItem(t, s, "def456xxxxx")
	insert := func(run StageRun, stage string, chunks ...int) {
		t.Helper()
		clk.Advance(time.Second)
		for _, c := range chunks {
			if err := s.Write().InsertLLMChunk(ctx, LLMChunk{
				StageRunID: run.ID, Chunk: c, ItemID: run.ItemID, Stage: stage, CacheKey: "k", Status: ChunkPending,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	get := func(itemID, stage string) []int {
		t.Helper()
		got, err := s.Read().LatestLLMChunkNumbers(ctx, itemID, stage)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := get(it.ID, "translate"); got == nil || len(got) != 0 {
		t.Errorf("從來沒有切分過 = %#v，預期空的（非 nil）切片", got)
	}

	// 較早的 stage run：1..4 連續。
	insert(seedStage(t, s, it.ID, "translate", "sha-1"), "translate", 1, 2, 3, 4)
	if got, want := get(it.ID, "translate"), []int{1, 2, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("連續編號 = %v，預期 %v", got, want)
	}

	// 別的步驟與別的影音各有自己的切分，不影響結果。
	insert(seedStage(t, s, it.ID, "correct", "sha-2"), "correct", 1, 2)
	insert(seedStage(t, s, other.ID, "translate", "sha-3"), "translate", 1, 2, 3, 4, 5, 6, 7)
	if got, want := get(it.ID, "translate"), []int{1, 2, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("別的步驟與影音不該混進來：%v，預期 %v", got, want)
	}

	// 較新的 stage run 前面的目標被沿用：只剩 4～6，只看最近一次（舊的 1～4 不混入），由小到大。
	insert(seedStage(t, s, it.ID, "translate", "sha-4"), "translate", 6, 4, 5)
	if got, want := get(it.ID, "translate"), []int{4, 5, 6}; !slices.Equal(got, want) {
		t.Errorf("有空洞的編號 = %v，預期 %v", got, want)
	}
	if got, want := get(it.ID, "correct"), []int{1, 2}; !slices.Equal(got, want) {
		t.Errorf("correct = %v，預期 %v", got, want)
	}
}
