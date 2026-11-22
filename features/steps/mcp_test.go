package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 沒有建立的 token：MCP 步驟失敗而不是 pending。
func TestMCPStepsNeedToken(t *testing.T) {
	s := newTestScenario(t)
	err := s.mcpListsTools("missing")
	if err == nil || errors.Is(err, godog.ErrPending) {
		t.Fatalf("沒有 token 應該失敗：%v", err)
	}
}

// 斷言步驟：還沒呼叫時失敗；成功的呼叫不能滿足「fails with」；not_implemented 是 pending；其他錯誤照常失敗。
func TestMCPAssertionSemantics(t *testing.T) {
	s := newTestScenario(t)
	if err := s.toolCallFailsWith("x"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Fatalf("還沒呼叫：%v", err)
	}
	text := func(v string) []mcp.Content { return []mcp.Content{&mcp.TextContent{Text: v}} }
	s.mcp = mcpState{called: true, result: &mcp.CallToolResult{Content: text(`{"a":1}`), StructuredContent: map[string]any{"a": 1}}}
	if err := s.toolCallFailsWith("a"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("成功的呼叫不應該滿足 fails with：%v", err)
	}
	if err := s.toolResultContains(&godog.DocString{Content: `{"a":1}`}); err != nil {
		t.Errorf("子集比對：%v", err)
	}
	s.mcp.result = &mcp.CallToolResult{IsError: true, Content: text("not_implemented: 這個功能尚未實作。")}
	if err := s.toolCallFailsWith("in_trash"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("not_implemented 應該 pending：%v", err)
	}
	if err := s.toolResultContains(&godog.DocString{Content: `{}`}); !errors.Is(err, godog.ErrPending) {
		t.Errorf("not_implemented 的結果斷言應該 pending：%v", err)
	}
	s.mcp.result = &mcp.CallToolResult{IsError: true, Content: text("not_found: 找不到這部影音。")}
	if err := s.toolCallFailsWith("in_trash"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("其他錯誤應該失敗：%v", err)
	}
	if err := s.toolCallFailsWith("not_found"); err != nil {
		t.Errorf("包含的文字：%v", err)
	}
	s.mcp = mcpState{listed: true, names: []string{"a", "b"}}
	if s.listedNamesInclude("a, b") != nil || s.listedNamesInclude("a, c") == nil ||
		s.listedNamesDoNotInclude("c,d") != nil || s.listedNamesDoNotInclude("d, b") == nil {
		t.Error("名稱清單的比對不正確")
	}
}
