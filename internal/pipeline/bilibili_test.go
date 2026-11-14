package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeBili 是 Bilibili 的替身：回傳預先設定的資訊，並記錄呼叫。
type fakeBili struct {
	mu         sync.Mutex
	parts      []bilibili.PartEntry
	partsErr   error
	partsCalls []string
	info       bilibili.Info
	probeErr   error
	probed     []int
	fetched    []string // 下載過的字幕軌原始鍵
}

func (f *fakeBili) Parts(_ context.Context, bv string) ([]bilibili.PartEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partsCalls = append(f.partsCalls, bv)
	return slices.Clone(f.parts), f.partsErr
}

func (f *fakeBili) Probe(_ context.Context, _ string, part int) (*bilibili.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probed = append(f.probed, part)
	if f.probeErr != nil {
		return nil, f.probeErr
	}
	i := f.info
	return &i, nil
}

func (f *fakeBili) FetchCaptions(_ context.Context, _ string, _ int, key, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, key)
	return []byte("1\n00:00:00,000 --> 00:00:02,000\n第一句\n\n2\n00:00:02,000 --> 00:00:04,000\n第二句\n"), nil
}

func (f *fakeBili) Version(context.Context) (string, error) { return "2026.11.01", nil }

func newBiliRig(t *testing.T) (*rig, *fakeBili) {
	t.Helper()
	fb := &fakeBili{info: bilibili.Info{ID: "BV1xx411c7mD", Title: "Part", Uploader: "UP主", UploaderID: "12345", Description: "desc"}}
	return newRig(t, func(d *Deps) { d.Bilibili = fb }), fb
}

const (
	biliBase = "https://www.bilibili.com/video/BV1xx411c7mD"
	biliP1   = biliBase + "?p=1"
)

func threeParts() []bilibili.PartEntry {
	return []bilibili.PartEntry{{Part: 1, Title: "One"}, {Part: 2, Title: "Two"}, {Part: 3, Title: "Three"}}
}

func partsOf(res []IngestResult) []int {
	var out []int
	for _, r := range res {
		out = append(out, r.Item.Part)
	}
	return out
}

// ING-R3：網址沒有 p 時展開全部分P，每個分P 一個影音，同一個交易建立。
func TestIngestBilibiliExpandsAllParts(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.parts = threeParts()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: biliBase})
	if err != nil || !slices.Equal(partsOf(res), []int{1, 2, 3}) {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if len(fb.partsCalls) != 1 || fb.partsCalls[0] != "BV1xx411c7mD" {
		t.Errorf("展開呼叫 = %v", fb.partsCalls)
	}
	for _, x := range res {
		it := x.Item
		if x.AlreadyInLibrary || it.Platform != "bilibili" || it.ExternalID != "BV1xx411c7mD" ||
			it.URL != biliBase+"?p="+string(rune('0'+it.Part)) {
			t.Errorf("結果 = %+v", x)
		}
		if _, err := r.st.Read().TaskByStageRun(context.Background(), r.stage(it, StageMetadata).ID); err != nil {
			t.Errorf("分P %d 沒有 metadata 任務：%v", it.Part, err)
		}
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 3 {
		t.Errorf("pending 任務數 = %d", n)
	}
}

// 有 p=N 時只建立那一個分P，不呼叫 yt-dlp。
func TestIngestBilibiliWithPartDoesNotExpand(t *testing.T) {
	r, fb := newBiliRig(t)
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: biliBase + "?p=2"})
	if err != nil || !slices.Equal(partsOf(res), []int{2}) {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if len(fb.partsCalls) != 0 {
		t.Errorf("不應呼叫 Parts：%v", fb.partsCalls)
	}
	if res[0].Item.URL != biliBase+"?p=2" {
		t.Errorf("URL = %q", res[0].Item.URL)
	}
}

func TestIngestBilibiliSelectedParts(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.parts = threeParts()
	// 重複的去掉；結果依分P 順序。
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: biliBase, Parts: []int{3, 1, 3}})
	if err != nil || !slices.Equal(partsOf(res), []int{1, 3}) {
		t.Fatalf("Ingest = %v, %v", partsOf(res), err)
	}
	if items, _ := r.p.Items(context.Background()); len(items) != 2 {
		t.Errorf("影音數 = %d", len(items))
	}
}

