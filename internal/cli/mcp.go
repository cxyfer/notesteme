package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/app"
)

// parseMCP 解析 `notesteme mcp [--token <t>] [--list-tools]`（MCP-R1）。
//
// stdio 的 MCP server 是一層代理：tools 與 prompts 的請求轉給 API 的 /mcp（Streamable HTTP），
// 所以三種模式（獨立、本機 client、--server）的行為與 HTTP 完全相同，scope 也由 /mcp 判斷：
//   - 獨立模式擁有全部權限（程序內呼叫 api.NewLocal），並在背景執行 worker。
//   - client 模式用 --token（或 NOTESTEME_TOKEN）；本機模式下指定了 --token 也會用它限制 scope。
//
// stdout 只輸出 JSON-RPC 訊息；token 只放在 Authorization 標頭，不寫進任何輸出或 log。
func parseMCP(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("mcp", g)
	list := fs.Bool("list-tools", false, "列出可用的 tool 之後結束，不啟動 stdio server")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("mcp 不接受位置參數（收到 %q）。", pos[0])
	}
	listOnly := *list
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		if s.be.token == "" {
			s.be.token = s.g.token
			if s.be.token == "" && s.be.mode == ModeClient {
				s.be.token = s.env.Vars[envToken]
			}
		}
		if listOnly {
			return exitOK, listMCPTools(ctx, s, s.g.json)
		}
		return exitOK, serveMCPStdio(ctx, s)
	}}, nil
}

// mcpHTTPClient 是連到 /mcp 的 client：不設回應標頭的逾時（get_video_context 最多等 600 秒才回應），
// 時間由每個請求的 ctx 控制；token 由 bearerTransport 加在每個請求上。
func (b *backend) mcpHTTPClient() *http.Client {
	tr := b.hc.Transport
	if t, ok := tr.(*http.Transport); ok && t.ResponseHeaderTimeout > 0 {
		t = t.Clone()
		t.ResponseHeaderTimeout = 0
		tr = t
	}
	if b.token != "" {
		tr = bearerTransport{token: b.token, base: tr}
	}
	return &http.Client{Transport: tr, CheckRedirect: b.hc.CheckRedirect}
}

// bearerTransport 在每個請求加上 Authorization 標頭。
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}

// connectMCP 以 MCP client 連到 backend 的 /mcp。
func connectMCP(ctx context.Context, s *session) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "notesteme-cli", Version: cliVersion(s)}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: s.be.base + "/mcp", HTTPClient: s.be.mcpHTTPClient(),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("無法連到 MCP（%s/mcp）：%w", s.be.base, err)
	}
	return cs, nil
}

func cliVersion(*session) string { return app.Version() }

// listMCPTools 印出目前的身分可以使用的 tool（每行一個名稱；--json 時是 name 與 description 的陣列）。
func listMCPTools(ctx context.Context, s *session, asJSON bool) error {
	cs, err := connectMCP(ctx, s)
	if err != nil {
		return err
	}
	defer cs.Close()
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var out []entry
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("列出 tool 失敗：%w", err)
		}
		out = append(out, entry{t.Name, t.Description})
	}
	if asJSON {
		return s.printJSON(out)
	}
	for _, e := range out {
		fmt.Fprintln(s.stdout, e.Name)
	}
	return nil
}

// serveMCPStdio 在 stdin／stdout 上執行 MCP server，直到 client 關閉 stdin 或 ctx 取消。
func serveMCPStdio(ctx context.Context, s *session) error {
	if s.stdin == nil {
		return usagef("mcp 需要從 stdin 讀取 MCP 訊息。")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cs, err := connectMCP(ctx, s)
	if err != nil {
		return err
	}
	defer cs.Close()

	// 獨立模式：沒有 serve 執行任務，在背景執行 worker（新增的影音才會被處理）。
	var wg sync.WaitGroup
	if sa := s.be.standalone; sa != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sa.RunWorker(ctx)
		}()
	}
	defer wg.Wait()
	defer cancel()

	opts := &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Prompts: &mcp.PromptCapabilities{}}}
	version := cliVersion(s)
	if ir := cs.InitializeResult(); ir != nil {
		opts.Instructions = ir.Instructions
		if ir.ServerInfo != nil {
			version = ir.ServerInfo.Version
		}
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "notesteme", Version: version}, opts)
	srv.AddReceivingMiddleware(forwardMCP(cs))
	pending := newPendingRequests()
	err = srv.Run(ctx, &mcp.IOTransport{
		Reader: io.NopCloser(&trackingReader{ctx: ctx, r: s.stdin, pending: pending}),
		Writer: trackingWriter{w: s.stdout, pending: pending},
	})
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, mcp.ErrConnectionClosed) {
		return nil
	}
	return fmt.Errorf("MCP stdio 中斷：%w", err)
}

