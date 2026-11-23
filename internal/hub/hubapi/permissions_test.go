package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/websession"
)

// permEnv 是有兩個帳號（alice、bob）的 Hub；群組權限同內建預設。
type permEnv struct {
	t     *testing.T
	h     http.Handler
	store hubstore.Store
	clk   *clock.Fake
	cfg   *hubconfig.Config
	dir   string
	users map[string]string // 名稱 → 帳號 ID
	toks  map[string]string // 名稱 → session token
}

func newPermEnv(t *testing.T, mod func(*hubconfig.Config)) *permEnv {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 23, 9, 0, 30, 0, time.UTC))
	dir := t.TempDir()
	st, err := sqlite.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &hubconfig.Config{
		PublicURL: "https://hub.example.org", SiteVisibility: hubconfig.VisibilityAuthenticated, SessionKey: strings.Repeat("k", 32),
		OAuth: map[hubconfig.Provider]hubconfig.OAuthClient{hubconfig.GitHub: {ClientID: "id", ClientSecret: "secret"}},
		Groups: map[string]hubconfig.Group{
			hubconfig.GroupMembers:      {Permissions: []string{"read", "download"}},
			hubconfig.GroupContributors: {Permissions: []string{"read", "download", "regenerate", "upload"}},
			hubconfig.GroupModerators:   {Permissions: []string{"read", "download", "moderate"}},
			hubconfig.GroupAdmins:       {Permissions: []string{"admin"}},
		},
		RateLimits: hubconfig.RateLimits{RequestsPerMinute: 600, AnonymousRequestsPerMinute: 60},
	}
	if mod != nil {
		mod(cfg)
	}
	auth, err := hubauth.New(hubauth.Deps{Config: cfg, Store: st, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	e := &permEnv{t: t, store: st, clk: clk, cfg: cfg, dir: dir, users: map[string]string{}, toks: map[string]string{}}
	for i, name := range []string{"alice", "bob"} {
		u, _, err := st.SignIn(context.Background(), hubstore.ExternalIdentity{Provider: "github", Subject: string(rune('1' + i)), Login: name}, name)
		if err != nil {
			t.Fatal(err)
		}
		tok, _ := websession.RandomToken(32)
		if err := st.CreateSession(context.Background(), hubstore.Session{
			ID: "s-" + name, UserID: u.ID, TokenHash: websession.HashToken(tok), CreatedAt: clk.Now(), AuthenticatedAt: clk.Now(), ExpiresAt: clk.Now().Add(1000 * 24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		e.users[name], e.toks[name] = u.ID, tok
	}
	e.h = New(Deps{Config: cfg, Store: st, Clock: clk, Auth: auth})
	return e
}

// as 送出 session 請求（非 GET 帶 CSRF）。
func (e *permEnv) as(who, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	tok := e.toks[who]
	r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: tok})
	r.AddCookie(&http.Cookie{Name: hubauth.CSRFCookie, Value: websession.CSRFFor(tok)})
	r.Header.Set(hubauth.CSRFHeader, websession.CSRFFor(tok))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

// bearer 送出 Hub token 請求；cookie 不為空時另外帶 session cookie（驗證 bearer 優先）。
func (e *permEnv) bearer(token, method, path, body, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *permEnv) grant(who, group string) {
	e.t.Helper()
	if _, err := e.store.SetManualGroup(context.Background(), e.users[who], e.users[who], group, true); err != nil {
		e.t.Fatal(err)
	}
}

func (e *permEnv) revokeGroup(who, group string) {
	e.t.Helper()
	if _, err := e.store.SetManualGroup(context.Background(), e.users[who], e.users[who], group, false); err != nil {
		e.t.Fatal(err)
	}
}

// createToken 建立 token 並回傳 id 與明文；失敗時測試失敗。
func (e *permEnv) createToken(who, body string) (string, string) {
	e.t.Helper()
	rec := e.as(who, "POST", "/api/v1/me/tokens", body)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("建立 token = %d %s", rec.Code, rec.Body)
	}
	m := decode(e.t, rec)
	return m["id"].(string), m["token"].(string)
}

func TestTokenCreationRules(t *testing.T) {
	e := newPermEnv(t, nil)
	id, tok := e.createToken("alice", `{"name":"reader","permissions":["read"]}`)
	if !hubauth.ValidPATFormat(tok) || len(tok) != 44 || !strings.HasPrefix(tok, "nsh_") {
		t.Fatalf("token 格式 = %q", tok)
	}
	// DB 只存 hash。
	if _, err := e.store.PATByHash(context.Background(), tok); err == nil {
		t.Error("token 明文不應該能直接查到")
	}
	pat, err := e.store.PATByHash(context.Background(), websession.HashToken(tok))
	if err != nil || pat.ID != id || !pat.ExpiresAt.Equal(e.clk.Now().Add(90*24*time.Hour)) {
		t.Fatalf("預設 90 天：%+v %v", pat, err)
	}
	// 列表不含 token 本身。
	rec := e.as("alice", "GET", "/api/v1/me/tokens", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), tok) || !strings.Contains(rec.Body.String(), `"reader"`) {
		t.Fatalf("列表 = %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		name, body, code, path string
	}{
		{"沒有的權限", `{"name":"x","permissions":["read","upload"]}`, "permission_not_held", "permissions.1"},
		{"admin 是提升權限", `{"name":"x","permissions":["admin"]}`, "permission_not_held", "permissions.0"},
		{"不認得的權限", `{"name":"x","permissions":["root"]}`, "invalid_request", "permissions.0"},
		{"沒有權限", `{"name":"x","permissions":[]}`, "invalid_request", "permissions"},
		{"沒有名稱", `{"permissions":["read"]}`, "invalid_request", "name"},
		{"名稱只有控制字元", `{"name":"\u0007","permissions":["read"]}`, "invalid_request", "name"},
		{"超過 365 天", `{"name":"x","permissions":["read"],"expires_in_days":366}`, "invalid_request", "expires_in_days"},
		{"0 天", `{"name":"x","permissions":["read"],"expires_in_days":0}`, "invalid_request", "expires_in_days"},
		{"負數", `{"name":"x","permissions":["read"],"expires_in_days":-1}`, "invalid_request", "expires_in_days"},
	}
	for _, c := range cases {
		rec := e.as("alice", "POST", "/api/v1/me/tokens", c.body)
		m := decode(t, rec)
		errs, _ := m["errors"].([]any)
		if rec.Code != 422 || m["code"] != c.code || len(errs) == 0 || errs[0].(map[string]any)["path"] != c.path {
			t.Errorf("%s = %d %s，預期 422 %s %s", c.name, rec.Code, rec.Body, c.code, c.path)
		}
	}
	// 365 天是上限內；重複的權限合併。
	_, tok2 := e.createToken("alice", `{"name":"y","permissions":["download","read","read"],"expires_in_days":365}`)
	pat2, _ := e.store.PATByHash(context.Background(), websession.HashToken(tok2))
	if strings.Join(pat2.Permissions, ",") != "read,download" {
		t.Errorf("權限 = %v", pat2.Permissions)
	}
}

func TestTokenAuthentication(t *testing.T) {
	e := newPermEnv(t, nil)
	_, tok := e.createToken("alice", `{"name":"reader","permissions":["read"]}`)

	rec := e.bearer(tok, "GET", "/api/v1/me", "", "")
	if rec.Code != 200 {
		t.Fatalf("/me = %d %s", rec.Code, rec.Body)
	}
	if m := decode(t, rec); strings.Join(toStrings(m["permissions"]), ",") != "read" {
		t.Errorf("PAT 的 /me permissions = %v，預期只有 read", m["permissions"])
	}
	// 權限：read 有、download 沒有（帳號有也不行）。
	expect(t, "read", e.bearer(tok, "GET", "/api/v1/versions", "", ""), 200, "")
	rec = e.bearer(tok, "GET", "/api/v1/versions/v1/download", "", "")
	expect(t, "download", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "download" {
		t.Errorf("required_permission = %v", decode(t, rec)["required_permission"])
	}
	// PAT 不能用在只接受 session 的路由（不能建立 token、連結登入方式）；PAT 不需要 CSRF。
	expect(t, "PAT 建立 token", e.bearer(tok, "POST", "/api/v1/me/tokens", `{"name":"x","permissions":["read"]}`, ""), 403, "session_required")
	expect(t, "PAT 列出 token", e.bearer(tok, "GET", "/api/v1/me/tokens", "", ""), 403, "session_required")
	expect(t, "PAT 連結", e.bearer(tok, "POST", "/api/v1/me/identities/google/link", "", ""), 403, "session_required")
	expect(t, "PAT 登出", e.bearer(tok, "POST", "/api/v1/auth/logout", "", ""), 403, "session_required")

	// bearer 優先：壞掉的 bearer 加上有效的 session cookie 仍然是 401。
	expect(t, "壞的 bearer + session", e.bearer("nsh_"+strings.Repeat("A", 40), "GET", "/api/v1/me", "", e.toks["alice"]), 401, "unauthorized")
	// 竄改：改一個字元、加長、換前綴、不是 Bearer。
	expect(t, "改一個字元", e.bearer(tok[:len(tok)-1]+flip(tok[len(tok)-1]), "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	expect(t, "加長", e.bearer(tok+"A", "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	expect(t, "Client 的前綴", e.bearer("nst_"+tok[4:], "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Basic "+tok)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	expect(t, "Basic", rec, 401, "unauthorized")
	// scheme 不分大小寫。
	r = httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "bearer "+tok)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	expect(t, "bearer 小寫", rec, 200, "")
}

func flip(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestTokenExpiryAndRevocation(t *testing.T) {
	e := newPermEnv(t, nil)
	_, tok := e.createToken("alice", `{"name":"day","permissions":["read"],"expires_in_days":1}`)
	e.clk.Advance(24*time.Hour - time.Millisecond)
	expect(t, "到期前", e.bearer(tok, "GET", "/api/v1/me", "", ""), 200, "")
	e.clk.Advance(time.Millisecond)
	expect(t, "到期的那一刻", e.bearer(tok, "GET", "/api/v1/me", "", ""), 401, "unauthorized")

	id, tok2 := e.createToken("alice", `{"name":"r","permissions":["read"]}`)
	// bob 不能撤銷 alice 的 token（看起來和不存在一樣）。
	expect(t, "撤銷別人的 token", e.as("bob", "DELETE", "/api/v1/me/tokens/"+id, ""), 404, "not_found")
	expect(t, "別人撤銷之後仍可用", e.bearer(tok2, "GET", "/api/v1/me", "", ""), 200, "")
	expect(t, "撤銷不存在的 token", e.as("alice", "DELETE", "/api/v1/me/tokens/nope", ""), 404, "not_found")
	expect(t, "撤銷", e.as("alice", "DELETE", "/api/v1/me/tokens/"+id, ""), 204, "")
	expect(t, "撤銷後", e.bearer(tok2, "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	expect(t, "再撤銷一次", e.as("alice", "DELETE", "/api/v1/me/tokens/"+id, ""), 204, "")
	if rec := e.as("alice", "GET", "/api/v1/me/tokens", ""); strings.Contains(rec.Body.String(), id) {
		t.Errorf("撤銷的 token 不應該列出：%s", rec.Body)
	}
}

func TestTokenFollowsAccountPermissions(t *testing.T) {
	e := newPermEnv(t, nil)
	e.grant("alice", hubconfig.GroupContributors)
	_, tok := e.createToken("alice", `{"name":"up","permissions":["upload"]}`)
	// upload 有，但 read 沒有（PAT 只有 upload）。
	expect(t, "upload", e.bearer(tok, "GET", "/api/v1/me/shares", "", ""), 200, "")
	expect(t, "PAT 沒有 read", e.bearer(tok, "GET", "/api/v1/versions", "", ""), 403, "forbidden")
	e.revokeGroup("alice", hubconfig.GroupContributors)
	rec := e.bearer(tok, "GET", "/api/v1/me/shares", "", "")
	expect(t, "帳號失去 upload 之後", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "upload" {
		t.Errorf("required_permission = %v", decode(t, rec)["required_permission"])
	}
	// 帳號停用：PAT 與 session 都是 403 account_suspended。
	if err := setSuspended(e, "alice"); err != nil {
		t.Fatal(err)
	}
	expect(t, "停用帳號的 PAT", e.bearer(tok, "GET", "/api/v1/me", "", ""), 403, "account_suspended")
	expect(t, "停用帳號的 session", e.as("alice", "GET", "/api/v1/me", ""), 403, "account_suspended")
}

// setSuspended 直接改 DB（只測 middleware；停用的路由見 moderation_test.go）。
func setSuspended(e *permEnv, who string) error {
	db, err := sqlitedb.Open(filepath.Join(e.dir, sqlite.DBFileName), false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`UPDATE users SET suspended_at = 1 WHERE id = ?`, e.users[who])
	return err
}

func TestAdminTokenHasEverything(t *testing.T) {
	e := newPermEnv(t, nil)
	if err := e.store.SetConfigGroup(context.Background(), e.users["alice"], hubconfig.GroupAdmins, true); err != nil {
		t.Fatal(err)
	}
	_, tok := e.createToken("alice", `{"name":"root","permissions":["admin"]}`)
	expect(t, "admin 的 PAT 可以 moderate", e.bearer(tok, "GET", "/api/v1/admin/takedowns", "", ""), 200, "")
	expect(t, "admin 的 PAT 可以看稽核", e.bearer(tok, "GET", "/api/v1/admin/audit", "", ""), 200, "")
	// read 的 PAT 不能做 admin 的事，即使帳號是 admin。
	_, rtok := e.createToken("alice", `{"name":"r","permissions":["read"]}`)
	expect(t, "read 的 PAT", e.bearer(rtok, "GET", "/api/v1/admin/audit", "", ""), 403, "forbidden")
}

func TestAdminGroupRoutes(t *testing.T) {
	e := newPermEnv(t, nil)
	bob := e.users["bob"]
	rec := e.as("bob", "PUT", "/api/v1/admin/users/"+bob+"/groups/moderators", "")
	expect(t, "非管理員指派", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "admin" {
		t.Errorf("required_permission = %v", decode(t, rec)["required_permission"])
	}
	if err := e.store.SetConfigGroup(context.Background(), e.users["alice"], hubconfig.GroupAdmins, true); err != nil {
		t.Fatal(err)
	}
	rec = e.as("alice", "PUT", "/api/v1/admin/users/"+bob+"/groups/admins", "")
	expect(t, "指派 admins", rec, 403, "admin_by_config")
	expect(t, "移除 admins", e.as("alice", "DELETE", "/api/v1/admin/users/"+e.users["alice"]+"/groups/admins", ""), 403, "admin_by_config")
	expect(t, "members", e.as("alice", "PUT", "/api/v1/admin/users/"+bob+"/groups/members", ""), 422, "invalid_request")
	expect(t, "不存在的群組", e.as("alice", "PUT", "/api/v1/admin/users/"+bob+"/groups/nope", ""), 404, "not_found")
	expect(t, "不存在的使用者", e.as("alice", "PUT", "/api/v1/admin/users/nope/groups/moderators", ""), 404, "not_found")
	expect(t, "指派 moderators", e.as("alice", "PUT", "/api/v1/admin/users/"+bob+"/groups/moderators", ""), 204, "")
	expect(t, "再指派一次", e.as("alice", "PUT", "/api/v1/admin/users/"+bob+"/groups/moderators", ""), 204, "")
	expect(t, "bob 可以 moderate", e.as("bob", "GET", "/api/v1/admin/takedowns", ""), 200, "")
	expect(t, "移除", e.as("alice", "DELETE", "/api/v1/admin/users/"+bob+"/groups/moderators", ""), 204, "")
	expect(t, "移除後", e.as("bob", "GET", "/api/v1/admin/takedowns", ""), 403, "forbidden")

	rec = e.as("alice", "GET", "/api/v1/admin/audit", "")
	var out struct {
		Entries []AuditView `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
		t.Fatalf("audit = %d %s", rec.Code, rec.Body)
	}
	// 依時間倒序；重複的指派不寫稽核紀錄。
	if len(out.Entries) != 2 || out.Entries[0].Action != "group.remove" || out.Entries[1].Action != "group.add" ||
		out.Entries[0].Actor != "alice" || out.Entries[0].TargetKind != "user" || out.Entries[0].TargetID != bob {
		t.Errorf("audit = %+v", out.Entries)
	}
}

func TestRateLimits(t *testing.T) {
	e := newPermEnv(t, func(c *hubconfig.Config) {
		c.RateLimits = hubconfig.RateLimits{RequestsPerMinute: 3, AnonymousRequestsPerMinute: 2}
	})
	// 時鐘在 09:00:30：視窗到 09:01:00。
	_, tok := e.createToken("alice", `{"name":"r","permissions":["read"]}`) // 第 1 個
	expect(t, "session 第 2 個", e.as("alice", "GET", "/api/v1/me", ""), 200, "")
	expect(t, "PAT 與 session 合計第 3 個", e.bearer(tok, "GET", "/api/v1/me", "", ""), 200, "")
	rec := e.bearer(tok, "GET", "/api/v1/me", "", "")
	expect(t, "第 4 個", rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") != "30" || decode(t, rec)["retry_after_s"] != float64(30) ||
		decode(t, rec)["message"] != "請求太頻繁，請在 30 秒後再試。" {
		t.Errorf("429 = %v %s", rec.Header(), rec.Body)
	}
	// 另一個帳號不受影響。
	expect(t, "bob", e.as("bob", "GET", "/api/v1/me", ""), 200, "")
	// 未登入以 IP 計：無效的 token 也算。
	expect(t, "匿名 1", e.bearer("nsh_bad", "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	expect(t, "匿名 2（公開路由）", e.do("GET", "/api/v1/auth/providers"), 200, "")
	expect(t, "匿名 3", e.bearer("nsh_bad", "GET", "/api/v1/me", "", ""), 429, "rate_limited")
	expect(t, "瀏覽器流程也限流", e.do("GET", "/auth/github/start"), 429, "rate_limited")
	expect(t, "健康檢查不限流", e.do("GET", "/healthz"), 200, "")
	// 跨過視窗後恢復。
	e.clk.Advance(29*time.Second + 999*time.Millisecond)
	expect(t, "視窗結束前", e.bearer(tok, "GET", "/api/v1/me", "", ""), 429, "rate_limited")
	e.clk.Advance(time.Millisecond)
	expect(t, "新的視窗", e.bearer(tok, "GET", "/api/v1/me", "", ""), 200, "")
	expect(t, "新的視窗（匿名）", e.do("GET", "/auth/github/start"), 302, "")
}

func (e *permEnv) do(method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}
