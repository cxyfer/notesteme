package hubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

var updateOpenAPI = flag.Bool("update-openapi", false, "重新產生 internal/hub/hubapi/openapi.json")

// TestOpenAPISnapshot 確認 commit 的 openapi.json 與目前的路由一致（Hub 的 Client 契約，ADR-0032 第 1 點）。
// 路由有變更時執行：go run ./internal/hub/hubapi/cmd/openapi -o internal/hub/hubapi/openapi.json
func TestOpenAPISnapshot(t *testing.T) {
	got, err := OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	if *updateOpenAPI {
		if err := os.WriteFile("openapi.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/hub/hubapi/openapi.json 已過期：執行 go run ./internal/hub/hubapi/cmd/openapi -o internal/hub/hubapi/openapi.json")
	}
}

// fakeStore 是只有骨架方法的 hubstore.Store。
type fakeStore struct{ writableErr error }

func (f *fakeStore) CheckWritable(context.Context) error          { return f.writableErr }
func (f *fakeStore) CountUsers(context.Context) (int64, error)    { return 0, nil }
func (f *fakeStore) SchemaVersion(context.Context) (int64, error) { return 1, nil }
func (f *fakeStore) Close() error                                 { return nil }

var _ hubstore.Store = (*fakeStore)(nil)

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{"x":1}`)))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("回應不是 JSON：%s", rec.Body)
	}
	return m
}

func TestHealthAndReady(t *testing.T) {
	st := &fakeStore{}
	h := New(Deps{Store: st, Clock: clock.Real{}, Version: "test"})
	if rec := do(h, "GET", "/healthz"); rec.Code != 200 || decode(t, rec)["status"] != "ok" {
		t.Errorf("/healthz = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/readyz"); rec.Code != 200 {
		t.Errorf("/readyz = %d %s", rec.Code, rec.Body)
	}
	st.writableErr = errors.New("locked at /secret/path")
	rec := do(h, "GET", "/readyz")
	m := decode(t, rec)
	if rec.Code != 503 || m["code"] != CodeNotReady || !reflect.DeepEqual(m["failed"], []any{"database"}) {
		t.Errorf("/readyz（不可寫）= %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "/secret/path") {
		t.Error("/readyz 不應洩漏內部錯誤")
	}
}

// GET /api/v1/openapi.json 是公開的，內容與快照相同（版本號除外）。
func TestOpenAPIServed(t *testing.T) {
	h := New(Deps{Store: &fakeStore{}, Version: "test"})
	rec := do(h, "GET", "/api/v1/openapi.json")
	if rec.Code != 200 {
		t.Fatalf("GET /api/v1/openapi.json = %d", rec.Code)
	}
	served := decode(t, rec)
	var exported map[string]any
	b, _ := OpenAPISpec()
	_ = json.Unmarshal(b, &exported)
	served["info"].(map[string]any)["version"] = OpenAPISnapshotVersion
	if !reflect.DeepEqual(served, exported) {
		t.Error("OpenAPISpec 與 GET /api/v1/openapi.json 的內容不同")
	}
}

// panic 轉成 500，不洩漏細節。
func TestRecover(t *testing.T) {
	h := recoverMiddleware(nilLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom secret") }))
	rec := do(h, "GET", "/x")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") || decode(t, rec)["code"] != "internal_error" {
		t.Errorf("panic = %d %s", rec.Code, rec.Body)
	}
}
