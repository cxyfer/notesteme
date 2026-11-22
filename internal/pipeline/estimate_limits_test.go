package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
)

func hasTopNote(est *Estimate, sub string) bool {
	for _, n := range est.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// 估價和新增一樣以 feed 網址找 feed key：已經新增成 show-2 的 feed 用 show-2，並標示已在媒體庫；
// 還沒新增過、slug 又被佔用的 feed 用新增此刻會取得的候選（show-3）。估價不建立頻道。
func TestEstimatePodcastFeedKeyMatchesIngest(t *testing.T) {
	const (
		otherURL = "https://other.example.org/show.xml"
		thirdURL = "https://third.example.net/show.xml"
	)
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed(), otherURL: showFeed(), thirdURL: showFeed()})
	for _, u := range []string{showURL, otherURL} {
		if _, err := r.ingestPodcast(u, "ep-42"); err != nil {
			t.Fatal(err)
		}
	}
	channels := r.count(`SELECT COUNT(*) FROM channels`)

	for _, tc := range []struct {
		url, ref string
		inLib    bool
	}{
		{otherURL, "pod:show-2/ep-42", true},
		{thirdURL, "pod:show-3/ep-42", false},
	} {
		est, err := r.p.Estimate(context.Background(), IngestRequest{URL: tc.url, Episodes: []string{"ep-42"}})
		if err != nil || len(est.Items) != 1 {
			t.Fatalf("%s：est = %+v, %v", tc.url, est, err)
		}
		if it := est.Items[0]; it.Ref != tc.ref || it.AlreadyInLibrary != tc.inLib {
			t.Errorf("%s：ref = %q、already_in_library = %v，想要 %q、%v", tc.url, it.Ref, it.AlreadyInLibrary, tc.ref, tc.inLib)
		}
	}
	if n := r.count(`SELECT COUNT(*) FROM channels`); n != channels {
		t.Errorf("估價不應建立頻道：%d → %d", channels, n)
	}
}

// feed 宣稱的長度不合理（1e308 秒）時視為不知道長度：金額是未知，不會變成無限大。
func TestEstimateIgnoresUnreasonableDuration(t *testing.T) {
	huge := "1" + strings.Repeat("0", 308)
	feed := podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: audio42, extra: `<itunes:duration>` + huge + `</itunes:duration>`})
	r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	r.p.groq = &fakeGroq{model: "whisper-large-v3-turbo", baseURL: "https://api.groq.com/openai/v1"}
	est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}})
	if err != nil || len(est.Items) != 1 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	it := est.Items[0]
	if it.DurationS != nil || it.ASRUSD != nil || it.TotalUSD != nil || !hasNote(it, "長度不合理") {
		t.Errorf("item = %+v", it)
	}
	if math.IsInf(est.TotalUSD, 0) || math.IsNaN(est.TotalUSD) || !hasTopNote(est, "部分金額未知") {
		t.Errorf("est = %+v", est)
	}
	r.noWrites()
}

// 播放清單超過 maxEstimateItems 部時只 Probe 前面這些，並在 notes 說明。
func TestEstimateCapsPlaylist(t *testing.T) {
	r, _, _ := estimateRig(t)
	var entries []youtube.PlaylistEntry
	for i := range maxEstimateItems + 1 {
		entries = append(entries, youtube.PlaylistEntry{ID: fmt.Sprintf("v%010d", i), Title: fmt.Sprint(i)})
	}
	r.yt.playlist = entries
	est, err := r.estimate(IngestRequest{URL: playlistURL, Overrides: fallbackOverride("groq")})
	if err != nil {
		t.Fatal(err)
	}
	if len(est.Items) != maxEstimateItems || r.yt.probes != maxEstimateItems || !hasTopNote(est, fmt.Sprintf("只估算前 %d 部", maxEstimateItems)) {
		t.Errorf("items = %d、probes = %d、notes = %v", len(est.Items), r.yt.probes, est.Notes)
	}
	r.noWrites()
}

