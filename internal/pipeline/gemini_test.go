package pipeline

import (
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

func geminiOver(extra map[string]any) map[string]any {
	note := map[string]any{"executor": "api", "engine": "gemini", "provider": "gemini", "model": "m", "prompt": "note-gemini-default"}
	for k, v := range extra {
		note[k] = v
	}
	return map[string]any{"stages": map[string]any{StageNote: note}}
}

// GEM-R2：engine: gemini 時 transcript 到 frames 都略過（gemini），優先於其他原因；metadata、sponsorblock、note、publish 照常。
func TestResolveExecutorGeminiSkips(t *testing.T) {
	eff := mustEffective(t, geminiOver(nil))
	yt := StageContext{Platform: "youtube"}
	for _, stage := range Stages {
		res, err := ResolveExecutor(eff, stage, yt)
		if err != nil {
			t.Fatalf("%s：%v", stage, err)
		}
		want := geminiSkippedStages[stage]
		if got := res.Skip && res.Reason == SkipGemini; got != want {
			t.Errorf("%s = %+v，預期略過（gemini）= %v", stage, res, want)
		}
	}
	// frames 預設 off，也改成 gemini（場景要求 skipped with the reason gemini）。
	if res, _ := ResolveExecutor(eff, StageFrames, yt); res.Reason != SkipGemini {
		t.Errorf("frames = %+v", res)
	}
	// note 本身是 api，planChain 視為已實作。
	c, err := planChain(eff, StageNote, "", yt)
	if err != nil || len(c) != 1 || c[0].Skip || c[0].Executor != ExecutorAPI {
		t.Errorf("planChain(note) = %+v %v", c, err)
	}
	// engine: pipeline、agent、off 不看 engine。
	for name, over := range map[string]map[string]any{
		"pipeline": geminiOver(map[string]any{"engine": "pipeline"}),
		"agent":    geminiOver(map[string]any{"executor": "agent"}),
		"off":      geminiOver(map[string]any{"executor": "off"}),
	} {
		e := mustEffective(t, over)
		if geminiEngine(e) {
			t.Errorf("%s：geminiEngine 應為 false", name)
		}
		if res, _ := ResolveExecutor(e, StageTranscript, yt); res.Skip {
			t.Errorf("%s：transcript = %+v", name, res)
		}
	}
	// M3-S4 之後 note 的一般流程已實作：engine: pipeline 走 api，不再是 not_implemented。
	c, _ = planChain(mustEffective(t, geminiOver(map[string]any{"engine": "pipeline", "provider": "anthropic"})), StageNote, StageNote, yt)
	if len(c) != 1 || c[0].Skip || c[0].Executor != ExecutorAPI {
		t.Errorf("engine: pipeline 的 note = %+v", c)
	}
}

func TestGeminiMaxDuration(t *testing.T) {
	if d := geminiMaxDuration(mustEffective(t, geminiOver(nil))); d != GeminiDefaultMaxDuration {
		t.Errorf("預設 = %v", d)
	}
	if d := geminiMaxDuration(mustEffective(t, geminiOver(map[string]any{"gemini": map[string]any{"max_duration": "45m"}}))); d != 45*time.Minute {
		t.Errorf("45m = %v", d)
	}
}

// GEM-R3：呼叫前的檢查與訊息。
func TestGeminiPrecheck(t *testing.T) {
	secs := func(n int) *int { return &n }
	cases := []struct {
		name string
		it   store.Item
		msg  string
	}{
		{"公開的 YouTube", store.Item{Platform: "youtube", ExternalID: "gem001xxxxx", Availability: "public", DurationS: secs(600)}, ""},
		{"沒有 availability", store.Item{Platform: "youtube", ExternalID: "gem001xxxxx"}, ""},
		{"剛好等於上限", store.Item{Platform: "youtube", ExternalID: "gem001xxxxx", DurationS: secs(3600)}, ""},
		{"Bilibili", store.Item{Platform: "bilibili", ExternalID: "BV1gm4y1c7mD"}, "Gemini 直接生成只支援 YouTube 影片。"},
		{"ID 不是 11 字元", store.Item{Platform: "youtube", ExternalID: "x&list=1"}, "Gemini 直接生成只支援 YouTube 影片。"},
		{"unlisted", store.Item{Platform: "youtube", ExternalID: "gem002xxxxx", Availability: "unlisted"}, "Gemini 只能讀取公開的 YouTube 影片，這部影片的狀態是 unlisted。"},
		{"availability 不可信", store.Item{Platform: "youtube", ExternalID: "gem002xxxxx", Availability: "x\n<script>"}, "Gemini 只能讀取公開的 YouTube 影片，這部影片的狀態是 unknown。"},
		{"太長", store.Item{Platform: "youtube", ExternalID: "gem003xxxxx", DurationS: secs(5400)}, "影片長度 1:30:00 超過 Gemini 直接生成的上限 1:00:00。"},
	}
	for _, tc := range cases {
		err := geminiPrecheck(tc.it, time.Hour)
		if tc.msg == "" {
			if err != nil {
				t.Errorf("%s：%v", tc.name, err)
			}
			continue
		}
		var ce *errclass.Error
		if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || ce.Message != tc.msg || len(ce.Actions) != 1 || ce.Actions[0] != ActionGeminiFallback {
			t.Errorf("%s：%+v", tc.name, err)
		}
	}
	if u := geminiFileURI(store.Item{ExternalID: "gem001xxxxx", URL: "https://youtu.be/gem001xxxxx?t=5"}); u != "https://www.youtube.com/watch?v=gem001xxxxx" {
		t.Errorf("geminiFileURI = %s", u)
	}
}
