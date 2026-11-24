package pipeline

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/youtube"
)

// estimateRig 是有 Groq（有 key）與 Supadata 的 rig；影片預設 600 秒、沒有任何字幕軌。
func estimateRig(t *testing.T, mods ...func(*Deps)) (*rig, *fakeGroq, *fakeSupadata) {
	t.Helper()
	g := &fakeGroq{lang: "en", baseURL: groq.DefaultBaseURL, model: groq.DefaultModel}
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newRig(t, append([]func(*Deps){func(d *Deps) { d.Groq = g; d.Supadata = sd }}, mods...)...)
	d := 600
	r.yt.info = youtube.Info{ID: "abc123xxxxx", Title: "PostgreSQL in 100 Seconds", Language: "en", DurationS: &d}
	return r, g, sd
}

func (r *rig) estimate(req IngestRequest) (*Estimate, error) {
	r.t.Helper()
	return r.p.Estimate(context.Background(), req)
}

// noWrites 確認估價沒有寫入任何影音、任務與來源紀錄。
func (r *rig) noWrites() {
	r.t.Helper()
	for _, table := range []string{"items", "stage_runs", "tasks", "source_probes", "channels", "config_snapshots"} {
		if n := r.count("SELECT COUNT(*) FROM " + table); n != 0 {
			r.t.Errorf("估價不應寫入 %s：%d 筆", table, n)
		}
	}
}

func near(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 1e-9 }

func srcOf(it EstimateItem) string {
	if it.ExpectedSource == nil {
		return "<nil>"
	}
	return *it.ExpectedSource
}

func hasNote(it EstimateItem, sub string) bool {
	for _, n := range it.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// ING-R8：有平台字幕時，預計使用平台來源，ASR 費用為 0；不建立影音。
func TestEstimateWithPlatformCaptionsHasNoASRCost(t *testing.T) {
	r, g, sd := estimateRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || len(est.Items) != 1 {
		t.Fatalf("Estimate = %+v, %v", est, err)
	}
	it := est.Items[0]
	if srcOf(it) != "youtube-auto" || !near(it.ASRUSD, 0) || !near(it.TotalUSD, 0) || !near(it.LLMUSD, 0) {
		t.Errorf("item = %+v", it)
	}
	if it.Ref != "yt:abc123xxxxx" || it.Title != "PostgreSQL in 100 Seconds" || !near(it.DurationS, 600) || it.RequiresCookie || it.AlreadyInLibrary {
		t.Errorf("item = %+v", it)
	}
	if est.TotalUSD != 0 || est.Currency != "USD" || est.PricesAsOf != "2026-11-21" || len(est.Notes) != 0 {
		t.Errorf("est = %+v", est)
	}
	if g.calls() != 0 || sd.calls() != 0 {
		t.Errorf("估價不應呼叫付費來源：groq=%d supadata=%d", g.calls(), sd.calls())
	}
	r.noWrites()
}

// 人工字幕優先於自動字幕，來源跟著選中的字幕軌。
func TestEstimateUsesTheSelectedTrackSource(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}, {Lang: "en", Kind: "manual"}}
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || srcOf(est.Items[0]) != "youtube-manual" {
		t.Fatalf("est = %+v, %v", est, err)
	}
}

// 沒有字幕時以 Groq 單價計算：600 秒 × 0.04 / 3600。
func TestEstimateWithoutCaptionsUsesGroqPrice(t *testing.T) {
	r, g, _ := estimateRig(t)
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	want := 600.0 * 0.04 / 3600
	if srcOf(it) != "groq" || math.Abs(*it.ASRUSD-want) > 1e-6 || math.Abs(*it.TotalUSD-want) > 1e-6 || math.Abs(est.TotalUSD-want) > 1e-6 {
		t.Errorf("item = %+v，total = %v，預期 %v", it, est.TotalUSD, want)
	}
	if g.calls() != 0 {
		t.Errorf("估價不應轉錄：%d", g.calls())
	}
	// 換 model：whisper-large-v3 的單價是 0.111。
	g.model = "whisper-large-v3"
	est, _ = r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if want := 600.0 * 0.111 / 3600; math.Abs(*est.Items[0].ASRUSD-want) > 1e-6 {
		t.Errorf("v3 asr = %v，預期 %v", *est.Items[0].ASRUSD, want)
	}
	// 不認得的 model 沒有內建單價：金額未知。
	g.model = "custom-whisper"
	est, _ = r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if it := est.Items[0]; srcOf(it) != "groq" || it.ASRUSD != nil || !hasNote(it, "內建單價") || len(est.Notes) != 1 {
		t.Errorf("item = %+v，notes = %v", it, est.Notes)
	}
	r.noWrites()
}

