package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// ASR 前查詢（HSHR-R12～R15）：即將呼叫第一個付費來源時，用 sha256 前綴向 Hub 查詢；
// 命中就用 Hub 的字幕，失敗時照常繼續並附上提醒。

// precheckRig 是沒有平台字幕、Supadata 可用的 rig：transcript 會走到付費來源。
func precheckRig(t *testing.T, stub *hubStub, mods ...func(*Deps)) (*hubRig, *fakeSupadata) {
	t.Helper()
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newHubRigOpts(t, stub, hubRigOpts{token: hubTestToken, sd: sd}, mods...)
	return r, sd
}

func (r *hubRig) runTranscript() (store.Item, store.StageRun) {
	r.t.Helper()
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	return it, r.stage(it, StageTranscript)
}

// isHub 回報現行的 source 字幕是不是 Hub 來的（看來源紀錄）。
func (r *hubRig) isHub(it store.Item) bool {
	r.t.Helper()
	return provenanceOf(r.rig, it)["executor"] == SourceHub
}

func TestPrecheckHitUsesHubTranscriptAndSkipsPaidSources(t *testing.T) {
	stub := newHubStub(t)
	v := ytVer("v1")
	stub.add(v)
	// 別的影片與別的平台的版本不會被選到。
	other := ytVer("v2")
	other.ExternalID = "zzzzzzzzzzz"
	other.Content = hubCues("en", "other video")
	stub.add(other)
	r, sd := precheckRig(t, stub)

	it, st := r.runTranscript()
	if st.Status != store.StatusDone || !r.isHub(it) || st.Message != "" {
		t.Fatalf("transcript = %+v", st)
	}
	if sd.calls() != 0 {
		t.Error("命中 Hub 時不呼叫付費來源")
	}
	if stub.count("/api/v1/lookup") != 1 || stub.count("/api/v1/versions/v1") != 2 {
		t.Errorf("requests = %+v", stub.requests())
	}
	prov := provenanceOf(r.rig, it)
	if prov["executor"] != "hub" || prov["source"] != "hub" || prov["cookie_required"] != false {
		t.Errorf("provenance = %v", prov)
	}
	if o, _ := prov["hub_origin"].(map[string]any); o["version_id"] != "v1" || o["url"] != stub.srv.URL {
		t.Errorf("hub_origin = %v", prov["hub_origin"])
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Cues[0].Text != "hello from hub" {
		t.Errorf("cues = %+v, %v", tr, err)
	}
	if got := sourcesOf(r.rig, it); !strings.HasSuffix(got, "hub:used:") {
		t.Errorf("sources = %q", got)
	}

	// 隱私（R13）：請求只含平台與 4 個十六進位字元的前綴，不含影片 ID、標題，且帶 token。
	for _, rq := range stub.requests() {
		if rq.Auth != "Bearer "+hubTestToken {
			t.Errorf("%s 沒有帶 token", rq.Path)
		}
	}
	lookup := stub.requests()[0]
	key := hubclient.VideoKeySHA256("youtube", "abc123xxxxx", 0)
	if lookup.Path != "/api/v1/lookup" || lookup.Query != "platform=youtube&prefix="+key[:4] {
		t.Errorf("lookup = %+v", lookup)
	}
	for _, rq := range stub.requests()[:1] {
		if strings.Contains(rq.Query, "abc123") || strings.Contains(rq.Path, "abc123") {
			t.Errorf("請求洩漏影片 ID：%+v", rq)
		}
	}
}

// 選擇的優先順序（R14）：source 優先於 model_verified 的 normalized；只有 normalized 時也接受。
func TestPrecheckPicksSourceOverVerifiedNormalized(t *testing.T) {
	stub := newHubStub(t)
	norm := ytVer("vn")
	norm.Variant, norm.Verified, norm.Content = "normalized", true, hubCues("en", "normalized text")
	stub.add(norm)
	src := ytVer("vs")
	src.Content = hubCues("en", "source text")
	stub.add(src)
	r, _ := precheckRig(t, stub)
	it, st := r.runTranscript()
	if st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	tr, _ := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if len(tr.Cues) != 1 || tr.Cues[0].Text != "source text" {
		t.Errorf("cues = %+v", tr.Cues)
	}

	stub2 := newHubStub(t)
	stub2.add(norm)
	r2, sd2 := precheckRig(t, stub2)
	it2, st := r2.runTranscript()
	if st.Status != store.StatusDone || !r2.isHub(it2) || sd2.calls() != 0 {
		t.Errorf("只有 normalized：%+v", st)
	}
}

func TestPrecheckMissContinuesToPaidSourceWithoutNote(t *testing.T) {
	// Hub 沒有這部影片、只有別部影片、或語言不同：都是沒有命中，不是失敗。
	ja := ytVer("vja")
	ja.Lang, ja.Content = "ja", hubCues("ja", "こんにちは")
	other := ytVer("v2")
	other.ExternalID = "zzzzzzzzzzz"
	otherPart := ytVer("v3")
	otherPart.Platform, otherPart.ExternalID, otherPart.Part = "bilibili", "BV1xx411c7mD", 1
	for name, vs := range map[string][]hubVer{
		"空的":        nil,
		"別部影片":      {other},
		"別的平台":      {otherPart},
		"語言與原文語言不同": {ja},
	} {
		stub := newHubStub(t)
		for _, v := range vs {
			stub.add(v)
		}
		r, sd := precheckRig(t, stub)
		it, st := r.runTranscript()
		if st.Status != store.StatusDone || r.isHub(it) || st.Message != "" || sd.calls() != 1 {
			t.Errorf("%s：transcript = %+v，supadata = %d", name, st, sd.calls())
		}
		if stub.count("/api/v1/lookup") != 1 || stub.count("/api/v1/versions/") != 0 {
			t.Errorf("%s：requests = %+v", name, stub.requests())
		}
	}
}

// Hub 的查詢結果是不可信的輸入：不合法的項目被略過，變成沒有命中。
func TestPrecheckSkipsInvalidLookupEntries(t *testing.T) {
	key := hubclient.VideoKeySHA256("youtube", "abc123xxxxx", 0)
	good := strings.Repeat("a", 64)
	entry := func(id, variant, lang, sha string) string {
		return fmt.Sprintf(`{"id":%q,"variant":%q,"lang":%q,"sha256":%q,"score":1}`, id, variant, lang, sha)
	}
	vid := func(key string, versions ...string) string {
		return `{"video_key_sha256":"` + key + `","versions":[` + strings.Join(versions, ",") + `]}`
	}
	cases := map[string]string{
		"版本 ID 不合法":     `{"videos":[` + vid(key, entry("../../x", "source", "en", good)) + `]}`,
		"sha256 不合法":    `{"videos":[` + vid(key, entry("v1", "source", "en", "short")) + `]}`,
		"sha256 含大寫":    `{"videos":[` + vid(key, entry("v1", "source", "en", strings.Repeat("A", 64))) + `]}`,
		"變體不是原始字幕":      `{"videos":[` + vid(key, entry("v1", "translated", "en", good)) + `]}`,
		"語言標記不合法":       `{"videos":[` + vid(key, entry("v1", "source", "en;x", good)) + `]}`,
		"影片雜湊與前綴不符":     `{"videos":[` + vid(strings.Repeat("0", 64), entry("v1", "source", "en", good)) + `]}`,
		"影片雜湊不是 sha256": `{"videos":[` + vid("not-a-hash", entry("v1", "source", "en", good)) + `]}`,
		"沒有版本":          `{"videos":[` + vid(key) + `]}`,
		"沒有 videos":     `{}`,
	}
	for name, raw := range cases {
		stub := newHubStub(t)
		stub.lookupRaw = raw
		r, sd := precheckRig(t, stub)
		it, st := r.runTranscript()
		if st.Status != store.StatusDone || r.isHub(it) || st.Message != "" || sd.calls() != 1 {
			t.Errorf("%s：transcript = %+v", name, st)
		}
		if stub.count("/api/v1/versions/") != 0 {
			t.Errorf("%s：不該取得版本", name)
		}
	}
}

func TestPrecheckNotAttemptedWhenDisabledUnconfiguredOrNotPublic(t *testing.T) {
	tests := []struct {
		name  string
		opts  hubRigOpts
		mods  []func(*Deps)
		avail string
	}{
		{"沒有接上開關", hubRigOpts{token: hubTestToken}, []func(*Deps){func(d *Deps) { d.HubPrecheck = nil }}, "public"},
		{"關閉", hubRigOpts{token: hubTestToken, precheck: func(context.Context) (bool, error) { return false, nil }}, nil, "public"},
		{"讀取開關失敗", hubRigOpts{token: hubTestToken, precheck: func(context.Context) (bool, error) { return true, errors.New("db") }}, nil, "public"},
		{"沒有 token", hubRigOpts{}, nil, "public"},
		{"沒有網址", hubRigOpts{token: hubTestToken, noURL: true}, nil, "public"},
		{"availability 不明的 YouTube", hubRigOpts{token: hubTestToken}, nil, ""},
		{"私人影片", hubRigOpts{token: hubTestToken}, nil, "private"},
		{"需要登入", hubRigOpts{token: hubTestToken}, nil, "needs_auth"},
	}
	for _, tt := range tests {
		stub := newHubStub(t)
		stub.add(ytVer("v1"))
		sd := &fakeSupadata{lang: "en", cues: 3}
		tt.opts.sd = sd
		r := newHubRigOpts(t, stub, tt.opts, tt.mods...)
		r.yt.info.Availability = tt.avail
		it, st := r.runTranscript()
		if st.Status != store.StatusDone || r.isHub(it) || st.Message != "" || sd.calls() != 1 {
			t.Errorf("%s：transcript = %+v，supadata = %d", tt.name, st, sd.calls())
		}
		if stub.total() != 0 {
			t.Errorf("%s：不該連線：%+v", tt.name, stub.requests())
		}
	}
}

// 失敗時照常繼續（R15）：用付費來源，完成後的訊息附上原因。
func TestPrecheckFailureNotesAndContinues(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{"401", 401, ``, "token 無效或權限不足"},
		{"403", 403, `{"required_permission":"regenerate"}`, "token 無效或權限不足"},
		{"404", 404, ``, "HTTP 404"},
		{"422", 422, `{"code":"invalid_request","message":"prefix"}`, "HTTP 422"},
		{"429", 429, `{"retry_after_s":5}`, "HTTP 429"},
		{"500", 500, `{"message":"boom"}`, "HTTP 500"},
		{"503", 503, ``, "HTTP 503"},
	}
	for _, tt := range tests {
		stub := newHubStub(t)
		stub.add(ytVer("v1"))
		stub.status, stub.errBody = tt.status, tt.body
		r, sd := precheckRig(t, stub)
		it, st := r.runTranscript()
		want := "無法查詢 Hub（" + tt.reason + "），這次改用付費來源。"
		if st.Status != store.StatusDone || r.isHub(it) || sd.calls() != 1 || st.Message != want {
			t.Errorf("%s：transcript = %+v，supadata = %d，預期訊息 %q", tt.name, st, sd.calls(), want)
		}
		if strings.Contains(st.Message, hubTestToken) {
			t.Errorf("%s：訊息含 token", tt.name)
		}
		// 只查一次，失敗之後不重試。
		if stub.total() != 1 {
			t.Errorf("%s：requests = %+v", tt.name, stub.requests())
		}
	}
}

