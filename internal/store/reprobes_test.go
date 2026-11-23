package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReprobeRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, sr := seedItem(t, s, "rp1")
	w := s.Write()
	if _, err := w.Reprobe(ctx, it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有時 = %v", err)
	}
	until := clk.Now().Add(7 * 24 * time.Hour)
	est := 1.5
	rp := Reprobe{ItemID: it.ID, Until: until, Runs: 2, Sources: true, SponsorBlock: false, AutoReruns: 1,
		Better: &BetterSource{Source: "youtube-manual", Lang: "en", Kind: "manual", FoundAt: clk.Now(), EstimateUSD: &est}}
	if err := w.SaveReprobe(ctx, rp); err != nil {
		t.Fatal(err)
	}
	got, err := w.Reprobe(ctx, it.ID)
	if err != nil || !got.Until.Equal(until) || got.Runs != 2 || !got.Sources || got.SponsorBlock || got.AutoReruns != 1 ||
		got.Better == nil || got.Better.Source != "youtube-manual" || *got.Better.EstimateUSD != 1.5 || !got.Better.FoundAt.Equal(clk.Now()) {
		t.Fatalf("got = %+v %v", got, err)
	}
	// 取代：清除 better。
	rp.Better, rp.Sources, rp.SponsorBlock = nil, false, true
	if err := w.SaveReprobe(ctx, rp); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Reprobe(ctx, it.ID); got.Better != nil || got.Sources || !got.SponsorBlock {
		t.Fatalf("取代後 = %+v", got)
	}
	// better_json 損壞時視為沒有更好的來源。
	for _, bad := range []string{`not json`, `{"source":""}`, `[]`} {
		if _, err := s.w.ExecContext(ctx, `UPDATE source_reprobes SET better_json = ? WHERE item_id = ?`, bad, it.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := w.Reprobe(ctx, it.ID); err != nil || got.Better != nil {
			t.Fatalf("%q：%+v %v", bad, got, err)
		}
	}

	// SetStageMessage 只改訊息與動作。
	if err := w.SetStageState(ctx, sr.ID, StageState{Status: StatusDone}); err != nil {
		t.Fatal(err)
	}
	before, _ := w.StageRunsByItem(ctx, it.ID)
	clk.Advance(time.Hour)
	run := before[0]
	if err := w.SetStageMessage(ctx, run, "提醒", []string{"動作"}); err != nil {
		t.Fatal(err)
	}
	after, _ := w.StageRunsByItem(ctx, it.ID)
	a := after[0]
	if a.Status != StatusDone || a.Message != "提醒" || len(a.Actions) != 1 || a.Actions[0] != "動作" ||
		(a.FinishedAt == nil) != (run.FinishedAt == nil) || (a.FinishedAt != nil && !a.FinishedAt.Equal(*run.FinishedAt)) {
		t.Fatalf("after = %+v before = %+v", a, run)
	}
	if err := w.SetStageMessage(ctx, StageRun{ID: "missing", ItemID: it.ID}, "x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在 = %v", err)
	}
}

func TestLatestProbesIgnoresReprobe(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "rp2")
	w := s.Write()
	if err := w.InsertProbes(ctx, it.ID, []NewProbe{{SourceID: "youtube-auto", Lang: "en", Kind: "auto", State: "used"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertProbesFor(ctx, it.ID, ProbeTriggerReprobe, []NewProbe{{SourceID: "youtube-manual", Lang: "en", Kind: "manual", State: "used"}}); err != nil {
		t.Fatal(err)
	}
	ps, err := w.LatestProbes(ctx, it.ID)
	if err != nil || len(ps) != 1 || ps[0].SourceID != "youtube-auto" {
		t.Fatalf("probes = %+v %v", ps, err)
	}
	// 之後的 stage probe 仍會取代。
	if err := w.InsertProbes(ctx, it.ID, []NewProbe{{SourceID: "youtube-manual", Lang: "en", Kind: "manual", State: "used"}}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := w.LatestProbes(ctx, it.ID); len(ps) != 1 || ps[0].SourceID != "youtube-manual" {
		t.Fatalf("probes = %+v", ps)
	}
}
