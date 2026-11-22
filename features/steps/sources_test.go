package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/source"
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
	// groq 與 local-sidecar／local-embedded 由 LOCAL 切片實作（source.Implemented），不放進這份「尚未實作」的清單。
	if err := scriptableSource("whisper-1"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("whisper-1 應該讓場景 pending：%v", err)
	}
	// podcast-transcript 由 PODCAST 切片實作：是否 pending 取決於 production 的宣告，不在這裡寫死。
	if err := scriptableSource("podcast-transcript"); source.Implemented("podcast-transcript") == errors.Is(err, godog.ErrPending) {
		t.Errorf("podcast-transcript 的 pending 狀態與 source.Implemented 不一致：%v", err)
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

// X4：第一次建立的 YouTube 影片都在同一個預設頻道；`with:` 的 channel、channel_id 可以覆寫，覆寫不會被之後的步驟洗掉。
func TestYouTubeVideoDefaultChannel(t *testing.T) {
	s := newTestScenario(t)
	if DefaultChannelID != "UCfixture000000000000000" || len(DefaultChannelID) != 24 || DefaultChannelName != "Fixture Channel" {
		t.Fatalf("預設頻道 = %q / %q", DefaultChannelID, DefaultChannelName)
	}
	for _, ref := range []string{"yt:chan01xxxxx", "yt:chan02xxxxx"} {
		if err := s.youtubeVideoTitled(ref, "A talk"); err != nil {
			t.Fatal(err)
		}
	}
	// 其他步驟第一次建立影片時也套用預設值。
	if err := s.editVideo("yt:chan03xxxxx", func(*faketool.Video) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"chan01xxxxx", "chan02xxxxx", "chan03xxxxx"} {
		v := s.World.Tools.Videos[id]
		if v.ChannelID != DefaultChannelID || v.Channel != DefaultChannelName {
			t.Errorf("%s 的頻道 = %q / %q，預期預設頻道", id, v.ChannelID, v.Channel)
		}
	}

	// `with:` 覆寫 channel 與 channel_id；第一次建立時就覆寫，沒有指定的欄位維持預設。
	if err := s.youtubeVideoWith("yt:chan04xxxxx", table(
		[]string{"field", "value"}, []string{"channel", "Other Channel"}, []string{"channel_id", "UCother0000000000000000"},
	)); err != nil {
		t.Fatal(err)
	}
	if v := s.World.Tools.Videos["chan04xxxxx"]; v.ChannelID != "UCother0000000000000000" || v.Channel != "Other Channel" {
		t.Errorf("覆寫後的頻道 = %q / %q", v.ChannelID, v.Channel)
	}
	if err := s.youtubeVideoWith("yt:chan05xxxxx", table([]string{"field", "value"}, []string{"channel", "Only Name"})); err != nil {
		t.Fatal(err)
	}
	if v := s.World.Tools.Videos["chan05xxxxx"]; v.ChannelID != DefaultChannelID || v.Channel != "Only Name" {
		t.Errorf("只覆寫 channel 的頻道 = %q / %q", v.ChannelID, v.Channel)
	}

	// 影片已存在：再編輯其他欄位不會把覆寫的頻道改回預設值。
	if err := s.youtubeVideoTitled("yt:chan04xxxxx", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if v := s.World.Tools.Videos["chan04xxxxx"]; v.ChannelID != "UCother0000000000000000" || v.Channel != "Other Channel" {
		t.Errorf("再編輯後的頻道 = %q / %q，不該被洗掉", v.ChannelID, v.Channel)
	}
}

// Bilibili 影片不套用預設頻道。
func TestBilibiliVideoHasNoDefaultChannel(t *testing.T) {
	s := newTestScenario(t)
	if err := s.bilibiliVideo("BV1xx411c7mD", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.editVideo("bili:BV1xx411c7mD/p1", func(v *faketool.Video) error { v.Title = "P1"; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"BV1xx411c7mD", "BV1xx411c7mD/p1", "BV1xx411c7mD/p2"} {
		if v := s.World.Tools.Videos[key]; v.ChannelID != "" || v.Channel != "" {
			t.Errorf("%s 的頻道 = %q / %q，Bilibili 不套用預設頻道", key, v.ChannelID, v.Channel)
		}
	}
}
