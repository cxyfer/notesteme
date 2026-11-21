package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// vttEnding 是兩句的 WEBVTT，最後一句在 sec 秒結束（sec 至少 10）。
func vttEnding(sec int) string {
	return fmt.Sprintf("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello there\n\n00:00:05.000 --> %02d:%02d:%02d.000\nBye now\n",
		sec/3600, sec%3600/60, sec%60)
}

// transcribe 以預設語言設定直接執行 transcript 步驟。
func (r *podRig) transcribe(it store.Item, lang Language) (result, error) {
	r.t.Helper()
	return r.p.runTranscript(context.Background(), it, lang)
}

type provFields struct {
	Source      string `json:"source"`
	Timing      string `json:"timing"`
	Track       struct{ Lang, Kind string }
	TimingCheck *struct {
		AudioDurationS float64 `json:"audio_duration_s"`
		LastCueEndS    float64 `json:"last_cue_end_s"`
		DriftRatio     float64 `json:"drift_ratio"`
		Flagged        bool    `json:"flagged"`
	} `json:"timing_check"`
}

func provOf(t *testing.T, res result) provFields {
	t.Helper()
	if res.artifact == nil {
		t.Fatalf("沒有產出：state = %+v，probes = %v", res.state, probesOf(res))
	}
	var p provFields
	if err := json.Unmarshal(res.artifact.Provenance, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// 字幕來源是 <podcast:transcript>：來源紀錄、語言、時間粒度與快取鍵。
func TestPodcastTranscriptIsUsed(t *testing.T) {
	r := newPodRig(t)
	r.fm.duration = 1800
	r.serveShow(vttTag)
	r.net.serve(ep42Transcript, 200, vttEnding(1798))
	it := r.ingestEpisode(StageMetadata)

	res, err := r.transcribe(it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	p := provOf(t, res)
	if p.Source != "podcast-transcript" || p.Timing != "cue" || p.Track.Lang != "en" || p.Track.Kind != "podcast" {
		t.Errorf("provenance = %+v", p)
	}
	if want := []string{"podcast-transcript:used:"}; !reflect.DeepEqual(probesOf(res), want) {
		t.Errorf("probes = %v，預期 %v", probesOf(res), want)
	}
	if res.state.Status != store.StatusDone || res.state.Message != "" {
		t.Errorf("state = %+v，偏移 0.11%% 不該有訊息", res.state)
	}
	if res.artifact.Lang != "en" || len(res.artifact.Timings) == 0 {
		t.Errorf("artifact = %+v", res.artifact)
	}
}

// 沒有宣告 type、網址也看不出格式的字幕：下載後看內容判斷，來源紀錄的 timing 依實際的格式記錄
// （字詞級的 JSON 是 word，不是 cue）。
func TestPodcastSniffedTranscriptFormatInProvenance(t *testing.T) {
	const typeless = "https://media.example.com/show/ep42-transcript"
	r := newPodRig(t)
	r.fm.duration = 4
	r.serveShow(`<podcast:transcript url="` + typeless + `"/>`)
	r.net.serve(typeless, 200, `{"version":"1.0.0","segments":[{"startTime":0,"endTime":0.5,"body":"Hello"},{"startTime":0.5,"endTime":1,"body":"there."},{"startTime":2,"endTime":3.9,"body":"Bye."}]}`)
	it := r.ingestEpisode(StageMetadata)

	res, err := r.transcribe(it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if p := provOf(t, res); p.Source != "podcast-transcript" || p.Timing != "word" {
		t.Errorf("provenance = %+v，預期 timing 為 word", p)
	}
}

// 音檔內容改變（sha256 不同）時快取鍵不同。
func TestPodcastTranscriptCacheKeyIncludesContentSHA(t *testing.T) {
	// 影音 ID 不同也會讓鍵不同，所以比較同一部影音：換掉音檔後重跑 metadata。
	r := newPodRig(t)
	r.serveShow(vttTag)
	r.net.serve(ep42Transcript, 200, vttEnding(100))
	it := r.ingestEpisode(StageMetadata)
	a, err := r.transcribe(it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := r.transcribe(it, DefaultLanguage())
	r.net.serve(audio42, 200, "DIFFERENT-AUDIO")
	res, err := r.p.runPodcastMetadata(context.Background(), it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	// 套用新的 metadata（模擬 apply 寫回）。
	if err := r.st.Write().UpdateItemMeta(context.Background(), it.ID, res.meta.item); err != nil {
		t.Fatal(err)
	}
	b, err := r.transcribe(r.reload(it.ID), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if a.stage.CacheKey != a2.stage.CacheKey || a.stage.CacheKey == b.stage.CacheKey {
		t.Errorf("快取鍵：同內容 %s / %s，不同內容 %s", a.stage.CacheKey, a2.stage.CacheKey, b.stage.CacheKey)
	}
}

// SRC-R15：偏移比例 > 2%（不含）才提醒；音檔長度未知不檢查。
func TestPodcastTranscriptTimingDrift(t *testing.T) {
	for _, tt := range []struct {
		name     string
		audioS   float64
		lastEnd  int
		wantMsg  bool
		wantNote bool // 來源紀錄有 timing_check
	}{
		{"超過 2%：3.23%", 3720, 3600, true, true},
		{"剛好 2% 不提醒", 1000, 980, false, true},
		{"略超過 2%", 1000, 979, true, true},
		{"字幕比音檔長 3%", 1000, 1030, true, true},
		{"幾乎相同", 1800, 1798, false, true},
		{"音檔長度未知", 0, 100, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newPodRig(t)
			r.fm.duration = tt.audioS
			r.serveShow(vttTag)
			r.net.serve(ep42Transcript, 200, vttEnding(tt.lastEnd))
			res, err := r.transcribe(r.ingestEpisode(StageMetadata), DefaultLanguage())
			if err != nil {
				t.Fatal(err)
			}
			if res.state.Status != store.StatusDone || res.state.ErrorClass != "" || len(res.state.Actions) != 0 {
				t.Errorf("state = %+v，步驟應為 done、沒有 error_class 與動作", res.state)
			}
			if got := res.state.Message == msgTimingDrift; got != tt.wantMsg || (!tt.wantMsg && res.state.Message != "") {
				t.Errorf("message = %q，預期有訊息 = %v", res.state.Message, tt.wantMsg)
			}
			p := provOf(t, res)
			if (p.TimingCheck != nil) != tt.wantNote {
				t.Fatalf("timing_check = %+v，預期存在 = %v", p.TimingCheck, tt.wantNote)
			}
			if p.TimingCheck != nil && (p.TimingCheck.Flagged != tt.wantMsg || p.TimingCheck.LastCueEndS != float64(tt.lastEnd) || p.TimingCheck.AudioDurationS != tt.audioS) {
				t.Errorf("timing_check = %+v", p.TimingCheck)
			}
		})
	}
}

func TestTimingDrift(t *testing.T) {
	cues := func(ends ...int64) []subtitle.Cue {
		var out []subtitle.Cue
		for _, e := range ends {
			out = append(out, subtitle.Cue{EndMS: e})
		}
		return out
	}
	if _, ok := timingDrift(0, cues(1000)); ok {
		t.Error("長度未知不該檢查")
	}
	if _, ok := timingDrift(100, nil); ok {
		t.Error("沒有 cue 不該檢查")
	}
	// 取最大的結束時間，不是最後一個 cue。
	if r, ok := timingDrift(100, cues(98000, 50000)); !ok || r != 0.02 {
		t.Errorf("ratio = %v, %v", r, ok)
	}
	if r, _ := timingDrift(100, cues(103000)); r < 0.0299 || r > 0.0301 {
		t.Errorf("字幕比音檔長：ratio = %v", r)
	}
}

// 快取命中（走 reuse）時，訊息也要和第一次一樣；命中時不再下載字幕。
func TestPodcastTranscriptDriftMessageOnCacheHit(t *testing.T) {
	r := newPodRig(t)
	r.fm.duration = 3720
	r.serveShow(vttTag)
	r.net.serve(ep42Transcript, 200, vttEnding(3600))
	it := r.ingestEpisode("")

	if s := r.latestStage(it, StageTranscript); s.Status != store.StatusDone || s.Message != msgTimingDrift {
		t.Fatalf("第一次 transcript = %s %q", s.Status, s.Message)
	}
	first := r.latestStage(it, StageTranscript)
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	r.run()
	second := r.latestStage(it, StageTranscript)
	if second.ID == first.ID || second.Status != store.StatusDone || second.Message != msgTimingDrift {
		t.Errorf("重跑 transcript = %s %q（id 相同 = %v）", second.Status, second.Message, second.ID == first.ID)
	}
	if n := r.net.count(ep42Transcript); n != 1 {
		t.Errorf("字幕下載 %d 次，快取命中不該再下載", n)
	}
}

func groqPodRig(t *testing.T) (*podRig, *fakeGroq) {
	t.Helper()
	g := &fakeGroq{lang: "en", baseURL: "u", model: "m"}
	return newPodRig(t, func(d *Deps) { d.Groq = g }), g
}

// 字幕抓不到、被防護擋下或解析失敗：記為 unavailable／permanent，改用 ASR，步驟不失敗；音檔用 metadata 下載的那份。
func TestPodcastTranscriptPermanentFailureFallsBackToASR(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(r *podRig)
	}{
		{"404", func(r *podRig) { r.net.serve(ep42Transcript, 404, "") }},
		{"403", func(r *podRig) { r.net.serve(ep42Transcript, 403, "") }},
		{"解析失敗", func(r *podRig) { r.net.serve(ep42Transcript, 200, "WEBVTT\n\nnot a timing line --> x\nhi\n") }},
		{"被防護擋下", func(r *podRig) {
			r.net.route(ep42Transcript, func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("dial: %w", netguard.ErrBlockedAddress)
			})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, g := groqPodRig(t)
			r.serveShow(vttTag)
			tt.setup(r)
			it := r.ingestEpisode(StageMetadata)

			res, err := r.transcribe(it, groqLocalLang())
			if err != nil {
				t.Fatalf("字幕有問題不該讓步驟失敗：%v", err)
			}
			want := []string{"podcast-transcript:unavailable:permanent", "groq:used:"}
			if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
				t.Fatalf("probes = %v，Groq %d 次，預期 %v 與 1 次", got, g.calls(), want)
			}
			prep := r.fm.ffmpegCalls()[0]
			if ffmpegKind(prep) != "prepare" || argAfter(prep, "-i") != filepath.Join(r.mediaDir(it), "episode.mp3") {
				t.Errorf("前處理 argv = %q", prep)
			}
			if p := provOf(t, res); p.Source != "groq" || p.TimingCheck != nil {
				t.Errorf("provenance = %+v", p)
			}
			if res.state.Message != "" {
				t.Errorf("ASR 的結果不做偏移檢查：message = %q", res.state.Message)
			}
		})
	}
}

// 第一條字幕軌失敗時，先試 feed 宣告的其餘字幕軌，不直接改用（要付費的）ASR（Codex P2）。
func TestPodcastTranscriptTriesRemainingTracksBeforeASR(t *testing.T) {
	const ep42SRT = "https://media.example.com/show/ep42.srt"
	tags := vttTag + `<podcast:transcript url="` + ep42SRT + `" type="application/x-subrip"/>`
	for _, tt := range []struct {
		name    string
		vtt     func(r *podRig)
		srt     func(r *podRig)
		want    []string
		wantASR int
	}{
		{"404 後用下一條", func(r *podRig) { r.net.serve(ep42Transcript, 404, "") }, func(r *podRig) { r.net.serve(ep42SRT, 200, srtEN) },
			[]string{"podcast-transcript:unavailable:permanent", "podcast-transcript:used:"}, 0},
		{"解析失敗後用下一條", func(r *podRig) { r.net.serve(ep42Transcript, 200, "WEBVTT\n\nnot a timing line --> x\nhi\n") },
			func(r *podRig) { r.net.serve(ep42SRT, 200, srtEN) },
			[]string{"podcast-transcript:unavailable:permanent", "podcast-transcript:used:"}, 0},
		{"空的字幕軌改用下一條", func(r *podRig) { r.net.serve(ep42Transcript, 200, "WEBVTT\n\n") },
			func(r *podRig) { r.net.serve(ep42SRT, 200, srtEN) },
			[]string{"podcast-transcript:rejected:empty", "podcast-transcript:used:"}, 0},
		{"全部是空的才用 ASR", func(r *podRig) { r.net.serve(ep42Transcript, 200, "WEBVTT\n\n") },
			func(r *podRig) { r.net.serve(ep42SRT, 200, "") },
			[]string{"podcast-transcript:rejected:empty", "podcast-transcript:rejected:empty", "groq:used:"}, 1},
		{"全部失敗才用 ASR", func(r *podRig) { r.net.serve(ep42Transcript, 404, "") }, func(r *podRig) { r.net.serve(ep42SRT, 403, "") },
			[]string{"podcast-transcript:unavailable:permanent", "podcast-transcript:unavailable:permanent", "groq:used:"}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, g := groqPodRig(t)
			r.fm.duration = 4
			r.serveShow(tags)
			tt.vtt(r)
			tt.srt(r)
			it := r.ingestEpisode(StageMetadata)

			res, err := r.transcribe(it, groqLocalLang())
			if err != nil {
				t.Fatal(err)
			}
			if got := probesOf(res); !reflect.DeepEqual(got, tt.want) || g.calls() != tt.wantASR {
				t.Fatalf("probes = %v，Groq %d 次，預期 %v 與 %d 次", got, g.calls(), tt.want, tt.wantASR)
			}
			if p := provOf(t, res); tt.wantASR == 0 && (p.Source != "podcast-transcript" || p.Track.Kind != "podcast") {
				t.Errorf("provenance = %+v", p)
			}
		})
	}
}

// 偏好的字幕軌無法使用、改由後面的字幕軌產生結果時，快取鍵要和偏好的字幕軌可用時不同（Codex P2）：
// 否則後面字幕軌的成品占用偏好字幕軌的鍵，之後偏好字幕軌恢復了也會直接命中，來源紀錄卻記成偏好的字幕軌。
func TestPodcastTranscriptCacheKeyIncludesDroppedTracks(t *testing.T) {
	const ep42SRT = "https://media.example.com/show/ep42.srt"
	tags := vttTag + `<podcast:transcript url="` + ep42SRT + `" type="application/x-subrip"/>`
	r, _ := groqPodRig(t)
	r.fm.duration = 4
	r.serveShow(tags)
	r.net.serve(ep42SRT, 200, srtEN)
	it := r.ingestEpisode(StageMetadata)

	r.net.serve(ep42Transcript, 404, "")
	dropped, err := r.transcribe(it, groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	r.net.serve(ep42Transcript, 200, vttEnding(10))
	preferred, err := r.transcribe(it, groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	if dropped.stage.CacheKey == preferred.stage.CacheKey {
		t.Errorf("偏好字幕軌被放棄與可用時的快取鍵相同：%s", dropped.stage.CacheKey)
	}
	// 同樣被放棄的情況下鍵要穩定，重跑才命中。
	r.net.serve(ep42Transcript, 404, "")
	again, err := r.transcribe(it, groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	if again.stage.CacheKey != dropped.stage.CacheKey {
		t.Errorf("相同情況的快取鍵不穩定：%s ≠ %s", again.stage.CacheKey, dropped.stage.CacheKey)
	}
}

// 沒有宣告 type 的字幕軌解析後是空的：下載時 Format 會被判斷出來，移除這條字幕軌不能因此失敗，
// 否則重新選軌又選到它而一直重試（Codex P2）。
func TestPodcastTranscriptTypelessEmptyTrackFallsThrough(t *testing.T) {
	const typeless = "https://media.example.com/show/ep42-transcript"
	r, g := groqPodRig(t)
	r.fm.duration = 4
	r.serveShow(`<podcast:transcript url="` + typeless + `"/>`)
	r.net.serve(typeless, 200, "WEBVTT\n\n")
	it := r.ingestEpisode(StageMetadata)

	var (
		res result
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = r.transcribe(it, groqLocalLang())
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("空的字幕軌一直重試，沒有改用下一個來源")
	}
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"podcast-transcript:rejected:empty", "groq:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
		t.Errorf("probes = %v，Groq %d 次，預期 %v 與 1 次", got, g.calls(), want)
	}
}

// 暫時性的錯誤（5xx、429、連線失敗）回傳錯誤讓步驟重試，不改用 ASR。
func TestPodcastTranscriptRetryableErrorIsReturned(t *testing.T) {
	for _, status := range []int{500, 503, 429} {
		r, g := groqPodRig(t)
		r.serveShow(vttTag)
		r.net.serve(ep42Transcript, status, "")
		it := r.ingestEpisode(StageMetadata)
		_, err := r.transcribe(it, groqLocalLang())
		if err == nil || g.calls() != 0 {
			t.Fatalf("HTTP %d：err = %v，Groq %d 次", status, err, g.calls())
		}
		if res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err); res.state.ErrorClass != "retryable" {
			t.Errorf("HTTP %d：classify = %+v", status, res.state)
		}
	}
}

// 沒有 <podcast:transcript>：no_track，改用 ASR。
func TestPodcastWithoutTranscriptTagUsesASR(t *testing.T) {
	r, g := groqPodRig(t)
	r.serveShow("")
	res, err := r.transcribe(r.ingestEpisode(StageMetadata), groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"podcast-transcript:unavailable:no_track", "groq:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
		t.Errorf("probes = %v，Groq %d 次", got, g.calls())
	}
}

// Supadata 不處理 Podcast：unsupported，不送出請求；預設 fallback 改用 Groq，而且資源類別歸為 asr。
func TestSupadataIsUnsupportedForPodcast(t *testing.T) {
	sd := &fakeSupadata{lang: "en", cues: 2}
	g := &fakeGroq{lang: "en", baseURL: "u", model: "m"}
	r := newPodRig(t, func(d *Deps) { d.Supadata = sd; d.Groq = g })
	r.serveShow("")
	if !transcriptNeedsASR("podcast", DefaultLanguage(), nil, "en") {
		t.Error("沒有字幕軌時 Podcast 略過 Supadata，第一個來源是 ASR：資源類別應為 asr")
	}
	res, err := r.transcribe(r.ingestEpisode(StageMetadata), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.urls) != 0 || g.calls() != 1 {
		t.Fatalf("supadata = %v，groq = %d", sd.urls, g.calls())
	}
	want := []string{"podcast-transcript:unavailable:no_track", "supadata:unavailable:unsupported", "groq:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) {
		t.Errorf("probes = %v，預期 %v", got, want)
	}
}

// feed 沒有 <language>：用選中的字幕軌語言推斷原文語言，lang_source 為 metadata。
func TestPodcastOriginalLangInferredFromTranscript(t *testing.T) {
	r := newPodRig(t)
	feed := podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: audio42,
		extra: `<podcast:transcript url="` + ep42Transcript + `" type="text/vtt" language="ja"/>`})
	r.net.serve(showURL, 200, strings.Replace(feed, "<language>en</language>", "", 1))
	r.net.serve(audio42, 200, podAudioBody)
	r.net.serve(ep42Transcript, 200, vttEnding(100))
	it := r.ingestEpisode(StageMetadata)
	if it.OriginalLang != "" {
		t.Fatalf("metadata 不該有原文語言：%q", it.OriginalLang)
	}
	res, err := r.transcribe(it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.meta == nil || res.meta.item.OriginalLang != "ja" || res.meta.item.LangSource != "metadata" || len(res.meta.item.Metadata) == 0 {
		t.Errorf("meta = %+v", res.meta)
	}
	if res.artifact.Lang != "ja" {
		t.Errorf("artifact.Lang = %q", res.artifact.Lang)
	}
}

// 沒有快照（metadata 還沒跑過）：probe 是 permanent，請從 metadata 重新處理。
func TestPodcastProbeWithoutSnapshot(t *testing.T) {
	r := newPodRig(t)
	r.serveShow(vttTag)
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.transcribe(res[0].Item, DefaultLanguage())
	if err == nil || !isPermanent(err) {
		t.Fatalf("err = %v", err)
	}
	if c := r.p.classify(store.Task{MaxAttempts: 3}, err); c.state.Message != "請從 metadata 重新處理。" {
		t.Errorf("message = %q", c.state.Message)
	}
}

// transcript 只讀快照：feed 輪替（這一集不見了）後仍然可以繼續。
func TestPodcastTranscriptUsesSnapshotAfterFeedRotation(t *testing.T) {
	r := newPodRig(t)
	r.serveShow(vttTag)
	r.net.serve(ep42Transcript, 200, vttEnding(100))
	it := r.ingestEpisode(StageMetadata)
	r.net.serve(showURL, 404, "")
	before := r.net.count(showURL)
	res, err := r.transcribe(it, DefaultLanguage())
	if err != nil || res.artifact == nil {
		t.Fatalf("res = %+v, %v", res.state, err)
	}
	if r.net.count(showURL) != before {
		t.Error("transcript 不該重新抓 feed")
	}
}

func TestPodcastAdapterFetchCues(t *testing.T) {
	const jsonBody = `{"version":"1.0.0","segments":[{"startTime":0,"endTime":2.5,"body":"Hello there"},{"startTime":2.5,"endTime":5,"body":"Bye now"}]}`
	srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n\n2\n00:00:02,000 --> 00:00:04,000\nWorld\n"
	for _, tt := range []struct {
		name   string
		format string
		body   string
		cues   int
	}{
		{"vtt", "vtt", vttEnding(10), 2},
		{"srt", "srt", srt, 2},
		{"json", "json", jsonBody, 2},
		{"沒有格式：WEBVTT 開頭", "", vttEnding(10), 2},
		{"沒有格式：JSON", "", jsonBody, 2},
		{"沒有格式：其他當 srt", "", srt, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := newPodNet()
			n.serve("https://media.example.com/t", 200, tt.body)
			ad := podcastAdapter{http: n.client()}
			cues, err := ad.fetchCues(context.Background(), store.Item{}, transcript.Track{Handle: "https://media.example.com/t", Format: tt.format}, "")
			if err != nil || len(cues) != tt.cues {
				t.Fatalf("cues = %d, %v", len(cues), err)
			}
		})
	}
	n := newPodNet()
	n.serve("https://media.example.com/bad", 200, "{not json")
	ad := podcastAdapter{http: n.client()}
	if _, err := ad.fetchCues(context.Background(), store.Item{}, transcript.Track{Handle: "https://media.example.com/bad", Format: "json"}, ""); !isPermanent(err) {
		t.Errorf("壞掉的 JSON 應是 permanent：%v", err)
	}
}

func TestPodcastTracksFilterAndLanguage(t *testing.T) {
	got := podcastTracks([]podcastTranscript{
		{URL: "u1", Type: "text/vtt"},
		{URL: "u2", Type: "text/html"},
		{URL: "u3", Type: "application/x-subrip", Language: "ja-jp"},
		{URL: "u4", Type: "text/plain"},
		{URL: "u5"}, // 沒有 type、看不出格式：留到下載後看內容
		{URL: "u6.json", Type: ""},
	}, "en-us")
	want := []transcript.Track{
		{Lang: "en-US", Kind: "podcast", Handle: "u1", Format: "vtt"},
		{Lang: "ja-JP", Kind: "podcast", Handle: "u3", Format: "srt"},
		{Lang: "en-US", Kind: "podcast", Handle: "u5"},
		{Lang: "en-US", Kind: "podcast", Handle: "u6.json", Format: "json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tracks = %+v\n預期 %+v", got, want)
	}
}

// ---- 音訊來源 ----

func TestPodcastAudioSource(t *testing.T) {
	r := newPodRig(t)
	r.serveShow("")
	it := r.ingestEpisode(StageMetadata)
	src := podcastAudioSource{r.p.audioDownloader()}
	want := filepath.Join(r.mediaDir(it), "episode.mp3")
	ctx := context.Background()

	// 快取的音檔還在：直接用，不下載。
	path, _, err := src.fetch(ctx, it, t.TempDir())
	if err != nil || path != want || r.net.count(audio42) != 1 {
		t.Fatalf("快取：path = %q, %v，下載 %d 次", path, err, r.net.count(audio42))
	}
	// 被刪除：用快照的 enclosure 重新下載，檔名沿用。
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	path, _, err = src.fetch(ctx, it, t.TempDir())
	if err != nil || path != want || r.net.count(audio42) != 2 {
		t.Fatalf("重新下載：path = %q, %v，下載 %d 次", path, err, r.net.count(audio42))
	}
	if b, _ := os.ReadFile(want); string(b) != podAudioBody {
		t.Errorf("內容 = %q", b)
	}
	// 大小和記錄不同（例如動態廣告）：檔案是驗證完才 rename 進來的，照樣沿用，不重新下載。
	if err := os.WriteFile(want, []byte("different size"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, _, err = src.fetch(ctx, it, t.TempDir()); err != nil || path != want || r.net.count(audio42) != 2 {
		t.Fatalf("大小不同：path = %q, %v，下載 %d 次", path, err, r.net.count(audio42))
	}
	if b, _ := os.ReadFile(want); string(b) != "different size" {
		t.Errorf("沿用的檔案被改動：%q", b)
	}
	// 重新下載失敗：包成 downloadError，記為 download_failed。
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	r.net.serve(audio42, 404, "")
	_, _, err = src.fetch(ctx, it, t.TempDir())
	var de *downloadError
	if !errors.As(err, &de) || probeReason(err, errclass.ClassPermanent) != reasonDownloadFailed {
		t.Errorf("err = %v", err)
	}
	// 沒有快照。
	if _, _, err := src.fetch(ctx, store.Item{ID: it.ID, Metadata: json.RawMessage(`{}`)}, t.TempDir()); !isPermanent(err) {
		t.Errorf("沒有快照：%v", err)
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

// 重新下載途中影音被移到垃圾桶：丟棄結果（errItemGone），不留下孤兒檔。
func TestPodcastAudioSourceRedownloadChecksItemAlive(t *testing.T) {
	r := newPodRig(t)
	r.serveShow("")
	it := r.ingestEpisode(StageMetadata)
	src := podcastAudioSource{r.p.audioDownloader()}
	if err := os.Remove(filepath.Join(r.mediaDir(it), "episode.mp3")); err != nil {
		t.Fatal(err)
	}
	// 讀取音檔 body 的途中把影音丟進垃圾桶。
	r.net.route(audio42, func(req *http.Request) (*http.Response, error) {
		resp := stubResponse(req, 200, podAudioBody)
		resp.Body = io.NopCloser(readFunc(func(p []byte) (int, error) {
			if _, err := r.p.Trash(context.Background(), it.ID, TrashOptions{}); err != nil {
				t.Error(err)
			}
			return copy(p, podAudioBody), io.EOF
		}))
		return resp, nil
	})
	if _, _, err := src.fetch(context.Background(), it, t.TempDir()); !errors.Is(err, errItemGone) {
		t.Fatalf("err = %v，預期 errItemGone", err)
	}
	if got := r.mediaFiles(it); len(got) != 0 {
		t.Errorf("媒體目錄 = %v，不該留下孤兒檔", got)
	}
}

func TestAudioSourceForPodcast(t *testing.T) {
	if audioSourceFor(Deps{}, "podcast") != nil {
		t.Error("沒有對外 client 時不該有 Podcast 的音訊來源")
	}
	if _, ok := audioSourceFor(Deps{HTTP: &http.Client{}}, "podcast").(podcastAudioSource); !ok {
		t.Error("Podcast 的音訊來源")
	}
}

// ---- 媒體端點 ----

func TestPodcastMediaFile(t *testing.T) {
	r := newPodRig(t)
	r.serveShow("")
	ctx := context.Background()
	res, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}})
	if err != nil {
		t.Fatal(err)
	}
	id := res[0].Item.ID

	// metadata 還沒下載音檔：no_media，訊息是這一集特有的。
	_, _, _, err = r.p.MediaFile(ctx, id)
	if !errors.Is(err, ErrNoMedia) || err.Error() != "這一集的音檔還沒下載。" {
		t.Fatalf("還沒下載：%v", err)
	}

	r.run()
	f, fi, it, err := r.p.MediaFile(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readAllClose(f)
	if string(b) != podAudioBody || fi.Size() != int64(len(b)) || it.ContentSHA256 != sumHex(podAudioBody) || filepath.Base(it.SourcePath) != "episode.mp3" {
		t.Errorf("內容 = %q size = %d sha = %q path = %q", b, fi.Size(), it.ContentSHA256, it.SourcePath)
	}

	// 檔案不見、或被換成目錄：media_missing。
	path := filepath.Join(r.mediaDir(it), "episode.mp3")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.p.MediaFile(ctx, id); !errors.Is(err, ErrMediaMissing) {
		t.Errorf("檔案不見：%v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.p.MediaFile(ctx, id); !errors.Is(err, ErrMediaMissing) {
		t.Errorf("目錄：%v", err)
	}
	// 快照裡的檔名不符合 episode.<ext>：不開啟任何檔案。
	for _, name := range []string{"../../secret.txt", "episode.mp3/../../x", "other.mp3", "episode.", "/etc/passwd"} {
		var m podcastMeta
		if err := json.Unmarshal(it.Metadata, &m); err != nil {
			t.Fatal(err)
		}
		m.Audio.File = name
		raw, _ := json.Marshal(m)
		if _, err := r.rawDB().Exec(`UPDATE items SET metadata_json = ? WHERE id = ?`, string(raw), id); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := r.p.MediaFile(ctx, id); !errors.Is(err, ErrMediaMissing) {
			t.Errorf("檔名 %q：%v", name, err)
		}
	}
	// 垃圾桶：in_trash。
	if _, err := r.p.Trash(ctx, id, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var tr *InTrashError
	if _, _, _, err := r.p.MediaFile(ctx, id); !errors.As(err, &tr) {
		t.Errorf("垃圾桶：%v", err)
	}
}

func readAllClose(f *os.File) ([]byte, error) {
	defer f.Close()
	return os.ReadFile(f.Name())
}