func TestPrecheckConnectionFailureAndTimeoutNotes(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r, sd := precheckRig(t, stub)
	stub.srv.Close()
	_, st := r.runTranscript()
	if st.Status != store.StatusDone || sd.calls() != 1 || st.Message != "無法查詢 Hub（連線失敗），這次改用付費來源。" {
		t.Errorf("連線失敗：%+v，supadata = %d", st, sd.calls())
	}

	// 逾時是 5 秒（LookupTimeout），以注入的 clock 推進。
	stub2 := newHubStub(t)
	stub2.add(ytVer("v1"))
	r2, sd2 := precheckRig(t, stub2)
	arrived := stub2.waitHang()
	it := r2.ingest(watch, nil, StageTranscript)
	if err := r2.runWithTimeout(arrived, hubclient.LookupTimeout, func() error { return r2.w.RunUntilIdle(context.Background()) }); err != nil {
		t.Fatal(err)
	}
	st = r2.stage(it, StageTranscript)
	if st.Status != store.StatusDone || sd2.calls() != 1 || st.Message != "無法查詢 Hub（連線逾時），這次改用付費來源。" {
		t.Errorf("逾時：%+v，supadata = %d", st, sd2.calls())
	}
}

// 取得 token 時才失敗（不是 hubclient 的錯誤）：當作連線失敗。
func TestPrecheckNonHubErrorIsConnectionFailure(t *testing.T) {
	stub := newHubStub(t)
	var calls atomic.Int32
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newHubRig(t, stub, func(d *Deps) {
		d.Supadata = sd
		d.Hub = hubclient.New(stub.srv.URL, func(context.Context) (string, bool, error) {
			if calls.Add(1) > 1 { // 第一次是 Configured
				return "", false, errors.New("secret store down")
			}
			return hubTestToken, true, nil
		}, stub.srv.Client(), clock.Real{})
	})
	_, st := r.runTranscript()
	if st.Status != store.StatusDone || sd.calls() != 1 || st.Message != "無法查詢 Hub（連線失敗），這次改用付費來源。" {
		t.Errorf("transcript = %+v", st)
	}
	if stub.total() != 0 {
		t.Error("取不到 token 時不該連線")
	}
}

