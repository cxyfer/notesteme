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

func (f *fakeGroq) Configured() bool { return !f.noKey }

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