// fallback 中沒有 key 的來源略過，改用下一個。
func TestEstimateSkipsUnconfiguredSources(t *testing.T) {
	// groq 沒有 key，改用 supadata。
	r, g, _ := estimateRig(t)
	g.noKey = true
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq", "supadata")})
	if err != nil || srcOf(est.Items[0]) != "supadata" || !hasNote(est.Items[0], "已略過 groq") {
		t.Fatalf("est = %+v, %v", est, err)
	}
	// supadata 沒有設定，改用 groq。
	r2, _, _ := estimateRig(t, func(d *Deps) { d.Supadata = nil })
	est, err = r2.estimate(IngestRequest{URL: watch})
	if err != nil || srcOf(est.Items[0]) != "groq" || math.Abs(*est.Items[0].ASRUSD-600.0*0.04/3600) > 1e-6 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	if !hasNote(est.Items[0], "已略過 supadata") {
		t.Errorf("notes = %v", est.Items[0].Notes)
	}
}

// Supadata 以 credit 計價：asr_usd 為 0，source_usd 未知（null），頂層註明部分金額未知。
func TestEstimateSupadataHasUnknownPrice(t *testing.T) {
	r, _, _ := estimateRig(t)
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	if srcOf(it) != "supadata" || !near(it.ASRUSD, 0) || it.SourceUSD != nil || !hasNote(it, "credit") {
		t.Errorf("item = %+v", it)
	}
	if len(est.Notes) != 1 || est.Notes[0] != "部分金額未知" {
		t.Errorf("notes = %v", est.Notes)
	}
}

// 沒有任何可用的來源：expected_source 為 null，並說明原因。
func TestEstimateWithoutAnySource(t *testing.T) {
	r, g, _ := estimateRig(t)
	g.noKey = true
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	if it.ExpectedSource != nil || !near(it.ASRUSD, 0) || !hasNote(it, "沒有可用的字幕來源") {
		t.Errorf("item = %+v", it)
	}
	// fallback 是空的也一樣。
	est, err = r.estimate(IngestRequest{URL: watch, Overrides: map[string]any{"language": map[string]any{"fallback": []any{}}}})
	if err != nil || est.Items[0].ExpectedSource != nil || !hasNote(est.Items[0], "沒有可用的字幕來源") {
		t.Errorf("est = %+v, %v", est, err)
	}
}

// 長度未知時 asr_usd 為 null，不計入總額，頂層註明部分金額未知。
func TestEstimateUnknownDuration(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.info.DurationS = nil
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	// 這個 rig 沒有付費的 LLM 步驟，LLM 費用確定是 0（已知），所以總額是 0 而不是 null。
	if srcOf(it) != "groq" || it.DurationS != nil || it.ASRUSD != nil || !near(it.LLMUSD, 0) || !near(it.TotalUSD, 0) || est.TotalUSD != 0 {
		t.Errorf("item = %+v，total = %v", it, est.TotalUSD)
	}
	if !hasNote(it, "長度") || len(est.Notes) != 1 || est.Notes[0] != "部分金額未知" {
		t.Errorf("notes = %v／%v", it.Notes, est.Notes)
	}
}

