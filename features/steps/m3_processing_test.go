package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// steps.md 4.13：`processed up to` M3 的步驟在產品實作之前是 pending，而且不啟動 server。
// sponsorblock、chapters 已在 M3-S1、S2 實作（pipeline.StageAvailable 為 true），不在其中。
func TestProcessedUpToM3StagePending(t *testing.T) {
	for _, stage := range []string{"frames", "note"} {
		s := newTestScenario(t)
		if err := s.itemProcessedUpTo("yt:talk01xxxxx", stage); !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s：err = %v，預期 pending", stage, err)
		}
		if s.server != nil {
			t.Errorf("%s：不該啟動 server", stage)
		}
	}
	// 其他不認識的名稱仍然失敗（frames.select 已合併成 frames）。
	s := newTestScenario(t)
	if err := s.itemProcessedUpTo("yt:talk01xxxxx", "frames.select"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("frames.select 應該失敗：%v", err)
	}
}

// 還沒有實作的 M3 步驟一律 skipped（not_implemented）：重跑不能從它開始（會回 501）。sponsorblock 已實作，這裡是 done。
func TestFirstUnfinishedSkipsUnimplementedM3Stages(t *testing.T) {
	var it itemJSON
	for _, name := range []string{"metadata", "transcript", "normalize", "segment", "correct", "translate",
		"sponsorblock", "chapters", "frames", "note", "publish"} {
		st := stageJSON{Stage: name, Status: "done"}
		switch name {
		case "frames", "note":
			st.Status, st.BlockReason = "skipped", "not_implemented"
		case "publish":
			st.Status = "pending"
		}
		it.Stages = append(it.Stages, st)
	}
	if got := firstUnfinished(it, "publish"); got != "publish" {
		t.Errorf("firstUnfinished = %q，預期 publish", got)
	}
	if !reached(stageJSON{Stage: "note", Status: "skipped", BlockReason: "off"}, "note") {
		t.Error("M3 的目標步驟 skipped 也算處理到（2c）")
	}
}
