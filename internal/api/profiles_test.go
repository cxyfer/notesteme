package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
)

// fakeRig 只掛 PRF 的路由，service 換成假的實作：驗證 handler 的對應（狀態碼、錯誤格式、body 傳遞），
// 不依賴 pipeline 的實作。身分由 X-Test-Scopes 標頭決定：沒有標頭是未驗證，"owner" 是擁有全部權限的 session，
// 其他是逗號分隔的 token scope。
type fakeRig struct {
	h        http.Handler
	profiles *fakeProfiles
	channels *fakeChannels
}

func newFakeRig(t *testing.T) *fakeRig {
	t.Helper()
	fp, fc := newFakeProfiles(), newFakeChannels()
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0"))
	rt := &routes{d: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	(&profilesAPI{rt: rt, svc: fp}).register(api)
	(&channelsAPI{rt: rt, svc: fc}).register(api)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Test-Scopes"); v != "" {
			p := auth.Principal{Kind: "session"}
			if v != "owner" {
				p = auth.Principal{Kind: "token", Scopes: strings.Split(v, ",")}
			}
			r = r.WithContext(context.WithValue(r.Context(), keyPrincipal, p))
		}
		mux.ServeHTTP(w, r)
	})
	return &fakeRig{h: h, profiles: fp, channels: fc}
}

