package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

type servicesRig struct {
	h      http.Handler
	st     *store.Store
	auth   *auth.Service
	logBuf *bytes.Buffer
	sess   string
	csrf   string
}

func newServicesRig(t *testing.T) *servicesRig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := secrets.Open(ctx, st, "mk", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := authSvc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, nil))
	h := New(Deps{
		Store: st, Auth: authSvc, Clock: clk, Log: log, Version: "test",
		Services: secrets.NewServices(st, keys),
	})
	ts := &testServer{h: h, clk: clk}
	sess, csrf := ts.login(t)
	return &servicesRig{h: h, st: st, auth: authSvc, logBuf: &logBuf, sess: sess, csrf: csrf}
}

func (r *servicesRig) owner(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	withCookies(r.sess, r.csrf, r.csrf)(req)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func TestSettingsServicesGetPutAndRedaction(t *testing.T) {
	r := newServicesRig(t)

	// 初始為空。
	rec := r.owner("GET", "/api/settings/services", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"providers":{}}` {
		t.Fatalf("initial GET = %d %s", rec.Code, rec.Body)
	}

	// 空 body 的 PUT 也回 200（AUTH-R8 範例使用）。
	if rec := r.owner("PUT", "/api/settings/services", ""); rec.Code != 200 {
		t.Fatalf("empty PUT = %d %s", rec.Code, rec.Body)
	}

	const groqKey = "gsk_supersecret1234"
	const supaKey = "sup_secret5678"
	rec = r.owner("PUT", "/api/settings/services", `{"providers":{"groq":{"api_key":"`+groqKey+`"},"supadata":{"api_key":"`+supaKey+`"}}}`)
	if rec.Code != 200 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), groqKey) || strings.Contains(rec.Body.String(), supaKey) {
		t.Fatalf("response leaked full key: %s", rec.Body)
	}

	var got secrets.ServiceSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Providers["groq"].APIKeyLast4 != "1234" || got.Providers["supadata"].APIKeyLast4 != "5678" {
		t.Fatalf("PUT result = %+v", got)
	}

	// GET 只回傳末四碼。
	rec = r.owner("GET", "/api/settings/services", "")
	if rec.Code != 200 {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Providers["groq"].APIKeyLast4 != "1234" || got.Providers["supadata"].APIKeyLast4 != "5678" {
		t.Fatalf("GET result = %+v", got)
	}

	// 省略／null 保留既有 key；空字串刪除 key。
	rec = r.owner("PUT", "/api/settings/services", `{"providers":{"groq":{},"supadata":{"api_key":""}}}`)
	if rec.Code != 200 {
		t.Fatalf("PUT partial/delete = %d %s", rec.Code, rec.Body)
	}
	got = secrets.ServiceSettings{}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Providers["groq"].APIKeyLast4 != "1234" {
		t.Errorf("groq should be preserved: %+v", got)
	}
	if _, exists := got.Providers["supadata"]; exists {
		t.Errorf("supadata should be deleted: %+v", got)
	}

	// log 不可出現明文 API key。
	if strings.Contains(r.logBuf.String(), groqKey) || strings.Contains(r.logBuf.String(), supaKey) {
		t.Errorf("logs leaked API key: %s", r.logBuf.String())
	}
}

func TestSettingsServicesValidationRejectsBadInputWithoutEchoingKey(t *testing.T) {
	r := newServicesRig(t)
	const badSecret = "secret_with_newline\nDO_NOT_ECHO"
	badJSON := `{"providers":{"groq":{"api_key":"secret_with_newline\nDO_NOT_ECHO"}}}`

	cases := []struct {
		name string
		body string
	}{
		{"unknown top-level field", `{"providers":{},"extra":1}`},
		{"unknown provider field", `{"providers":{"groq":{"api_key":"x","nope":true}}}`},
		{"invalid provider id", `{"providers":{"unknown-ai":{"api_key":"DO_NOT_ECHO_UNKNOWN"}}}`},
		{"control char in api_key", badJSON},
		{"wrong providers type", `{"providers":"groq"}`},
		{"wrong api_key type", `{"providers":{"groq":{"api_key":123}}}`},
	}
	for _, c := range cases {
		rec := r.owner("PUT", "/api/settings/services", c.body)
		if rec.Code != 422 || decodeError(t, rec.Body.Bytes()).Code != CodeInvalidInput {
			t.Errorf("%s: status=%d body=%s, want 422", c.name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "DO_NOT_ECHO") || strings.Contains(rec.Body.String(), badSecret) {
			t.Errorf("%s: error echoed key value: %s", c.name, rec.Body)
		}
	}
	if strings.Contains(r.logBuf.String(), "DO_NOT_ECHO") {
		t.Errorf("logs leaked bad key value: %s", r.logBuf.String())
	}
}
