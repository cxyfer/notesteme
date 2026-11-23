package hubmcp

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// CodeRateLimited 是超過限流時 JSON-RPC 錯誤的代碼（server error 的保留範圍）。
const CodeRateLimited = -32029

// RateLimitedReply 為被限流的請求組出 JSON-RPC 回應（HMCP-R8）：
//   - tools/call：tool 的錯誤結果（isError），訊息是 message；
//   - 其他有 id 的請求（initialize、tools/list…）：JSON-RPC 錯誤，message 相同，data 有 code 與 retry_after_s。
//
// 通知、回應、batch 或無法解析的本體回傳 ok == false，由呼叫者回 HTTP 429。
// 用 HTTP 200 帶回錯誤是因為 MCP client（go-sdk）把 429 當成暫時性錯誤，不會讀出本體，agent 看不到要等幾秒。
func RateLimitedReply(body []byte, message string, retryAfterS int) (reply []byte, ok bool) {
	msg, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		return nil, false
	}
	req, isReq := msg.(*jsonrpc.Request)
	if !isReq || !req.IsCall() {
		return nil, false
	}
	resp := &jsonrpc.Response{ID: req.ID}
	if req.Method == "tools/call" {
		raw, err := json.Marshal(RateLimitedResult(message, retryAfterS))
		if err != nil {
			return nil, false
		}
		resp.Result = raw
	} else {
		data, _ := json.Marshal(map[string]any{"code": "rate_limited", "retry_after_s": retryAfterS})
		resp.Error = &jsonrpc.Error{Code: CodeRateLimited, Message: message, Data: data}
	}
	out, err := jsonrpc.EncodeMessage(resp)
	if err != nil {
		return nil, false
	}
	return out, true
}
