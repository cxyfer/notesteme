package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// steps.md 4.13、4.18：`processed up to` M3 的步驟與 M4 的 summary 在產品實作之前是 pending，而且不啟動 server。
// M3-S1～S4 之後 sponsorblock、chapters、frames、note 都已實作（pipeline.StageAvailable 為 true）；M4-S6 之後 summary 也是。
func TestProcessedUpToM3StagePending(t *testing.T) {
	for _, stage := range []string{pipeline.StageSponsorBlock, pipeline.StageChapters, pipeline.StageFrames, pipeline.StageNote, pipeline.StageSummary} {
		if !pipeline.StageAvailable(stage) {
			t.Errorf("%s 應該已經實作", stage)
		}
	}
	// 不認識的名稱仍然失敗（frames.select 已合併成 frames），而且不是 pending。
	s := newTestScenario(t)
	if err := s.itemProcessedUpTo("yt:talk01xxxxx", "frames.select"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("frames.select 應該失敗：%v", err)
	}
}

// 已實作的步驟的 skipped 是重跑的起點：M4-S6 之後 summary 的 skipped（off）也是（同 M3 的四個步驟）。
func TestFirstUnfinishedSkipsUnimplementedM3Stages(t *testing.T) {
	var it itemJSON
	for _, name := range []string{"metadata", "transcript", "normalize", "summary", "segment", "correct", "translate",
		"sponsorblock", "chapters", "frames", "note", "publish"} {
		st := stageJSON{Stage: name, Status: "done"}
		switch name {
		case "summary":
			// M4-S6：summary 已實作，從它重跑不再回 501。
			st.Status, st.BlockReason = "skipped", "off"
		case "note":
			st.Status, st.BlockReason = "skipped", "not_implemented"
		case "publish":
			st.Status = "pending"
		}
		it.Stages = append(it.Stages, st)
	}
	if got := firstUnfinished(it, "publish"); got != "summary" {
		t.Errorf("firstUnfinished = %q，預期 summary", got)
	}
	if got := firstUnfinished(it, "normalize"); got != "" {
		t.Errorf("做到 normalize：firstUnfinished = %q，預期沒有", got)
	}
	if !reached(stageJSON{Stage: "note", Status: "skipped", BlockReason: "off"}, "note") {
		t.Error("M3 的目標步驟 skipped 也算處理到（2c）")
	}
	if !reached(stageJSON{Stage: "summary", Status: "skipped", BlockReason: "off"}, "summary") {
		t.Error("summary 的目標步驟 skipped 也算處理到（2c）")
	}
}
