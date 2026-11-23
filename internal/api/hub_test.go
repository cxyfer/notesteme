package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/hubsettings"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// Hub 路由（HSHR）：設定、測試連線、分享與重新生成的 scope、狀態碼對應與機密不外洩。

const apiHubToken = "nsh_api_test_secret_token_0123456789"

// apiHub 是最小的假 Hub：status 非 0 時所有請求回該狀態；versions 以版本 ID 為鍵。
type apiHub struct {
	srv *httptest.Server
	mu  sync.Mutex
	// status、body：非 0 時所有請求直接回應。
	status int
	body   string
	// versions 是版本的 JSON 資訊（id → 本體）與內容（id → bytes）。
	meta    map[string]string
	content map[string][]byte
	hits    []string
	hook    func(w http.ResponseWriter, r *http.Request) bool
}

func newAPIHub(t *testing.T) *apiHub {
	t.Helper()
	h := &apiHub{meta: map[string]string{}, content: map[string][]byte{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *apiHub) hitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.hits)
}

func (h *apiHub) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	h.mu.Lock()
	h.hits = append(h.hits, r.Method+" "+r.URL.Path)
	status, body, hook := h.status, h.body, h.hook
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if hook != nil && hook(w, r) {
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+apiHubToken {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"code":"unauthorized"}`)
		return
	}
	switch {
	case r.URL.Path == "/api/v1/me":
		_, _ = io.WriteString(w, `{"id":"u1","display_name":"Tester","permissions":["read","upload","bogus"]}`)
	case r.URL.Path == "/api/v1/shares":
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"share_id":"share-1","versions":[{"id":"ver1","kind":"transcript","variant":"source","lang":"en","deduplicated":false}]}`)
	case strings.HasPrefix(r.URL.Path, "/api/v1/versions/"):
		id, isContent := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/versions/"), "/content")
		h.mu.Lock()
		meta, ok := h.meta[id]
		data := h.content[id]
		h.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if isContent {
			_, _ = w.Write(data)
			return
		}
		_, _ = io.WriteString(w, meta)
	default:
		w.WriteHeader(404)
	}
}

// addVersion 加入一個 YouTube 影片 abc123xxxxx 的原始字幕版本。
func (h *apiHub) addVersion(id, kind, variant string, content []byte) {
	h.addVersionSHA(id, kind, variant, content, subtitleSHA(content))
}

func subtitleSHA(b []byte) string { return hubclient.SHA256Hex(b) }

func (h *apiHub) addVersionSHA(id, kind, variant string, content []byte, sha string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.meta[id] = fmt.Sprintf(`{"id":%q,"video":{"platform":"youtube","external_id":"abc123xxxxx","part":null},"kind":%q,"variant":%q,"lang":"en","sha256":%q,"uploaders":[{"display_name":"Alice"}]}`,
		id, kind, variant, sha)
	h.content[id] = content
}

