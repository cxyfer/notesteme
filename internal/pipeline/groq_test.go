package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// fakeGroq 是 Groq 的替身：記錄收到的音檔與語言提示，依 err 回應。
type fakeGroq struct {
	mu      sync.Mutex
	noKey   bool
	baseURL string
	model   string
	err     error
	lang    string
	audios  []string
	hints   []string
	existed []bool // 呼叫當下音檔是否存在
}

func (f *fakeGroq) Configured(context.Context) bool { return !f.noKey }

func (f *fakeGroq) Endpoint() (string, string) { return f.baseURL, f.model }

func (f *fakeGroq) Transcribe(_ context.Context, audioPath, langHint string) (*groq.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, statErr := os.Stat(audioPath)
	f.audios, f.hints, f.existed = append(f.audios, audioPath), append(f.hints, langHint), append(f.existed, statErr == nil)
	if f.err != nil {
		return nil, f.err
	}
	return &groq.Result{Lang: f.lang, Cues: []subtitle.Cue{
		{Index: 1, StartMS: 0, EndMS: 2000, Text: "hello", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 2000, EndMS: 4000, Text: "world", Timing: subtitle.TimingSource},
	}}, nil
}

func (f *fakeGroq) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.audios)
}

func newGroqRig(t *testing.T) (*rig, *fakeGroq) {
	t.Helper()
	g := &fakeGroq{lang: "en", baseURL: groq.DefaultBaseURL, model: groq.DefaultModel}
	return newRig(t, func(d *Deps) { d.Groq = g }), g
}

func groqOnly() map[string]any { return fallbackOverride("groq") }

func TestGroqTranscribesTheDownloadedAudio(t *testing.T) {
	r, g := newGroqRig(t)
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()

	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if g.calls() != 1 || !g.existed[0] || g.hints[0] != "en" {
		t.Errorf("音檔 = %v，存在 = %v，語言提示 = %v", g.audios, g.existed, g.hints)
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Lang != "en" {
		t.Fatalf("cues = %+v, %v", tr, err)
	}
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	var prov struct {
		Source       string            `json:"source"`
		Timing       string            `json:"timing"`
		ToolVersions map[string]string `json:"tool_versions"`
	}
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov.Source != "groq" || prov.Timing != "segment" || prov.ToolVersions["yt-dlp"] != "2026.11.01" {
		t.Errorf("provenance = %+v", prov)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	if last := d.Sources[len(d.Sources)-1]; last.SourceID != "groq" || last.State != "used" {
		t.Errorf("sources = %+v", d.Sources)
	}
}

// 暫存的音檔目錄在 data/tmp 底下；成功與失敗都要清掉。
func TestGroqRemovesTheAudioDirectory(t *testing.T) {
	r, g := newGroqRig(t)
	r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if len(r.yt.audioDirs) != 1 || !strings.HasPrefix(r.yt.audioDirs[0], r.dir+string(os.PathSeparator)+"tmp"+string(os.PathSeparator)) {
		t.Fatalf("暫存目錄 = %v", r.yt.audioDirs)
	}
	if _, err := os.Stat(r.yt.audioDirs[0]); !os.IsNotExist(err) {
		t.Errorf("成功後暫存目錄應已清除：%v", err)
	}

	// Transcribe 失敗（permanent）。
	r, g = newGroqRig(t)
	g.err = &errclass.Error{Class: errclass.ClassPermanent, Source: "groq", Detail: "HTTP 413"}
	r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if len(r.yt.audioDirs) != 1 {
		t.Fatalf("暫存目錄 = %v", r.yt.audioDirs)
	}
	if _, err := os.Stat(r.yt.audioDirs[0]); !os.IsNotExist(err) {
		t.Errorf("失敗後暫存目錄應已清除：%v", err)
	}

	// 下載失敗：仍然清掉目錄。
	r, _ = newGroqRig(t)
	r.yt.audioErr = errors.New("boom")
	r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	for _, dir := range r.yt.audioDirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("下載失敗後暫存目錄應已清除：%v", err)
		}
	}
	if len(r.yt.audioDirs) == 0 {
		t.Error("應該嘗試過下載")
	}
}

