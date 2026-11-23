package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 假 Hub：用 httptest 提供 Client 會呼叫的 /api/v1 路由，記錄每個請求，並可以注入各種惡意或失敗的回應。

const hubTestToken = "nsh_unit_test_secret_token_0123456789"

// hubVer 是假 Hub 上的一個版本。
type hubVer struct {
	ID, Platform, ExternalID string
	Part                     int
	Kind, Variant, Lang      string
	Content                  []byte
	// VersionSHA、LookupSHA 空字串時用 Content 的 sha256；測試用它們製造不一致。
	VersionSHA, LookupSHA string
	Score                 int
	Verified              bool
	Uploader              string
	TranscribeModel       string
}

type hubReq struct {
	Method, Path, Query, Auth string
}

type hubStub struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	versions map[string]hubVer
	order    []string
	reqs     []hubReq
	shares   [][]byte     // 收到的 bundle
	forms    []url.Values // 收到的分享的其他欄位（attest、visibility）

	// status 非 0 時，所有請求都直接回這個狀態與 errBody。
	status  int
	errBody string
	// hook 回傳 true 代表它已經處理了這個請求。
	hook func(w http.ResponseWriter, r *http.Request) bool
	// lookupRaw、versionRaw、contentRaw 直接指定回應本體（versionRaw、contentRaw 以版本 ID 為鍵）。
	lookupRaw  string
	versionRaw map[string]string
	contentRaw map[string][]byte
	shareResp  string
}

func newHubStub(t *testing.T) *hubStub {
	t.Helper()
	s := &hubStub{t: t, versions: map[string]hubVer{}, versionRaw: map[string]string{}, contentRaw: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *hubStub) add(v hubVer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[v.ID]; !ok {
		s.order = append(s.order, v.ID)
	}
	s.versions[v.ID] = v
}

// count 回傳 method + path 的請求次數（path 以前綴比對）。
func (s *hubStub) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reqs {
		if strings.HasPrefix(r.Path, prefix) {
			n++
		}
	}
	return n
}

func (s *hubStub) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *hubStub) requests() []hubReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hubReq(nil), s.reqs...)
}

// parseShareBody 拆開 multipart 的分享請求：回傳 bundle 與其他欄位。
func parseShareBody(contentType string, body []byte) ([]byte, url.Values) {
	form := url.Values{}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, form
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var bundle []byte
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FormName() == "bundle" {
			bundle = data
		} else {
			form.Add(part.FormName(), string(data))
		}
	}
	return bundle, form
}

func (s *hubStub) lastForm() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.forms) == 0 {
		return nil
	}
	return s.forms[len(s.forms)-1]
}

func (s *hubStub) lastShare() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shares) == 0 {
		return nil
	}
	return s.shares[len(s.shares)-1]
}

func (v hubVer) versionSHA() string {
	if v.VersionSHA != "" {
		return v.VersionSHA
	}
	return hubclient.SHA256Hex(v.Content)
}

func (v hubVer) lookupSHA() string {
	if v.LookupSHA != "" {
		return v.LookupSHA
	}
	return hubclient.SHA256Hex(v.Content)
}

func hubJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	switch b := body.(type) {
	case string:
		_, _ = io.WriteString(w, b)
	case []byte:
		_, _ = w.Write(b)
	default:
		_ = json.NewEncoder(w).Encode(b)
	}
}