func apiCues(texts ...string) []byte {
	var cues []subtitle.Cue
	for i, s := range texts {
		cues = append(cues, subtitle.Cue{Index: i + 1, StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000, Text: s, Timing: subtitle.TimingSource})
	}
	var buf bytes.Buffer
	if err := subtitle.RenderCuesJSON(&buf, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource, Cues: cues}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

type hubAPIRig struct {
	*itemsRig
	hub      *apiHub
	hubClk   *clock.Fake
	settings *hubsettings.Settings
	auth     *auth.Service
	logBuf   *bytes.Buffer
}

// newHubAPIRig 建立接上假 Hub 的 API 環境；availability 是影片的公開狀態；withToken 為 true 時已設定 Hub token。
func newHubAPIRig(t *testing.T, availability string, withToken bool) *hubAPIRig {
	t.Helper()
	r := &hubAPIRig{hub: newAPIHub(t), hubClk: clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)), logBuf: &bytes.Buffer{}}
	dur := 100
	r.itemsRig = newItemsRigAPI(t, func(ad *Deps) {
		ad.Hub = r.settings
		ad.Log = slog.New(slog.NewJSONHandler(r.logBuf, nil))
		r.auth = ad.Auth
	}, func(d *pipeline.Deps) {
		keys, err := secrets.Open(context.Background(), d.Store, "mk", secrets.TestKDF)
		if err != nil {
			t.Fatal(err)
		}
		r.settings = &hubsettings.Settings{URL: r.hub.srv.URL, Store: d.Store, Secrets: secrets.NewServices(d.Store, keys)}
		d.YouTube = stubYT{info: youtube.Info{ID: "abc123xxxxx", Title: "Hello", Language: "en", DurationS: &dur, Availability: availability,
			Tracks: []youtube.Track{{Lang: "en", Kind: "manual"}}}}
		d.Hub = hubclient.New(r.hub.srv.URL, r.settings.Token, r.hub.srv.Client(), r.hubClk)
		d.HubPrecheck = r.settings.PrecheckEnabled
	})
	if withToken {
		if err := r.settings.Secrets.SetHubToken(context.Background(), apiHubToken); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// addItem 新增影音並跑完，回傳 ID。
func (r *hubAPIRig) addItem() string {
	r.t.Helper()
	rec := r.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	if rec.Code != 201 {
		r.t.Fatalf("POST /api/items = %d %s", rec.Code, rec.Body)
	}
	body := decode[itemsBody](r.t, rec)
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	return body.Items[0].ID
}

func (r *hubAPIRig) tokenFor(name string, scopes ...string) string {
	r.t.Helper()
	ct, err := r.auth.CreateToken(context.Background(), name, scopes, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return ct.Token
}

const okShare = `{"attest":true,"license":"CC-BY-4.0"}`

// noSecretLeak 確認回應與 log 都沒有出現 Hub token。
func (r *hubAPIRig) noSecretLeak(what string, rec *httptest.ResponseRecorder) {
	r.t.Helper()
	if strings.Contains(rec.Body.String(), apiHubToken) {
		r.t.Errorf("%s：回應含 Hub token：%s", what, rec.Body)
	}
	if strings.Contains(r.logBuf.String(), apiHubToken) {
		r.t.Errorf("%s：log 含 Hub token", what)
	}
}

// ---- 設定 ----

func TestHubSettingsGetPut(t *testing.T) {
	r := newHubAPIRig(t, "public", false)

	rec := r.owner("GET", "/api/settings/hub", "")
	if rec.Code != 200 {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	pre := raw["precheck"].(map[string]any)
	if raw["url"] != r.hub.srv.URL || raw["token_set"] != false || pre["enabled"] != false || pre["disclosure_version"] != hubsettings.DisclosureVersion ||
		!strings.Contains(pre["disclosure"].(string), r.hub.srv.URL) {
		t.Errorf("GET = %s", rec.Body)
	}

	// 設定 token：回應只有 token_set，沒有 token；不會連線到 Hub。
	rec = r.owner("PUT", "/api/settings/hub", `{"token":"`+apiHubToken+`"}`)
	if rec.Code != 200 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	r.noSecretLeak("PUT", rec)
	if got := decode[hubsettings.HubSettings](t, rec); !got.TokenSet {
		t.Errorf("PUT = %+v", got)
	}
	if r.hub.hitCount() != 0 {
		t.Error("設定 token 不該連線到 Hub")
	}
	rec = r.owner("GET", "/api/settings/hub", "")
	r.noSecretLeak("GET", rec)
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if raw["token_set"] != true {
		t.Errorf("GET = %s", rec.Body)
	}
	for k := range raw {
		if k == "token" {
			t.Error("GET 不該回傳 token")
		}
	}

	// 省略 token 時不變；空字串刪除。
	if rec := r.owner("PUT", "/api/settings/hub", `{}`); rec.Code != 200 || !decode[hubsettings.HubSettings](t, rec).TokenSet {
		t.Errorf("PUT {} = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("PUT", "/api/settings/hub", `{"token":""}`); rec.Code != 200 || decode[hubsettings.HubSettings](t, rec).TokenSet {
		t.Errorf("PUT 刪除 = %d %s", rec.Code, rec.Body)
	}
}

// 不合法的 token：控制字元、超過 512 位元組、前後空白（422），錯誤訊息不回顯 token，也沒有寫入。
func TestHubSettingsRejectsInvalidTokens(t *testing.T) {
	r := newHubAPIRig(t, "public", false)
	bad := map[string]string{
		"換行":     "nsh_abc\\ndef",
		"NUL":    "nsh_abc\\u0000def",
		"ESC":    "nsh_\\u001b[31mred",
		"DEL":    "nsh_abc\\u007fdef",
		"超過長度":   "nsh_" + strings.Repeat("a", 509),
		"前面空白":   " nsh_abc",
		"後面空白":   "nsh_abc ",
		"尾端換行":   "nsh_abc\\n",
		"只有空白":   "   ",
		"定位字元":   "nsh_\\tabc",
		"Tab 結尾": "nsh_abc\\t",
	}
	for name, tok := range bad {
		rec := r.owner("PUT", "/api/settings/hub", `{"token":"`+tok+`"}`)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != 422 || e.Code != CodeInvalidInput || len(e.Errors) != 1 || e.Errors[0].Path != "token" {
			t.Errorf("%s：%d %s", name, rec.Code, rec.Body)
			continue
		}
		if strings.Contains(rec.Body.String(), "nsh_") || strings.Contains(r.logBuf.String(), "nsh_abc") {
			t.Errorf("%s：訊息回顯了 token：%s", name, rec.Body)
		}
	}
	if _, ok, _ := r.settings.Token(context.Background()); ok {
		t.Error("不合法的 token 不該被儲存")
	}
	// 剛好 512 位元組可以。
	rec := r.owner("PUT", "/api/settings/hub", `{"token":"nsh_`+strings.Repeat("a", 508)+`"}`)
	if rec.Code != 200 {
		t.Errorf("512 位元組：%d %s", rec.Code, rec.Body)
	}
}

// 開啟 ASR 前查詢要確認目前版本的說明（HSHR-R12）；沒確認時 token 也不寫入。
func TestHubSettingsPrecheckRequiresAcknowledgement(t *testing.T) {
	r := newHubAPIRig(t, "public", false)
	for name, body := range map[string]string{
		"沒有 acknowledge": `{"precheck":{"enabled":true},"token":"` + apiHubToken + `"}`,
		"舊版本":            `{"precheck":{"enabled":true,"acknowledge":"hub-precheck-v0"},"token":"` + apiHubToken + `"}`,
		"空字串":            `{"precheck":{"enabled":true,"acknowledge":""}}`,
	} {
		rec := r.owner("PUT", "/api/settings/hub", body)
		var raw map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		if rec.Code != 422 || raw["code"] != "acknowledgement_required" || raw["disclosure_version"] != hubsettings.DisclosureVersion ||
			!strings.Contains(fmt.Sprint(raw["disclosure"]), "SHA-256") || !strings.Contains(fmt.Sprint(raw["disclosure"]), r.hub.srv.URL) {
			t.Errorf("%s：%d %s", name, rec.Code, rec.Body)
		}
		r.noSecretLeak(name, rec)
	}
	if _, ok, _ := r.settings.Token(context.Background()); ok {
		t.Error("沒有確認說明時，同一個請求的 token 也不該寫入")
	}
	if on, _ := r.settings.PrecheckEnabled(context.Background()); on {
		t.Error("不該開啟")
	}

	rec := r.owner("PUT", "/api/settings/hub", `{"precheck":{"enabled":true,"acknowledge":"`+hubsettings.DisclosureVersion+`"}}`)
	if got := decode[hubsettings.HubSettings](t, rec); rec.Code != 200 || !got.Precheck.Enabled {
		t.Fatalf("確認後：%d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/settings/hub", ""); !decode[hubsettings.HubSettings](t, rec).Precheck.Enabled {
		t.Errorf("GET = %s", rec.Body)
	}
	// 關閉不需要確認；省略 enabled 時不變。
	if rec := r.owner("PUT", "/api/settings/hub", `{"precheck":{}}`); !decode[hubsettings.HubSettings](t, rec).Precheck.Enabled {
		t.Errorf("省略 enabled：%s", rec.Body)
	}
	if rec := r.owner("PUT", "/api/settings/hub", `{"precheck":{"enabled":false}}`); rec.Code != 200 || decode[hubsettings.HubSettings](t, rec).Precheck.Enabled {
		t.Errorf("關閉：%d %s", rec.Code, rec.Body)
	}
}

func TestHubSettingsStoreFailureIs500WithoutLeaking(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	// 設定換成讀不到資料的儲存（已關閉）。
	dead, err := store.Open(context.Background(), t.TempDir(), r.clk)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.Open(context.Background(), dead, "mk", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	r.settings.Store, r.settings.Secrets = dead, secrets.NewServices(dead, keys)
	_ = dead.Close()

	for _, c := range []struct{ method, body string }{{"GET", ""}, {"PUT", `{"token":"` + apiHubToken + `"}`}} {
		rec := r.owner(c.method, "/api/settings/hub", c.body)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != 500 || e.Code != CodeInternal {
			t.Errorf("%s = %d %s", c.method, rec.Code, rec.Body)
		}
		r.noSecretLeak(c.method, rec)
	}
	if !strings.Contains(r.logBuf.String(), "Hub 設定失敗") {
		t.Errorf("應該記錄錯誤：%s", r.logBuf)
	}
}

// 沒有組裝 Hub 設定（Deps.Hub 為 nil）：501。
func TestHubSettingsNotImplementedWithoutSettings(t *testing.T) {
	r := newItemsRig(t)
	for _, c := range []struct{ method, body string }{{"GET", ""}, {"PUT", `{}`}} {
		rec := r.owner(c.method, "/api/settings/hub", c.body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != 501 || e.Code != CodeNotImplemented {
			t.Errorf("%s = %d %s", c.method, rec.Code, rec.Body)
		}
	}
}

// ---- scope ----

func TestHubRoutesScopes(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	id := r.addItem()
	r.hub.addVersion("v1", "transcript", "source", apiCues("a"))
	toks := map[string]string{
		auth.ScopeRead:   r.tokenFor("r", auth.ScopeRead),
		auth.ScopeAgent:  r.tokenFor("ag", auth.ScopeAgent),
		auth.ScopeIngest: r.tokenFor("in", auth.ScopeIngest),
		auth.ScopeAdmin:  r.tokenFor("ad", auth.ScopeAdmin),
	}
	cases := []struct {
		method, path, body, want string
		ok                       int // 有正確 scope 時預期的狀態
	}{
		{"GET", "/api/settings/hub", "", auth.ScopeAdmin, 200},
		{"PUT", "/api/settings/hub", `{}`, auth.ScopeAdmin, 200},
		{"POST", "/api/settings/hub/test", "", auth.ScopeAdmin, 200},
		{"POST", "/api/items/" + id + "/hub/share", okShare, auth.ScopeAdmin, 201},
		{"POST", "/api/hub/import", `{"version":"v1"}`, auth.ScopeIngest, 202},
	}
	for _, c := range cases {
		for name, tok := range toks {
			if name == c.want {
				continue
			}
			rec := r.do(c.method, c.path, c.body, withBearer(tok))
			e := decodeError(t, rec.Body.Bytes())
			if rec.Code != 403 || e.Code != CodeForbidden || e.RequiredScope != c.want {
				t.Errorf("%s %s 用 %s：%d %s", c.method, c.path, name, rec.Code, rec.Body)
			}
		}
		// 沒有帶 token：401。
		if rec := r.do(c.method, c.path, c.body, nil); rec.Code != 401 {
			t.Errorf("%s %s 沒有登入：%d", c.method, c.path, rec.Code)
		}
		// 缺 scope 時，空本文也是 403，不是欄位驗證的 422（AUTH-R8）。
		if rec := r.do(c.method, c.path, "", withBearer(toks[auth.ScopeRead])); rec.Code != 403 {
			t.Errorf("%s %s 空本文缺 scope：%d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if r.hub.hitCount() != 0 {
		t.Fatalf("scope 不足時不該連線到 Hub：%d", r.hub.hitCount())
	}
	for _, c := range cases {
		rec := r.do(c.method, c.path, c.body, withBearer(toks[c.want]))
		if rec.Code != c.ok {
			t.Errorf("%s %s 用 %s：%d %s，預期 %d", c.method, c.path, c.want, rec.Code, rec.Body, c.ok)
		}
		r.noSecretLeak(c.method+" "+c.path, rec)
	}
	// 擁有者（session）可以用所有路由。
	if rec := r.owner("POST", "/api/settings/hub/test", ""); rec.Code != 200 {
		t.Errorf("owner test = %d %s", rec.Code, rec.Body)
	}
}

// 沒有組裝 Pipeline：回 500（內部錯誤），不是 panic。
func TestHubRoutesWithoutPipeline(t *testing.T) {
	r := newItemsRigAPI(t, func(ad *Deps) { ad.Pipeline = nil })
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/settings/hub/test", ""},
		{"POST", "/api/items/x/hub/share", okShare},
		{"POST", "/api/hub/import", `{"version":"v1"}`},
	} {
		rec := r.owner(c.method, c.path, c.body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != 500 || e.Code != CodeInternal {
			t.Errorf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// ---- 測試連線 ----

func TestHubTestConnection(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	rec := r.owner("POST", "/api/settings/hub/test", "")
	if rec.Code != 200 {
		t.Fatalf("test = %d %s", rec.Code, rec.Body)
	}
	// 不認得的權限名稱被濾掉。
	if got := decode[HubTestView](t, rec); got.DisplayName != "Tester" || strings.Join(got.Permissions, ",") != "read,upload" {
		t.Errorf("view = %+v", got)
	}
	r.noSecretLeak("test", rec)
}

func TestHubTestNotConfigured(t *testing.T) {
	r := newHubAPIRig(t, "public", false)
	rec := r.owner("POST", "/api/settings/hub/test", "")
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 409 || e.Code != "hub_not_configured" || e.Message != hubclient.MsgNotConfigured || len(e.Actions) != 1 || e.Actions[0] != hubclient.ActionConfigure {
		t.Errorf("test = %d %s", rec.Code, rec.Body)
	}
	if r.hub.hitCount() != 0 {
		t.Error("沒有 token 時不該連線")
	}
}

// Hub 的狀態碼對應（ADR-0032 第 5 點）。
func TestHubErrorMappingOnTestRoute(t *testing.T) {
	tests := []struct {
		status int
		body   string
		code   string
		http   int
		msg    string
		retry  int
	}{
		{401, `{"message":"` + apiHubToken + `"}`, "hub_unauthorized", 502, "token 無效或已過期", 0},
		{403, `{"required_permission":"upload"}`, "hub_forbidden", 502, "「upload」權限", 0},
		{403, `{}`, "hub_forbidden", 502, "拒絕了這個請求", 0},
		{404, `{}`, "hub_version_not_found", 404, "找不到這個版本", 0},
		{410, `{}`, "hub_version_not_found", 404, "找不到這個版本", 0},
		{422, `{"code":"bad_thing","message":"不合法","errors":[{"path":"x","message":"壞"}]}`, "bad_thing", 422, "不合法", 0},
		{429, `{"retry_after_s":33}`, "hub_rate_limited", 502, "33 秒", 33},
		{500, `{"message":"stack at /srv/hub/x.go"}`, "hub_unavailable", 502, "HTTP 500", 0},
		{503, ``, "hub_unavailable", 502, "HTTP 503", 0},
		{418, ``, "hub_unavailable", 502, "HTTP 418", 0},
	}
	for _, tt := range tests {
		r := newHubAPIRig(t, "public", true)
		r.hub.status, r.hub.body = tt.status, tt.body
		rec := r.owner("POST", "/api/settings/hub/test", "")
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != tt.http || e.Code != tt.code || !strings.Contains(e.Message, tt.msg) {
			t.Errorf("%d：%d %s", tt.status, rec.Code, rec.Body)
		}
		if tt.retry != 0 && (e.RetryAfterS == nil || *e.RetryAfterS != tt.retry) {
			t.Errorf("%d：retry_after_s = %v", tt.status, e.RetryAfterS)
		}
		if tt.retry == 0 && e.RetryAfterS != nil {
			t.Errorf("%d：不該有 retry_after_s", tt.status)
		}
		if tt.status == 422 && (len(e.Errors) != 1 || e.Errors[0].Path != "x") {
			t.Errorf("422 的欄位錯誤 = %+v", e.Errors)
		}
		if strings.Contains(rec.Body.String(), "/srv/hub") {
			t.Errorf("%d：回應含 Hub 的原文：%s", tt.status, rec.Body)
		}
		r.noSecretLeak(fmt.Sprint(tt.status), rec)
		// 失敗記錄代碼與狀態，不含訊息本文。
		if !strings.Contains(r.logBuf.String(), `"code":"`+tt.code+`"`) || !strings.Contains(r.logBuf.String(), "Hub 請求失敗") {
			t.Errorf("%d：log = %s", tt.status, r.logBuf)
		}
	}
}

// 逾時以注入的 clock 推進；連線失敗是 hub_unavailable。
func TestHubTestTimeoutAndConnectionFailure(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	arrived := make(chan struct{})
	var once sync.Once
	r.hub.hook = func(_ http.ResponseWriter, req *http.Request) bool {
		once.Do(func() { close(arrived) })
		<-req.Context().Done()
		return true
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- r.owner("POST", "/api/settings/hub/test", "") }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("請求沒有到達 Hub")
	}
	r.hubClk.Advance(hubclient.DefaultTimeout)
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("逾時後沒有回應")
	}
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 502 || e.Code != "hub_unavailable" || !strings.Contains(e.Message, "連線逾時") {
		t.Errorf("逾時 = %d %s", rec.Code, rec.Body)
	}

	r.hub.srv.Close()
	rec = r.owner("POST", "/api/settings/hub/test", "")
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 502 || e.Code != "hub_unavailable" || !strings.Contains(e.Message, "連線失敗") {
		t.Errorf("連線失敗 = %d %s", rec.Code, rec.Body)
	}
	r.noSecretLeak("連線失敗", rec)
}

// ---- 分享 ----

func TestHubShareSuccessRecordsShareOnItem(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	id := r.addItem()
	rec := r.owner("POST", "/api/items/"+id+"/hub/share", `{"attest":true,"license":"CC0-1.0","visibility":"private","include_frames":false,"artifacts":[{"kind":"transcript","variant":"source","lang":"en"}]}`)
	if rec.Code != 201 {
		t.Fatalf("share = %d %s", rec.Code, rec.Body)
	}
	got := decode[HubShareView](t, rec)
	if got.ShareID != "share-1" || len(got.Versions) != 1 || got.Versions[0].VersionID != "ver1" || got.Versions[0].Deduplicated {
		t.Errorf("share = %+v", got)
	}
	r.noSecretLeak("share", rec)

	// GET /api/items/:id 列出分享紀錄。
	rec = r.owner("GET", "/api/items/"+id, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var detail struct {
		HubShares []HubShareRef `json:"hub_shares"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if len(detail.HubShares) != 1 || detail.HubShares[0].ShareID != "share-1" || detail.HubShares[0].HubURL != r.hub.srv.URL || detail.HubShares[0].CreatedAt.IsZero() {
		t.Errorf("hub_shares = %+v", detail.HubShares)
	}

	// 沒分享過的影音：沒有 hub_shares 欄位。
	rec = r.owner("GET", "/api/items", "")
	if strings.Contains(rec.Body.String(), "hub_shares") {
		t.Errorf("列表不該有 hub_shares：%s", rec.Body)
	}
}

func TestHubShareAcceptsRefAsItemID(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	r.addItem()
	rec := r.owner("POST", "/api/items/yt:abc123xxxxx/hub/share", okShare)
	if rec.Code != 201 {
		t.Errorf("以代號分享 = %d %s", rec.Code, rec.Body)
	}
}

// 請求本身的檢查（HSHR-R3）在找影音之前：不存在的影音也是 422；通過之後才 404。
func TestHubShareValidatesRequestBeforeLookingUpTheItem(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	tests := []struct {
		body string
		code string
		path string
	}{
		{`{"license":"CC-BY-4.0"}`, "attest_required", ""},
		{`{"attest":false,"license":"CC-BY-4.0"}`, "attest_required", ""},
		{`{"attest":true}`, "invalid_request", "license"},
		{`{"attest":true,"license":"GPL-3.0"}`, "license_invalid", ""},
		{`{"attest":true,"license":"cc0-1.0"}`, "license_invalid", ""},
	}
	for _, tt := range tests {
		rec := r.owner("POST", "/api/items/no-such-item/hub/share", tt.body)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != 422 || e.Code != tt.code {
			t.Errorf("%s：%d %s", tt.body, rec.Code, rec.Body)
		}
		if tt.path != "" && (len(e.Errors) != 1 || e.Errors[0].Path != tt.path) {
			t.Errorf("%s：errors = %+v", tt.body, e.Errors)
		}
	}
	rec := r.owner("POST", "/api/items/no-such-item/hub/share", okShare)
	if rec.Code != 404 {
		t.Errorf("不存在的影音：%d %s", rec.Code, rec.Body)
	}
	if r.hub.hitCount() != 0 {
		t.Error("被拒絕的請求不該連線")
	}
}

func TestHubShareRefusals(t *testing.T) {
	// 非公開影音。
	for _, avail := range []string{"", "private", "needs_auth"} {
		r := newHubAPIRig(t, avail, true)
		id := r.addItem()
		rec := r.owner("POST", "/api/items/"+id+"/hub/share", okShare)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != 422 || e.Code != "not_shareable" {
			t.Errorf("availability %q：%d %s", avail, rec.Code, rec.Body)
		}
		if r.hub.hitCount() != 0 {
			t.Errorf("availability %q：不該連線", avail)
		}
	}

	r := newHubAPIRig(t, "public", true)
	id := r.addItem()
	// 沒有的產出、沒有可分享的內容。
	rec := r.owner("POST", "/api/items/"+id+"/hub/share", `{"attest":true,"license":"CC0-1.0","artifacts":[{"kind":"transcript","variant":"edited"}]}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 422 || e.Code != "artifact_not_found" {
		t.Errorf("artifact_not_found：%d %s", rec.Code, rec.Body)
	}

	// 垃圾桶中的影音：分享回 404 in_trash。
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code != 200 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/items/"+id+"/hub/share", okShare)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 404 || e.Code != CodeInTrash || e.ItemID != id || len(e.Actions) == 0 {
		t.Errorf("垃圾桶：%d %s", rec.Code, rec.Body)
	}
	if r.hub.hitCount() != 0 {
		t.Error("垃圾桶中的影音不該連線")
	}
}

func TestHubShareNotConfigured(t *testing.T) {
	r := newHubAPIRig(t, "public", false)
	id := r.addItem()
	rec := r.owner("POST", "/api/items/"+id+"/hub/share", okShare)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 409 || e.Code != "hub_not_configured" || len(e.Actions) != 1 || e.Actions[0] != hubclient.ActionConfigure {
		t.Errorf("share = %d %s", rec.Code, rec.Body)
	}
	if r.hub.hitCount() != 0 {
		t.Error("沒有 token 不該連線")
	}
}

func TestHubShareMapsHubFailures(t *testing.T) {
	tests := []struct {
		status int
		body   string
		code   string
		http   int
	}{
		{401, `{}`, "hub_unauthorized", 502},
		{403, `{"required_permission":"upload"}`, "hub_forbidden", 502},
		{429, `{"retry_after_s":9}`, "hub_rate_limited", 502},
		{422, `{"code":"license_conflict","message":"授權衝突","errors":[{"path":"license","message":"不相容"}]}`, "license_conflict", 422},
		{500, `{}`, "hub_unavailable", 502},
		{404, `{}`, "hub_version_not_found", 404},
	}
	for _, tt := range tests {
		r := newHubAPIRig(t, "public", true)
		id := r.addItem()
		r.hub.status, r.hub.body = tt.status, tt.body
		rec := r.owner("POST", "/api/items/"+id+"/hub/share", okShare)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != tt.http || e.Code != tt.code {
			t.Errorf("%d：%d %s", tt.status, rec.Code, rec.Body)
		}
		if tt.status == 429 && (e.RetryAfterS == nil || *e.RetryAfterS != 9) {
			t.Errorf("429：retry_after_s = %v", e.RetryAfterS)
		}
		if tt.status == 422 && (len(e.Errors) != 1 || e.Errors[0].Path != "license" || e.Errors[0].Message != "不相容") {
			t.Errorf("422：errors = %+v", e.Errors)
		}
		r.noSecretLeak(fmt.Sprint(tt.status), rec)
		// 失敗不會在影音上留下分享紀錄。
		if strings.Contains(r.owner("GET", "/api/items/"+id, "").Body.String(), "hub_shares") {
			t.Errorf("%d：失敗的分享不該記錄", tt.status)
		}
	}
}

// ---- 重新生成 ----

func TestHubImportCreatesItem(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	r.hub.addVersion("v1", "transcript", "source", apiCues("from the hub", "two"))
	rec := r.owner("POST", "/api/hub/import", `{"version":"v1"}`)
	if rec.Code != 202 {
		t.Fatalf("import = %d %s", rec.Code, rec.Body)
	}
	got := decode[HubImportView](t, rec)
	if got.Item.ID == "" || got.Item.Ref != "yt:abc123xxxxx" {
		t.Fatalf("view = %+v", got)
	}
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec = r.owner("GET", "/api/items/"+got.Item.ID+"/cues?variant=source", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "from the hub") {
		t.Errorf("cues = %d %s", rec.Code, rec.Body)
	}
	r.noSecretLeak("import", rec)

	// 影音已存在：從 transcript 重跑，不新增影音。
	r.hub.addVersion("v2", "transcript", "normalized", apiCues("again"))
	rec = r.owner("POST", "/api/hub/import", `{"version":"v2"}`)
	if rec.Code != 202 || decode[HubImportView](t, rec).Item.ID != got.Item.ID {
		t.Errorf("再次匯入 = %d %s", rec.Code, rec.Body)
	}
}

func TestHubImportRejectsBadRequests(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	// 本文驗證（Huma）：空的、太長、缺欄位、未知欄位。
	for name, body := range map[string]string{
		"空的版本":   `{"version":""}`,
		"版本太長":   `{"version":"` + strings.Repeat("a", 65) + `"}`,
		"沒有版本":   `{}`,
		"版本不是字串": `{"version":123}`,
	} {
		rec := r.owner("POST", "/api/hub/import", body)
		if rec.Code != 422 {
			t.Errorf("%s：%d %s", name, rec.Code, rec.Body)
		}
	}
	// 不合法的版本 ID 不會連線：404 hub_version_not_found。
	for _, id := range []string{"../etc/passwd", "a/b", "a b", "-x", "v1?x=1", "v1%2f..", "v1\\n"} {
		body, _ := json.Marshal(map[string]string{"version": id})
		rec := r.owner("POST", "/api/hub/import", string(body))
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != 404 || e.Code != "hub_version_not_found" {
			t.Errorf("%q：%d %s", id, rec.Code, rec.Body)
		}
	}
	if r.hub.hitCount() != 0 {
		t.Errorf("不合法的請求不該連線：%d", r.hub.hitCount())
	}
	// 處理方案不存在：欄位錯誤。
	r.hub.addVersion("v1", "transcript", "source", apiCues("x"))
	rec := r.owner("POST", "/api/hub/import", `{"version":"v1","profile":"no-such"}`)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 422 || len(e.Errors) == 0 || e.Errors[0].Path != "profile" {
		t.Errorf("profile：%d %s", rec.Code, rec.Body)
	}
}

func TestHubImportMapsFailures(t *testing.T) {
	// 惡意或錯誤的版本：每個回應都是 4xx／5xx，不建立影音。
	r := newHubAPIRig(t, "public", true)
	good := apiCues("hello")
	r.hub.addVersion("vtr", "transcript", "translated", good)
	r.hub.addVersion("vch", "chapters", "", good)
	r.hub.addVersionSHA("vsha", "transcript", "source", good, strings.Repeat("0", 64))
	r.hub.addVersion("vbad", "transcript", "source", []byte(`<html>`))
	r.hub.addVersion("vempty", "transcript", "source", []byte(`{"lang":"en","variant":"source","cues":[]}`))
	r.hub.meta["vpod"] = `{"id":"vpod","video":{"platform":"podcast","external_id":"feed/guid","part":null},"kind":"transcript","variant":"source","lang":"en","sha256":"` + hubclient.SHA256Hex(good) + `"}`
	r.hub.content["vpod"] = good
	r.hub.meta["vurl"] = `{"id":"vurl","video":{"platform":"youtube","external_id":"http://evil.example/x","part":null},"kind":"transcript","variant":"source","lang":"en","sha256":"` + hubclient.SHA256Hex(good) + `"}`
	r.hub.content["vurl"] = good
	tests := []struct {
		version string
		status  int
		code    string
	}{
		{"vtr", 422, "not_raw"},
		{"vch", 422, "not_raw"},
		{"vsha", 502, "hub_integrity"},
		{"vbad", 422, "hub_content_invalid"},
		{"vempty", 422, "hub_content_invalid"},
		{"vpod", 422, "not_in_library"},
		{"vurl", 422, "hub_content_invalid"},
		{"missing", 404, "hub_version_not_found"},
	}
	for _, tt := range tests {
		rec := r.owner("POST", "/api/hub/import", `{"version":"`+tt.version+`"}`)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != tt.status || e.Code != tt.code {
			t.Errorf("%s：%d %s", tt.version, rec.Code, rec.Body)
		}
		r.noSecretLeak(tt.version, rec)
	}
	if body := r.owner("GET", "/api/items", "").Body.String(); !strings.Contains(body, `"items":[]`) {
		t.Errorf("驗證失敗不該建立影音：%s", body)
	}

	// Hub 本身的錯誤。
	for _, tt := range []struct {
		status int
		body   string
		code   string
		http   int
	}{
		{401, `{}`, "hub_unauthorized", 502},
		{403, `{}`, "hub_forbidden", 502},
		{429, `{}`, "hub_rate_limited", 502},
		{500, `{}`, "hub_unavailable", 502},
		{410, `{}`, "hub_version_not_found", 404},
	} {
		r.hub.status, r.hub.body = tt.status, tt.body
		rec := r.owner("POST", "/api/hub/import", `{"version":"vsha"}`)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != tt.http || e.Code != tt.code {
			t.Errorf("%d：%d %s", tt.status, rec.Code, rec.Body)
		}
		if tt.status == 429 && (e.RetryAfterS == nil || *e.RetryAfterS != 60) {
			t.Errorf("429 沒有 retry_after_s 時用預設 60：%v", e.RetryAfterS)
		}
	}
}

func TestHubImportNotConfiguredAndTrash(t *testing.T) {
	r := newHubAPIRig(t, "public", false)
	rec := r.owner("POST", "/api/hub/import", `{"version":"v1"}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 409 || e.Code != "hub_not_configured" || len(e.Actions) != 1 {
		t.Errorf("沒有 token：%d %s", rec.Code, rec.Body)
	}

	// 影音在垃圾桶中：匯入回 409（不是分享的 404），不連線取代內容。
	r = newHubAPIRig(t, "public", true)
	id := r.addItem()
	r.hub.addVersion("v1", "transcript", "source", apiCues("x"))
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code != 200 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/hub/import", `{"version":"v1"}`)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 409 || e.Code != CodeInTrash || e.ItemID != id {
		t.Errorf("垃圾桶：%d %s", rec.Code, rec.Body)
	}
}

// Hub 的 422 訊息會原樣轉出給呼叫者（先清理控制字元與長度）；其他錯誤只用 Client 自己的訊息。
func TestHubImportDoesNotEchoHubMessagesExceptValidation(t *testing.T) {
	r := newHubAPIRig(t, "public", true)
	r.hub.status = 500
	r.hub.body = `{"code":"x","message":"internal path /srv/hub and token ` + apiHubToken + `"}`
	rec := r.owner("POST", "/api/hub/import", `{"version":"v1"}`)
	if strings.Contains(rec.Body.String(), "/srv/hub") || strings.Contains(rec.Body.String(), apiHubToken) {
		t.Errorf("5xx 不該回顯 Hub 的訊息：%s", rec.Body)
	}
	r.noSecretLeak("500", rec)

	r.hub.status = 422
	r.hub.body = `{"code":"bad","message":"` + strings.Repeat("長", 400) + `\u001b[31m"}`
	rec = r.owner("POST", "/api/hub/import", `{"version":"v1"}`)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 422 || e.Code != "bad" || strings.ContainsRune(e.Message, '\x1b') || len([]rune(e.Message)) > 301 {
		t.Errorf("422：%d %s", rec.Code, rec.Body)
	}
}