func TestIngestBilibiliPartsValidation(t *testing.T) {
	var inv *InvalidError
	tests := []struct {
		name string
		req  IngestRequest
	}{
		{"網址已有 p 又給 parts", IngestRequest{URL: biliBase + "?p=2", Parts: []int{2}}},
		{"超出範圍", IngestRequest{URL: biliBase, Parts: []int{1, 4}}},
		{"分P 小於 1", IngestRequest{URL: biliBase, Parts: []int{0}}},
		{"Bilibili 影片沒有單集", IngestRequest{URL: biliBase, Episodes: []string{"x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, fb := newBiliRig(t)
			fb.parts = threeParts()
			res, err := r.p.Ingest(context.Background(), tt.req)
			if !errors.As(err, &inv) || res != nil {
				t.Fatalf("res = %+v，err = %v，預期 422", res, err)
			}
			if items, _ := r.p.Items(context.Background()); len(items) != 0 {
				t.Errorf("失敗時不應建立影音：%d", len(items))
			}
		})
	}
	// 網址已有 p 又給 parts：在呼叫 yt-dlp 之前就拒絕。
	r, fb := newBiliRig(t)
	_, _ = r.p.Ingest(context.Background(), IngestRequest{URL: biliBase + "?p=2", Parts: []int{2}})
	if len(fb.partsCalls) != 0 {
		t.Errorf("不應呼叫 Parts：%v", fb.partsCalls)
	}
}

// 展開失敗沿用 ING-R4 的對應：影片不存在 422、其他 502。
func TestIngestBilibiliExpansionFailures(t *testing.T) {
	gone := &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "-404"}
	transient := &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "412"}
	missing := &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: errors.New("找不到 yt-dlp")}
	login := &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Message: "Bilibili 要求登入。", ResumeOn: "cookie:bilibili"}
	tests := []struct {
		name    string
		entries []bilibili.PartEntry
		err     error
		check   func(error) bool
	}{
		{"影片不存在", nil, gone, func(e error) bool {
			var inv *InvalidError
			return errors.As(e, &inv) && inv.Msg == "找不到這部 Bilibili 影片，可能已刪除或有地區限制。"
		}},
		{"暫時性失敗", nil, transient, func(e error) bool {
			var up *UpstreamError
			return errors.As(e, &up) && up.Msg == "yt-dlp 讀取 Bilibili 分P 失敗，請稍後再試。" && errors.Is(e, transient)
		}},
		{"找不到 yt-dlp", nil, missing, func(e error) bool {
			var up *UpstreamError
			return errors.As(e, &up) && up.Msg == "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"
		}},
		{"要求登入", nil, login, func(e error) bool {
			var up *UpstreamError
			return errors.As(e, &up) && up.Msg == "Bilibili 要求登入。"
		}},
		{"沒有分P", nil, nil, func(e error) bool {
			var inv *InvalidError
			return errors.As(e, &inv) && inv.Msg == "這部影片沒有可新增的分P。"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, fb := newBiliRig(t)
			fb.parts, fb.partsErr = tt.entries, tt.err
			res, err := r.p.Ingest(context.Background(), IngestRequest{URL: biliBase})
			if err == nil || !tt.check(err) || res != nil {
				t.Fatalf("res = %+v，err = %v", res, err)
			}
			if items, _ := r.p.Items(context.Background()); len(items) != 0 {
				t.Errorf("失敗時不應建立影音：%d", len(items))
			}
		})
	}
}

// metadata：走 Bilibili 的 Probe，uploader 當頻道。
func TestBilibiliMetadata(t *testing.T) {
	r, fb := newBiliRig(t)
	dur := 90
	fb.info.DurationS = &dur
	it := r.ingest(biliBase+"?p=2", nil, StageMetadata)
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusDone {
		t.Fatalf("metadata = %+v", st)
	}
	if !slices.Equal(fb.probed, []int{2}) {
		t.Errorf("Probe 的分P = %v", fb.probed)
	}
	got, _ := r.st.Read().ItemByID(context.Background(), it.ID)
	if got.Title != "Part" || got.DurationS == nil || *got.DurationS != 90 || got.ChannelID == "" {
		t.Errorf("item = %+v", got)
	}
	if got.OriginalLang != "" {
		t.Errorf("Bilibili 沒有語言中繼資料：%q", got.OriginalLang)
	}
}

