package pipeline_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/cxyfer/notesteme/internal/benchdata"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// BenchmarkListItems10k 量 pipeline 層的列表耗時（lib-10k 的影音數，資料由 internal/benchdata 產生）。
// 只量 pipeline 與 SQLite，不經過 HTTP；NFR-PERF-1 的門檻由 internal/nfrbench 的 BenchmarkPerf1ItemsList 檢查。
// 每部的句數縮成 20 句：列表不讀字幕內容，句數只影響產生時間。
func BenchmarkListItems10k(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	clk := benchdata.DefaultClock()
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	prof := benchdata.Lib10k
	prof.CuesPerItem = 20
	if _, err := benchdata.Populate(ctx, st, dir, prof, benchdata.Options{Clock: clk, BatchSize: 500}); err != nil {
		b.Fatal(err)
	}
	p := pipeline.New(pipeline.Deps{Store: st, Clock: clk, DataDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test"})
	ip := func(v int) *int { return &v }

	cases := []struct {
		name string
		q    pipeline.ItemQuery
	}{
		{"first_page", pipeline.ItemQuery{Limit: 50}},
		{"max_page", pipeline.ItemQuery{Limit: 200}},
		{"platform", pipeline.ItemQuery{Platform: "youtube", Limit: 50}},
		{"status", pipeline.ItemQuery{Status: map[string][]string{pipeline.StageNormalize: {"done"}, pipeline.StageSegment: {"done", "failed"}}, Limit: 50}},
		{"q", pipeline.ItemQuery{Q: "agile", Limit: 50}},
		{"q_rare", pipeline.ItemQuery{Q: "video 9999 ", Limit: 50}},
		{"timing", pipeline.ItemQuery{Timing: "interpolated", Limit: 50}},
		{"all_filters", pipeline.ItemQuery{Platform: "youtube", Lang: "en", Timing: "interpolated", Q: "postgresql",
			Status: map[string][]string{pipeline.StageNormalize: {"done"}}, DurationMin: ip(10), Limit: 50}},
		{"group_channel", pipeline.ItemQuery{GroupBy: store.GroupChannel, Limit: 200}},
		{"group_published", pipeline.ItemQuery{GroupBy: store.GroupPublished, Limit: 200}},
		{"group_status", pipeline.ItemQuery{GroupBy: store.GroupStatus, Limit: 200}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				page, err := p.ListItems(ctx, c.q)
				if err != nil {
					b.Fatal(err)
				}
				if page.Total == 0 && c.name != "q_rare" {
					b.Fatal("沒有資料")
				}
			}
		})
	}
	// 深頁：游標在最舊的一端，檢查 keyset 不會變慢。
	b.Run("deep_page", func(b *testing.B) {
		first, err := p.ListItems(ctx, pipeline.ItemQuery{Limit: 200})
		if err != nil {
			b.Fatal(err)
		}
		cur := first.Next
		for range 40 {
			page, err := p.ListItems(ctx, pipeline.ItemQuery{Limit: 200, After: cur})
			if err != nil || page.Next == nil {
				b.Fatal(err)
			}
			cur = page.Next
		}
		b.ResetTimer()
		for b.Loop() {
			if _, err := p.ListItems(ctx, pipeline.ItemQuery{Limit: 50, After: cur}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