func (s *hubStub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, hubReq{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")})
	hook, status, errBody := s.hook, s.status, s.errBody
	s.mu.Unlock()
	if hook != nil && hook(w, r) {
		return
	}
	if status != 0 {
		if errBody == "" {
			errBody = `{}`
		}
		hubJSON(w, status, errBody)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+hubTestToken {
		hubJSON(w, 401, `{"code":"unauthorized"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
		hubJSON(w, 200, `{"id":"u1","display_name":"Tester","permissions":["read","upload","regenerate","root"]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/shares":
		bundle, form := parseShareBody(r.Header.Get("Content-Type"), body)
		s.mu.Lock()
		s.shares = append(s.shares, bundle)
		s.forms = append(s.forms, form)
		resp := s.shareResp
		s.mu.Unlock()
		if resp == "" {
			resp = `{"share_id":"share1","versions":[{"id":"ver1","kind":"transcript","variant":"source","lang":"en","deduplicated":false}]}`
		}
		hubJSON(w, 201, resp)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/lookup":
		s.serveLookup(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/versions/"):
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/versions/")
		id, content := strings.CutSuffix(rest, "/content")
		s.serveVersion(w, id, content)
	default:
		hubJSON(w, 404, `{"code":"not_found"}`)
	}
}

func (s *hubStub) serveLookup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookupRaw != "" {
		hubJSON(w, 200, s.lookupRaw)
		return
	}
	platform, prefix := r.URL.Query().Get("platform"), r.URL.Query().Get("prefix")
	groups := map[string][]map[string]any{}
	var keys []string
	for _, id := range s.order {
		v := s.versions[id]
		if v.Platform != platform || v.Kind != "transcript" {
			continue
		}
		key := hubclient.VideoKeySHA256(v.Platform, v.ExternalID, v.Part)
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], map[string]any{
			"id": v.ID, "variant": v.Variant, "lang": v.Lang, "sha256": v.lookupSHA(), "score": v.Score, "model_verified": v.Verified,
		})
	}
	videos := []map[string]any{}
	for _, k := range keys {
		videos = append(videos, map[string]any{"video_key_sha256": k, "versions": groups[k]})
	}
	hubJSON(w, 200, map[string]any{"videos": videos})
}

func (s *hubStub) serveVersion(w http.ResponseWriter, id string, content bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[id]
	if content {
		if raw, ok := s.contentRaw[id]; ok {
			hubJSON(w, 200, raw)
			return
		}
		if !ok {
			hubJSON(w, 404, `{"code":"not_found"}`)
			return
		}
		hubJSON(w, 200, v.Content)
		return
	}
	if raw, ok := s.versionRaw[id]; ok {
		hubJSON(w, 200, raw)
		return
	}
	if !ok {
		hubJSON(w, 404, `{"code":"not_found"}`)
		return
	}
	video := map[string]any{"platform": v.Platform, "external_id": v.ExternalID, "part": nil}
	if v.Part > 0 {
		video["part"] = v.Part
	}
	hubJSON(w, 200, map[string]any{
		"id": v.ID, "video": video, "kind": v.Kind, "variant": v.Variant, "lang": v.Lang, "sha256": v.versionSHA(),
		"transcribe_model": v.TranscribeModel, "model_verified": v.Verified,
		"uploaders": []map[string]string{{"display_name": v.Uploader}},
	})
}

// hubCues 產生 cues JSON（變體 source、timing source）。
func hubCues(lang string, texts ...string) []byte {
	var cues []subtitle.Cue
	for i, s := range texts {
		cues = append(cues, subtitle.Cue{Index: i + 1, StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000, Text: s, Timing: subtitle.TimingSource})
	}
	var buf bytes.Buffer
	if err := subtitle.RenderCuesJSON(&buf, subtitle.Transcript{Lang: lang, Variant: subtitle.VariantSource, Cues: cues}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ytVer 是 watch 這部影片的原始字幕版本。
func ytVer(id string) hubVer {
	return hubVer{ID: id, Platform: "youtube", ExternalID: "abc123xxxxx", Kind: "transcript", Variant: "source", Lang: "en",
		Content: hubCues("en", "hello from hub", "second line"), Uploader: "Alice"}
}

// hubRig 是接上假 Hub 的 rig；hubClk 是 hubclient 的 clock（與 rig 的 clock 分開，推進逾時不影響 worker）。
type hubRig struct {
	*rig
	stub   *hubStub
	hubClk *clock.Fake
}

type hubRigOpts struct {
	token    string // 空字串代表沒有設定 token
	noURL    bool
	precheck func(context.Context) (bool, error)
	sd       *fakeSupadata
}

func newHubRig(t *testing.T, stub *hubStub, mods ...func(*Deps)) *hubRig {
	return newHubRigOpts(t, stub, hubRigOpts{token: hubTestToken}, mods...)
}

func newHubRigOpts(t *testing.T, stub *hubStub, o hubRigOpts, mods ...func(*Deps)) *hubRig {
	t.Helper()
	hubClk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	url := stub.srv.URL
	if o.noURL {
		url = ""
	}
	tok := o.token
	tokenFn := func(context.Context) (string, bool, error) { return tok, tok != "", nil }
	pre := o.precheck
	if pre == nil {
		pre = func(context.Context) (bool, error) { return true, nil }
	}
	r := newRig(t, func(d *Deps) {
		d.Hub = hubclient.New(url, tokenFn, stub.srv.Client(), hubClk)
		d.HubPrecheck = pre
		if o.sd != nil {
			d.Supadata = o.sd
		}
		for _, m := range mods {
			m(d)
		}
	})
	r.yt.info.Availability = "public"
	return &hubRig{rig: r, stub: stub, hubClk: hubClk}
}

// item 重新讀出影音（取得 metadata 步驟寫入的欄位）。
func (r *rig) item(id string) store.Item {
	r.t.Helper()
	it, err := r.st.Read().ItemByID(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return it
}

// hubErrOf 取出 *hubclient.Error。
func hubErrOf(t *testing.T, err error) *hubclient.Error {
	t.Helper()
	var he *hubclient.Error
	if !errors.As(err, &he) {
		t.Fatalf("err = %T %v，預期 *hubclient.Error", err, err)
	}
	return he
}

func hubErrIs(t *testing.T, err error, status int, code string) *HubError {
	t.Helper()
	var he *HubError
	if !errors.As(err, &he) || he.Status != status || he.Code != code {
		t.Fatalf("err = %#v，預期 HubError %d %s", err, status, code)
	}
	return he
}

// ---- 單元：判斷函式 ----

func TestPublicForHubAndCookieRequired(t *testing.T) {
	tests := []struct {
		name        string
		it          store.Item
		src         string
		public      bool
		cookieNeeds bool
	}{
		{"YouTube public", store.Item{Platform: source.PlatformYouTube, Availability: "public"}, "youtube-manual", true, false},
		{"YouTube 空值不是公開（availability 只有 YouTube 會回報）", store.Item{Platform: source.PlatformYouTube}, "youtube-manual", false, false},
		{"YouTube needs_auth", store.Item{Platform: source.PlatformYouTube, Availability: "needs_auth"}, "youtube-manual", false, true},
		{"YouTube private", store.Item{Platform: source.PlatformYouTube, Availability: "private"}, "youtube-manual", false, true},
		{"YouTube subscriber_only", store.Item{Platform: source.PlatformYouTube, Availability: "subscriber_only"}, "youtube-manual", false, true},
		{"Bilibili 空值視為公開", store.Item{Platform: source.PlatformBilibili}, transcript.SourceBilibiliCC, true, false},
		{"Bilibili AI 字幕一定要登入", store.Item{Platform: source.PlatformBilibili}, transcript.SourceBilibiliAI, true, true},
		{"Podcast 空值視為公開", store.Item{Platform: source.PlatformPodcast}, "podcast-feed", true, false},
		{"Podcast 非 public", store.Item{Platform: source.PlatformPodcast, Availability: "private"}, "podcast-feed", false, true},
		{"本機檔案", store.Item{Platform: source.PlatformLocal}, "local", false, false},
		{"本機檔案即使有 availability", store.Item{Platform: source.PlatformLocal, Availability: "public"}, "local", false, false},
	}
	for _, tt := range tests {
		if got := publicForHub(tt.it); got != tt.public {
			t.Errorf("%s：publicForHub = %v", tt.name, got)
		}
		if got := cookieRequired(tt.it, tt.src); got != tt.cookieNeeds {
			t.Errorf("%s：cookieRequired = %v", tt.name, got)
		}
	}
}

func TestCheckShareRequest(t *testing.T) {
	ok := HubShareRequest{Attest: true, License: "CC-BY-4.0"}
	if err := CheckShareRequest(ok); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"CC-BY-4.0", "CC-BY-SA-4.0", "CC-BY-NC-4.0", "CC-BY-NC-SA-4.0", "CC0-1.0"} {
		if err := CheckShareRequest(HubShareRequest{Attest: true, License: l}); err != nil {
			t.Errorf("%s：%v", l, err)
		}
	}
	hubErrIs(t, CheckShareRequest(HubShareRequest{License: "CC0-1.0"}), 422, "attest_required")
	// attest 先於 license：兩者都缺時回 attest_required。
	hubErrIs(t, CheckShareRequest(HubShareRequest{}), 422, "attest_required")
	e := hubErrIs(t, CheckShareRequest(HubShareRequest{Attest: true}), 422, "invalid_request")
	if e.Path != "license" {
		t.Errorf("path = %q", e.Path)
	}
	// 不支援的授權：訊息只含清理過、截斷的內容（Client 的輸入也當作不可信）。
	evil := "MIT\x1b[31m\n" + strings.Repeat("x", 500)
	e = hubErrIs(t, CheckShareRequest(HubShareRequest{Attest: true, License: evil}), 422, "license_invalid")
	if strings.ContainsAny(e.Msg, "\x1b\n") || len([]rune(e.Msg)) > 150 {
		t.Errorf("訊息沒有清理或截斷：%q", e.Msg)
	}
	if e.Error() != e.Msg {
		t.Error("Error() 應該回傳 Msg")
	}
	for _, l := range []string{"cc0-1.0", "CC0-1.0 ", "GPL-3.0"} {
		hubErrIs(t, CheckShareRequest(HubShareRequest{Attest: true, License: l}), 422, "license_invalid")
	}
}

func TestSmallHubHelpers(t *testing.T) {
	if got := clip("a\x00b\x7fcé", 10); got != "abcé" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("你好世界嗎", 3); got != "你好世…" {
		t.Errorf("clip 以字元截斷：%q", got)
	}
	if cmpOr("", "b") != "b" || cmpOr("a", "b") != "a" {
		t.Error("cmpOr")
	}
	for in, want := range map[string]string{"": "", "und": "", "en": "en", "zh-Hant": "zh-Hant", "en_US": "", "e": "", "en-": "", "zh-Hant-TW-x-a-b-c": ""} {
		if got := validLang(in); got != want {
			t.Errorf("validLang(%q) = %q，預期 %q", in, got, want)
		}
	}
	if executorOf(json.RawMessage(`{"executor":"hub"}`)) != "hub" || executorOf(json.RawMessage(`not json`)) != "" {
		t.Error("executorOf")
	}
	part := 2
	v := hubclient.VideoRef{Platform: "bilibili", ExternalID: "BV1xx411c7mD", Part: &part}
	if !sameVideo(v, store.Item{Platform: "bilibili", ExternalID: "BV1xx411c7mD", Part: 2}) ||
		sameVideo(v, store.Item{Platform: "bilibili", ExternalID: "BV1xx411c7mD", Part: 1}) ||
		sameVideo(v, store.Item{Platform: "youtube", ExternalID: "BV1xx411c7mD", Part: 2}) ||
		sameVideo(hubclient.VideoRef{Platform: "youtube", ExternalID: "abc123xxxxx"}, store.Item{Platform: "youtube", ExternalID: "abc123xxxxy"}) {
		t.Error("sameVideo")
	}
	if !sameVideo(hubclient.VideoRef{Platform: "youtube", ExternalID: "abc123xxxxx"}, store.Item{Platform: "youtube", ExternalID: "abc123xxxxx"}) {
		t.Error("沒有分P 時 part 為 0")
	}
	if !paidSource("supadata") || !paidSource("groq") || paidSource("youtube") {
		t.Error("paidSource")
	}
	if msgHubPrecheckFailed("HTTP 503") != "無法查詢 Hub（HTTP 503），這次改用付費來源。" {
		t.Error(msgHubPrecheckFailed("HTTP 503"))
	}

	res := result{state: store.StageState{Status: store.StatusDone}}
	addNote(&res, "")
	addNote(&res, "n1")
	addNote(&res, "n2")
	if res.state.Message != "n1；n2" {
		t.Errorf("message = %q", res.state.Message)
	}
	failed := result{state: store.StageState{Status: store.StatusFailed}}
	addNote(&failed, "n")
	if failed.state.Message != "" {
		t.Error("沒有完成的步驟不附提醒")
	}
}

// ---- HubMe、ItemHubShares ----

func TestHubMe(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	me, err := r.p.HubMe(ctx)
	if err != nil || me.DisplayName != "Tester" || strings.Join(me.Permissions, ",") != "read,upload,regenerate" {
		t.Fatalf("me = %+v, %v", me, err)
	}
	if reqs := stub.requests(); len(reqs) != 1 || reqs[0].Auth != "Bearer "+hubTestToken {
		t.Errorf("requests = %+v", reqs)
	}

	// 沒有 token、沒有網址、沒有 Hub：hub_not_configured，不送出任何請求。
	for name, rg := range map[string]*hubRig{
		"沒有 token": newHubRigOpts(t, newHubStub(t), hubRigOpts{}),
		"沒有網址":     newHubRigOpts(t, newHubStub(t), hubRigOpts{token: hubTestToken, noURL: true}),
	} {
		if _, err := rg.p.HubMe(ctx); !errors.Is(err, hubclient.ErrNotConfigured) {
			t.Errorf("%s：%v", name, err)
		}
		if rg.stub.total() != 0 {
			t.Errorf("%s：不該連線", name)
		}
	}
	if _, err := newRig(t).p.HubMe(ctx); !errors.Is(err, hubclient.ErrNotConfigured) {
		t.Errorf("Deps.Hub 為 nil：%v", err)
	}

	// 讀取 token 失敗：原樣回傳錯誤。
	boom := errors.New("secret store down")
	rg := newRig(t, func(d *Deps) {
		d.Hub = hubclient.New(stub.srv.URL, func(context.Context) (string, bool, error) { return "", false, boom }, stub.srv.Client(), clock.Real{})
	})
	if _, err := rg.p.HubMe(ctx); !errors.Is(err, boom) {
		t.Errorf("token 錯誤：%v", err)
	}

	// 401：hub_unauthorized，訊息不含 token。
	stub.status = 401
	_, err = r.p.HubMe(ctx)
	he := hubErrOf(t, err)
	if he.Code() != "hub_unauthorized" || he.HTTPStatus() != 502 || strings.Contains(he.Error(), hubTestToken) {
		t.Errorf("err = %+v %q", he, he.Error())
	}
}

func TestItemHubSharesListsRecordedShares(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	it := r.ingest(watch, nil, StageMetadata)
	if got, err := r.p.ItemHubShares(ctx, it.ID); err != nil || len(got) != 0 {
		t.Fatalf("shares = %+v, %v", got, err)
	}
	if err := r.st.Write().InsertHubShare(ctx, store.HubShare{ItemID: it.ID, HubURL: "https://hub.example", ShareID: "s1", VersionsJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.p.ItemHubShares(ctx, it.ID)
	if err != nil || len(got) != 1 || got[0].ShareID != "s1" || got[0].HubURL != "https://hub.example" {
		t.Fatalf("shares = %+v, %v", got, err)
	}
}

// waitHang 在假 Hub 上讓請求懸著，直到請求被取消；第一個請求到達時關閉 arrived。
func (s *hubStub) waitHang() <-chan struct{} {
	arrived := make(chan struct{})
	var once sync.Once
	s.mu.Lock()
	s.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		once.Do(func() { close(arrived) })
		<-r.Context().Done()
		return true
	}
	s.mu.Unlock()
	return arrived
}

// runWithTimeout 在 goroutine 中執行 f，等請求到達假 Hub 後推進 hubclient 的 clock，回傳 f 的錯誤。
func (r *hubRig) runWithTimeout(arrived <-chan struct{}, d time.Duration, f func() error) error {
	r.t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case <-arrived:
	case err := <-done:
		r.t.Fatalf("請求沒有到達假 Hub：%v", err)
	case <-time.After(5 * time.Second):
		r.t.Fatal("等不到請求")
	}
	r.hubClk.Advance(d)
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		r.t.Fatal("逾時後呼叫沒有返回")
	}
	return nil
}

func TestHubMeTimeoutUsesInjectedClock(t *testing.T) {
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	arrived := stub.waitHang()
	err := r.runWithTimeout(arrived, hubclient.DefaultTimeout, func() error { _, err := r.p.HubMe(context.Background()); return err })
	he := hubErrOf(t, err)
	if !he.Timeout || he.Code() != "hub_unavailable" || he.HTTPStatus() != 502 || !strings.Contains(he.Error(), "連線逾時") {
		t.Errorf("err = %+v %q", he, he.Error())
	}
	if strings.Contains(fmt.Sprint(err), hubTestToken) {
		t.Error("錯誤訊息含 token")
	}
}