// do 以指定的 scope 送出請求；scopes 為空代表沒有身分。
func (r *fakeRig) do(method, path, body string, scopes ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(scopes) > 0 {
		req.Header.Set("X-Test-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

// fakeProfiles 是記憶體內的處理方案 service：只做到足以驗證 handler；合併與驗證的真正行為在 pipeline。
type fakeProfiles struct {
	byName      map[string]*pipeline.Profile
	versions    map[string][]pipeline.ProfileVersion
	defaultName string
	now         time.Time

	// invalid 不是 nil 時，CreateProfile 與 PatchProfile 回這些欄位錯誤。
	invalid []validate.FieldError
	// lastCreate、lastPatch 記下 handler 傳給 service 的內容。
	lastCreate, lastPatch map[string]any
}

func newFakeProfiles() *fakeProfiles {
	f := &fakeProfiles{
		byName: map[string]*pipeline.Profile{}, versions: map[string][]pipeline.ProfileVersion{},
		defaultName: "default", now: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC),
	}
	f.byName["default"] = &pipeline.Profile{Name: "default", Version: 1, Config: map[string]any{}, Builtin: true, SHA256: "s0"}
	f.versions["default"] = []pipeline.ProfileVersion{{Version: 1, Config: map[string]any{}, SHA256: "s0"}}
	return f
}

func (f *fakeProfiles) view(p *pipeline.Profile) pipeline.Profile {
	out := *p
	out.IsDefault = p.Name == f.defaultName
	return out
}

func (f *fakeProfiles) Profiles(context.Context) ([]pipeline.Profile, error) {
	var out []pipeline.Profile
	for _, p := range f.byName {
		out = append(out, f.view(p))
	}
	slices.SortFunc(out, func(a, b pipeline.Profile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeProfiles) Profile(_ context.Context, name string) (pipeline.Profile, error) {
	p, ok := f.byName[name]
	if !ok {
		return pipeline.Profile{}, pipeline.ErrProfileNotFound
	}
	return f.view(p), nil
}

func (f *fakeProfiles) CreateProfile(_ context.Context, name string, cfg map[string]any) (pipeline.Profile, error) {
	f.lastCreate = cfg
	if f.invalid != nil {
		return pipeline.Profile{}, &pipeline.InvalidError{Msg: f.invalid[0].Path + "：" + f.invalid[0].Message, Fields: f.invalid}
	}
	if _, ok := f.byName[name]; ok {
		return pipeline.Profile{}, pipeline.ErrProfileExists
	}
	p := &pipeline.Profile{Name: name, Version: 1, Config: cfg, SHA256: "s1", CreatedAt: f.now, UpdatedAt: f.now}
	f.byName[name] = p
	f.versions[name] = []pipeline.ProfileVersion{{Version: 1, Config: cfg, SHA256: "s1", CreatedAt: f.now}}
	return f.view(p), nil
}

func (f *fakeProfiles) PatchProfile(_ context.Context, name string, patch map[string]any) (pipeline.Profile, error) {
	f.lastPatch = patch
	p, ok := f.byName[name]
	if !ok {
		return pipeline.Profile{}, pipeline.ErrProfileNotFound
	}
	if f.invalid != nil {
		return pipeline.Profile{}, &pipeline.InvalidError{Msg: f.invalid[0].Path + "：" + f.invalid[0].Message, Fields: f.invalid}
	}
	p.Version++
	p.Config = patch // 假的實作不合併，只要版本加 1
	f.versions[name] = append(f.versions[name], pipeline.ProfileVersion{Version: p.Version, Config: patch, SHA256: "s2", CreatedAt: f.now})
	return f.view(p), nil
}

func (f *fakeProfiles) DeleteProfile(_ context.Context, name string) error {
	p, ok := f.byName[name]
	switch {
	case !ok:
		return pipeline.ErrProfileNotFound
	case p.Builtin:
		return pipeline.ErrBuiltinProfile
	case name == f.defaultName:
		return &pipeline.InUseError{Code: pipeline.CodeProfileInUse, UsedBy: []string{"default_profile"}}
	}
	delete(f.byName, name)
	delete(f.versions, name)
	return nil
}

func (f *fakeProfiles) ProfileVersions(_ context.Context, name string) ([]pipeline.ProfileVersion, error) {
	if _, ok := f.byName[name]; !ok {
		return nil, pipeline.ErrProfileNotFound
	}
	return f.versions[name], nil
}

func (f *fakeProfiles) SetDefaultProfile(_ context.Context, name string) (pipeline.Profile, error) {
	p, ok := f.byName[name]
	if !ok {
		return pipeline.Profile{}, pipeline.ErrProfileNotFound
	}
	f.defaultName = name
	return f.view(p), nil
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("回應不是 JSON 物件：%v\n%s", err, rec.Body)
	}
	return m
}

func TestProfilesAPI_CreateGetPatchVersions(t *testing.T) {
	r := newFakeRig(t)

	// 建立：201，內容是 {name, version, config, builtin, ...}；config 回的是存下來的這一層。
	rec := r.do("POST", "/api/profiles", `{"name":"standard","config":{"timing":{"split_timing":"forbid"}}}`, auth.ScopeAdmin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s，預期 201", rec.Code, rec.Body)
	}
	got := decodeMap(t, rec)
	if got["name"] != "standard" || got["version"] != float64(1) || got["builtin"] != false || got["default"] != false {
		t.Errorf("POST 回應 = %v", got)
	}
	if want := map[string]any{"timing": map[string]any{"split_timing": "forbid"}}; !reflect.DeepEqual(got["config"], want) {
		t.Errorf("config = %v，預期 %v", got["config"], want)
	}
	if got["created_at"] == nil || got["updated_at"] == nil {
		t.Errorf("缺少 created_at 或 updated_at：%v", got)
	}

	// 重複的名稱：409 profile_exists。
	rec = r.do("POST", "/api/profiles", `{"name":"standard"}`, auth.ScopeAdmin)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != "profile_exists" {
		t.Errorf("重複建立 = %d %s，預期 409 profile_exists", rec.Code, rec.Body)
	}

	// 讀取：200；不存在：404。
	rec = r.do("GET", "/api/profiles/standard", "", auth.ScopeRead)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["name"] != "standard" {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("GET", "/api/profiles/nope", "", auth.ScopeRead)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || e.Code != CodeNotFound {
		t.Errorf("GET 不存在 = %d %s，預期 404 not_found", rec.Code, rec.Body)
	}

	// 修改：200，帶新的 version；body 的 config 原樣（包含 null）交給 service。
	rec = r.do("PATCH", "/api/profiles/standard", `{"config":{"stages":{"correct":{"model":"m2","prompt":null}}}}`, auth.ScopeAdmin)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["version"] != float64(2) {
		t.Fatalf("PATCH = %d %s，預期 200 與 version 2", rec.Code, rec.Body)
	}
	wantPatch := map[string]any{"stages": map[string]any{"correct": map[string]any{"model": "m2", "prompt": nil}}}
	if !reflect.DeepEqual(r.profiles.lastPatch, wantPatch) {
		t.Errorf("傳給 service 的 patch = %#v，預期 %#v", r.profiles.lastPatch, wantPatch)
	}

	// 版本：依版本遞增，每筆有 version、sha256、config、created_at。
	rec = r.do("GET", "/api/profiles/standard/versions", "", auth.ScopeRead)
	var vs struct {
		Name     string `json:"name"`
		Versions []struct {
			Version   int            `json:"version"`
			SHA256    string         `json:"sha256"`
			Config    map[string]any `json:"config"`
			CreatedAt time.Time      `json:"created_at"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &vs); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET versions = %d %s", rec.Code, rec.Body)
	}
	if vs.Name != "standard" || len(vs.Versions) != 2 || vs.Versions[0].Version != 1 || vs.Versions[1].Version != 2 ||
		vs.Versions[0].SHA256 == "" || vs.Versions[1].Config == nil || vs.Versions[1].CreatedAt.IsZero() {
		t.Errorf("versions = %+v", vs)
	}
	if rec = r.do("GET", "/api/profiles/nope/versions", "", auth.ScopeRead); rec.Code != http.StatusNotFound {
		t.Errorf("不存在的 versions = %d，預期 404", rec.Code)
	}

	// 列表：依名稱排序，default 在 standard 之前。
	rec = r.do("GET", "/api/profiles", "", auth.ScopeRead)
	var list struct {
		Profiles []struct {
			Name    string `json:"name"`
			Builtin bool   `json:"builtin"`
			Default bool   `json:"default"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET list = %d %s", rec.Code, rec.Body)
	}
	if len(list.Profiles) != 2 || list.Profiles[0].Name != "default" || !list.Profiles[0].Builtin || !list.Profiles[0].Default || list.Profiles[1].Name != "standard" {
		t.Errorf("profiles = %+v", list.Profiles)
	}
}

// POST 的 config 省略或 null 視為 {}；name 缺少交給 service 驗證（回 422 errors），不是 Huma 的必填錯誤。
func TestProfilesAPI_CreateConfigOptional(t *testing.T) {
	r := newFakeRig(t)
	for i, body := range []string{`{"name":"a"}`, `{"name":"b","config":null}`, `{"name":"c","config":{}}`} {
		rec := r.do("POST", "/api/profiles", body, auth.ScopeAdmin)
		if rec.Code != http.StatusCreated {
			t.Fatalf("#%d %s = %d %s", i, body, rec.Code, rec.Body)
		}
		if !reflect.DeepEqual(r.profiles.lastCreate, map[string]any{}) {
			t.Errorf("#%d 傳給 service 的 config = %#v，預期 {}", i, r.profiles.lastCreate)
		}
		if cfg, ok := decodeMap(t, rec)["config"].(map[string]any); !ok || len(cfg) != 0 {
			t.Errorf("#%d 回應的 config = %v，預期 {}", i, decodeMap(t, rec)["config"])
		}
	}
	r.profiles.invalid = []validate.FieldError{{Path: "name", Message: "名稱不正確。"}}
	rec := r.do("POST", "/api/profiles", `{}`, auth.ScopeAdmin)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusUnprocessableEntity || len(e.Errors) != 1 || e.Errors[0].Path != "name" {
		t.Errorf("缺 name = %d %s，預期 422 errors[0].path = name", rec.Code, rec.Body)
	}
}

func TestProfilesAPI_Invalid422Errors(t *testing.T) {
	r := newFakeRig(t)
	r.profiles.invalid = []validate.FieldError{
		{Path: "stages.correct.provider", Message: "executor 為 api 時必須指定 provider。"},
		{Path: "stages.correct.model", Message: "executor 為 api 時必須指定 model。"},
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/profiles", `{"name":"broken","config":{"stages":{"correct":{"executor":"api"}}}}`},
		{"PATCH", "/api/profiles/default", `{"config":{"stages":{"correct":{"executor":"api"}}}}`},
	} {
		rec := r.do(c.method, c.path, c.body, auth.ScopeAdmin)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != http.StatusUnprocessableEntity || e.Code != CodeInvalidInput {
			t.Fatalf("%s %s = %d %s，預期 422 invalid_request", c.method, c.path, rec.Code, rec.Body)
		}
		// errors 依 service 給的順序，errors[0] 是第一個錯誤；message 是第一個錯誤的說明。
		if !reflect.DeepEqual(e.Errors, r.profiles.invalid) {
			t.Errorf("%s errors = %+v", c.method, e.Errors)
		}
		if !strings.HasPrefix(e.Message, "stages.correct.provider") {
			t.Errorf("%s message = %q，預期以第一個錯誤的路徑開頭", c.method, e.Message)
		}
	}
	// 422 之後沒有留下任何東西。
	if len(r.profiles.byName) != 1 {
		t.Errorf("422 之後不應建立處理方案：%v", r.profiles.byName)
	}

	// PATCH 沒有 config 物件：422，路徑 config（不進 service）。
	r.profiles.lastPatch = nil
	for _, body := range []string{`{}`, `{"config":null}`} {
		rec := r.do("PATCH", "/api/profiles/default", body, auth.ScopeAdmin)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != http.StatusUnprocessableEntity || len(e.Errors) != 1 || e.Errors[0].Path != "config" {
			t.Errorf("PATCH %s = %d %s，預期 422 errors[0].path = config", body, rec.Code, rec.Body)
		}
	}
	if r.profiles.lastPatch != nil {
		t.Errorf("沒有 config 的 PATCH 不應呼叫 service：%v", r.profiles.lastPatch)
	}
}

func TestProfilesAPI_DeleteDefault409(t *testing.T) {
	r := newFakeRig(t)

	// 內建的 default：409 builtin_profile。
	rec := r.do("DELETE", "/api/profiles/default", "", auth.ScopeAdmin)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodeBuiltinProfile {
		t.Errorf("DELETE default = %d %s，預期 409 builtin_profile", rec.Code, rec.Body)
	}
	if _, ok := r.profiles.byName["default"]; !ok {
		t.Error("內建的 default 被刪除了")
	}

	// 目前的預設處理方案：409 profile_in_use，used_by 列出 default_profile。
	r.do("POST", "/api/profiles", `{"name":"standard"}`, auth.ScopeAdmin)
	if rec = r.do("POST", "/api/profiles/standard/default", "", auth.ScopeAdmin); rec.Code != http.StatusOK {
		t.Fatalf("設為預設 = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("DELETE", "/api/profiles/standard", "", auth.ScopeAdmin)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusConflict || e.Code != CodeProfileInUse || !reflect.DeepEqual(e.UsedBy, []string{"default_profile"}) {
		t.Errorf("DELETE 預設處理方案 = %d %s，預期 409 profile_in_use used_by=[default_profile]", rec.Code, rec.Body)
	}

	// 不是預設的：204 沒有內容；之後讀取是 404；刪不存在的是 404。
	r.do("POST", "/api/profiles", `{"name":"other"}`, auth.ScopeAdmin)
	rec = r.do("DELETE", "/api/profiles/other", "", auth.ScopeAdmin)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("DELETE 一般處理方案 = %d %q，預期 204 沒有內容", rec.Code, rec.Body)
	}
	if rec = r.do("GET", "/api/profiles/other", "", auth.ScopeRead); rec.Code != http.StatusNotFound {
		t.Errorf("刪除之後 GET = %d，預期 404", rec.Code)
	}
	if rec = r.do("DELETE", "/api/profiles/other", "", auth.ScopeAdmin); rec.Code != http.StatusNotFound {
		t.Errorf("再刪一次 = %d，預期 404", rec.Code)
	}
}

func TestProfilesAPI_SetDefault(t *testing.T) {
	r := newFakeRig(t)
	r.do("POST", "/api/profiles", `{"name":"standard"}`, auth.ScopeAdmin)

	rec := r.do("POST", "/api/profiles/standard/default", "", auth.ScopeAdmin)
	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || got["name"] != "standard" || got["default"] != true {
		t.Fatalf("POST default = %d %s，預期 200 且 default=true", rec.Code, rec.Body)
	}
	if r.profiles.defaultName != "standard" {
		t.Errorf("預設處理方案 = %q", r.profiles.defaultName)
	}
	// 列表反映新的預設。
	if body := r.do("GET", "/api/profiles/default", "", auth.ScopeRead).Body.String(); !strings.Contains(body, `"default":false`) {
		t.Errorf("default 不再是預設：%s", body)
	}
	// 不存在：404，預設不變。
	rec = r.do("POST", "/api/profiles/nope/default", "", auth.ScopeAdmin)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || e.Code != CodeNotFound {
		t.Errorf("不存在 = %d %s，預期 404", rec.Code, rec.Body)
	}
	if r.profiles.defaultName != "standard" {
		t.Errorf("失敗之後預設處理方案 = %q", r.profiles.defaultName)
	}
}

// 沒有注入 Pipeline 是設定錯誤：回 500，不是 501（501 代表功能還沒做）。
func TestProfilesAPI_NoPipelineIs500(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0"))
	rt := &routes{d: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	rt.registerProfiles(api)
	rt.registerChannels(api)
	for _, p := range []string{"/api/profiles", "/api/channels/youtube:UC1/overrides"} {
		req := httptest.NewRequest("GET", p, nil)
		req = req.WithContext(context.WithValue(req.Context(), keyPrincipal, auth.Principal{Kind: "session"}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusInternalServerError || e.Code != CodeInternal {
			t.Errorf("GET %s 沒有 Pipeline = %d %s，預期 500", p, rec.Code, rec.Body)
		}
	}
}