// SRC-R10：沒有 API key 時記為 not_configured，不下載音訊；全部來源都無法執行時 blocked。
func TestGroqWithoutKeyIsNotConfiguredAndBlocks(t *testing.T) {
	r, g := newGroqRig(t)
	g.noKey = true
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || !slices.Equal(st.Actions, []string{"設定 API key"}) {
		t.Errorf("transcript = %+v", st)
	}
	if len(r.yt.audioDirs) != 0 || g.calls() != 0 {
		t.Errorf("沒有 key 不該下載或轉錄：%v、%d", r.yt.audioDirs, g.calls())
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	if last := d.Sources[len(d.Sources)-1]; last.SourceID != "groq" || last.State != "unavailable" || last.Reason != "not_configured" {
		t.Errorf("sources = %+v", d.Sources)
	}
}

// key 被拒：blocked，訊息說明 Groq 的 API key 被拒，動作「設定 API key」。
func TestGroqRejectedKeyBlocksWithItsMessage(t *testing.T) {
	r, g := newGroqRig(t)
	g.err = &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "groq", Detail: "HTTP 401", Err: groq.ErrKeyRejected,
		Message: groq.MsgKeyRejected, Actions: []string{groq.ActionSetKey},
	}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.Message != groq.MsgKeyRejected ||
		!slices.Equal(st.Actions, []string{"設定 API key"}) || !strings.Contains(st.Message, "Groq") {
		t.Errorf("transcript = %+v", st)
	}
}

