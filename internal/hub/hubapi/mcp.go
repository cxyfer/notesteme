package hubapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubmcp"
)

// Hub MCP（HMCP-R1～R8）：/mcp 是 Streamable HTTP 的 MCP server（stateless、JSON 回應）。
//
// 順序：
//  1. 只接受 Hub token（Authorization: Bearer nsh_…）；沒有、無效、過期或撤銷都回 401，session cookie 不看（HMCP-R1）。
//     帳號停用時 403 account_suspended。無效的憑證計入 IP 的額度（同 accessMiddleware）。
//  2. Origin 與 Host（HMCP-R2）：Host 是 public_url 的主機名稱或 mcp.allowed_hosts；有 Origin 時要與 public_url 同源或在
//     mcp.allowed_origins 中。在讀 body 之前檢查。
//  3. 以帳號限流，與 API 共用額度，每個 HTTP 請求算一次（HMCP-R8）；被限流的 JSON-RPC 請求以 200 回 tool 錯誤或 JSON-RPC 錯誤。
//  4. body 上限、同時處理數與時間上限，之後交給 go-sdk。
//
// tool 以 loopback 在程序內呼叫 /api/v1 的 GET 路由：principal 經 context 傳入，accessMiddleware 不再驗證與限流，
// 但仍檢查權限（與 hubmcp 的檢查構成雙重檢查），可見度由路由與 SQL 處理（HMCP-R5）。

var (
	// hubMCPMaxBody 是單一 JSON-RPC 請求的大小上限：Hub MCP 只有唯讀的 tool，參數都很短。
	hubMCPMaxBody int64 = 1 << 20
	// hubMCPMaxConcurrent 是同時處理中的 /mcp 請求數；滿了回 503，不排隊。
	hubMCPMaxConcurrent = 32
	// hubMCPRequestTimeout 是一個 /mcp 請求的時間上限。
	hubMCPRequestTimeout = 60 * time.Second
	// hubMCPMinLoopbackBody 是 tool 呼叫 API 時單一回應的最小上限；實際上限另外放得下 bundles.max_file_bytes 的兩倍
	// （cues JSON 重新編碼之後可能變大）。
	hubMCPMinLoopbackBody int64 = 16 << 20
)

// loopbackKey 是 loopback 請求的 context key：值是 /mcp 驗證過的 principal。外部請求無法設定它。
type loopbackKey struct{}

func loopbackPrincipal(ctx context.Context) *hubauth.Principal {
	p, _ := ctx.Value(loopbackKey{}).(*hubauth.Principal)
	return p
}

type mcpEndpoint struct {
	rt    *routes
	inner http.Handler // Huma 的路由（tool 的 loopback 呼叫它）
	sdk   *mcp.StreamableHTTPHandler
	slots chan struct{}
	cache *mcp.SchemaCache
}

func newMCPEndpoint(rt *routes, inner http.Handler) *mcpEndpoint {
	e := &mcpEndpoint{rt: rt, inner: inner, slots: make(chan struct{}, hubMCPMaxConcurrent), cache: mcp.NewSchemaCache()}
	e.sdk = mcp.NewStreamableHTTPHandler(e.server, &mcp.StreamableHTTPOptions{
		// 每個請求各自獨立：身分只來自這個請求的 token，沒有可以被接手的 session。
		Stateless:    true,
		JSONResponse: true,
		// Host 與 Origin 由 checkMCPOrigin 依 public_url 檢查；SDK 的 localhost 防護不適用於遠端服務。
		DisableLocalhostProtection:   true,
		MaxRequestBodyBytes:          hubMCPMaxBody,
		PropagateRequestCancellation: true,
	})
	return e
}

// mcpPrincipalKey 是 /mcp 請求驗證過的 principal 與有效權限（server 建立時讀取）。
type mcpPrincipalKey struct{}

type mcpIdentity struct {
	p     *hubauth.Principal
	perms []string
}

func (e *mcpEndpoint) server(r *http.Request) *mcp.Server {
	id, ok := r.Context().Value(mcpPrincipalKey{}).(mcpIdentity)
	if !ok {
		return nil
	}
	var maxResult int64
	if cfg := e.rt.d.Config; cfg != nil {
		maxResult = cfg.MCP.MaxResultBytes
	}
	return hubmcp.NewServer(hubmcp.Deps{
		Backend:        &mcpLoopback{e: e, base: r.Context(), p: id.p},
		Allowed:        func(perm string) bool { return slices.Contains(id.perms, perm) },
		MaxResultBytes: maxResult,
		Version:        e.rt.d.Version,
		SchemaCache:    e.cache,
	})
}

