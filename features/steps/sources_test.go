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
	for _, id := range []string{"whisper-1", "podcast-transcript", "local-sidecar"} {
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

func TestVideoKeyAndBilibiliVideo(t *testing.T) {
	s := newTestScenario(t)
	// 分P 不存在（影片還沒建立、超過分P 數、p0）時失敗。
	for _, ref := range []string{"bili:BV1xx411c7mD/p1", "yt:", "pod:feed/ep1", "local:a.mp4"} {
		if _, err := s.videoKey(ref); err == nil {
			t.Errorf("%q 應該失敗", ref)
		}
	}
	if err := s.bilibiliVideo("BV1xx411c7mD", 3); err != nil {
		t.Fatal(err)
	}
	if key, err := s.videoKey("bili:BV1xx411c7mD/p3"); err != nil || key != "BV1xx411c7mD/p3" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	for _, ref := range []string{"bili:BV1xx411c7mD/p4", "bili:BV1xx411c7mD/p0"} {
		if _, err := s.videoKey(ref); err == nil {
			t.Errorf("%q 應該失敗（只有 3 個分P）", ref)
		}
	}
	if key, err := s.videoKey("yt:abc123xxxxx"); err != nil || key != "abc123xxxxx" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	// 各分P 沒有字幕軌、沒有原文語言，標題預設為 BV 號。
	for n := 1; n <= 3; n++ {
		p := s.World.Tools.Videos[faketool.PartKey("BV1xx411c7mD", n)]
		if p == nil || p.Title != "BV1xx411c7mD" || p.Language != "" || len(p.Tracks) != 0 {
			t.Errorf("p%d = %+v", n, p)
		}
	}
	if v := s.World.Tools.Videos["BV1xx411c7mD"]; v.Parts != 3 {
		t.Errorf("Parts = %d", v.Parts)
	}
	if err := s.bilibiliVideo("bili:BV1xx411c7mD", 1); err == nil {
		t.Error("帶 bili: 的參數應該失敗")
	}
	// 重新建立不會洗掉已設定的分P 字幕軌。
	if err := s.hasCaptions("bili:BV1xx411c7mD/p2", "auto", "zh-Hans"); err != nil {
		t.Fatal(err)
	}
	if err := s.bilibiliVideo("BV1xx411c7mD", 3); err != nil {
		t.Fatal(err)
	}
	if len(s.World.Tools.Videos["BV1xx411c7mD/p2"].Tracks) != 1 {
		t.Error("字幕軌被洗掉")
	}
	// Bilibili 沒有平台翻譯字幕。
	if err := s.hasCaptions("bili:BV1xx411c7mD/p1", "platform-translated", "en"); err == nil {
		t.Error("platform-translated 應該失敗")
	}
}

// Bilibili 的平台來源：腳本化寫進分P 的字幕軌，並檢查來源與平台相符。
func TestBilibiliPlatformSources(t *testing.T) {
	s := newTestScenario(t)
	if err := s.bilibiliVideo("BV1ai411c7mD", 1); err != nil {
		t.Fatal(err)
	}
	ref := "bili:BV1ai411c7mD/p1"
	if err := s.hasCaptions(ref, "auto", "zh-Hans"); err != nil {
		t.Fatal(err)
	}
	if err := s.platformReturns("bilibili-ai", "bilibili-ai-zh.json", ref); err != nil {
		t.Fatal(err)
	}
	tr := s.World.Tools.Videos["BV1ai411c7mD/p1"].Tracks
	if len(tr) != 1 || tr[0].Kind != faketool.KindAuto || tr[0].Lang != "zh-Hans" || tr[0].Fixture != "bilibili-ai-zh.json" {
		t.Fatalf("字幕軌 = %+v", tr)
	}
	// 來源與平台不符：失敗，不是 pending。
	if err := s.platformReturns("bilibili-ai", "bilibili-ai-zh.json", "yt:abc123xxxxx"); err == nil {
		t.Error("對 yt: 影片腳本化 bilibili-ai 應該失敗")
	}
	if err := s.platformReturns("youtube-auto", "bilibili-ai-zh.json", ref); err == nil {
		t.Error("對 bili: 影音腳本化 youtube-auto 應該失敗")
	}
	// bilibili-cc 對應 manual。
	if err := s.platformNothing("bilibili-ai", ref); err != nil || len(s.World.Tools.Videos["BV1ai411c7mD/p1"].Tracks) != 0 {
		t.Fatalf("has nothing：%v", err)
	}
	// 需要 cookie：參數是 cookie 的平台，寫進假 yt-dlp 的狀態。
	if err := s.platformRequiresCookie("bilibili-ai", "bilibili"); err != nil {
		t.Fatal(err)
	}
	st, err := faketool.ReadState(s.World.Root)
	if err != nil || st.CookieKinds[faketool.KindAuto] != "bilibili" {
		t.Fatalf("狀態檔 = %+v, %v", st, err)
	}
	if err := s.platformRequiresCookie("bilibili-ai", "youtube"); err == nil {
		t.Error("cookie 平台不符應該失敗")
	}
	if err := s.platformRequiresCookie("youtube-auto", "youtube"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("youtube-* 應該失敗：%v", err)
	}
}
