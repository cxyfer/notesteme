package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// 腳本化來源的前提由 production 宣告的已實作清單決定（source.Implemented）。
func TestScriptableSource(t *testing.T) {
	if err := scriptableSource("supadata"); err != nil {
		t.Errorf("supadata 已實作，應該可以腳本化：%v", err)
	}
	// 平台字幕已實作，由假 yt-dlp 的字幕軌腳本化，不再是 pending。
	for _, id := range []string{"youtube-manual", "youtube-auto"} {
		if err := scriptableSource(id); err != nil {
			t.Errorf("%s 已實作，應該可以腳本化：%v", id, err)
		}
	}
	// groq 由另一個切片實作（source.Implemented），不放進這份「尚未實作」的清單。
	for _, id := range []string{"bilibili-ai", "whisper-1", "podcast-transcript"} {
		if err := scriptableSource(id); !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s 應該讓場景 pending：%v", id, err)
		}
	}
}

// 平台字幕的腳本化寫進假 yt-dlp 的字幕軌狀態。
func TestPlatformSourceScripting(t *testing.T) {
	s := newTestScenario(t)
	if err := s.hasCaptions("yt:mix001xxxxx", "auto", "zh"); err != nil {
		t.Fatal(err)
	}
	if err := s.sourceReturns("youtube-auto", "auto-zh-en-mixed.json", "yt:mix001xxxxx"); err != nil {
		t.Fatal(err)
	}
	tracks := s.World.Tools.Videos["mix001xxxxx"].Tracks
	if len(tracks) != 1 || tracks[0].Kind != faketool.KindAuto || tracks[0].Lang != "zh" || tracks[0].Fixture != "auto-zh-en-mixed.json" {
		t.Fatalf("字幕軌 = %+v", tracks)
	}
	st, err := faketool.ReadState(s.World.Root)
	if err != nil || st.Videos["mix001xxxxx"].Tracks[0].Fixture != "auto-zh-en-mixed.json" {
		t.Fatalf("狀態檔沒有同步：%+v, %v", st, err)
	}

	// 沒有既有字幕軌時用影片的語言；兩者都沒有就失敗（不是 pending）。
	s.World.Video("manual1xxxx").Language = "en"
	if err := s.sourceReturns("youtube-manual", "auto-zh-en-mixed.json", "yt:manual1xxxx"); err != nil {
		t.Fatal(err)
	}
	if tr := s.World.Tools.Videos["manual1xxxx"].Tracks; len(tr) != 1 || tr[0].Kind != faketool.KindManual || tr[0].Lang != "en" {
		t.Fatalf("字幕軌 = %+v", tr)
	}
	if err := s.sourceReturns("youtube-manual", "auto-zh-en-mixed.json", "yt:nolangxxxxx"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Fatalf("沒有語言應該失敗而不是 pending：%v", err)
	}
	// fixture 不存在：失敗。
	if err := s.sourceReturns("youtube-auto", "nope.json", "yt:mix001xxxxx"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Fatalf("fixture 不存在應該失敗：%v", err)
	}

	// has nothing：移除這個種類的字幕軌，其他種類不動。
	if err := s.hasCaptions("yt:mix001xxxxx", "manual", "zh"); err != nil {
		t.Fatal(err)
	}
	if err := s.sourceHasNothing("youtube-auto", "yt:mix001xxxxx"); err != nil {
		t.Fatal(err)
	}
	if tr := s.World.Tools.Videos["mix001xxxxx"].Tracks; len(tr) != 1 || tr[0].Kind != faketool.KindManual {
		t.Fatalf("字幕軌 = %+v", tr)
	}
}

func TestSizePattern(t *testing.T) {
	for _, ok := range []string{"25MB", "100 KB", "2GB"} {
		if !sizePattern.MatchString(ok) {
			t.Errorf("%q 應該有效", ok)
		}
	}
	for _, bad := range []string{"25", "MB", "1.5MB", "25mb"} {
		if sizePattern.MatchString(bad) {
			t.Errorf("%q 應該無效", bad)
		}
	}
}
