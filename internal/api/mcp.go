package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/mcptools"
)

// MCP Streamable HTTP（MCP-R1）的上限：/mcp 是對外的輸入面，請求大小、同時處理數與時間都要有上限。
var (
	// mcpMaxBody 是單一 JSON-RPC 請求的大小上限：import_transcript 的 content 最多 8 MiB（OUT-R13），
	// JSON 跳脫之後會變大，所以留一些空間。
	mcpMaxBody int64 = 12 << 20
	// mcpMaxConcurrent 是同時處理中的 /mcp 請求數；滿了回 503，不排隊。
	mcpMaxConcurrent = 32
	// mcpMaxWaits 是同時在等待的 get_video_context 數；滿了的時候不等待，直接回報目前的狀態。
	mcpMaxWaits = 8
	// mcpRequestTimeout 是一個 /mcp 請求的時間上限（get_video_context 最多等 600 秒，另外留讀取與渲染的時間）。
	mcpRequestTimeout = 11 * time.Minute
	// mcpMaxLoopbackBody 是 tool 呼叫 API 時，單一回應的大小上限；超過時請 agent 縮小範圍。
	mcpMaxLoopbackBody = 16 << 20
)

// mcpEndpoint 是 /mcp 的 handler。remote 為 true（TCP listener）時檢查 Origin 與 Host（AUTH-R11）；
// 本機 socket 與獨立模式沒有瀏覽器能碰到，不檢查。
type mcpEndpoint struct {
	d      Deps
	inner  http.Handler // 不含驗證 middleware 的 API 路由，tool 在程序內呼叫它（MCP-R3）
	remote bool
	sdk    *mcp.StreamableHTTPHandler
	slots  chan struct{}
	waits  chan struct{}
	cache  *mcp.SchemaCache
}

func newMCPEndpoint(d Deps, inner http.Handler, remote bool) *mcpEndpoint {
	e := &mcpEndpoint{
		d: d, inner: inner, remote: remote,
		slots: make(chan struct{}, mcpMaxConcurrent),
		waits: make(chan struct{}, mcpMaxWaits),
		cache: mcp.NewSchemaCache(),
	}
	e.sdk = mcp.NewStreamableHTTPHandler(e.server, &mcp.StreamableHTTPOptions{
		// 每個請求各自獨立：身分只來自這個請求的憑證，沒有可以被別的 token 接手的 session。
		Stateless:    true,
		JSONResponse: true,
		// Host 與 Origin 由 checkOrigin 檢查；SDK 內建的 localhost 防護會擋下程序內的請求（Host 是 notesteme.local）。
		DisableLocalhostProtection:   true,
		MaxRequestBodyBytes:          mcpMaxBody,
		PropagateRequestCancellation: true,
	})
	return e
}

// server 為一個請求建立 MCP server：身分取自通過驗證的 principal（AUTH-R8）。
func (e *mcpEndpoint) server(r *http.Request) *mcp.Server {
	p, ok := principalFrom(r.Context())
	if !ok {
		return nil
	}
	lb := &loopback{h: e.inner, base: r.Context()}
	return mcptools.NewServer(mcptools.Deps{
		Backend:     lb,
		Allowed:     p.HasScope,
		Events:      e.d.EventBus,
		Clock:       e.d.Clock,
		Waits:       e.waits,
		Request:     r.Context(),
		Version:     e.d.Version,
		SchemaCache: e.cache,
		Agent:       e.agent(r.Context()),
		AgentName:   agentName(p),
	})
}

// agent 是 AGT 的任務 tool 的後端；沒有 pipeline 時是 nil（tool 回 not_implemented）。
func (e *mcpEndpoint) agent(ctx context.Context) mcptools.AgentBackend {
	if e.d.Pipeline == nil {
		return nil
	}
	return agentBackend{p: e.d.Pipeline, log: func(msg string, err error) {
		e.d.Log.ErrorContext(ctx, msg, "error", err)
	}}
}

