package pipeline

import (
	"testing"
)

// frames 的略過原因（FRM-R1）：off 優先，其次是沒有影像串流（audio_only），再來是 api 沒有 provider（not_configured）。
func TestResolveFramesSkips(t *testing.T) {
	cases := []struct {
		cfg    map[string]any
		sc     StageContext
		skip   bool
		reason string
	}{
		{map[string]any{"executor": "off"}, StageContext{AudioOnly: true}, true, SkipOff},
		{map[string]any{"executor": "rules"}, StageContext{AudioOnly: true}, true, SkipAudioOnly},
		{map[string]any{"executor": "api"}, StageContext{AudioOnly: true}, true, SkipAudioOnly},
		{map[string]any{"executor": "api"}, StageContext{}, true, SkipNotConfigured},
		{map[string]any{"executor": "rules"}, StageContext{Platform: "youtube"}, false, ""},
	}
	for _, c := range cases {
		res, err := ResolveExecutor(mustEffective(t, stagesOver(StageFrames, c.cfg)), StageFrames, c.sc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Skip != c.skip || res.Reason != c.reason {
			t.Errorf("%v %+v：%+v，預期 skip=%v %q", c.cfg, c.sc, res, c.skip, c.reason)
		}
	}
	if !stageImplemented(StageFrames, ExecutorRules) || !stageImplemented(StageFrames, ExecutorAPI) || stageImplemented(StageFrames, ExecutorAgent) {
		t.Error("frames 的 rules、api 已實作，agent 還沒有（F6）")
	}
}

func TestFramesParamsAndChapters(t *testing.T) {
	eff := mustEffective(t, stagesOver(StageFrames, map[string]any{
		"executor": "rules", "scene_threshold": 0.4, "min_interval_s": 30, "max_frames": 7, "hint_words": []any{"look"},
	}))
	p, err := framesParams(eff)
	if err != nil {
		t.Fatal(err)
	}
	if p.SceneThreshold != 0.4 || p.MinIntervalS != 30 || p.MaxFrames != 7 || p.DedupeWindow != 5 || len(p.HintWords) != 1 {
		t.Errorf("params = %+v", p)
	}
	chs, ok := parseChaptersDoc([]byte(`{"origin":"llm","chapters":[{"start_ms":0,"title":"Why cache","cue":1},{"start_ms":120000,"title":"Eviction","cue":9}]}`))
	if !ok || len(chs) != 2 || chs[1].StartMS != 120000 || chs[1].Title != "Eviction" {
		t.Errorf("chapters = %+v %v", chs, ok)
	}
	if _, ok := parseChaptersDoc([]byte(`{"origin":"platform"}`)); ok {
		t.Error("沒有 chapters 陣列時不算")
	}
	if got := FrameTimeText(3_723_045); got != "01:02:03.045" {
		t.Errorf("FrameTimeText = %q", got)
	}
	for _, v := range []string{"../../etc", "a/b", "", "短"} {
		if versionPattern.MatchString(v) {
			t.Errorf("版本 %q 不應通過", v)
		}
	}
}
