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
	"github.com/cxyfer/notesteme/internal/store"
)

type tokenRig struct {
	*testServer
	auth   *auth.Service
	logBuf *bytes.Buffer
	sess   string
	csrf   string
}

func newTokenRig(t *testing.T) *tokenRig {
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
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, nil))
	h := New(Deps{Store: st, Auth: svc, Clock: clk, Log: log, Version: "test"})
	ts := &testServer{h: h, clk: clk}
	sess, csrf := ts.login(t)
	return &tokenRig{testServer: ts, auth: svc, logBuf: &logBuf, sess: sess, csrf: csrf}
}

func (r *tokenRig) owner(method, path, body string) *httptest.ResponseRecorder {
	return r.do(method, path, body, withCookies(r.sess, r.csrf, r.csrf))
}

func withBearer(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func TestTokensLifecycleAPI(t *testing.T) {
	r := newTokenRig(t)

	// 初始清單為空陣列，不是 null。
	rec := r.owner("GET", "/api/tokens", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"tokens":[]}` {
		t.Fatalf("GET /api/tokens = %d %s", rec.Code, rec.Body)
	}

	// 建立有期限的 token（支援 d 後綴）。
	rec = r.owner("POST", "/api/tokens", `{"name":"ci","scopes":["read","ingest"],"expires_in":"30d"}`)
	if rec.Code != 201 {
		t.Fatalf("POST /api/tokens = %d %s", rec.Code, rec.Body)
	}
	var created auth.CreatedToken
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "ci" || !strings.HasPrefix(created.Token, auth.TokenPrefix) ||
		created.Prefix != created.Token[:8] || created.ExpiresAt == nil || !created.ExpiresAt.Equal(r.clk.Now().Add(30*24*time.Hour)) {
		t.Fatalf("created = %+v", created)
	}
	if strings.Contains(r.logBuf.String(), created.Token) {
		t.Errorf("log 不得含明文 token：%s", r.logBuf.String())
	}

	// 列表不含明文 token 或 token_hash。
	rec = r.owner("GET", "/api/tokens", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), created.Token) || strings.Contains(rec.Body.String(), "token_hash") {
		t.Fatalf("GET /api/tokens = %d %s", rec.Code, rec.Body)
	}

	// 同名有效的 token 回 409 token_exists。
	rec = r.owner("POST", "/api/tokens", `{"name":"ci","scopes":["read"]}`)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != 409 || e.Code != CodeTokenExists {
		t.Fatalf("duplicate name = %d %s", rec.Code, rec.Body)
	}

	// 建立的 token 可以用來呼叫 read 路由，不需要 CSRF。
	if rec := r.do("GET", "/api/items", "", withBearer(created.Token)); rec.Code != 200 {
		t.Errorf("bearer GET /api/items = %d %s", rec.Code, rec.Body)
	}

	// 依名稱撤銷 token 後立即失效（401）。
	rec = r.owner("DELETE", "/api/tokens/ci", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"revoked":true}` {
		t.Fatalf("DELETE /api/tokens/ci = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("GET", "/api/items", "", withBearer(created.Token)); rec.Code != 401 {
		t.Errorf("revoked token GET = %d, want 401", rec.Code)
	}

	// 已撤銷的名稱可以重複建立，並可依 ID 撤銷。
	rec = r.owner("POST", "/api/tokens", `{"name":"ci","scopes":["read"]}`)
	if rec.Code != 201 {
		t.Fatalf("recreate after revoke = %d %s", rec.Code, rec.Body)
	}
	var recreated auth.CreatedToken
	_ = json.Unmarshal(rec.Body.Bytes(), &recreated)
	if rec := r.owner("DELETE", "/api/tokens/"+recreated.ID, ""); rec.Code != 200 {
		t.Fatalf("DELETE by ID = %d %s", rec.Code, rec.Body)
	}

	// 找不到或已撤銷的 token 回 404。
	if rec := r.owner("DELETE", "/api/tokens/missing", ""); rec.Code != 404 || decodeError(t, rec.Body.Bytes()).Code != CodeNotFound {
		t.Errorf("DELETE missing = %d %s", rec.Code, rec.Body)
	}
}

func TestTokensValidationErrors(t *testing.T) {
	r := newTokenRig(t)
	badBodies := []string{
		`{"name":"","scopes":["read"]}`,
		`{"name":"bad\nname","scopes":["read"]}`,
		`{"name":"bad/name","scopes":["read"]}`,
		`{"name":"a","scopes":[]}`,
		`{"name":"a","scopes":["superadmin"]}`,
		`{"name":"a","scopes":["read"],"expires_in":"0d"}`,
		`{"name":"a","scopes":["read"],"expires_in":"-5h"}`,
		`{"name":"a","scopes":["read"],"expires_in":"+24h"}`,
		`{"name":"a","scopes":["read"],"expires_in":"1.5d"}`,
		`{"name":"a","scopes":["read"],"expires_in":"1d+2h"}`,
		`{"name":"a","scopes":["read"],"expires_in":"nope"}`,
		`{"name":"a","scopes":["read"],"expires_in":" 1h"}`,
		`{"name":"a","scopes":["read"],"extra":1}`,
	}
	for _, b := range badBodies {
		rec := r.owner("POST", "/api/tokens", b)
		if rec.Code != 422 || decodeError(t, rec.Body.Bytes()).Code != CodeInvalidInput {
			t.Errorf("body %s = %d %s, want 422", b, rec.Code, rec.Body)
		}
	}
}

func TestExpiredTokenReturns401(t *testing.T) {
	r := newTokenRig(t)
	ct, err := r.auth.CreateToken(context.Background(), "temp", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := r.do("GET", "/api/items", "", withBearer(ct.Token)); rec.Code != 200 {
		t.Fatalf("before expire = %d", rec.Code)
	}
	if err := r.auth.ExpireTokenByName(context.Background(), "temp"); err != nil {
		t.Fatal(err)
	}
	if rec := r.do("GET", "/api/items", "", withBearer(ct.Token)); rec.Code != 401 {
		t.Errorf("after expire = %d, want 401", rec.Code)
	}
}

// 確認 Auth 檢查先於一切（不依賴 io.Discard 的靜默 logger 以外的狀態）。
func TestTokensRequireAdminScope(t *testing.T) {
	r := newTokenRig(t)
	readTok, err := r.auth.CreateToken(context.Background(), "reader", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := r.auth.CreateToken(context.Background(), "adm", []string{auth.ScopeAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/tokens", ""},
		{"POST", "/api/tokens", `{"name":"x","scopes":["read"]}`},
		{"DELETE", "/api/tokens/x", ""},
	} {
		rec := r.do(c.method, c.path, c.body, withBearer(readTok.Token))
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != 403 || e.Code != CodeForbidden || e.RequiredScope != auth.ScopeAdmin {
			t.Errorf("read token %s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if rec := r.do("GET", "/api/tokens", "", withBearer(adminTok.Token)); rec.Code != 200 {
		t.Errorf("admin token GET /api/tokens = %d %s", rec.Code, rec.Body)
	}
}