// overrides.language.fallback 會生效；overrides 不正確時回 422，不呼叫 yt-dlp。
func TestEstimateOverridesApply(t *testing.T) {
	r, _, _ := estimateRig(t)
	// 預設是 supadata 優先；覆寫成只用 groq。
	est, _ := r.estimate(IngestRequest{URL: watch})
	if srcOf(est.Items[0]) != "supadata" {
		t.Fatalf("預設 = %s", srcOf(est.Items[0]))
	}
	est, _ = r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if srcOf(est.Items[0]) != "groq" {
		t.Errorf("覆寫後 = %s", srcOf(est.Items[0]))
	}
	// 覆寫 caption_priority：只要手動字幕，自動字幕就不採用，改走 fallback。
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	est, _ = r.estimate(IngestRequest{URL: watch, Overrides: map[string]any{"language": map[string]any{
		"caption_priority": []any{map[string]any{"lang": "original", "kind": "manual"}}, "fallback": []any{"groq"},
	}}})
	if srcOf(est.Items[0]) != "groq" {
		t.Errorf("只要手動字幕時 = %s", srcOf(est.Items[0]))
	}
	probes := r.yt.probes
	var inv *InvalidError
	if _, err := r.estimate(IngestRequest{URL: watch, Overrides: map[string]any{"language": map[string]any{"bogus": 1}}}); !errors.As(err, &inv) {
		t.Errorf("err = %v", err)
	}
	if r.yt.probes != probes {
		t.Errorf("覆寫不正確時不應 Probe")
	}
}

// 全域設定也會生效。
func TestEstimateUsesGlobalSettings(t *testing.T) {
	r, _, _ := estimateRig(t)
	if _, err := r.p.SetSettings(context.Background(), "language", map[string]any{"fallback": []any{"groq"}}); err != nil {
		t.Fatal(err)
	}
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || srcOf(est.Items[0]) != "groq" {
		t.Fatalf("est = %+v, %v", est, err)
	}
}

// 播放清單展開成多項，total_usd 是各項已知金額的和。
func TestEstimatePlaylistSumsTotals(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.playlist = threeVideos()
	est, err := r.estimate(IngestRequest{URL: playlistURL, Overrides: fallbackOverride("groq")})
	if err != nil || len(est.Items) != 3 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	want := 3 * 600.0 * 0.04 / 3600
	if math.Abs(est.TotalUSD-want) > 1e-5 {
		t.Errorf("total = %v，預期 %v", est.TotalUSD, want)
	}
	if len(r.yt.playlists) != 1 || r.yt.probes != 3 {
		t.Errorf("展開 %v 次、Probe %d 次", r.yt.playlists, r.yt.probes)
	}
	r.noWrites()
}

// 播放清單中有一部 Probe 失敗：整份估價不失敗，該項說明原因，總額只算已知的。
func TestEstimatePlaylistItemFailureIsReportedPerItem(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.playlist = threeVideos()
	r.yt.probeErr = &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "timed out"}
	est, err := r.estimate(IngestRequest{URL: playlistURL})
	if err != nil || len(est.Items) != 3 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	for _, it := range est.Items {
		if it.ASRUSD != nil || it.TotalUSD != nil || !hasNote(it, "yt-dlp") {
			t.Errorf("item = %+v", it)
		}
	}
	// 三部都失敗：頂層 notes 說明全部失敗，以及部分金額未知。
	if est.TotalUSD != 0 || len(est.Notes) != 2 || !hasTopNote(est, "所有影音都無法取得資訊") {
		t.Errorf("est = %+v", est)
	}
}

// 單一影音 Probe 失敗時對應成錯誤：暫時性 502、不存在 422、找不到 yt-dlp 502。
func TestEstimateSingleProbeFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		inv  bool
	}{
		{"暫時性失敗", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "timed out"}, false},
		{"影音不存在", &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "Video unavailable"}, true},
		{"找不到 yt-dlp", &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: errors.New("not found")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := estimateRig(t)
			r.yt.probeErr = tc.err
			_, err := r.estimate(IngestRequest{URL: watch})
			var inv *InvalidError
			var up *UpstreamError
			if tc.inv && !errors.As(err, &inv) || !tc.inv && !errors.As(err, &up) {
				t.Fatalf("err = %v（%T）", err, err)
			}
			r.noWrites()
		})
	}
}

// Bilibili 的 parts：只估算指定的分P，每個分P Probe 一次。
func TestEstimateBilibiliParts(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.parts = threeParts()
	d := 1800
	fb.info.DurationS = &d
	fb.info.Tracks = []string{"ai-zh"}
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: biliBase, Parts: []int{3, 1}})
	if err != nil || len(est.Items) != 2 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	if est.Items[0].Ref != "bili:BV1xx411c7mD/p1" || est.Items[1].Ref != "bili:BV1xx411c7mD/p3" {
		t.Errorf("refs = %s, %s", est.Items[0].Ref, est.Items[1].Ref)
	}
	for _, it := range est.Items {
		if srcOf(it) != "bilibili-ai" || !near(it.ASRUSD, 0) || !near(it.DurationS, 1800) {
			t.Errorf("item = %+v", it)
		}
	}
	// 網址有 p：不展開，只有一項。
	fb.partsCalls = nil
	est, err = r.p.Estimate(context.Background(), IngestRequest{URL: biliP1})
	if err != nil || len(est.Items) != 1 || len(fb.partsCalls) != 0 {
		t.Fatalf("est = %+v, %v，Parts 呼叫 %v", est, err, fb.partsCalls)
	}
	r.noWrites()
}

