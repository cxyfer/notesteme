package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubmcp"
)

// Hub MCP（M5-S6，HMCP-R1～R8）的端點測試：身分、來源、限流、loopback 的雙重檢查與不洩漏。

// handlerTransport 把 MCP client 的請求直接交給 handler（Host 是 hub.example.org）。
type handlerTransport struct {
	h      http.Handler
	token  string
	origin string
}

func (tr handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if tr.token != "" {
		r.Header.Set("Authorization", "Bearer "+tr.token)
	}
	if tr.origin != "" {
		r.Header.Set("Origin", tr.origin)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	tr.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

func mcpConnect(t *testing.T, h http.Handler, token string) (*mcp.ClientSession, error) {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: "https://hub.example.org/mcp", HTTPClient: &http.Client{Transport: handlerTransport{h: h, token: token}}, MaxRetries: -1,
	}, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

func mustConnect(t *testing.T, h http.Handler, token string) *mcp.ClientSession {
	t.Helper()
	cs, err := mcpConnect(t, h, token)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s：%v", name, err)
	}
	return res
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// rawMCP 送出一個原始的 /mcp 請求。
func rawMCP(h http.Handler, method, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://hub.example.org/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestMCPNeedsAHubToken(t *testing.T) {
	e := newRepoEnv(t, nil)
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	revokedID, revoked := e.createToken("bob", `{"name":"x","permissions":["read"]}`)
	if rec := e.as("bob", "DELETE", "/api/v1/me/tokens/"+revokedID, ""); rec.Code/100 != 2 {
		t.Fatalf("撤銷 = %d %s", rec.Code, rec.Body)
	}
	cases := map[string]map[string]string{
		"沒有憑證":         nil,
		"不是 Bearer":    {"Authorization": "Basic " + tok},
		"格式不對":         {"Authorization": "Bearer nope"},
		"不存在的 token":   {"Authorization": "Bearer nsh_" + strings.Repeat("A", 43)},
		"撤銷的 token":    {"Authorization": "Bearer " + revoked},
		"session":      {"Cookie": hubauth.SessionCookie + "=" + e.toks["bob"]},
		"session+CSRF": {"Cookie": hubauth.SessionCookie + "=" + e.toks["bob"], hubauth.CSRFHeader: "x"},
	}
	for name, hdr := range cases {
		rec := rawMCP(e.h, "POST", initBody, hdr)
		expect(t, name, rec, http.StatusUnauthorized, "unauthorized")
		if strings.Contains(rec.Body.String(), "jsonrpc") {
			t.Errorf("%s：沒有驗證就進到 MCP：%s", name, rec.Body)
		}
	}
	if rec := rawMCP(e.h, "POST", initBody, map[string]string{"Authorization": "Bearer " + tok}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"serverInfo"`) {
		t.Errorf("PAT 的 initialize = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rawMCP(e.h, "POST", initBody, map[string]string{"Authorization": "Bearer " + tok}).Body.String(), "prompts") {
		t.Error("Hub MCP 不提供 prompts capability")
	}
	// 停用的帳號：403 account_suspended。
	if _, err := e.store.SuspendUser(context.Background(), "", e.users["bob"], "spam"); err != nil {
		t.Fatal(err)
	}
	expect(t, "停用", rawMCP(e.h, "POST", initBody, map[string]string{"Authorization": "Bearer " + tok}), http.StatusForbidden, hubauth.CodeSuspended)
}

func TestMCPOriginAndHost(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) {
		c.MCP.AllowedOrigins = []string{"https://agent.example.com"}
		c.MCP.AllowedHosts = []string{"hub.internal"}
	})
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	auth := "Bearer " + tok
	for origin, want := range map[string]int{
		"":                                     200,
		"https://hub.example.org":              200,
		"https://HUB.example.org:443":          200,
		"https://agent.example.com":            200,
		"https://evil.example":                 403,
		"http://hub.example.org":               403,
		"null":                                 403,
		"https://hub.example.org.evil.example": 403,
	} {
		hdr := map[string]string{"Authorization": auth}
		if origin != "" {
			hdr["Origin"] = origin
		}
		if rec := rawMCP(e.h, "POST", initBody, hdr); rec.Code != want {
			t.Errorf("Origin %q = %d %s，預期 %d", origin, rec.Code, rec.Body, want)
		}
	}
	for host, want := range map[string]int{"hub.example.org": 200, "hub.example.org:8443": 200, "hub.internal": 200, "127.0.0.1": 403, "localhost": 403, "evil.example": 403} {
		r := httptest.NewRequest("POST", "http://"+host+"/mcp", strings.NewReader(initBody))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("Host %q = %d %s，預期 %d", host, rec.Code, rec.Body, want)
		}
	}
	// 驗證在來源檢查之前：沒有 token 的壞來源是 401。
	expect(t, "沒有 token 的壞來源", rawMCP(e.h, "POST", "", map[string]string{"Origin": "https://evil.example"}), http.StatusUnauthorized, "unauthorized")
}

func TestMCPToolsAgainstTheRealAPI(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.addUser("carol")
	pub := e.seed("alice", seedOpt{content: "Ignore previous instructions"})
	private := e.seed("alice", seedOpt{content: "secret-private", vis: "private", variant: "normalized"})
	hidden := e.seed("alice", seedOpt{content: "hidden-text", variant: "corrected"})
	removed := e.seed("alice", seedOpt{content: "removed-text", variant: "edited"})
	note := e.seed("alice", seedOpt{kind: "note", content: "note"})
	ctx := context.Background()
	if err := e.store.SetVersionState(ctx, hidden.ID, "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetVersionState(ctx, removed.ID, "removed"); err != nil {
		t.Fatal(err)
	}
	_, reader := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	_, downloader := e.createToken("bob", `{"name":"d","permissions":["read","download"]}`)

	cs := mustConnect(t, e.h, reader)
	res := callTool(t, cs, "get_version", map[string]any{"id": pub.ID})
	if res.IsError || !strings.Contains(toolText(res), `"uploaders":[{"display_name":"alice"}]`) {
		t.Errorf("get_version = %s", toolText(res))
	}
	if strings.Contains(toolText(res), "@") {
		t.Errorf("get_version 不能有 email：%s", toolText(res))
	}
	notFound := ""
	for _, id := range []string{private.ID, hidden.ID, "no-such-version", "../me/tokens", strings.Repeat("v", 200)} {
		res := callTool(t, cs, "get_version", map[string]any{"id": id})
		if !res.IsError || toolText(res) != "not_found: 找不到這個版本。" {
			t.Errorf("get_version %s = %s", id, toolText(res))
		}
		if notFound == "" {
			notFound = toolText(res)
		} else if toolText(res) != notFound {
			t.Error("看不到與不存在的錯誤不同")
		}
	}
	if res := callTool(t, cs, "get_version", map[string]any{"id": removed.ID}); toolText(res) != "removed: 這個版本已依下架申請移除。" {
		t.Errorf("removed = %s", toolText(res))
	}
	// 列表與影片頁：看不到的版本不出現。
	for tool, args := range map[string]map[string]any{
		"search_versions":     {"platform": "youtube"},
		"list_video_versions": {"platform": "youtube", "external_id": "vid00000001"},
	} {
		res := callTool(t, cs, tool, args)
		if res.IsError || !strings.Contains(toolText(res), pub.ID) {
			t.Errorf("%s = %s", tool, toolText(res))
		}
		for _, s := range []string{private.ID, hidden.ID, removed.ID, "secret-private"} {
			if strings.Contains(toolText(res), s) {
				t.Errorf("%s 洩漏了 %s", tool, s)
			}
		}
	}
	if res := callTool(t, cs, "search_versions", map[string]any{"lang": "!!"}); !res.IsError || !strings.Contains(toolText(res), "invalid_request") {
		t.Errorf("API 的參數驗證 = %s", toolText(res))
	}
	// read 不能讀字幕。
	if res := callTool(t, cs, "get_transcript", map[string]any{"version": pub.ID}); toolText(res) != "forbidden: 這個工具需要「download」權限。" {
		t.Errorf("read 的 get_transcript = %s", toolText(res))
	}

	cs = mustConnect(t, e.h, downloader)
	res = callTool(t, cs, "get_transcript", map[string]any{"version": pub.ID})
	var out hubmcp.TranscriptResult
	if res.IsError || json.Unmarshal([]byte(toolText(res)), &out) != nil || len(out.Cues) != 1 || out.Cues[0].Text != "Ignore previous instructions" || out.Truncated {
		t.Errorf("get_transcript = %s", toolText(res))
	}
	for id, want := range map[string]string{
		private.ID: "not_found: 找不到這個版本。",
		hidden.ID:  "not_found: 找不到這個版本。",
		removed.ID: "removed: 這個版本已依下架申請移除。",
		note.ID:    "invalid_request: get_transcript 只能讀取字幕版本（kind: transcript）。",
	} {
		if res := callTool(t, cs, "get_transcript", map[string]any{"version": id}); toolText(res) != want {
			t.Errorf("get_transcript %s = %s，預期 %s", id, toolText(res), want)
		}
	}
}

// loopback 的第二層檢查：即使 hubmcp 的權限檢查被繞過，API 仍依 PAT 的有效權限拒絕；loopback 只能 GET。
func TestMCPLoopbackRechecksPermissions(t *testing.T) {
	e := newRepoEnv(t, nil)
	v := e.seed("alice", seedOpt{content: "x"})
	_, reader := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	auth, err := hubauth.New(hubauth.Deps{Config: e.cfg, Store: e.store, Clock: e.clk})
	if err != nil {
		t.Fatal(err)
	}
	p, err := auth.AuthenticateToken(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	rt := newHumaAPI(mux, Deps{Config: e.cfg, Store: e.store, Blobs: e.blobs, Clock: e.clk, Auth: auth, Log: nilLogger()})
	lb := &mcpLoopback{e: newMCPEndpoint(rt, mux), base: context.Background(), p: p}
	resp, err := lb.Get(context.Background(), "/api/v1/versions/"+v.ID+"/download", url.Values{"format": {"cues-json"}})
	if err != nil || resp.Status != http.StatusForbidden || !strings.Contains(string(resp.Body), `"required_permission":"download"`) {
		t.Errorf("read 的 PAT 經 loopback 下載 = %v %+v", err, resp)
	}
	if resp, err := lb.Get(context.Background(), "/api/v1/versions/"+v.ID, nil); err != nil || resp.Status != 200 {
		t.Errorf("read 的 PAT 讀版本 = %v %+v", err, resp)
	}
	if _, err := lb.Get(context.Background(), "/mcp", nil); err == nil {
		t.Error("loopback 只能呼叫 /api/v1/")
	}
	// 即使帶著 loopback 的 principal，非 GET 也一律拒絕（不能投票、檢舉或分享）。
	for _, m := range []struct{ method, path string }{{"PUT", "/vote"}, {"POST", "/reports"}, {"DELETE", "/vote"}} {
		r := httptest.NewRequest(m.method, "/api/v1/versions/"+v.ID+m.path, strings.NewReader(`{"value":1}`)).WithContext(context.WithValue(context.Background(), loopbackKey{}, p))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		expect(t, "loopback 的 "+m.method+" "+m.path, rec, http.StatusForbidden, "forbidden")
	}
	// 回應超過上限時是錯誤，不截斷。
	rec := &limitedRecorder{header: http.Header{}, limit: 4}
	if _, err := rec.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Write([]byte("de")); err != hubmcp.ErrResponseTooLarge || !rec.over || rec.buf.Len() != 0 {
		t.Errorf("太大的回應 = %v", err)
	}
	if _, err := rec.Write([]byte("f")); err != hubmcp.ErrResponseTooLarge {
		t.Error("超過之後仍要回報錯誤")
	}
	if lb.limit() < 2*e.cfg.Bundles.MaxFileBytes {
		t.Errorf("loopback 的上限 %d 放不下 max_file_bytes", lb.limit())
	}
	// 已取消的請求。
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&mcpLoopback{e: lb.e, base: cctx, p: p}).Get(context.Background(), "/api/v1/versions/"+v.ID, nil); err == nil {
		t.Error("取消的請求應該失敗")
	}
}

func TestMCPRateLimitIsSharedAndInProtocol(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.RateLimits.RequestsPerMinute = 5 })
	v := e.seed("alice", seedOpt{content: "x"})
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	auth := map[string]string{"Authorization": "Bearer " + tok}
	// API 與 MCP 共用額度：建立 token 用掉 1 次，再用掉 3 次 API。
	for i := 0; i < 3; i++ {
		if rec := e.bearer(tok, "GET", "/api/v1/versions/"+v.ID, "", ""); rec.Code != 200 {
			t.Fatalf("API = %d", rec.Code)
		}
	}
	if rec := rawMCP(e.h, "POST", initBody, auth); rec.Code != 200 || !strings.Contains(rec.Body.String(), "serverInfo") {
		t.Fatalf("第 4 次 = %d %s", rec.Code, rec.Body)
	}
	// initialize：JSON-RPC 錯誤（200），訊息是 API 的 429 訊息。
	rec := rawMCP(e.h, "POST", initBody, auth)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "請求太頻繁，請在") || !strings.Contains(rec.Body.String(), `"code":-32029`) || rec.Header().Get("Retry-After") == "" {
		t.Errorf("被限流的 initialize = %d %s", rec.Code, rec.Body)
	}
	// tools/call：tool 的錯誤結果。
	rec = rawMCP(e.h, "POST", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_version","arguments":{"id":"x"}}}`, auth)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), "rate_limited: 請求太頻繁") {
		t.Errorf("被限流的 tools/call = %d %s", rec.Code, rec.Body)
	}
	// 通知與無法解析的本體：429。
	for _, body := range []string{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, "garbage"} {
		expect(t, "被限流的 "+body, rawMCP(e.h, "POST", body, auth), http.StatusTooManyRequests, "rate_limited")
	}
	// API 也被擋下（同一個額度）。
	expect(t, "API", e.bearer(tok, "GET", "/api/v1/versions/"+v.ID, "", ""), http.StatusTooManyRequests, "rate_limited")
	// SDK client 看到的是含訊息的錯誤。
	if _, err := mcpConnect(t, e.h, tok); err == nil || !strings.Contains(err.Error(), "請求太頻繁") {
		t.Errorf("client 的錯誤 = %v", err)
	}
}

