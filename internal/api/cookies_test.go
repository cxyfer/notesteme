package api

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/cxyfer/notesteme/internal/cookies"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

type fakeResumer struct {
	mu     sync.Mutex
	tokens []string
	err    error
}

func (f *fakeResumer) ResumeBlocked(_ context.Context, token string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token)
	return 2, f.err
}

type cookieRig struct {
	*testServer
	st      *store.Store
	resumer *fakeResumer
	local   http.Handler
}

func newCookieRig(t *testing.T) *cookieRig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.Open(ctx, st, "k", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	res := &fakeResumer{}
	d := Deps{
		Store: st, Auth: svc, Clock: clk, Version: "test",
		Log:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Cookies: cookies.New(st, keys, dir), Resumer: res,
	}
	return &cookieRig{testServer: &testServer{h: New(d), clk: clk}, st: st, resumer: res, local: NewLocal(d)}
}

func (r *cookieRig) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	session, csrf := r.login(t)
	return r.do("POST", "/api/cookies", body, withCookies(session, csrf, csrf))
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body = %s", rec.Body)
	}
	return e.Code
}

func TestPostCookiesStoresAndResumes(t *testing.T) {
	r := newCookieRig(t)
	rec := r.post(t, `{"platform":"youtube","content":"SID=top-secret-sid; HSID=top-secret-hsid"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out struct {
		Platform  string    `json:"platform"`
		Cookies   int       `json:"cookies"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Platform != "youtube" || out.Cookies != 2 || !out.UpdatedAt.Equal(r.clk.Now()) {
		t.Errorf("回應 = %+v", out)
	}
	if strings.Contains(rec.Body.String(), "top-secret") {
		t.Errorf("回應含 cookie 的值：%s", rec.Body)
	}
	if got := r.resumer.tokens; len(got) != 1 || got[0] != "cookie:youtube" {
		t.Errorf("Resumer 收到 %v，預期 [cookie:youtube]", got)
	}
	if _, err := r.st.Read().Secret(context.Background(), "cookie", "youtube"); err != nil {
		t.Errorf("cookie 應已存入：%v", err)
	}
}

func TestPostCookiesBilibili(t *testing.T) {
	r := newCookieRig(t)
	rec := r.post(t, `{"platform":"bilibili","content":"SESSDATA=s; bili_jct=j"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if got := r.resumer.tokens; len(got) != 1 || got[0] != "cookie:bilibili" {
		t.Errorf("Resumer 收到 %v", got)
	}
}

func TestPostCookiesValidationErrors(t *testing.T) {
	cases := []struct {
		name, body, code string
	}{
		{"平台不認識", `{"platform":"twitter","content":"a=secret-v"}`, CodeCookieUnsupportedPlatform},
		{"空內容", `{"platform":"youtube","content":""}`, CodeCookieEmpty},
		{"空白內容", `{"platform":"youtube","content":"  \n"}`, CodeCookieEmpty},
		{"超過 64 KiB", `{"platform":"youtube","content":"a=` + strings.Repeat("x", cookies.MaxContentSize) + `"}`, CodeCookieTooLarge},
		{"沒有剩下 cookie", "{\"platform\":\"youtube\",\"content\":\".google.com\\tTRUE\\t/\\tTRUE\\t0\\tX\\tsecret-v\\n\"}", CodeCookieNoMatch},
		{"bilibili 缺 SESSDATA", `{"platform":"bilibili","content":"bili_jct=secret-v"}`, CodeCookieMissingSessdata},
		{"不合法字元", `{"platform":"youtube","content":"a=b\u0001secret-v"}`, CodeCookieInvalidFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newCookieRig(t)
			rec := r.post(t, c.body)
			if rec.Code != 422 {
				t.Fatalf("status = %d, body = %.200s", rec.Code, rec.Body)
			}
			if got := errCode(t, rec); got != c.code {
				t.Errorf("code = %q，預期 %q", got, c.code)
			}
			if strings.Contains(rec.Body.String(), "secret-v") {
				t.Errorf("錯誤回應含 cookie 的值：%s", rec.Body)
			}
			if len(r.resumer.tokens) != 0 {
				t.Errorf("驗證失敗不應呼叫 Resumer：%v", r.resumer.tokens)
			}
			if list, _ := r.st.Read().ListSecrets(context.Background()); len(list) != 0 {
				t.Errorf("驗證失敗不應存入：%+v", list)
			}
		})
	}
}

func TestPostCookiesMissingFields(t *testing.T) {
	r := newCookieRig(t)
	for _, body := range []string{`{}`, `{"platform":"youtube"}`, `not json`} {
		rec := r.post(t, body)
		if rec.Code != 422 && rec.Code != 400 {
			t.Errorf("body %q：status = %d", body, rec.Code)
		}
	}
}

func TestPostCookiesAuth(t *testing.T) {
	r := newCookieRig(t)
	body := `{"platform":"youtube","content":"a=b"}`

	rec := r.do("POST", "/api/cookies", body, nil)
	if rec.Code != 401 {
		t.Errorf("沒登入：status = %d，預期 401", rec.Code)
	}
	session, csrf := r.login(t)
	rec = r.do("POST", "/api/cookies", body, withCookies(session, csrf, ""))
	if rec.Code != 403 || errCode(t, rec) != CodeCSRF {
		t.Errorf("缺 CSRF：status = %d，body = %s", rec.Code, rec.Body)
	}
	if len(r.resumer.tokens) != 0 {
		t.Errorf("未通過驗證不應呼叫 Resumer")
	}
}

// 程序內的 handler（standalone、本機 socket）不需要 session 與 CSRF，也要能上傳。
func TestPostCookiesLocalHandler(t *testing.T) {
	r := newCookieRig(t)
	req := httptest.NewRequest("POST", "/api/cookies", strings.NewReader(`{"platform":"youtube","content":"a=b"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.local.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if got := r.resumer.tokens; len(got) != 1 || got[0] != "cookie:youtube" {
		t.Errorf("Resumer 收到 %v", got)
	}
}

// 恢復失敗不影響 cookie 已經儲存的事實：回 200，錯誤只記 log。
func TestPostCookiesResumeFailureStillSucceeds(t *testing.T) {
	r := newCookieRig(t)
	r.resumer.err = errors.New("db busy")
	rec := r.post(t, `{"platform":"youtube","content":"a=b"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestPostCookiesNotConfigured(t *testing.T) {
	s := newTestServer(t) // 沒有 Cookies
	session, csrf := s.login(t)
	rec := s.do("POST", "/api/cookies", `{"platform":"youtube","content":"a=b"}`, withCookies(session, csrf, csrf))
	if rec.Code != 501 {
		t.Errorf("status = %d，預期 501", rec.Code)
	}
}