// Bilibili 看不到字幕（要登入）：標示 requires_cookie；Groq 不支援 Bilibili 的音訊，所以不會估成語音轉錄。
func TestEstimateBilibiliNeedsLogin(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.NeedsLogin = true
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: biliP1})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	if !it.RequiresCookie || srcOf(it) != "bilibili-ai" || !hasNote(it, bilibili.MsgAICaptionsNeedLogin) {
		t.Errorf("item = %+v", it)
	}
}

// 已在媒體庫中的影音照樣估價，另外標示。
func TestEstimateMarksItemsAlreadyInLibrary(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	r.ingest(watch, nil, "")
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || !est.Items[0].AlreadyInLibrary || srcOf(est.Items[0]) != "youtube-auto" {
		t.Fatalf("est = %+v, %v", est, err)
	}
	// 估價沒有再建立影音。
	if n := r.count("SELECT COUNT(*) FROM items"); n != 1 {
		t.Errorf("items = %d", n)
	}
}

// 需要 cookie 的影片（availability）標示 requires_cookie。
func TestEstimateFlagsRequiresCookie(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.info.Availability = "subscriber_only"
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || !est.Items[0].RequiresCookie {
		t.Fatalf("est = %+v, %v", est, err)
	}
}

// 無法辨識、頻道、處理方案：與新增相同的錯誤，而且不呼叫 yt-dlp。
func TestEstimateRejections(t *testing.T) {
	r, _, _ := estimateRig(t)
	if _, err := r.estimate(IngestRequest{URL: "https://www.youtube.com/@someone"}); !errors.Is(err, ErrChannel) {
		t.Errorf("頻道：err = %v", err)
	}
	if _, err := r.estimate(IngestRequest{URL: "not a url"}); !errors.Is(err, ErrUnrecognizedURL) {
		t.Errorf("無法辨識：err = %v", err)
	}
	var inv *InvalidError
	if _, err := r.estimate(IngestRequest{URL: watch, Profile: "fast"}); !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "profile" {
		t.Errorf("處理方案不存在：err = %v", err)
	}
	if _, err := r.estimate(IngestRequest{URL: watch, Parts: []int{1}}); !errors.As(err, &inv) {
		t.Errorf("YouTube 的 parts：err = %v", err)
	}
	// until 可以帶，估價時忽略（即使不正確）。
	if _, err := r.estimate(IngestRequest{URL: watch, Until: "bogus"}); err != nil {
		t.Errorf("until 應被忽略：err = %v", err)
	}
	r.noWrites()
}

// Podcast：長度與字幕來自 feed，不 Probe；沒有 feed 字幕時走 fallback。
func TestEstimatePodcastEpisodes(t *testing.T) {
	feed := podFeed("The Show",
		podEp{guid: "ep-41", title: "Episode 41", audio: audio41, extra: `<itunes:duration>1800</itunes:duration>`},
		podEp{guid: "ep-42", title: "Episode 42", audio: audio42, extra: `<itunes:duration>3600</itunes:duration>`},
	)
	r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	g := &fakeGroq{model: groq.DefaultModel, baseURL: groq.DefaultBaseURL}
	r.p.groq = g
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42", "ep-41"}})
	if err != nil || len(est.Items) != 2 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	// Supadata 沒有設定，Supadata 也不處理 feed 網址；兩集都走 groq。
	if it := est.Items[0]; it.Ref != "pod:show/ep-42" || it.Title != "Episode 42" || srcOf(it) != "groq" || math.Abs(*it.ASRUSD-0.04) > 1e-6 {
		t.Errorf("ep-42 = %+v", it)
	}
	if it := est.Items[1]; math.Abs(*it.ASRUSD-0.02) > 1e-6 {
		t.Errorf("ep-41 = %+v", it)
	}
	if math.Abs(est.TotalUSD-0.06) > 1e-6 {
		t.Errorf("total = %v", est.TotalUSD)
	}
	if r.yt.probes != 0 {
		t.Errorf("Podcast 不應 Probe：%d", r.yt.probes)
	}
	r.noWrites()

	// 沒有指定 episodes：同新增，422。
	var inv *InvalidError
	if _, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL}); !errors.As(err, &inv) {
		t.Errorf("err = %v", err)
	}
}