// forwardMCP 把 tools 與 prompts 的請求轉給 /mcp；initialize、ping 等由 stdio 端的 server 自己回應。
// 取消（notifications/cancelled）會取消 handler 的 ctx，連帶取消轉送中的 HTTP 請求。
func forwardMCP(cs *mcp.ClientSession) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch p := req.GetParams().(type) {
			case *mcp.ListToolsParams:
				if method == "tools/list" {
					return cs.ListTools(ctx, p)
				}
			case *mcp.CallToolParamsRaw:
				if method == "tools/call" {
					return cs.CallTool(ctx, &mcp.CallToolParams{Meta: p.Meta, Name: p.Name, Arguments: rawArgs(p.Arguments)})
				}
			case *mcp.ListPromptsParams:
				if method == "prompts/list" {
					return cs.ListPrompts(ctx, p)
				}
			case *mcp.GetPromptParams:
				if method == "prompts/get" {
					return cs.GetPrompt(ctx, p)
				}
			}
			return next(ctx, method, req)
		}
	}
}

// rawArgs 讓沒有參數的呼叫送出 {}，不是 null。
func rawArgs(a json.RawMessage) any {
	if len(a) == 0 {
		return map[string]any{}
	}
	return a
}

// pendingRequests 追蹤 stdin 讀到、還沒有回應的 JSON-RPC 請求：stdin 結束（EOF）時先等它們都回應，
// 否則 `printf … | notesteme mcp` 這種一次送完就關閉 stdin 的用法會遺失回應。
type pendingRequests struct {
	mu   sync.Mutex
	ids  map[string]int
	zero chan struct{} // ids 變成空的時候關閉；之後有新的請求時重新建立
}

func newPendingRequests() *pendingRequests {
	z := make(chan struct{})
	close(z)
	return &pendingRequests{ids: map[string]int{}, zero: z}
}

// idKey 取出一行 JSON-RPC 訊息的 id；沒有 id（通知）或不是 JSON 物件時 ok 為 false。
// 回應（有 result 或 error）與請求分開判斷：isResponse 為 true。
func idKey(line []byte) (key string, isResponse, ok bool) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(line, &m) != nil || len(m.ID) == 0 || string(m.ID) == "null" {
		return "", false, false
	}
	return string(m.ID), m.Method == "" && (m.Result != nil || m.Error != nil), true
}

func (p *pendingRequests) add(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ids) == 0 {
		p.zero = make(chan struct{})
	}
	p.ids[key]++
}

func (p *pendingRequests) done(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ids[key] == 0 {
		return
	}
	if p.ids[key]--; p.ids[key] == 0 {
		delete(p.ids, key)
	}
	if len(p.ids) == 0 {
		close(p.zero)
	}
}

func (p *pendingRequests) idle() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.zero
}

// trackingReader 把讀到的每一行請求記進 pending；底層讀到 EOF 時，等 pending 清空（或 ctx 結束）才回傳 EOF。
type trackingReader struct {
	ctx     context.Context
	r       io.Reader
	pending *pendingRequests
	partial []byte
}

// maxTrackedLine 是追蹤時保留的未完成行的上限；超過的行不追蹤（SDK 自己會拒絕過長的訊息）。
const maxTrackedLine = 16 << 20

func (t *trackingReader) Read(b []byte) (int, error) {
	n, err := t.r.Read(b)
	t.scan(b[:n])
	if errors.Is(err, io.EOF) {
		select {
		case <-t.pending.idle():
		case <-t.ctx.Done():
		}
	}
	return n, err
}

func (t *trackingReader) scan(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if len(t.partial)+len(b) <= maxTrackedLine {
				t.partial = append(t.partial, b...)
			} else {
				t.partial = t.partial[:0]
			}
			return
		}
		line := append(t.partial, b[:i]...)
		if key, isResp, ok := idKey(line); ok && !isResp {
			t.pending.add(key)
		}
		t.partial = t.partial[:0]
		b = b[i+1:]
	}
}

// trackingWriter 在寫出回應時把對應的請求從 pending 移除。SDK 每次寫出一則完整的訊息（一行）。
type trackingWriter struct {
	w       io.Writer
	pending *pendingRequests
}

func (t trackingWriter) Write(b []byte) (int, error) {
	n, err := t.w.Write(b)
	for _, line := range splitLines(b) {
		if key, isResp, ok := idKey(line); ok && isResp {
			t.pending.done(key)
		}
	}
	return n, err
}

func (trackingWriter) Close() error { return nil }

func splitLines(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return append(out, b)
		}
		out = append(out, b[:i])
		b = b[i+1:]
	}
	return out
}
