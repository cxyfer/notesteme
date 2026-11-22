package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpState 是最近一次 MCP 步驟的結果（steps.md 4.7）。
type mcpState struct {
	// result 與 err 是最近一次 tools/call：err 是 JSON-RPC 層的錯誤，result.IsError 是 tool 的錯誤。
	result *mcp.CallToolResult
	err    error
	called bool
	// names 是最近一次 tools/list 或 prompts/list 列出的名稱；tools 是最近一次 tools/list 的 tool。
	names  []string
	tools  []*mcp.Tool
	listed bool
}

// mcpCallTimeout 是一次 MCP 步驟的上限（只是防止卡死的保險）。
const mcpCallTimeout = 60 * time.Second

// bearerRoundTripper 在每個請求加上 bearer token。
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpSession 以 token 透過 Streamable HTTP（/mcp）連到場景的 server，用官方 SDK 的 client（MCP-R1）。
// server 尚未啟動時隱含啟動；Given 累積的設定先寫入。token 不存在時步驟失敗（不是 pending）。
func (s *Scenario) mcpSession(ctx context.Context, name string) (*mcp.ClientSession, error) {
	tok, ok := s.World.Refs["token:"+name]
	if !ok || tok == "" {
		return nil, fmt.Errorf("還沒建立名稱為 %q 的 token", name)
	}
	if err := s.prepareOwner(); err != nil {
		return nil, err
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "notesteme-acceptance", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   "http://" + s.server.Addr() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: tok, base: s.client.Transport}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("MCP client 連線失敗：%w", err)
	}
	return cs, nil
}

// mcpCalls 呼叫 tool。呼叫進行中，worker 在另一個 goroutine 反覆執行到閒置（測試的 worker 是手動模式，
// get_video_context 的等待需要有人處理任務）；呼叫結束後先等 worker 停下，之後的步驟才斷言狀態。
func (s *Scenario) mcpCalls(name, tool string, doc *godog.DocString) error {
	var args map[string]any
	if err := json.Unmarshal(s.requestBody(doc), &args); err != nil {
		return fmt.Errorf("tool 參數不是 JSON 物件：%w", err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.mcpSession(ctx, name)
	if err != nil {
		return err
	}
	defer cs.Close()

	stop := s.driveWorker()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if werr := stop(); werr != nil {
		return fmt.Errorf("worker：%w", werr)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && s.ctx.Err() == nil {
		return fmt.Errorf("MCP tool %s 在 %d 秒內沒有回應", tool, int(mcpCallTimeout.Seconds()))
	}
	s.mcp.result, s.mcp.err, s.mcp.called = res, err, true
	return nil
}

// driveWorker 在背景反覆執行 worker，直到回傳的 stop 被呼叫；stop 等目前這一輪結束（不取消執行中的任務）。
func (s *Scenario) driveWorker() (stop func() error) {
	done := make(chan struct{})
	var (
		wg   sync.WaitGroup
		werr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			if err := s.server.RunUntilIdle(s.ctx); err != nil && s.ctx.Err() == nil {
				werr = err
				return
			}
			select {
			case <-done:
				return
			case <-s.ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() error {
		close(done)
		wg.Wait()
		return werr
	}
}

func (s *Scenario) mcpListsTools(name string) error {
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.mcpSession(ctx, name)
	if err != nil {
		return err
	}
	defer cs.Close()
	var names []string
	var tools []*mcp.Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("tools/list：%w", err)
		}
		names, tools = append(names, t.Name), append(tools, t)
	}
	s.mcp.names, s.mcp.tools, s.mcp.listed = names, tools, true
	return nil
}

func (s *Scenario) mcpListsPrompts(name string) error {
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.mcpSession(ctx, name)
	if err != nil {
		return err
	}
	defer cs.Close()
	var names []string
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			return fmt.Errorf("prompts/list：%w", err)
		}
		names = append(names, p.Name)
	}
	s.mcp.names, s.mcp.tools, s.mcp.listed = names, nil, true
	return nil
}

// toolText 是 tool 結果的文字內容。
func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// toolNotImplemented 回報 tool 的錯誤是不是「還沒實作」（AGT 的 tool 在 S10 之前）：斷言時視為 pending（ADR-0015）。
func toolNotImplemented(res *mcp.CallToolResult) bool {
	return res != nil && res.IsError && strings.HasPrefix(toolText(res), "not_implemented:")
}

// toolResultContains 以子集比對 tool 的結構化結果（steps.md：子集比對）。
func (s *Scenario) toolResultContains(doc *godog.DocString) error {
	if !s.mcp.called {
		return errors.New("還沒有呼叫任何 MCP tool")
	}
	if s.mcp.err != nil {
		return fmt.Errorf("tool 呼叫失敗：%v", s.mcp.err)
	}
	res := s.mcp.result
	if toolNotImplemented(res) {
		return godog.ErrPending
	}
	if res.IsError {
		return fmt.Errorf("tool 回傳錯誤：%s", toolText(res))
	}
	got, err := json.Marshal(res.StructuredContent)
	if err != nil || res.StructuredContent == nil {
		got = []byte(toolText(res))
	}
	want := []byte(s.World.Subst(s.World.Expand(doc.Content)))
	return jsonSubset(want, []byte(s.World.Unexpand(string(got))))
}

// toolCallFailsWith 確認最近一次 tool 呼叫失敗（JSON-RPC 錯誤或 tool 的錯誤結果），錯誤包含指定文字。
func (s *Scenario) toolCallFailsWith(want string) error {
	if !s.mcp.called {
		return errors.New("還沒有呼叫任何 MCP tool")
	}
	var msg string
	switch {
	case s.mcp.err != nil:
		msg = s.mcp.err.Error()
	case s.mcp.result.IsError:
		msg = toolText(s.mcp.result)
	default:
		return fmt.Errorf("預期 tool 呼叫失敗（含 %q），但成功了：%s", want, toolText(s.mcp.result))
	}
	if strings.Contains(msg, want) {
		return nil
	}
	if toolNotImplemented(s.mcp.result) {
		return godog.ErrPending
	}
	return fmt.Errorf("tool 的錯誤不含 %q：%s", want, msg)
}

func splitNames(list string) []string {
	var out []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (s *Scenario) listedNamesInclude(list string) error {
	if !s.mcp.listed {
		return errors.New("還沒有列出任何 tool 或 prompt")
	}
	var missing []string
	for _, n := range splitNames(list) {
		if !slices.Contains(s.mcp.names, n) {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("列出的名稱沒有 %s（實際：%s）", strings.Join(missing, ", "), strings.Join(s.mcp.names, ", "))
	}
	return nil
}

func (s *Scenario) listedNamesDoNotInclude(list string) error {
	if !s.mcp.listed {
		return errors.New("還沒有列出任何 tool 或 prompt")
	}
	var found []string
	for _, n := range splitNames(list) {
		if slices.Contains(s.mcp.names, n) {
			found = append(found, n)
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("列出的名稱不應該有 %s", strings.Join(found, ", "))
	}
	return nil
}

func (s *Scenario) toolDescriptionContains(tool, want string) error {
	if s.mcp.tools == nil {
		return errors.New("還沒有列出 tool")
	}
	for _, t := range s.mcp.tools {
		if t.Name == tool {
			if !strings.Contains(t.Description, want) {
				return fmt.Errorf("tool %s 的說明不含 %q：%s", tool, want, t.Description)
			}
			return nil
		}
	}
	return fmt.Errorf("列出的 tool 沒有 %s", tool)
}