func sourcesOf(r *rig, it store.Item) string {
	r.t.Helper()
	d, err := r.p.Item(context.Background(), it.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	var got []string
	for _, s := range d.Sources {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	return strings.Join(got, " ")
}

func provenanceOf(r *rig, it store.Item) map[string]any {
	r.t.Helper()
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	if len(arts) == 0 {
		r.t.Fatal("沒有 transcript 產出")
	}
	var prov map[string]any
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		r.t.Fatal(err)
	}
	return prov
}

// CC 字幕：kind manual、來源 bilibili-cc、timing cue；彈幕不是字幕。
func TestBilibiliCCCaptionsAreUsed(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.Tracks = []string{"danmaku", "zh-Hans"}
	it := r.ingest(biliP1, map[string]any{"language": map[string]any{"fallback": []any{}}}, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if !slices.Equal(fb.fetched, []string{"zh-Hans"}) {
		t.Errorf("下載的字幕軌 = %v", fb.fetched)
	}
	prov := provenanceOf(r, it)
	if prov["source"] != "bilibili-cc" || prov["timing"] != "cue" {
		t.Errorf("provenance = %v", prov)
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Lang != "zh-Hans" {
		t.Fatalf("cues = %+v, %v", tr, err)
	}
}

// AI 字幕：來源 bilibili-ai、timing cue；原文語言用 AI 字幕軌的語言（lang_source=metadata），
// 而且寫回 item 時保留 metadata 步驟寫下的 metadata_json。
func TestBilibiliAICaptionsSetOriginalLanguage(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.Tracks = []string{"ai-zh"}
	it := r.ingest(biliP1, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if !slices.Equal(fb.fetched, []string{"ai-zh"}) {
		t.Errorf("下載的字幕軌 = %v", fb.fetched)
	}
	prov := provenanceOf(r, it)
	if prov["source"] != "bilibili-ai" || prov["timing"] != "cue" {
		t.Errorf("provenance = %v", prov)
	}
	got, _ := r.st.Read().ItemByID(context.Background(), it.ID)
	if got.OriginalLang != "zh" || got.LangSource != "metadata" {
		t.Errorf("原文語言 = %q（%s）", got.OriginalLang, got.LangSource)
	}
	if got.Title != "Part" || !strings.Contains(string(got.Metadata), `"description":"desc"`) {
		t.Errorf("應保留 metadata 步驟寫下的欄位：title = %q，metadata = %s", got.Title, got.Metadata)
	}
	if want := "bilibili-cc:unavailable:no_track bilibili-ai:used:"; !strings.Contains(sourcesOf(r, it), want) {
		t.Errorf("sources = %s", sourcesOf(r, it))
	}
}

// SRC-R8：未登入（NeedsLogin）、沒有選到字幕軌、caption_priority 有 auto 規則時，transcript 為 blocked，
// 等 Bilibili cookie；bilibili-ai 記為 unavailable／needs_action。
func TestBilibiliAICaptionsWithoutCookieBlock(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.NeedsLogin = true
	// fallback 來源都不該被嘗試。
	it := r.ingest(biliP1, nil, StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.ResumeOn != "cookie:bilibili" {
		t.Fatalf("transcript = %+v", st)
	}
	if st.Message != "這部影片的 AI 字幕需要登入 Bilibili 才能取得。上傳 Bilibili cookie，或改用語音轉錄。" ||
		!slices.Equal(st.Actions, []string{"上傳 cookie", "改用語音轉錄"}) {
		t.Errorf("訊息 = %q，動作 = %v", st.Message, st.Actions)
	}
	if !strings.Contains(sourcesOf(r, it), "bilibili-ai:unavailable:needs_action") {
		t.Errorf("sources = %s", sourcesOf(r, it))
	}
	blocked, err := r.st.Read().BlockedRunsByResume(context.Background(), "cookie:bilibili")
	if err != nil || len(blocked) != 1 || blocked[0].ID != st.ID {
		t.Errorf("BlockedRunsByResume = %+v, %v", blocked, err)
	}
}

// NeedsLogin 但 caption_priority 沒有 auto 規則：不要求 cookie，照 fallback 處理（這裡沒有 fallback，失敗）。
func TestBilibiliLoginWarningWithoutAutoRuleDoesNotBlock(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.NeedsLogin = true
	ov := map[string]any{"language": map[string]any{
		"caption_priority": []any{map[string]any{"lang": "original", "kind": "manual"}},
		"fallback":         []any{},
	}}
	it := r.ingest(biliP1, ov, StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status == store.StatusBlocked || st.ResumeOn != "" {
		t.Errorf("transcript = %+v", st)
	}
}

// 有選到字幕軌時（例如帶了 cookie），NeedsLogin 不影響結果。
func TestBilibiliLoginWarningIgnoredWhenTrackChosen(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.NeedsLogin = true
	fb.info.Tracks = []string{"ai-zh"}
	it := r.ingest(biliP1, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
}

// fallback 來源的 needs_action 錯誤帶了 ResumeOn 時，blocked 狀態要記錄它。
func TestFallbackNeedsActionRecordsResumeOn(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "supadata", Message: "需要 cookie。", Actions: []string{"上傳 cookie"},
		ResumeOn: "cookie:youtube",
	}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked || st.ResumeOn != "cookie:youtube" {
		t.Errorf("transcript = %+v", st)
	}
}

// 沒有 Bilibili 依賴時（尚未接線）新增 Bilibili 影片維持 501。
func TestIngestBilibiliWithoutClientIsNotImplemented(t *testing.T) {
	r := newRig(t)
	var ni *NotImplementedError
	if _, err := r.p.Ingest(context.Background(), IngestRequest{URL: biliBase}); !errors.As(err, &ni) {
		t.Errorf("err = %v", err)
	}
}
