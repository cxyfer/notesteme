package hubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/websession"
)

// 投票、檢舉、下架申請、版主處理與停用（M5-S5）的單元測試：重點在越權、重複的操作、對隱藏與已移除的版本的操作，
// 以及不洩漏版本是否存在。

const testHubMasterKey = "unit-test-hub-master-key-0123456789"

// newCommunityEnv 是有 alice（contributors）、bob、carol、mod（moderators）與 root（admins）的 Hub。
func newCommunityEnv(t *testing.T, mod func(*hubconfig.Config)) *repoEnv {
	t.Helper()
	e := newRepoEnv(t, func(c *hubconfig.Config) {
		c.MasterKey = testHubMasterKey
		c.Moderation = hubconfig.Moderation{AutoHideReports: 2, AutoHideMinAccountAge: 7 * 24 * time.Hour}
		c.RateLimits.VotesPerHour = 60
		c.RateLimits.ReportsPerDay = 10
		c.RateLimits.TakedownsPerHour = 5
		if mod != nil {
			mod(c)
		}
	})
	for _, name := range []string{"carol", "mod", "root"} {
		e.addUser(name)
	}
	e.grant("mod", hubconfig.GroupModerators)
	if err := e.store.SetConfigGroup(context.Background(), e.users["root"], hubconfig.GroupAdmins, true); err != nil {
		t.Fatal(err)
	}
	return e
}

// addUser 建立帳號與 session。
func (e *permEnv) addUser(name string) {
	e.t.Helper()
	ctx := context.Background()
	u, _, err := e.store.SignIn(ctx, hubstore.ExternalIdentity{Provider: "github", Subject: "9" + name, Login: name}, name)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, _ := websession.RandomToken(32)
	if err := e.store.CreateSession(ctx, hubstore.Session{ID: "s-" + name, UserID: u.ID, TokenHash: websession.HashToken(tok),
		CreatedAt: e.clk.Now(), AuthenticatedAt: e.clk.Now(), ExpiresAt: e.clk.Now().Add(1000 * 24 * time.Hour)}); err != nil {
		e.t.Fatal(err)
	}
	e.users[name], e.toks[name] = u.ID, tok
}

