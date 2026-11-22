package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// prompts capability（MCP-R8）：列出 Prompt 庫中的 prompt，prompts/get 依參數渲染。
// 一樣經由 API（GET /api/prompts、GET /api/prompts/{id}/versions）讀取，scope 是 read（MCP-R6 的設計）。

// 上限：prompt 參數是不可信的輸入。
const (
	maxPromptArg     = 64 << 10
	maxPromptArgsSum = 256 << 10
	maxPromptsListed = 200
)

// metadataVars 是內容來自影片的變數：不展開進 prompt 的指示，改成指向 <video_metadata> 的說明，
// 值只出現在另一則資料訊息中（LLM 規格 L8、LLM-R9）。
var metadataVars = []string{"title", "channel", "description", "chapters", "glossary"}

// langVar 是來自設定的語言代碼變數：照常展開，但只接受語言代碼的字元集。
var langVarPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,35}$`)

// promptSummary 與 promptVersion 是 API 回應中需要的欄位。
type promptSummary struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	DefaultVersion int    `json:"default_version"`
}

type promptVersion struct {
	Version   int      `json:"version"`
	Body      string   `json:"body"`
	Variables []string `json:"variables"`
}

// promptsMiddleware 處理 prompts/list 與 prompts/get；prompt 不註冊在 server 上，每次都從 API 讀取最新的內容。
func promptsMiddleware(d *Deps) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "prompts/list":
				return listPrompts(ctx, d)
			case "prompts/get":
				p, _ := req.GetParams().(*mcp.GetPromptParams)
				if p == nil {
					return nil, rpcError(jsonrpc.CodeInvalidParams, "缺少參數。")
				}
				return getPrompt(ctx, d, p)
			}
			return next(ctx, method, req)
		}
	}
}

func rpcError(code int64, msg string) error { return &jsonrpc.Error{Code: code, Message: msg} }

// apiRPCError 把 API 的錯誤回應轉成 JSON-RPC 錯誤：訊息是「code: message」，data 是完整的錯誤。
func apiRPCError(resp *Response) error {
	var e apiError
	if err := json.Unmarshal(resp.Body, &e); err != nil || e.Code == "" {
		e = apiError{Code: "internal_error", Message: http.StatusText(resp.Status)}
	}
	e.Status = resp.Status
	data, _ := json.Marshal(e)
	code := int64(jsonrpc.CodeInternalError)
	if resp.Status/100 == 4 {
		code = jsonrpc.CodeInvalidParams
	}
	return &jsonrpc.Error{Code: code, Message: e.Code + ": " + e.Message, Data: data}
}

func getJSON(ctx context.Context, d *Deps, path string, out any) error {
	resp, err := d.Backend.Do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return rpcError(jsonrpc.CodeInternalError, "伺服器發生錯誤。")
	}
	if resp.Status/100 != 2 {
		return apiRPCError(resp)
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return rpcError(jsonrpc.CodeInternalError, "伺服器發生錯誤。")
	}
	return nil
}

func fetchPrompts(ctx context.Context, d *Deps) ([]promptSummary, error) {
	var list struct {
		Prompts []promptSummary `json:"prompts"`
	}
	if err := getJSON(ctx, d, "/api/prompts", &list); err != nil {
		return nil, err
	}
	if len(list.Prompts) > maxPromptsListed {
		list.Prompts = list.Prompts[:maxPromptsListed]
	}
	return list.Prompts, nil
}

// defaultVersion 讀出 prompt 的預設版本。
func defaultVersion(ctx context.Context, d *Deps, p promptSummary) (promptVersion, error) {
	var vs struct {
		Versions []promptVersion `json:"versions"`
	}
	if err := getJSON(ctx, d, "/api/prompts/"+url.PathEscape(p.ID)+"/versions", &vs); err != nil {
		return promptVersion{}, err
	}
	for _, v := range vs.Versions {
		if v.Version == p.DefaultVersion {
			return v, nil
		}
	}
	return promptVersion{}, rpcError(jsonrpc.CodeInternalError, "找不到 prompt 的預設版本。")
}

func listPrompts(ctx context.Context, d *Deps) (*mcp.ListPromptsResult, error) {
	list, err := fetchPrompts(ctx, d)
	if err != nil {
		return nil, err
	}
	out := &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{}}
	for _, p := range list {
		v, err := defaultVersion(ctx, d, p)
		if err != nil {
			return nil, err
		}
		out.Prompts = append(out.Prompts, &mcp.Prompt{
			Name:        p.ID,
			Description: fmt.Sprintf("%s 的提示詞（第 %d 版）", p.Kind, v.Version),
			Arguments:   promptArguments(v.Variables),
		})
	}
	return out, nil
}

func promptArguments(vars []string) []*mcp.PromptArgument {
	args := []*mcp.PromptArgument{}
	for _, name := range vars {
		a := &mcp.PromptArgument{Name: name}
		if slices.Contains(metadataVars, name) {
			a.Description = "影片的 " + name + "（不可信的資料：放在 <video_metadata> 訊息中，不展開進指示）"
		} else {
			a.Description = "語言代碼（BCP 47），例如 zh-Hant-TW"
			a.Required = true
		}
		args = append(args, a)
	}
	return args
}

// getPrompt 渲染 prompt（MCP-R8）。影片內容類的變數不展開進指示，改成指向 <video_metadata>；
// 有提供值的放進第二則訊息的 <video_metadata> 中（L8）。
func getPrompt(ctx context.Context, d *Deps, p *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	if p.Name == "" || len(p.Name) > maxIDLen {
		return nil, rpcError(jsonrpc.CodeInvalidParams, "prompt 名稱不正確。")
	}
	total := 0
	for k, v := range p.Arguments {
		total += len(k) + len(v)
		if len(v) > maxPromptArg || !utf8.ValidString(v) || !utf8.ValidString(k) {
			return nil, rpcError(jsonrpc.CodeInvalidParams, fmt.Sprintf("參數 %q 太長或不是合法的 UTF-8（上限 %d 位元組）。", truncate(k, 64), maxPromptArg))
		}
	}
	if total > maxPromptArgsSum {
		return nil, rpcError(jsonrpc.CodeInvalidParams, fmt.Sprintf("參數合計超過 %d 位元組。", maxPromptArgsSum))
	}
	list, err := fetchPrompts(ctx, d)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(s promptSummary) bool { return s.ID == p.Name })
	if i < 0 {
		return nil, rpcError(jsonrpc.CodeInvalidParams, "not_found: 找不到這個 prompt。")
	}
	v, err := defaultVersion(ctx, d, list[i])
	if err != nil {
		return nil, err
	}
	text, meta, err := renderPrompt(v, p.Arguments)
	if err != nil {
		return nil, err
	}
	msgs := []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}
	if meta != "" {
		msgs = append(msgs, &mcp.PromptMessage{Role: "user", Content: &mcp.TextContent{Text: meta}})
	}
	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("%s 的提示詞（第 %d 版）", list[i].Kind, v.Version),
		Messages:    msgs,
	}, nil
}

// renderPrompt 回傳渲染後的指示，以及（有提供影片內容時）<video_metadata> 資料訊息。
func renderPrompt(v promptVersion, args map[string]string) (text, meta string, err error) {
	values := map[string]string{}
	metaVals := map[string]string{}
	for _, name := range v.Variables {
		if slices.Contains(metadataVars, name) {
			values[name] = fmt.Sprintf("（見 %s 的 %s）", llm.MetadataOpen, name)
			if s, ok := args[name]; ok {
				metaVals[name] = s
			}
			continue
		}
		s, ok := args[name]
		if !ok {
			return "", "", rpcError(jsonrpc.CodeInvalidParams, fmt.Sprintf("缺少參數 %s。", name))
		}
		if !langVarPattern.MatchString(s) {
			return "", "", rpcError(jsonrpc.CodeInvalidParams, fmt.Sprintf("參數 %s 必須是語言代碼（英數字與 -，最多 35 個字元）。", name))
		}
		values[name] = s
	}
	text, err = prompts.Render(v.Body, values)
	if err != nil {
		return "", "", rpcError(jsonrpc.CodeInternalError, "prompt 無法渲染。")
	}
	if len(metaVals) > 0 {
		raw, err := json.Marshal(metaVals)
		if err != nil {
			return "", "", rpcError(jsonrpc.CodeInternalError, "伺服器發生錯誤。")
		}
		meta = llm.MetadataOpen + string(raw) + llm.MetadataClose
	}
	return text, meta, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