// 估價的總時間用完時，還沒輪到的影音不再 Probe，標為未知；全部都失敗時頂層 notes 也說明。
func TestEstimateBudgetExhausted(t *testing.T) {
	old := estimateBudget
	estimateBudget = 0
	t.Cleanup(func() { estimateBudget = old })
	r, _, _ := estimateRig(t)
	r.yt.playlist = threeVideos()
	est, err := r.estimate(IngestRequest{URL: playlistURL})
	if err != nil || len(est.Items) != 3 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	if r.yt.probes != 0 {
		t.Errorf("總時間用完後不應 Probe：%d", r.yt.probes)
	}
	for _, it := range est.Items {
		if it.Ref == "" || it.TotalUSD != nil || !hasNote(it, "估價逾時") {
			t.Errorf("item = %+v", it)
		}
	}
	if !hasTopNote(est, "所有影音都無法取得資訊") || !hasTopNote(est, "部分金額未知") {
		t.Errorf("notes = %v", est.Notes)
	}

	// 只有一部影音時回傳錯誤。
	var up *UpstreamError
	if _, err := r.estimate(IngestRequest{URL: watch}); err == nil || !errors.As(err, &up) || !strings.Contains(up.Msg, "估價逾時") {
		t.Errorf("err = %v", err)
	}
}

// Groq 設定了自訂的 base_url（可能是其他相容服務）時，內建單價不適用，語音轉錄費用是未知。
func TestEstimateCustomGroqBaseURLHasNoPrice(t *testing.T) {
	r, g, _ := estimateRig(t)
	g.baseURL = "https://whisper.internal.example/v1"
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("groq")})
	if err != nil || len(est.Items) != 1 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	if it := est.Items[0]; srcOf(it) != "groq" || it.ASRUSD != nil || !hasNote(it, "自訂的 base_url") {
		t.Errorf("item = %+v", it)
	}
}

// serve 有服務金鑰庫時一律裝上 Supadata 的 client，但資料庫中可能沒有 key：
// 處理時 Supadata 會記為 not_configured 並改用 Groq，估價也要略過它，估出 Groq 的費用。
func TestEstimateSkipsSupadataWithoutStoredKey(t *testing.T) {
	r, _, _ := estimateRig(t, func(d *Deps) {
		d.Supadata = &supadata.Client{KeyFunc: func(context.Context) (string, error) { return "", nil }}
	})
	est, err := r.estimate(IngestRequest{URL: watch, Overrides: fallbackOverride("supadata", "groq")})
	if err != nil || len(est.Items) != 1 {
		t.Fatalf("est = %+v, %v", est, err)
	}
	it := est.Items[0]
	if srcOf(it) != "groq" || !near(it.ASRUSD, roundUSD(600.0/3600*0.04)) || !hasNote(it, "已略過 supadata：沒有設定 API key。") {
		t.Errorf("item = %+v", it)
	}
}

// Supadata 不處理 Podcast：先看平台、再看 API key，沒有 key 時估價不能提示「沒有設定 API key」，
// 設定 key 也不會讓它處理 feed 網址（處理時 runFallback 也是先判斷 supports）。
func TestEstimateDoesNotMentionSupadataKeyForPodcast(t *testing.T) {
	feed := podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: audio42, extra: `<itunes:duration>3600</itunes:duration>`})
	for _, tt := range []struct {
		name  string
		setup func(r *rig)
	}{
		{"已註冊但沒有 key", func(r *rig) {
			r.p.fetchers[supadata.ID] = fallbackFetcher{
				supports:   supadataSupports,
				configured: func(context.Context) (bool, error) { return false, nil },
			}
		}},
		{"沒有註冊", func(*rig) {}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
			r.p.groq = &fakeGroq{model: groq.DefaultModel, baseURL: groq.DefaultBaseURL}
			tt.setup(r)
			est, err := r.p.Estimate(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Overrides: fallbackOverride("supadata", "groq")})
			if err != nil || len(est.Items) != 1 {
				t.Fatalf("est = %+v, %v", est, err)
			}
			it := est.Items[0]
			if srcOf(it) != "groq" || hasNote(it, "supadata") {
				t.Errorf("item = %+v，不該有 supadata 的 notes", it)
			}
		})
	}
}

// 直播或還沒首播的影音在 notes 提醒：要等結束後才會處理。
func TestEstimateNotesLiveVideos(t *testing.T) {
	r, _, _ := estimateRig(t)
	r.yt.info.LiveStatus = "is_upcoming"
	r.yt.info.DurationS = nil
	est, err := r.estimate(IngestRequest{URL: watch})
	if err != nil || len(est.Items) != 1 || !hasNote(est.Items[0], "直播") {
		t.Errorf("est = %+v, %v", est, err)
	}
}