// anon 送出不帶任何身分的請求。
func (e *permEnv) anon(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func scoreOf(t *testing.T, e *repoEnv, id string) int {
	t.Helper()
	v, err := e.store.Version(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v.Score
}

func paths(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var out struct {
		Errors []struct {
			Path string `json:"path"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	var ps []string
	for _, x := range out.Errors {
		ps = append(ps, x.Path)
	}
	return ps
}

func TestVoteRules(t *testing.T) {
	e := newCommunityEnv(t, func(c *hubconfig.Config) { c.RateLimits.VotesPerHour = 4 })
	v := e.seed("alice", seedOpt{})
	url := "/api/v1/versions/" + v.ID + "/vote"
	expect(t, "新帳號", e.as("bob", "PUT", url, `{"value":1}`), 403, codeAccountTooNew)
	e.clk.Advance(25 * time.Hour)
	expect(t, "自己的版本", e.as("alice", "PUT", url, `{"value":1}`), 403, codeOwnVersion)
	for _, b := range []string{`{"value":2}`, `{"value":0}`, `{}`} {
		rec := e.as("bob", "PUT", url, b)
		expect(t, "value "+b, rec, 422, "invalid_request")
		if p := paths(t, rec); len(p) != 1 || p[0] != "value" {
			t.Errorf("%s 的 errors = %v", b, p)
		}
	}
	rec := e.as("bob", "PUT", url, `{"value":1}`)
	if rec.Code != 200 || decode(t, rec)["score"] != float64(1) {
		t.Fatalf("投票 = %d %s", rec.Code, rec.Body)
	}
	if rec := e.as("bob", "PUT", url, `{"value":-1}`); decode(t, rec)["score"] != float64(-1) {
		t.Errorf("改票 = %s", rec.Body)
	}
	expect(t, "收回", e.as("bob", "DELETE", url, ""), 204, "")
	if s := scoreOf(t, e, v.ID); s != 0 {
		t.Errorf("收回後分數 = %d", s)
	}
	// 每小時的次數（含收回）：第 5 次 429。
	expect(t, "第 4 次", e.as("bob", "DELETE", url, ""), 204, "")
	rec = e.as("bob", "PUT", url, `{"value":1}`)
	expect(t, "超過每小時的次數", rec, 429, CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" || decode(t, rec)["retry_after_s"] == nil {
		t.Errorf("429 沒有 Retry-After：%v %s", rec.Header(), rec.Body)
	}
	e.clk.Advance(time.Hour)
	expect(t, "一小時後", e.as("bob", "PUT", url, `{"value":1}`), 200, "")
	// PAT 只要 read。
	_, tok := e.createToken("carol", `{"name":"r","permissions":["read"]}`)
	if rec := e.bearer(tok, "PUT", url, `{"value":1}`, ""); rec.Code != 200 || decode(t, rec)["score"] != float64(2) {
		t.Errorf("PAT 投票 = %d %s", rec.Code, rec.Body)
	}
}

func TestVoteAndReportTargets(t *testing.T) {
	e := newCommunityEnv(t, nil)
	e.clk.Advance(25 * time.Hour)
	ctx := context.Background()
	private := e.seed("alice", seedOpt{ext: "priv0000001", vis: "private"})
	missing := "0190f0f0-0000-7000-8000-000000000000"
	for _, c := range []struct{ method, suffix, body, bad string }{
		{"PUT", "/vote", `{"value":1}`, `{"value":9}`}, {"DELETE", "/vote", "", ""}, {"POST", "/reports", `{"reason":"spam"}`, `{"reason":"nope"}`},
	} {
		a := e.as("bob", c.method, "/api/v1/versions/"+private.ID+c.suffix, c.body)
		b := e.as("bob", c.method, "/api/v1/versions/"+missing+c.suffix, c.body)
		expect(t, "看不到的版本 "+c.method+c.suffix, a, 404, "not_found")
		if a.Body.String() != b.Body.String() {
			t.Errorf("看不到與不存在的回應不同：%s / %s", a.Body, b.Body)
		}
		// body 不正確也一樣：先確認版本，不讓 422 洩漏存在性。
		bad := e.as("bob", c.method, "/api/v1/versions/"+private.ID+c.suffix, c.bad)
		expect(t, "看不到的版本加上錯的 body", bad, 404, "not_found")
	}
	hidden := e.seed("alice", seedOpt{ext: "hide0000001"})
	_ = e.store.SetVersionState(ctx, hidden.ID, "hidden")
	expect(t, "hidden 對一般人", e.as("bob", "PUT", "/api/v1/versions/"+hidden.ID+"/vote", `{"value":1}`), 404, "not_found")
	expect(t, "hidden 對版主", e.as("mod", "PUT", "/api/v1/versions/"+hidden.ID+"/vote", `{"value":1}`), 409, codeVersionHidden)
	expect(t, "hidden 的檢舉", e.as("mod", "POST", "/api/v1/versions/"+hidden.ID+"/reports", `{"reason":"spam"}`), 409, codeVersionHidden)
	removed := e.seed("alice", seedOpt{ext: "gone0000001"})
	_ = e.store.SetVersionState(ctx, removed.ID, "removed")
	expect(t, "removed 的投票", e.as("bob", "PUT", "/api/v1/versions/"+removed.ID+"/vote", `{"value":1}`), 410, codeVersionRemoved)
	expect(t, "removed 的檢舉", e.as("bob", "POST", "/api/v1/versions/"+removed.ID+"/reports", `{"reason":"spam"}`), 410, codeVersionRemoved)
	withdrawn := e.seed("alice", seedOpt{ext: "with0000001"})
	_ = e.store.SetVersionState(ctx, withdrawn.ID, "withdrawn")
	expect(t, "withdrawn 的投票", e.as("alice", "PUT", "/api/v1/versions/"+withdrawn.ID+"/vote", `{"value":1}`), 404, "not_found")
	// members 沒有 read 以外的限制，但 PAT 沒有 read 時是 403。
	_, tok := e.createToken("alice", `{"name":"u","permissions":["upload"]}`)
	rec := e.bearer(tok, "PUT", "/api/v1/versions/"+private.ID+"/vote", `{"value":1}`, "")
	expect(t, "PAT 沒有 read", rec, 403, "forbidden")
}

func TestReportRules(t *testing.T) {
	e := newCommunityEnv(t, func(c *hubconfig.Config) { c.RateLimits.ReportsPerDay = 2 })
	v := e.seed("alice", seedOpt{})
	url := "/api/v1/versions/" + v.ID + "/reports"
	expect(t, "新帳號", e.as("bob", "POST", url, `{"reason":"spam"}`), 403, codeAccountTooNew)
	e.clk.Advance(25 * time.Hour)
	for _, r := range []string{"copyright", "privacy"} {
		rec := e.as("bob", "POST", url, `{"reason":"`+r+`"}`)
		expect(t, r, rec, 422, codeUseTakedown)
		if a, _ := decode(t, rec)["actions"].([]any); len(a) != 1 || a[0] != actionTakedown {
			t.Errorf("%s 的 actions = %v", r, decode(t, rec)["actions"])
		}
	}
	rec := e.as("bob", "POST", url, `{"reason":"nope","note":"`+strings.Repeat("x", 1001)+`"}`)
	if p := paths(t, rec); rec.Code != 422 || len(p) != 2 || p[0] != "reason" || p[1] != "note" {
		t.Errorf("不合法的檢舉 = %d %v", rec.Code, p)
	}
	expect(t, "控制字元", e.as("bob", "POST", url, `{"reason":"spam","note":"a\u0007b"}`), 422, "invalid_request")
	rec = e.as("bob", "POST", url, `{"reason":"spam","note":"reported by bob"}`)
	expect(t, "檢舉", rec, 201, "")
	if decode(t, rec)["state"] != "open" || decode(t, rec)["id"] == "" {
		t.Errorf("檢舉的回應 = %s", rec.Body)
	}
	expect(t, "重複檢舉", e.as("bob", "POST", url, `{"reason":"bad_quality"}`), 409, codeAlreadyReported)
	// 上傳者看不到檢舉者。
	if rec := e.as("alice", "GET", "/api/v1/versions/"+v.ID, ""); strings.Contains(rec.Body.String(), "bob") {
		t.Errorf("版本詳情洩漏檢舉者：%s", rec.Body)
	}
	// 每天的次數：bob 已有 1 筆，第 2 筆可以、第 3 筆 429。
	v2, v3 := e.seed("alice", seedOpt{ext: "two00000001"}), e.seed("alice", seedOpt{ext: "thr00000001"})
	expect(t, "第 2 筆", e.as("bob", "POST", "/api/v1/versions/"+v2.ID+"/reports", `{"reason":"spam"}`), 201, "")
	expect(t, "第 3 筆", e.as("bob", "POST", "/api/v1/versions/"+v3.ID+"/reports", `{"reason":"spam"}`), 429, CodeRateLimited)
	// 一般人與 PAT 看不到檢舉列表。
	rec = e.as("bob", "GET", "/api/v1/admin/reports", "")
	expect(t, "一般人看檢舉", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "moderate" {
		t.Errorf("required_permission = %v", decode(t, rec)["required_permission"])
	}
	rec = e.as("mod", "GET", "/api/v1/admin/reports?state=open", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"display_name":"bob"`) {
		t.Errorf("版主看檢舉 = %d %s", rec.Code, rec.Body)
	}
	rec = e.as("mod", "GET", "/api/v1/admin/reports?state=nope&limit=0", "")
	if p := paths(t, rec); rec.Code != 422 || len(p) != 2 || p[0] != "query.state" || p[1] != "query.limit" {
		t.Errorf("列表參數 = %d %v", rec.Code, p)
	}
}

func TestAutoHideAndReportDecisions(t *testing.T) {
	e := newCommunityEnv(t, nil)
	ctx := context.Background()
	e.clk.Advance(8 * 24 * time.Hour)
	v := e.seed("alice", seedOpt{})
	url := "/api/v1/versions/" + v.ID + "/reports"
	expect(t, "bob", e.as("bob", "POST", url, `{"reason":"spam"}`), 201, "")
	rec := e.as("carol", "POST", url, `{"reason":"offensive"}`)
	expect(t, "carol", rec, 201, "")
	last := decode(t, rec)["id"].(string)
	if got, _ := e.store.Version(ctx, v.ID); got.State != "hidden" {
		t.Fatalf("兩個帳號齡足夠的檢舉之後 state = %q", got.State)
	}
	expect(t, "隱藏後一般人", e.as("bob", "GET", "/api/v1/versions/"+v.ID, ""), 404, "not_found")
	if rec := e.as("alice", "GET", "/api/v1/versions/"+v.ID, ""); decode(t, rec)["state"] != "hidden" {
		t.Errorf("上傳者看到 = %s", rec.Body)
	}
	dec := "/api/v1/admin/reports/" + last + "/decision"
	expect(t, "一般人處理", e.as("bob", "POST", dec, `{"decision":"dismiss"}`), 403, "forbidden")
	rec = e.as("mod", "POST", dec, `{"decision":"delete"}`)
	if p := paths(t, rec); rec.Code != 422 || len(p) != 1 || p[0] != "decision" {
		t.Errorf("不認得的決定 = %d %v", rec.Code, p)
	}
	expect(t, "不存在的檢舉", e.as("mod", "POST", "/api/v1/admin/reports/nope/decision", `{"decision":"dismiss"}`), 404, "not_found")
	rec = e.as("mod", "POST", dec, `{"decision":"dismiss","note":"fine"}`)
	if rec.Code != 200 || decode(t, rec)["version_state"] != "visible" || decode(t, rec)["state"] != "dismissed" {
		t.Fatalf("駁回 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "重複處理", e.as("mod", "POST", dec, `{"decision":"remove"}`), 409, codeReportDecided)

	// 依檢舉移除：版本 removed、blob 刪掉、上傳者在我的分享看到原因。
	rec = e.as("bob", "POST", url, `{"reason":"wrong_language"}`)
	id := decode(t, rec)["id"].(string)
	rec = e.as("mod", "POST", "/api/v1/admin/reports/"+id+"/decision", `{"decision":"remove"}`)
	if rec.Code != 200 || decode(t, rec)["version_state"] != "removed" {
		t.Fatalf("移除 = %d %s", rec.Code, rec.Body)
	}
	if ok, _ := e.blobs.Exists(ctx, v.SHA256); ok {
		t.Error("移除後 blob 應刪除")
	}
	expect(t, "移除後", e.as("bob", "GET", "/api/v1/versions/"+v.ID, ""), 410, codeVersionRemoved)
	rec = e.as("alice", "GET", "/api/v1/me/shares", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"removal":{"source":"report","reason":"wrong_language"}`) ||
		!strings.Contains(rec.Body.String(), `"state":"removed"`) {
		t.Errorf("我的分享 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "沒有 upload 的我的分享", e.as("bob", "GET", "/api/v1/me/shares", ""), 403, "forbidden")
	expect(t, "我的分享的 limit", e.as("alice", "GET", "/api/v1/me/shares?limit=abc", ""), 422, "invalid_request")
	audit := e.as("root", "GET", "/api/v1/admin/audit", "")
	if n := strings.Count(audit.Body.String(), `"action":"report.decide"`); n != 2 {
		t.Errorf("report.decide 的稽核紀錄 = %d：%s", n, audit.Body)
	}
}

func TestHideVersion(t *testing.T) {
	e := newCommunityEnv(t, nil)
	v := e.seed("alice", seedOpt{})
	url := "/api/v1/admin/versions/" + v.ID + "/hide"
	expect(t, "一般人", e.as("bob", "POST", url, ""), 403, "forbidden")
	expect(t, "隱藏", e.as("mod", "POST", url, ""), 200, "")
	expect(t, "再隱藏", e.as("mod", "POST", url, ""), 200, "")
	expect(t, "不存在", e.as("mod", "POST", "/api/v1/admin/versions/nope/hide", ""), 404, "not_found")
	expect(t, "隱藏後一般人", e.as("bob", "GET", "/api/v1/versions/"+v.ID, ""), 404, "not_found")
	_ = e.store.SetVersionState(context.Background(), v.ID, "removed")
	expect(t, "隱藏已移除的", e.as("mod", "POST", url, ""), 409, codeVersionState)
	audit := e.as("root", "GET", "/api/v1/admin/audit", "")
	if n := strings.Count(audit.Body.String(), `"action":"version.hide"`); n != 1 {
		t.Errorf("version.hide 的稽核紀錄 = %d：%s", n, audit.Body)
	}
}

func takedownBody(versionID string) string {
	return `{"version_id":"` + versionID + `","reason":"copyright","statement":"I own the copyright of this video.","contact_email":"rights@example.com","good_faith":true}`
}

func TestTakedownRequests(t *testing.T) {
	e := newCommunityEnv(t, func(c *hubconfig.Config) { c.RateLimits.TakedownsPerHour = 2 })
	v := e.seed("alice", seedOpt{})
	rec := e.anon("POST", "/api/v1/takedowns", takedownBody(v.ID))
	expect(t, "不需要登入", rec, 201, "")
	m := decode(t, rec)
	id := m["id"].(string)
	if m["state"] != "open" || m["message"] != "已收到下架申請，編號 "+id+"。管理員處理後會在這裡更新狀態。" {
		t.Errorf("回應 = %s", rec.Body)
	}
	rec = e.anon("GET", "/api/v1/takedowns/"+id, "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"id":"`+id+`","state":"open"}` {
		t.Errorf("查詢狀態 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "不存在的申請", e.anon("GET", "/api/v1/takedowns/nope", ""), 404, "not_found")
	// 所有欄位都錯：依 body 的順序列出。
	rec = e.anon("POST", "/api/v1/takedowns", `{"version_id":"x","reason":"spam","statement":"short","contact_email":"Bob <b@example.com>","good_faith":false}`)
	want := "version_id reason statement contact_email good_faith"
	if p := strings.Join(paths(t, rec), " "); rec.Code != 422 || p != want {
		t.Errorf("驗證 = %d %q", rec.Code, p)
	}
	// 失敗的申請不佔額度；版本不存在也回 201（不洩漏存在性）。
	ghost := "0190f0f0-0000-7000-8000-000000000000"
	expect(t, "不存在的版本", e.anon("POST", "/api/v1/takedowns", takedownBody(ghost)), 201, "")
	ghostID := decode(t, e.as("mod", "GET", "/api/v1/admin/takedowns", ""))["takedowns"].([]any)[0].(map[string]any)["id"].(string)
	rec = e.anon("POST", "/api/v1/takedowns", takedownBody(v.ID))
	expect(t, "超過每小時的次數", rec, 429, CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 沒有 Retry-After")
	}
	e.clk.Advance(time.Hour + time.Second)
	expect(t, "一小時後", e.anon("POST", "/api/v1/takedowns", takedownBody(v.ID)), 201, "")

	// 聯絡 email 只有版主看得到，DB 中是密文。
	expect(t, "一般人看申請", e.as("bob", "GET", "/api/v1/admin/takedowns", ""), 403, "forbidden")
	rec = e.as("mod", "GET", "/api/v1/admin/takedowns?state=open&limit=10", "")
	if rec.Code != 200 || strings.Count(rec.Body.String(), `"contact_email":"rights@example.com"`) != 3 {
		t.Errorf("版主看申請 = %d %s", rec.Code, rec.Body)
	}
	db, err := sqlitedb.Open(filepath.Join(e.dir, sqlite.DBFileName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var raw []byte
	if err := db.QueryRow(`SELECT contact_sealed || dek_sealed FROM takedowns WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("rights@")) {
		t.Error("聯絡 email 沒有加密")
	}
	// 申請 ID 綁定在 AAD 中：把別筆的密文搬過來就解不開（列表回 null，不是 500）。
	if _, err := db.Exec(`UPDATE takedowns SET dek_nonce = (SELECT dek_nonce FROM takedowns WHERE id = ?),
		dek_sealed = (SELECT dek_sealed FROM takedowns WHERE id = ?) WHERE id = ?`, id, id, ghostID); err != nil {
		t.Fatal(err)
	}
	rec = e.as("mod", "GET", "/api/v1/admin/takedowns", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"contact_email":null`) {
		t.Errorf("被竄改的密文 = %d %s", rec.Code, rec.Body)
	}
}

func TestTakedownDecisions(t *testing.T) {
	e := newCommunityEnv(t, nil)
	ctx := context.Background()
	v := e.seed("alice", seedOpt{})
	create := func(versionID string) string {
		rec := e.anon("POST", "/api/v1/takedowns", takedownBody(versionID))
		if rec.Code != 201 {
			t.Fatalf("建立申請 = %d %s", rec.Code, rec.Body)
		}
		return decode(t, rec)["id"].(string)
	}
	a, b := create(v.ID), create(v.ID)
	dec := func(id string) string { return "/api/v1/admin/takedowns/" + id + "/decision" }
	expect(t, "一般人決定", e.as("bob", "POST", dec(a), `{"decision":"remove"}`), 403, "forbidden")
	expect(t, "PAT 沒有 moderate", e.bearer(func() string { _, t := e.createToken("mod", `{"name":"r","permissions":["read"]}`); return t }(),
		"POST", dec(a), `{"decision":"remove"}`, ""), 403, "forbidden")
	expect(t, "不認得的決定", e.as("mod", "POST", dec(a), `{"decision":"dismiss"}`), 422, "invalid_request")
	expect(t, "太長的說明", e.as("mod", "POST", dec(a), `{"decision":"reject","note":"`+strings.Repeat("n", 2001)+`"}`), 422, "invalid_request")
	expect(t, "不存在", e.as("mod", "POST", dec("nope"), `{"decision":"reject"}`), 404, "not_found")
	rec := e.as("mod", "POST", dec(a), `{"decision":"reject","note":"Not a valid claim."}`)
	if rec.Code != 200 || decode(t, rec)["state"] != "rejected" {
		t.Fatalf("駁回 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "駁回後再決定", e.as("mod", "POST", dec(a), `{"decision":"remove"}`), 409, codeTakedownDecided)
	if got, _ := e.store.Version(ctx, v.ID); got.State != "visible" {
		t.Fatalf("駁回後 state = %q", got.State)
	}
	rec = e.as("mod", "POST", dec(b), `{"decision":"remove"}`)
	if rec.Code != 200 || decode(t, rec)["state"] != "removed" {
		t.Fatalf("移除 = %d %s", rec.Code, rec.Body)
	}
	if ok, _ := e.blobs.Exists(ctx, v.SHA256); ok {
		t.Error("移除後 blob 應刪除")
	}
	expect(t, "移除後的版本", e.as("bob", "GET", "/api/v1/versions/"+v.ID, ""), 410, codeVersionRemoved)
	expect(t, "移除後的下載", e.as("bob", "GET", "/api/v1/versions/"+v.ID+"/download", ""), 410, codeVersionRemoved)
	if rec := e.anon("GET", "/api/v1/takedowns/"+b, ""); decode(t, rec)["state"] != "removed" {
		t.Errorf("申請人看到 = %s", rec.Body)
	}
	if rec := e.as("alice", "GET", "/api/v1/me/shares", ""); !strings.Contains(rec.Body.String(), `"removal":{"source":"takedown","reason":"copyright"}`) {
		t.Errorf("我的分享 = %s", rec.Body)
	}
	// 申請的版本不存在：不能移除，只能駁回。
	g := create("0190f0f0-0000-7000-8000-000000000000")
	expect(t, "移除不存在的版本", e.as("mod", "POST", dec(g), `{"decision":"remove"}`), 422, "invalid_request")
	expect(t, "駁回不存在的版本", e.as("mod", "POST", dec(g), `{"decision":"reject"}`), 200, "")
	audit := e.as("root", "GET", "/api/v1/admin/audit", "")
	if n := strings.Count(audit.Body.String(), `"action":"takedown.decide"`); n != 3 {
		t.Errorf("takedown.decide 的稽核紀錄 = %d：%s", n, audit.Body)
	}
}

func TestTakedownWithoutMasterKey(t *testing.T) {
	e := newCommunityEnv(t, func(c *hubconfig.Config) { c.MasterKey = "" })
	v := e.seed("alice", seedOpt{})
	expect(t, "沒有主金鑰", e.anon("POST", "/api/v1/takedowns", takedownBody(v.ID)), 503, "not_configured")
}

func TestSuspendAndUnsuspend(t *testing.T) {
	e := newCommunityEnv(t, nil)
	e.clk.Advance(25 * time.Hour)
	v := e.seed("alice", seedOpt{})
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	expect(t, "carol 投票", e.as("carol", "PUT", "/api/v1/versions/"+v.ID+"/vote", `{"value":1}`), 200, "")
	expect(t, "bob 投票", e.as("bob", "PUT", "/api/v1/versions/"+v.ID+"/vote", `{"value":1}`), 200, "")
	bob := e.users["bob"]
	url := "/api/v1/admin/users/" + bob + "/suspend"
	rec := e.as("mod", "POST", url, `{"reason":"spam"}`)
	expect(t, "版主不能停用", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "admin" {
		t.Errorf("required_permission = %v", decode(t, rec)["required_permission"])
	}
	expect(t, "沒有原因", e.as("root", "POST", url, `{"reason":"  "}`), 422, "invalid_request")
	expect(t, "停用自己", e.as("root", "POST", "/api/v1/admin/users/"+e.users["root"]+"/suspend", `{"reason":"x"}`), 422, "invalid_request")
	expect(t, "不存在的使用者", e.as("root", "POST", "/api/v1/admin/users/nope/suspend", `{"reason":"x"}`), 404, "not_found")
	rec = e.as("root", "POST", url, `{"reason":"spam uploads"}`)
	if rec.Code != 200 || decode(t, rec)["suspended"] != true {
		t.Fatalf("停用 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "再停用一次", e.as("root", "POST", url, `{"reason":"again"}`), 200, "")
	expect(t, "停用後的 session", e.as("bob", "GET", "/api/v1/me", ""), 403, hubauth.CodeSuspended)
	expect(t, "停用後的 PAT", e.bearer(tok, "GET", "/api/v1/me", "", ""), 403, hubauth.CodeSuspended)
	if s := scoreOf(t, e, v.ID); s != 1 {
		t.Errorf("停用後的分數 = %d，預期 1（bob 的票不計入）", s)
	}
	rec = e.as("root", "GET", "/api/v1/admin/users?limit=500", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"suspended_reason":"spam uploads"`) {
		t.Errorf("使用者列表 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "一般人看使用者", e.as("carol", "GET", "/api/v1/admin/users", ""), 403, "forbidden")
	expect(t, "使用者列表的 limit", e.as("root", "GET", "/api/v1/admin/users?limit=501", ""), 422, "invalid_request")
	rec = e.as("root", "POST", "/api/v1/admin/users/"+bob+"/unsuspend", "")
	if rec.Code != 200 || decode(t, rec)["suspended"] != false {
		t.Fatalf("恢復 = %d %s", rec.Code, rec.Body)
	}
	expect(t, "恢復不存在的使用者", e.as("root", "POST", "/api/v1/admin/users/nope/unsuspend", ""), 404, "not_found")
	if s := scoreOf(t, e, v.ID); s != 2 {
		t.Errorf("恢復後的分數 = %d，預期 2", s)
	}
	// 停用前的 session 與 PAT 在恢復後失效，要重新登入。
	expect(t, "恢復後舊的 session", e.as("bob", "GET", "/api/v1/me", ""), 401, "unauthorized")
	expect(t, "恢復後舊的 PAT", e.bearer(tok, "GET", "/api/v1/me", "", ""), 401, "unauthorized")
	audit := e.as("root", "GET", "/api/v1/admin/audit", "")
	var out struct {
		Entries []AuditView `json:"entries"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &out); err != nil || len(out.Entries) < 2 {
		t.Fatalf("audit = %s", audit.Body)
	}
	if got := out.Entries[0]; got.Action != hubstore.AuditUserUnsuspend || got.Actor != "root" || got.TargetKind != "user" || got.TargetID != bob {
		t.Errorf("最新的稽核紀錄 = %+v", got)
	}
	if got := out.Entries[1]; got.Action != hubstore.AuditUserSuspend {
		t.Errorf("停用的稽核紀錄 = %+v", got)
	}
}

func TestHourlyLimiterRelease(t *testing.T) {
	e := newCommunityEnv(t, nil)
	l := newHourlyLimiter(e.clk)
	r1, _, ok := l.reserve("ip:a", 1)
	if !ok {
		t.Fatal("第一筆要成功")
	}
	if _, wait, ok := l.reserve("ip:a", 1); ok || wait <= 0 {
		t.Fatalf("第二筆要被擋：%v %v", wait, ok)
	}
	r1()
	if _, _, ok := l.reserve("ip:a", 1); !ok {
		t.Fatal("歸還後要成功")
	}
	if _, _, ok := l.reserve("ip:b", 0); !ok {
		t.Fatal("limit 0 不限")
	}
	e.clk.Advance(2 * time.Hour)
	if _, _, ok := l.reserve("ip:a", 1); !ok || len(l.hits) != 1 {
		t.Fatalf("過期的紀錄要清掉：%v %d", ok, len(l.hits))
	}
}