// 版本與查詢結果不一致、內容被竄改或不合法：視為驗證失敗，改用付費來源。
func TestPrecheckRejectsInconsistentOrTamperedVersions(t *testing.T) {
	key := hubclient.VideoKeySHA256("youtube", "abc123xxxxx", 0)
	mutate := map[string]func(v *hubVer){
		"內容被竄改（sha256 不符）": func(v *hubVer) {
			v.VersionSHA, v.LookupSHA = hubclient.SHA256Hex([]byte("x")), hubclient.SHA256Hex([]byte("x"))
		},
		"查詢與版本的 sha256 不同": func(v *hubVer) { v.LookupSHA = strings.Repeat("0", 64) },
		"版本是別部影片":          func(v *hubVer) { v.ExternalID = "zzzzzzzzzzz" },
		"版本是別的平台":          func(v *hubVer) { v.Platform = "bilibili"; v.ExternalID = "BV1xx411c7mD" },
		"版本有分P":            func(v *hubVer) { v.Platform, v.ExternalID, v.Part = "bilibili", "abc123xxxxx", 1 },
		"不是字幕":             func(v *hubVer) { v.Kind = "chapters" },
		"不是原始字幕":           func(v *hubVer) { v.Variant = "translated" },
		"內容不是字幕":           func(v *hubVer) { v.Content = []byte(`{"cues":[]}`) },
		"內容不是 JSON":        func(v *hubVer) { v.Content = []byte("<html>") },
	}
	for name, mut := range mutate {
		stub := newHubStub(t)
		v := ytVer("v1")
		mut(&v)
		stub.add(v)
		// 查詢結果一律指向這部影片（惡意的 Hub 把別的版本放在這個鍵下）。
		sha := v.lookupSHA()
		stub.lookupRaw = `{"videos":[{"video_key_sha256":"` + key + `","versions":[{"id":"v1","variant":"source","lang":"en","sha256":"` + sha + `","score":1}]}]}`
		r, sd := precheckRig(t, stub)
		it, st := r.runTranscript()
		if st.Status != store.StatusDone || r.isHub(it) || sd.calls() != 1 || st.Message != "無法查詢 Hub（內容驗證失敗），這次改用付費來源。" {
			t.Errorf("%s：transcript = %+v，supadata = %d", name, st, sd.calls())
		}
	}
}

