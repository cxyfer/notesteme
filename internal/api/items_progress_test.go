package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// LIB-R24：只有 running 的步驟有 progress 與 provider；total 為 0 時省略 progress、沒有 provider 時省略 provider。
func TestToDetailRunningStageProgress(t *testing.T) {
	d := pipeline.Detail{
		Item: store.Item{ID: "i1", Platform: "youtube", ExternalID: "abc"},
		Stages: []store.StageRun{
			{ID: "r1", Stage: "transcript", Status: store.StatusDone},
			{ID: "r2", Stage: "correct", Status: store.StatusRunning},
			{ID: "r3", Stage: "translate", Status: store.StatusRunning},
		},
		Running: map[string]pipeline.RunningStage{
			"r2": {Done: 4, Total: 5, Provider: "groq"},
			"r3": {Done: 0, Total: 0},
		},
	}
	out := toDetail(d)
	if out.Stages[0].Progress != nil || out.Stages[0].Provider != "" {
		t.Fatalf("done 的步驟不該有 progress、provider：%+v", out.Stages[0])
	}
	if p := out.Stages[1].Progress; p == nil || p.Done != 4 || p.Total != 5 || out.Stages[1].Provider != "groq" {
		t.Fatalf("running 的步驟：%+v", out.Stages[1])
	}
	b, err := json.Marshal(out.Stages[2])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"progress"`) || strings.Contains(string(b), `"provider"`) {
		t.Fatalf("沒有任務、沒有 provider 時應省略兩個欄位：%s", b)
	}
}
