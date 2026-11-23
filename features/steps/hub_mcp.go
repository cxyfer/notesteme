package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/features/internal/harness"
)

// Hub MCP 的步驟（steps.md 4.30，M5-S6，HMCP）：以 Hub token 透過 Streamable HTTP 連到真 Hub 的 /mcp，用官方 SDK 的 client。
// 結果記在和 Client MCP 相同的 s.mcp，`the tool result contains:`、`the tool call fails with`、`the listed names …`、
// `the description of the tool …` 直接沿用（steps.md 4.7）。

// hubMCPSession 以 Hub token 連線；Hub 的設定改變過時先重新啟動。
func (s *Scenario) hubMCPSession(ctx context.Context, name string) (*mcp.ClientSession, error) {
	tok, ok := s.hub.tokens[name]
	if !ok {
		return nil, fmt.Errorf("沒有名為 %q 的 Hub token（先用 `Given a Hub token … for … with the permissions …`）", name)
	}
	if s.World.HubKind() != harness.HubReal {
		return nil, fmt.Errorf("真 Hub 沒有在執行（Background 要有 `Given a Hub is running`）")
	}
	if err := s.World.RestartHubIfNeeded(s.ctx); err != nil {
		return nil, err
	}
	hc := s.World.HubHTTPClient()
	c := mcp.NewClient(&mcp.Implementation{Name: "notesteme-acceptance", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   harness.HubPublicURL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: tok, base: hc.Transport}, CheckRedirect: hc.CheckRedirect},
		MaxRetries: -1,
	}, nil)
}

// hubMCPCalls 呼叫 tool。連線（initialize）本身失敗時也記成這次呼叫的錯誤：被限流時 initialize 回的是
// JSON-RPC 錯誤（HMCP-R8），`the tool call fails with` 斷言它的訊息。
func (s *Scenario) hubMCPCalls(name, tool string, doc *godog.DocString) error {
	var args map[string]any
	if err := json.Unmarshal(s.requestBody(doc), &args); err != nil {
		return fmt.Errorf("tool 參數不是 JSON 物件：%w", err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.hubMCPSession(ctx, name)
	if err != nil {
		if _, ok := s.hub.tokens[name]; !ok || s.ctx.Err() != nil {
			return err
		}
		s.mcp.result, s.mcp.err, s.mcp.called = nil, fmt.Errorf("MCP 連線失敗：%w", err), true
		return nil
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if ctx.Err() != nil && s.ctx.Err() == nil {
		return fmt.Errorf("Hub MCP tool %s 在 %d 秒內沒有回應", tool, int(mcpCallTimeout.Seconds()))
	}
	s.mcp.result, s.mcp.err, s.mcp.called = res, err, true
	return nil
}

func (s *Scenario) hubMCPListsTools(name string) error {
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.hubMCPSession(ctx, name)
	if err != nil {
		return fmt.Errorf("Hub MCP 連線失敗：%w", err)
	}
	defer func() { _ = cs.Close() }()
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

// hubTokenSendsFromOrigin 以 Hub token 送出帶 Origin 標頭的請求（HMCP-R2）。
func (s *Scenario) hubTokenSendsFromOrigin(spec, name, origin string) error {
	tok, ok := s.hub.tokens[name]
	if !ok {
		return fmt.Errorf("沒有名為 %q 的 Hub token", name)
	}
	method, path, err := parseRequest(spec)
	if err != nil {
		return err
	}
	_, err = s.hubDo(hubRequest{method: method, target: s.World.SubstRequest(path), bearer: tok, origin: origin})
	return err
}