// 即使有多個付費來源，也只查詢一次（R13）。
func TestPrecheckRunsOncePerTranscript(t *testing.T) {
	stub := newHubStub(t)
	r, sd := precheckRig(t, stub)
	sd.err = supadata.ErrNoTranscript
	r.p.fetchers["groq"] = fakeSource(errclass.ErrNoResult)
	it := r.ingest(watch, fallbackOverride("supadata", "groq"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.ErrorClass != "permanent" {
		t.Errorf("transcript = %+v", st)
	}
	if n := stub.count("/api/v1/lookup"); n != 1 {
		t.Errorf("lookup 次數 = %d", n)
	}
	if sd.calls() != 1 {
		t.Errorf("supadata = %d", sd.calls())
	}
}

// 沒有平台字幕、也沒有設定付費來源時不查詢（沒有「即將呼叫付費來源」）。
func TestPrecheckSkippedWhenPaidSourceIsNotConfigured(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r := newHubRig(t, stub) // 沒有 Supadata
	it := r.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked {
		t.Errorf("transcript = %+v", st)
	}
	if stub.total() != 0 {
		t.Errorf("不該連線：%+v", stub.requests())
	}
}

// 查詢到一半被取消：回傳 context 的錯誤，不當作 Hub 失敗。
func TestPrecheckHubCancellationIsNotAHubFailure(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r, _ := precheckRig(t, stub)
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	it = r.item(it.ID)

	arrived := stub.waitHang()
	ctx, cancel := context.WithCancel(context.Background())
	type out struct {
		o   hubPrecheckOutcome
		err error
	}
	done := make(chan out, 1)
	go func() {
		o, err := r.p.precheckHub(ctx, it, nil)
		done <- out{o, err}
	}()
	<-arrived
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || got.o.note != "" || got.o.hit != nil {
		t.Errorf("outcome = %+v, err = %v", got.o, got.err)
	}
}

// 命中的結果帶著先前的 probe 決定，再加上 hub 的使用紀錄。
func TestPrecheckHitKeepsEarlierDecisions(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r, _ := precheckRig(t, stub)
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	it = r.item(it.ID)
	prior := []transcript.Decision{{Source: "youtube-manual", State: transcript.StateUnavailable, Reason: "no_track"}}
	out, err := r.p.precheckHub(context.Background(), it, prior)
	if err != nil || out.hit == nil || out.note != "" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if len(prior) != 1 {
		t.Error("不該改動傳入的 decisions")
	}
	probes := out.hit.probes
	if len(probes) != 2 || probes[0].SourceID != "youtube-manual" || probes[1].SourceID != SourceHub || probes[1].State != transcript.StateUsed || probes[1].Lang != "en" {
		t.Errorf("probes = %+v", probes)
	}
	if out.hit.artifact == nil || out.hit.task != taskDone || !out.hit.advance {
		t.Errorf("result = %+v", out.hit)
	}
}

// 預設 Hub 回應 5xx 之外的狀態 200 但本體不是 JSON：內容驗證失敗。
func TestPrecheckMalformedLookupBody(t *testing.T) {
	stub := newHubStub(t)
	stub.hook = func(w http.ResponseWriter, r *http.Request) bool {
		hubJSON(w, 200, `<html>captive portal</html>`)
		return true
	}
	r, sd := precheckRig(t, stub)
	_, st := r.runTranscript()
	if st.Status != store.StatusDone || sd.calls() != 1 || st.Message != "無法查詢 Hub（內容驗證失敗），這次改用付費來源。" {
		t.Errorf("transcript = %+v", st)
	}
}

// 付費來源的 API key 存在資料庫中、目前沒有設定時，不算「即將呼叫付費來源」，不查詢；
// 讀取設定失敗時視為有設定（交給 fetch 處理），照常查詢。
func TestPrecheckFollowsPaidSourceConfiguredCheck(t *testing.T) {
	tests := []struct {
		name       string
		configured func(context.Context) (bool, error)
		lookups    int
	}{
		{"沒有設定", func(context.Context) (bool, error) { return false, nil }, 0},
		{"有設定", func(context.Context) (bool, error) { return true, nil }, 1},
		{"讀取失敗視為有設定", func(context.Context) (bool, error) { return false, errors.New("db") }, 1},
	}
	for _, tt := range tests {
		stub := newHubStub(t)
		r := newHubRig(t, stub)
		sd := &fakeSupadata{lang: "en", cues: 3}
		f := sd.asFetcher("segment")
		f.configured = tt.configured
		r.p.fetchers["supadata"] = f
		r.ingest(watch, nil, StageTranscript)
		r.run()
		if n := stub.count("/api/v1/lookup"); n != tt.lookups {
			t.Errorf("%s：lookup 次數 = %d，預期 %d", tt.name, n, tt.lookups)
		}
	}
}

// 命中 Hub 的結果有自己的 cache key：關閉查詢後重跑，不會拿到快取的 Hub 字幕，而是照常呼叫付費來源。
func TestPrecheckHitIsNotReusedAfterTurningItOff(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	var on atomic.Bool
	on.Store(true)
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newHubRigOpts(t, stub, hubRigOpts{token: hubTestToken, sd: sd,
		precheck: func(context.Context) (bool, error) { return on.Load(), nil }})
	it, st := r.runTranscript()
	if st.Status != store.StatusDone || !r.isHub(it) || sd.calls() != 0 {
		t.Fatalf("第一次應該用 Hub：%+v", st)
	}
	run := r.stage(it, StageTranscript)
	if run.Executor != SourceHub {
		t.Errorf("executor = %q，預期 hub", run.Executor)
	}
	on.Store(false)
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if r.isHub(it) || sd.calls() != 1 {
		t.Errorf("關閉查詢後重跑應該呼叫付費來源：hub=%v supadata=%d", r.isHub(it), sd.calls())
	}
}
