package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// principalProbe 讓測試看到 handler 收到的身分。
func principalProbe(t *testing.T, got *auth.Principal) http.Handler {
	t.Helper()
	return localMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFrom(r.Context())
		if !ok {
			t.Error("context 裡沒有身分")
		}
		*got = p
	}))
}

func TestLocalMiddlewareSetsOwnerPrincipal(t *testing.T) {
	var got auth.Principal
	principalProbe(t, &got).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/items", nil))
	if got.Kind != PrincipalLocal || got.Scopes != nil {
		t.Errorf("principal = %+v，預期 Kind=local 且 Scopes 為 nil（全部權限）", got)
	}
}

// NewLocal 與 New 共用路由：同一個受保護的路由，local 不需要任何憑證與 CSRF，New 則要求登入。
func TestNewLocalNeedsNoCredentials(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{
		Store: st, Clock: clk, Version: "test",
		Auth: auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams}),
		Log:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	local, remote := NewLocal(d), New(d)

	do := func(h http.Handler, method, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("")))
		return rec.Code
	}
	if code := do(local, "GET", "/api/items"); code != 200 {
		t.Errorf("local GET /api/items = %d，預期 200", code)
	}
	if code := do(remote, "GET", "/api/items"); code != 401 {
		t.Errorf("New GET /api/items = %d，預期 401", code)
	}
	// 非 GET 沒有 CSRF token 也不會被擋（回 501 是因為這個路由還沒實作，不是驗證失敗）。
	if code := do(local, "POST", "/api/tasks/t1/retry"); code != 501 {
		t.Errorf("local POST /api/tasks/t1/retry = %d，預期 501", code)
	}
	if code := do(remote, "POST", "/api/tasks/t1/retry"); code != 401 {
		t.Errorf("New POST /api/tasks/t1/retry = %d，預期 401", code)
	}
}