// Groq 沒有結果（靜音等）：和 Supadata 一樣視為這個來源沒有結果，繼續下一個來源。
func TestGroqNoTranscriptMovesOn(t *testing.T) {
	r, g := newGroqRig(t)
	g.err = groq.ErrNoTranscript
	next := &fakeSupadata{lang: "en", cues: 2}
	r.p.fetchers["asr-fake"] = next.asFetcher("segment")
	it := r.ingest(watch, fallbackOverride("groq", "asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	var got []string
	for _, s := range d.Sources[len(d.Sources)-2:] {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	if strings.Join(got, " ") != "groq:unavailable:no_transcript asr-fake:used:" {
		t.Errorf("sources = %v", got)
	}
}

// 下載音訊遇到 403：yt-dlp 的錯誤帶著更新 yt-dlp、上傳 cookie 的訊息（SRC-R9），步驟照用。
func TestGroqAudioDownloadForbiddenUsesTheErrorMessage(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.audioErr = &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "yt-dlp", Message: "YouTube 拒絕了下載請求（403）。", Actions: []string{"更新 yt-dlp", "上傳 cookie"},
	}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.Message != "YouTube 拒絕了下載請求（403）。" || !slices.Equal(st.Actions, []string{"更新 yt-dlp", "上傳 cookie"}) {
		t.Errorf("transcript = %+v", st)
	}
	if g.calls() != 0 {
		t.Errorf("下載失敗不該轉錄：%d", g.calls())
	}
	// 下載是 yt-dlp 的問題，不是 groq 要使用者處理的事：probe 記為 download_failed，不是 needs_action。
	if got := r.lastSources(it, 1); got != "groq:unavailable:download_failed" {
		t.Errorf("sources = %v", got)
	}
}

// 下載音訊失敗（影片不存在等 permanent）：groq 記為 download_failed，繼續下一個來源。
func TestGroqAudioDownloadPermanentMovesOn(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.audioErr = &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "Video unavailable"}
	next := &fakeSupadata{lang: "en", cues: 2}
	r.p.fetchers["asr-fake"] = next.asFetcher("segment")
	it := r.ingest(watch, fallbackOverride("groq", "asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if got := r.lastSources(it, 2); got != "groq:unavailable:download_failed asr-fake:used:" {
		t.Errorf("sources = %v", got)
	}
	if g.calls() != 0 {
		t.Errorf("下載失敗不該轉錄：%d", g.calls())
	}
}

// groq 自己要使用者處理的事（key 被拒）仍記為 needs_action。
func TestGroqKeyRejectedProbeIsNeedsAction(t *testing.T) {
	r, g := newGroqRig(t)
	g.err = &errclass.Error{Class: errclass.ClassNeedsAction, Source: "groq", Err: groq.ErrKeyRejected, Message: groq.MsgKeyRejected, Actions: []string{groq.ActionSetKey}}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if got := r.lastSources(it, 1); got != "groq:unavailable:needs_action" {
		t.Errorf("sources = %v", got)
	}
}

// lastSources 回傳影音詳情中最後 n 列來源，格式為「來源:狀態:原因」，以空白分隔。
func (r *rig) lastSources(it store.Item, n int) string {
	r.t.Helper()
	d, err := r.p.Item(context.Background(), it.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	var got []string
	for _, s := range d.Sources[max(0, len(d.Sources)-n):] {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	return strings.Join(got, " ")
}

// base_url 與 model 影響結果，是快取鍵的一部分；相同設定重跑則重用。
func TestGroqEndpointIsPartOfTheCacheKey(t *testing.T) {
	r, g := newGroqRig(t)
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	rerun := func() {
		t.Helper()
		if err := r.rerunTranscript(it, nil); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	rerun()
	if g.calls() != 1 || r.sourceVersions(it) != 1 {
		t.Fatalf("相同設定應重用快取：呼叫 %d 次，版本 %d", g.calls(), r.sourceVersions(it))
	}
	g.model = "whisper-large-v3"
	rerun()
	if g.calls() != 2 || r.sourceVersions(it) != 2 {
		t.Fatalf("model 改變應重新轉錄：呼叫 %d 次，版本 %d", g.calls(), r.sourceVersions(it))
	}
	g.baseURL = "https://other.example/v1"
	rerun()
	if g.calls() != 3 || r.sourceVersions(it) != 3 {
		t.Fatalf("base_url 改變應重新轉錄：呼叫 %d 次，版本 %d", g.calls(), r.sourceVersions(it))
	}
}

// 平台字幕軌下載後是空的而改用 Groq 時，換 model 重跑不可以命中舊結果。
func TestGroqAfterEmptyTrackKeysOnModel(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.cues = 0
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	g.model = "whisper-large-v3"
	if err := r.rerunTranscript(it, nil); err != nil {
		t.Fatal(err)
	}
	r.run()
	if g.calls() != 2 {
		t.Errorf("model 改變應重新轉錄：呼叫 %d 次", g.calls())
	}
}

// 查詢 yt-dlp 版本失敗時，不可以已經轉錄過：轉錄要付費，之後才失敗會重付費（重試時再轉錄一次）。
func TestGroqVersionIsResolvedBeforeTranscribing(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.versionErr = errors.New("version boom")
	r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if g.calls() != 0 {
		t.Errorf("版本查詢失敗時不該呼叫 Transcribe：%d", g.calls())
	}
}

// provenance 的 params_sha256 必須納入實際送出的語言提示（groq.LangHint），才能區分指定語言與自動偵測的轉錄結果。
func TestGroqProvenanceParamsHashIncludesLangHint(t *testing.T) {
	runWithLang := func(origLang string) (hint, paramsHash string) {
		t.Helper()
		r, g := newGroqRig(t)
		r.yt.info.Language = origLang
		g.lang = origLang
		it := r.ingest(watch, groqOnly(), StageTranscript)
		r.run()
		arts, err := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
		if err != nil || len(arts) != 1 {
			t.Fatalf("artifacts(%q) = %v, %v", origLang, arts, err)
		}
		var prov struct {
			ParamsSHA256 string `json:"params_sha256"`
		}
		if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
			t.Fatal(err)
		}
		return g.hints[0], prov.ParamsSHA256
	}

	hintEn, hashEn := runWithLang("en")
	hintEnUS, hashEnUS := runWithLang("en-US")
	hintEmpty, hashEmpty := runWithLang("")
	hintYue, hashYue := runWithLang("yue")

	if hintEn != "en" || hintEnUS != "en" || hintEmpty != "" || hintYue != "" {
		t.Fatalf("意外的語言提示：en=%q, en-US=%q, empty=%q, yue=%q", hintEn, hintEnUS, hintEmpty, hintYue)
	}
	if hashEn == "" || hashEmpty == "" {
		t.Fatalf("params_sha256 不應為空：en=%q, empty=%q", hashEn, hashEmpty)
	}
	if hashEn == hashEmpty {
		t.Errorf("強制指定語言與自動偵測的 params_sha256 不應相同：%s", hashEn)
	}
	if hashEn != hashEnUS {
		t.Errorf("實際語言提示同為 en 時 params_sha256 應相同：en=%s, en-US=%s", hashEn, hashEnUS)
	}
	if hashYue != hashEmpty {
		t.Errorf("實際語言提示同為空（不支援的三字母代碼與未指定）時 params_sha256 應相同：yue=%s, empty=%s", hashYue, hashEmpty)
	}
	wantEn := paramsSHA(map[string]string{
		"base_url":  groq.DefaultBaseURL,
		"model":     groq.DefaultModel,
		"lang_hint": "en",
	})
	if hashEn != wantEn {
		t.Errorf("params_sha256 = %s，預期 %s", hashEn, wantEn)
	}
}