func (e *mcpEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 驗證（401）已經由外層的 middleware 完成；Origin 與 Host 在它之後檢查（403）。
	if e.remote {
		if err := checkOrigin(r, e.d.MCPAllowedOrigins, e.d.MCPAllowedHosts); err != nil {
			writeError(w, err)
			return
		}
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeError(w, newError(http.StatusServiceUnavailable, "busy", "同時處理的 MCP 請求太多，請稍後再試。"))
		return
	}
	// 先讀完本體（有上限），再取消這條連線的讀取期限：server 的 ReadTimeout 到期會取消 request context，
	// get_video_context 的等待會超過它。整個請求另有 mcpRequestTimeout。
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxBody))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, newError(http.StatusRequestEntityTooLarge, CodeInvalidInput, "請求內容太大。"))
				return
			}
			writeError(w, newError(http.StatusBadRequest, CodeInvalidInput, "請求格式不正確。"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{}) // 程序內的 transport 不支援，忽略
	ctx, cancel := context.WithTimeout(r.Context(), mcpRequestTimeout)
	defer cancel()
	e.sdk.ServeHTTP(w, r.WithContext(ctx))
}

// checkOrigin 實作 AUTH-R11：
//   - Host 必須是 IP 位址、localhost，或設定的 mcp.allowed_hosts（防 DNS rebinding）。
//   - 有 Origin 時，必須和請求同源，或在 mcp.allowed_origins 中。
func checkOrigin(r *http.Request, origins, hosts []string) *Error {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if !hostAllowed(host, hosts) {
		return errForbidden("MCP 不接受這個主機名稱（Host）。請把它加進設定檔的 mcp.allowed_hosts。")
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	o, ok := config.NormalizeOrigin(origin)
	if !ok {
		return errForbidden("MCP 不接受這個來源（Origin）。")
	}
	if self, ok := config.NormalizeOrigin(requestScheme(r) + "://" + r.Host); ok && o == self {
		return nil
	}
	if slices.Contains(origins, o) {
		return nil
	}
	return errForbidden("MCP 不接受這個來源（Origin）。請把它加進設定檔的 mcp.allowed_origins。")
}

func hostAllowed(host string, allowed []string) bool {
	if host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	return slices.Contains(allowed, host)
}

// requestScheme 是請求實際使用的 scheme（含反向代理的 X-Forwarded-Proto）。
func requestScheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

// loopback 讓 tool 以同一個請求的身分在程序內呼叫 API 路由（MCP-R3）。
type loopback struct {
	h    http.Handler
	base context.Context // /mcp 請求的 context：帶著 principal 與 requestInfo，連線中斷時取消
}

func (l *loopback) Do(ctx context.Context, method, path string, query url.Values, body any) (*mcptools.Response, error) {
	if !strings.HasPrefix(path, "/api/") {
		return nil, errors.New("api: mcp loopback 只能呼叫 /api/ 底下的路由")
	}
	// tool 的 context 不一定跟著 HTTP 連線取消（舊版 protocol），兩者任一結束都停止。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(l.base, cancel)
	defer stop()
	ctx = context.WithValue(ctx, keyPrincipal, l.base.Value(keyPrincipal))
	ctx = context.WithValue(ctx, keyRequestInfo, infoFrom(l.base))

	var rd io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	u := &url.URL{Scheme: "http", Host: "mcp.internal", Path: mustUnescape(path), RawPath: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	rec := &limitedRecorder{header: http.Header{}, limit: mcpMaxLoopbackBody}
	l.h.ServeHTTP(rec, req)
	if rec.over {
		return nil, mcptools.ErrResponseTooLarge
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	return &mcptools.Response{Status: status, Header: rec.header, Body: rec.buf.Bytes()}, nil
}

func mustUnescape(p string) string {
	if s, err := url.PathUnescape(p); err == nil {
		return s
	}
	return p
}

// limitedRecorder 收下 API 的回應；超過上限時丟掉內容並記下 over。
type limitedRecorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
	limit  int
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
		return 0, mcptools.ErrResponseTooLarge
	}
	if r.buf.Len()+len(b) > r.limit {
		r.over = true
		r.buf.Reset()
		return 0, mcptools.ErrResponseTooLarge
	}
	return r.buf.Write(b)
}