// Podcast 的 feed 字幕依 transcript 步驟的選軌規則判斷：讀得懂、語言相符才不計 ASR；
// HTML 或語言不符（feed 是 en、字幕只有 ja）的集走 fallback。
func TestEstimatePodcastTranscriptSelection(t *testing.T) {
	feed := podFeed("The Show",
		podEp{guid: "vtt-en", title: "A", audio: audio41, extra: `<itunes:duration>3600</itunes:duration><podcast:transcript url="https://media.example.com/a.vtt" type="text/vtt" language="en"/>`},
		podEp{guid: "html", title: "B", audio: audio41, extra: `<itunes:duration>3600</itunes:duration><podcast:transcript url="https://media.example.com/b.html" type="text/html"/>`},
		podEp{guid: "vtt-ja", title: "C", audio: audio41, extra: `<itunes:duration>3600</itunes:duration><podcast:transcript url="https://media.example.com/c.vtt" type="text/vtt" language="ja"/>`},
	)
	r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	r.p.groq = &fakeGroq{model: groq.DefaultModel, baseURL: groq.DefaultBaseURL}
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"vtt-en", "html", "vtt-ja"}})
	if err != nil || len(est.Items) != 3 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	want := []struct {
		src string
		asr float64
	}{{"podcast-transcript", 0}, {"groq", 0.04}, {"groq", 0.04}}
	for i, w := range want {
		it := est.Items[i]
		if srcOf(it) != w.src || it.ASRUSD == nil || math.Abs(*it.ASRUSD-w.asr) > 1e-6 {
			t.Errorf("item %d（%s）= source %q、asr %v，想要 %q、%v", i, it.Ref, srcOf(it), it.ASRUSD, w.src, w.asr)
		}
	}
	r.noWrites()
}

// 已經新增過的 Podcast 單集標示 already_in_library。
func TestEstimatePodcastAlreadyInLibrary(t *testing.T) {
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	if _, err := r.ingestPodcast(showURL, "ep-42"); err != nil {
		t.Fatal(err)
	}
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-41", "ep-42"}})
	if err != nil || len(est.Items) != 2 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	if est.Items[0].AlreadyInLibrary || !est.Items[1].AlreadyInLibrary {
		t.Errorf("already_in_library = %v, %v", est.Items[0].AlreadyInLibrary, est.Items[1].AlreadyInLibrary)
	}
}

// 估價不寫入 Podcast 的頻道（feedKey 只在新增時建立）。
func TestEstimatePodcastDoesNotCreateChannel(t *testing.T) {
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	if _, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}}); err != nil {
		t.Fatal(err)
	}
	r.noWrites()
}

// 查詢媒體庫失敗（找不到之外的錯誤）時整份估價失敗，不能當成「不在媒體庫中」回傳 200。
func TestEstimateLibraryLookupFailureIsAnError(t *testing.T) {
	t.Run("YouTube", func(t *testing.T) {
		r, _, _ := estimateRig(t)
		r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
		if _, err := r.rawDB().Exec(`ALTER TABLE items RENAME TO items_unreadable`); err != nil {
			t.Fatal(err)
		}
		var le *libraryError
		if est, err := r.estimate(IngestRequest{URL: watch}); !errors.As(err, &le) {
			t.Fatalf("est = %+v, err = %v", est, err)
		}
	})
	t.Run("Podcast", func(t *testing.T) {
		r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
		if _, err := r.ingestPodcast(showURL, "ep-42"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.rawDB().Exec(`ALTER TABLE items RENAME TO items_unreadable`); err != nil {
			t.Fatal(err)
		}
		var le *libraryError
		if est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}}); !errors.As(err, &le) {
			t.Fatalf("est = %+v, err = %v", est, err)
		}
	})
}
