package steps

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

func newTestScenario(t *testing.T) *Scenario {
	t.Helper()
	s := &Scenario{}
	s.reset(harness.NewT(t))
	t.Cleanup(func() { _ = s.close() })
	return s
}

// 「有沒有準備字幕來源」只看場景累積的狀態：平台字幕軌、seed、腳本化的 fallback 來源。
func TestHasTranscriptSource(t *testing.T) {
	s := newTestScenario(t)
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("什麼都沒準備")
	}

	// 影片有人工字幕軌，但原文語言未知：不符合 caption_priority。
	v := s.World.Video("abc123xxxxx")
	v.Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("原文語言未知時，字幕軌不符合條件")
	}
	v.Language = "en"
	if !s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("符合條件的平台字幕軌")
	}

	// 只有機器翻譯的字幕軌不算。
	s.World.Video("tr0001xxxxx").Language = "en"
	s.World.Video("tr0001xxxxx").Tracks = []faketool.Track{{Kind: faketool.KindPlatformTranslated, Lang: "zh-Hans"}}
	if s.hasTranscriptSource("yt:tr0001xxxxx") {
		t.Error("機器翻譯的字幕軌不符合條件")
	}

	// 腳本化的 fallback 來源。
	s.World.Sources.Returns("supadata", "yt:abc999xxxxx", "supadata-en.json")
	if !s.hasTranscriptSource("yt:abc999xxxxx") {
		t.Error("腳本化了 supadata")
	}
	s.World.Sources.Nothing("supadata", "yt:none01xxxxx")
	if s.hasTranscriptSource("yt:none01xxxxx") {
		t.Error("has nothing 不算來源")
	}

	// 已 seed 的字幕。
	s.seeded = map[string]bool{"yt:seed01xxxxx": true}
	if !s.hasTranscriptSource("yt:seed01xxxxx") {
		t.Error("已 seed 的 transcript")
	}
}

func TestItemFixtureFor(t *testing.T) {
	s := newTestScenario(t)
	// 沒有準備來源、也沒有 fixture：pending。
	if _, err := s.itemFixtureFor("yt:abc123xxxxx", "transcript"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("err = %v", err)
	}
	// 目標是 metadata：不需要字幕來源。
	if fx, err := s.itemFixtureFor("yt:abc123xxxxx", "metadata"); err != nil || fx != nil {
		t.Errorf("fx = %v, err = %v", fx, err)
	}
	// 有準備來源：照常跑 pipeline，之後沒處理到就是失敗。
	s.World.Sources.Returns("supadata", "yt:abc123xxxxx", "supadata-en.json")
	if fx, err := s.itemFixtureFor("yt:abc123xxxxx", "normalize"); err != nil || fx != nil {
		t.Errorf("fx = %v, err = %v", fx, err)
	}

	// 有 fixture、影片沒有任何字幕軌：回傳 fixture。
	fx, err := s.itemFixtureFor("yt:pg100xxxxxx", "normalize")
	if err != nil || fx == nil || fx.Source.Lang != "en" || len(fx.Source.Cues) < 3 {
		t.Fatalf("fx = %+v, err = %v", fx, err)
	}
	// 影片有字幕軌（即使不符合 caption_priority）：不用 fixture，也不 pending 以外的結果。
	s.World.Video("pg100xxxxxx").Tracks = []faketool.Track{{Kind: faketool.KindPlatformTranslated, Lang: "zh-Hans"}}
	if _, err := s.itemFixtureFor("yt:pg100xxxxxx", "normalize"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("有字幕軌時不該用 fixture：err = %v", err)
	}
}

func TestItemFixtureForRefMismatch(t *testing.T) {
	s := newTestScenario(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"ref":"yt:other01xxxx","source":{"lang":"en","cues":[]}}`
	if err := os.WriteFile(filepath.Join(dir, "items", "yt-abc123xxxxx.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s.World.FixturesDir = dir
	_, err := s.itemFixtureFor("yt:abc123xxxxx", "transcript")
	if err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("ref 與檔名不一致應失敗而非 pending：err = %v", err)
	}
}

// fixture 規則 2 只適用於 yt: 與 bili:；local: 與 pod: 即使有 fixture 也一律 pending（落到規則 3）。
func TestItemFixtureForOnlyYouTubeAndBilibili(t *testing.T) {
	s := newTestScenario(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"yt:fx0001xxxxx":       "yt-fx0001xxxxx.json",
		"bili:BV1xx411c7mD/p1": "bili-BV1xx411c7mD-p1.json",
		"local:meetup.mp4":     "local-meetup.mp4.json",
		"pod:show/ep1":         "pod-show-ep1.json",
	}
	for ref, name := range files {
		body := `{"ref":"` + ref + `","source":{"lang":"en","cues":[{"start":"00:00:01.000","end":"00:00:03.000","text":"hello"}]}}`
		if err := os.WriteFile(filepath.Join(dir, "items", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.World.FixturesDir = dir
	for _, ref := range []string{"yt:fx0001xxxxx", "bili:BV1xx411c7mD/p1"} {
		if fx, err := s.itemFixtureFor(ref, "normalize"); err != nil || fx == nil || fx.Ref != ref {
			t.Errorf("%s 應該使用 fixture：fx = %+v, err = %v", ref, fx, err)
		}
	}
	for _, ref := range []string{"local:meetup.mp4", "pod:show/ep1"} {
		if fx, err := s.itemFixtureFor(ref, "normalize"); fx != nil || !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s 不該使用 fixture，應該 pending：fx = %+v, err = %v", ref, fx, err)
		}
	}
}

func TestItemFixtureName(t *testing.T) {
	for ref, want := range map[string]string{
		"yt:pg100xxxxxx":       "yt-pg100xxxxxx.json",
		"bili:BV1xx411c7mD/p1": "bili-BV1xx411c7mD-p1.json",
	} {
		if got := itemFixtureName(ref); got != want {
			t.Errorf("itemFixtureName(%q) = %q，預期 %q", ref, got, want)
		}
	}
}
