package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 來源中立的結果可以覆寫時間粒度，並帶上工具版本（Groq 之類的來源會用到）。
func TestFetchResultTimingAndToolVersionsReachProvenance(t *testing.T) {
	r := newRig(t)
	r.p.fetchers["asr-fake"] = fallbackFetcher{
		timing: "segment",
		params: func(Language) any { return nil },
		fetch: func(context.Context, store.Item, Language) (*fetchResult, error) {
			return &fetchResult{
				Lang: "en", Timing: "word", ToolVersions: map[string]string{"ffmpeg": "7.1"},
				Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "hi", Timing: subtitle.TimingSource}},
			}, nil
		},
	}
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %v, %v", arts, err)
	}
	var prov struct {
		Source       string            `json:"source"`
		Timing       string            `json:"timing"`
		ToolVersions map[string]string `json:"tool_versions"`
	}
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov.Source != "asr-fake" || prov.Timing != "word" || prov.ToolVersions["ffmpeg"] != "7.1" || prov.ToolVersions["notesteme"] != "test" {
		t.Errorf("provenance = %+v", prov)
	}
}
