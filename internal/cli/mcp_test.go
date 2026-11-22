package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/app"
)

// MCP-R1：獨立模式不需要 serve，擁有全部權限（列得出 AGT 的 tool）。
func TestMCPListToolsStandalone(t *testing.T) {
	env, _ := testEnv(t)
	res, out, errOut := run(t, env, "mcp", "--list-tools")
	if res.Code != 0 || res.Mode != ModeStandalone {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	for _, want := range []string{"claim_tasks", "ingest", "get_video_context"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("輸出沒有 %s：%s", want, out)
		}
	}
	res, out, _ = run(t, env, "mcp", "--list-tools", "--json")
	var list []struct{ Name, Description string }
	if res.Code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) == 0 {
		t.Fatalf("--json：%+v %s", res, out)
	}
}

// 本機模式下指定 --token：用 token 的 scope 限制（MCP 規格待決問題）；無效的 token 失敗，而且不會出現在任何輸出。
func TestMCPTokenRestrictsLocalModes(t *testing.T) {
	env, _ := testEnv(t)
	res, out, errOut := run(t, env, "tokens", "create", "--name", "r", "--scopes", "read", "--json")
	if res.Code != 0 {
		t.Fatalf("建立 token：%+v %s", res, errOut)
	}
	var ct struct{ Token string }
	if err := json.Unmarshal([]byte(out), &ct); err != nil || ct.Token == "" {
		t.Fatalf("token：%s", out)
	}
	res, out, errOut = run(t, env, "mcp", "--list-tools", "--token", ct.Token)
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if strings.Contains(out, "ingest\n") || strings.Contains(out, "claim_tasks") || !strings.Contains(out, "get_transcript\n") {
		t.Errorf("read token 的 tools：%s", out)
	}
	if strings.Contains(out+errOut, ct.Token) {
		t.Error("輸出不應該含 token")
	}

	const bad = "nst_this_is_a_secret_value_xyz"
	res, out, errOut = run(t, env, "mcp", "--list-tools", "--token", bad)
	if res.Code == 0 {
		t.Errorf("無效的 token 應該失敗：%s", out)
	}
	if strings.Contains(out+errOut, bad) {
		t.Errorf("token 出現在輸出中：%s %s", out, errOut)
	}
}

// 官方 SDK 的 client 經 stdio 連到 `notesteme mcp`：initialize、tools/list、tools/call、prompts/list，
// stdin 關閉時正常結束；stdout 只有 JSON-RPC 訊息。
func TestMCPStdioInterop(t *testing.T) {
	env, _ := testEnv(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var stdout bytes.Buffer
	var errOut syncBuffer
	done := make(chan Result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		res := Execute(ctx, []string{"mcp"}, app.IO{Stdin: inR, Stdout: io.MultiWriter(outW, &stdout), Stderr: &errOut}, env)
		outW.Close()
		done <- res
	}()
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatalf("連線：%v（stderr：%s）", err, errOut.String())
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("tools/list：%v", err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "x", "mode": "semantic"}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "unsupported_mode") {
		t.Fatalf("search：%v %+v", err, res)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_items"})
	if err != nil || res.IsError {
		t.Fatalf("list_items：%v %+v", err, res)
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) == 0 {
		t.Fatalf("prompts/list：%v", err)
	}
	cs.Close()
	inW.Close()
	r := <-done
	if r.Code != 0 || r.Mode != ModeStandalone {
		t.Fatalf("結束：%+v，stderr：%s", r, errOut.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if !json.Valid([]byte(line)) || !strings.Contains(line, `"jsonrpc":"2.0"`) {
			t.Errorf("stdout 有不是 JSON-RPC 的內容：%q", line)
		}
	}
}

// 一次送完就關閉 stdin：每個請求的回應都要送出之後才結束。
func TestMCPStdioDrainsOnEOF(t *testing.T) {
	env, _ := testEnv(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"three","method":"prompts/list"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := Execute(ctx, []string{"mcp"}, app.IO{Stdin: strings.NewReader(in), Stdout: &out, Stderr: io.Discard}, env)
	if res.Code != 0 {
		t.Fatalf("結果 = %+v", res)
	}
	for _, id := range []string{`"id":1`, `"id":2`, `"id":"three"`} {
		if !strings.Contains(out.String(), id) {
			t.Errorf("缺少 %s 的回應：%s", id, out.String())
		}
	}
}

// stdio 端的取消（notifications/cancelled）會取消轉送中的請求，後端的 tool 看得到 ctx 被取消。
func TestMCPForwardCancels(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	backend := mcp.NewServer(&mcp.Implementation{Name: "b", Version: "1"}, nil)
	mcp.AddTool(backend, &mcp.Tool{Name: "block"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, nil, ctx.Err()
	})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return backend },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}))
	defer srv.Close()

	ctx := context.Background()
	up, err := mcp.NewClient(&mcp.Implementation{Name: "p", Version: "1"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	proxy := mcp.NewServer(&mcp.Implementation{Name: "proxy", Version: "1"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	proxy.AddReceivingMiddleware(forwardMCP(up))
	ct, st := mcp.NewInMemoryTransports()
	ss, err := proxy.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	cctx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(cctx, &mcp.CallToolParams{Name: "block", Arguments: map[string]any{}})
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("後端的 tool 沒有開始")
	}
	cancel()
	if err := <-errc; err == nil {
		t.Error("取消後應該回錯誤")
	}
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("取消沒有傳到後端")
	}
}

func TestPendingRequests(t *testing.T) {
	p := newPendingRequests()
	select {
	case <-p.idle():
	default:
		t.Fatal("一開始應該是閒置的")
	}
	r := &trackingReader{ctx: context.Background(), r: strings.NewReader(""), pending: p}
	r.scan([]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}` + "\n" + `{"jsonrpc":"2.0","method":"note"}` + "\n" + `{"id":`))
	r.scan([]byte(`"a","method":"y"}` + "\n"))
	if len(p.ids) != 2 {
		t.Fatalf("pending = %v", p.ids)
	}
	w := trackingWriter{w: io.Discard, pending: p}
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"a","error":{"code":1,"message":"x"}}` + "\n"))
	select {
	case <-p.idle():
	default:
		t.Fatalf("回應之後應該是閒置的：%v", p.ids)
	}
}

func FuzzIDKey(f *testing.F) {
	for _, s := range []string{`{"id":1,"method":"x"}`, `{"id":null}`, `{"id":"a","result":{}}`, `[]`, `{`, ""} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		p := newPendingRequests()
		r := &trackingReader{ctx: context.Background(), r: strings.NewReader(""), pending: p}
		r.scan(b)
		r.scan([]byte("\n"))
		w := trackingWriter{w: io.Discard, pending: p}
		_, _ = w.Write(b)
	})
}

// syncBuffer 是可以同時寫入與讀取的 buffer（stderr 在另一個 goroutine 寫入）。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