func (e *mcpEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt := e.rt
	p, err := e.authenticate(r)
	if err != nil {
		// 停用的帳號也在限流之內（HMCP-R8、HACC-R18，同 accessMiddleware）：沒有可用的身分，以 IP 計入。
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Code == hubauth.CodeSuspended {
			if le := rt.limitAnonymous(r.RemoteAddr); le != nil {
				writeRawErr(w, r, le)
				return
			}
		}
		writeRawErr(w, r, rt.apiError(r.Context(), err))
		return
	}
	if p == nil {
		if le := rt.limitAnonymous(r.RemoteAddr); le != nil {
			writeRawErr(w, r, le)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="notesteme-hub"`)
		writeRawErr(w, r, apierr.New(http.StatusUnauthorized, hubauth.CodeUnauthorized, "Hub MCP 需要 Hub token（Authorization: Bearer nsh_…）。"))
		return
	}
	if oe := checkMCPOrigin(r, rt.d.Config); oe != nil {
		writeRawErr(w, r, oe)
		return
	}
	if wait, ok := rt.limiter.allow(accountKey(p.User.ID), rt.rateLimits().RequestsPerMinute); !ok {
		e.writeRateLimited(w, r, errRateLimited(wait))
		return
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeRawErr(w, r, apierr.New(http.StatusServiceUnavailable, "busy", "同時處理的 MCP 請求太多，請稍後再試。"))
		return
	}
	perms, err := rt.d.Auth.EffectivePermissions(r.Context(), p)
	if err != nil {
		writeRawErr(w, r, rt.apiError(r.Context(), err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hubMCPRequestTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, mcpPrincipalKey{}, mcpIdentity{p: p, perms: perms})
	e.sdk.ServeHTTP(w, r.WithContext(ctx))
}

// authenticate 只看 Authorization 標頭（HMCP-R1）；沒有有效的 Hub token 時回 (nil, nil)，帳號停用時回 403 的錯誤。
func (e *mcpEndpoint) authenticate(r *http.Request) (*hubauth.Principal, error) {
	if e.rt.d.Auth == nil {
		return nil, nil
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil, nil
	}
	p, err := e.rt.d.Auth.AuthenticateToken(r.Context(), strings.TrimSpace(tok))
	if hubauth.IsNoSession(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p == nil || p.PAT == nil {
		return nil, nil
	}
	return p, nil
}

// writeRateLimited 回應被限流的請求（HMCP-R8）：能解析成 JSON-RPC 請求時以 200 回 tool 錯誤或 JSON-RPC 錯誤，否則 429。
func (e *mcpEndpoint) writeRateLimited(w http.ResponseWriter, r *http.Request, le *apierr.Error) {
	setRetryAfter(w.Header().Set, le)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost && r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, hubMCPMaxBody+1))
		if err == nil && int64(len(raw)) <= hubMCPMaxBody {
			if reply, ok := hubmcp.RateLimitedReply(raw, le.Message, *le.RetryAfterS); ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(reply)
				return
			}
		}
	}
	apierr.WriteFor(w, r, le)
}

// checkMCPOrigin 實作 HMCP-R2（同 AUTH-R11，但以 public_url 為準：Hub 通常在反向代理之後，請求本身的 scheme 不可靠）。
func checkMCPOrigin(r *http.Request, cfg *hubconfig.Config) *apierr.Error {
	if cfg == nil {
		return mcpForbidden("MCP 不接受這個主機名稱（Host）。")
	}
	public, _ := url.Parse(cfg.PublicURL)
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" || (public == nil || host != strings.ToLower(public.Hostname())) && !slices.Contains(cfg.MCP.AllowedHosts, host) {
		return mcpForbidden("MCP 不接受這個主機名稱（Host）。請把它加進設定檔的 mcp.allowed_hosts。")
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	o, ok := hubconfig.NormalizeOrigin(origin)
	if !ok {
		return mcpForbidden("MCP 不接受這個來源（Origin）。")
	}
	if self, ok := hubconfig.NormalizeOrigin(cfg.PublicURL); ok && o == self {
		return nil
	}
	if slices.Contains(cfg.MCP.AllowedOrigins, o) {
		return nil
	}
	return mcpForbidden("MCP 不接受這個來源（Origin）。請把它加進設定檔的 mcp.allowed_origins。")
}

// mcpLoopback 讓 tool 以 /mcp 請求的身分在程序內呼叫 /api/v1 的 GET 路由。
type mcpLoopback struct {
	e    *mcpEndpoint
	base context.Context // /mcp 請求的 context：連線中斷時取消
	p    *hubauth.Principal
}

func (l *mcpLoopback) limit() int64 {
	n := hubMCPMinLoopbackBody
	if cfg := l.e.rt.d.Config; cfg != nil && 2*cfg.Bundles.MaxFileBytes+(1<<20) > n {
		n = 2*cfg.Bundles.MaxFileBytes + (1 << 20)
	}
	return n
}

func (l *mcpLoopback) Get(ctx context.Context, path string, query url.Values) (*hubmcp.Response, error) {
	if !strings.HasPrefix(path, "/api/v1/") {
		return nil, errors.New("hubapi: mcp loopback 只能呼叫 /api/v1/ 底下的路由")
	}
	// base 已取消時 AfterFunc 會在另一個 goroutine 非同步呼叫 cancel，內部請求可能搶先做完；
	// 所以開始前與結束後都直接檢查 base，不只依賴 AfterFunc。
	if err := l.base.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(l.base, cancel)
	defer stop()
	ctx = context.WithValue(ctx, loopbackKey{}, l.p)
	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "http", Host: "mcp.internal", Path: unescaped, RawPath: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	rec := &limitedRecorder{header: http.Header{}, limit: l.limit()}
	l.e.inner.ServeHTTP(rec, req)
	if rec.over {
		return nil, hubmcp.ErrResponseTooLarge
	}
	if err := l.base.Err(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	return &hubmcp.Response{Status: status, Body: rec.buf.Bytes()}, nil
}

// limitedRecorder 收下 API 的回應；超過上限時丟掉內容並記下 over。
type limitedRecorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
	limit  int64
	over   bool
}

func (r *limitedRecorder) Header() http.Header { return r.header }

func (r *limitedRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *limitedRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.over {
		return 0, hubmcp.ErrResponseTooLarge
	}
	if int64(r.buf.Len()+len(b)) > r.limit {
		r.over = true
		r.buf.Reset()
		return 0, hubmcp.ErrResponseTooLarge
	}
	return r.buf.Write(b)
}

// mcpForbidden 是 Origin 或 Host 不被接受（HMCP-R2）。
func mcpForbidden(msg string) *apierr.Error {
	return apierr.New(http.StatusForbidden, hubauth.CodeForbidden, msg)
}
