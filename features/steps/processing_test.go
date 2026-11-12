package steps

import (
	"errors"
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
	if s.hasTranscriptSource("yt:abc123") {
		t.Error("什麼都沒準備")
	}

	// 影片有人工字幕軌，但原文語言未知：不符合 caption_priority。
	v := s.World.Video("abc123")
	v.Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if s.hasTranscriptSource("yt:abc123") {
		t.Error("原文語言未知時，字幕軌不符合條件")
	}
	v.Language = "en"
	if !s.hasTranscriptSource("yt:abc123") {
		t.Error("符合條件的平台字幕軌")
	}

	// 只有機器翻譯的字幕軌不算。
	s.World.Video("tr0001").Language = "en"
	s.World.Video("tr0001").Tracks = []faketool.Track{{Kind: faketool.KindPlatformTranslated, Lang: "zh-Hans"}}
	if s.hasTranscriptSource("yt:tr0001") {
		t.Error("機器翻譯的字幕軌不符合條件")
	}

	// 腳本化的 fallback 來源。
	s.World.Sources.Returns("supadata", "yt:abc999", "supadata-en.json")
	if !s.hasTranscriptSource("yt:abc999") {
		t.Error("腳本化了 supadata")
	}
	s.World.Sources.Nothing("supadata", "yt:none01")
	if s.hasTranscriptSource("yt:none01") {
		t.Error("has nothing 不算來源")
	}

	// 已 seed 的字幕。
	s.seeded = map[string]bool{"yt:seed01": true}
	if !s.hasTranscriptSource("yt:seed01") {
		t.Error("已 seed 的 transcript")
	}
}

func TestPendingWithoutItemFixture(t *testing.T) {
	s := newTestScenario(t)
	// 沒有準備來源，目標步驟需要字幕：pending。
	if err := s.pendingWithoutItemFixture("yt:abc123", "transcript"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("err = %v", err)
	}
	// 目標是 metadata：不需要字幕來源。
	if err := s.pendingWithoutItemFixture("yt:abc123", "metadata"); err != nil {
		t.Errorf("err = %v", err)
	}
	// 有準備來源：不 pending，之後沒處理到就是失敗。
	s.World.Sources.Returns("supadata", "yt:abc123", "supadata-en.json")
	if err := s.pendingWithoutItemFixture("yt:abc123", "normalize"); err != nil {
		t.Errorf("err = %v", err)
	}
}