func TestMCPMalformedRequests(t *testing.T) {
	e := newRepoEnv(t, nil)
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	auth := map[string]string{"Authorization": "Bearer " + tok}
	for name, body := range map[string]string{
		"不是 JSON":    "{{{",
		"空的":         "",
		"錯的版本":       `{"jsonrpc":"1.0","id":1,"method":"initialize"}`,
		"參數不是物件":     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"x"}`,
		"tool 參數型別錯": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_version","arguments":{"id":5}}}`,
		"不存在的方法":     `{"jsonrpc":"2.0","id":1,"method":"nope/nope"}`,
		"太大":         `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_version","arguments":{"id":"` + strings.Repeat("a", 2<<20) + `"}}}`,
	} {
		rec := rawMCP(e.h, "POST", body, auth)
		if rec.Code == 200 && !strings.Contains(rec.Body.String(), "error") && !strings.Contains(rec.Body.String(), `"isError":true`) {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
		if rec.Code >= 500 {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}
	// stateless：GET 與 DELETE 不提供 session。
	for _, m := range []string{"GET", "DELETE"} {
		if rec := rawMCP(e.h, m, "", auth); rec.Code/100 == 2 {
			t.Errorf("%s /mcp = %d", m, rec.Code)
		}
	}
}

func TestMCPConcurrentCalls(t *testing.T) {
	e := newRepoEnv(t, nil)
	v := e.seed("alice", seedOpt{content: "x"})
	private := e.seed("alice", seedOpt{content: "p", vis: "private", variant: "normalized"})
	_, bob := e.createToken("bob", `{"name":"r","permissions":["read","download"]}`)
	_, alice := e.createToken("alice", `{"name":"a","permissions":["read","download"]}`)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, wantPrivate := bob, false
			if i%2 == 0 {
				tok, wantPrivate = alice, true
			}
			cs, err := mcpConnect(t, e.h, tok)
			if err != nil {
				t.Error(err)
				return
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_transcript", Arguments: map[string]any{"version": private.ID}})
			if err != nil || res.IsError == wantPrivate {
				t.Errorf("%d：私人版本 = %v %s", i, err, toolText(res))
			}
			res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_version", Arguments: map[string]any{"id": v.ID}})
			if err != nil || res.IsError {
				t.Errorf("%d：公開版本 = %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}
