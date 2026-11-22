package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 prompt 的試跑與比較（PRM-R6、R7）。它們會呼叫 LLM、花錢，所以要 `ingest` scope；
// 不經過 task queue 與 stage run，也不寫入任何產出。

func (rt *routes) registerPlayground(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "prompts-playground", Method: http.MethodPost, Path: "/api/prompts/playground",
		Summary: "用指定的提示詞版本與 provider／model 對影音的一段字幕試跑一個步驟；不寫入任何產出、不改變步驟狀態，會花錢（PRM-R6）",
	}, auth.ScopeIngest, rt.playground)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-compare", Method: http.MethodPost, Path: "/api/prompts/{id}/compare",
		Summary: "比較提示詞的兩個版本：內文的行級差異；指定影音與範圍時，另外回傳兩者在同一段字幕上的輸出（兩次試跑，花費是試跑的兩倍）（PRM-R7）",
	}, auth.ScopeIngest, rt.comparePrompts)
}

type playgroundInput struct {
	Body struct {
		Prompt   string `json:"prompt" doc:"提示詞引用：<id>、<id>@default 或 <id>@<N>；它的類型決定要試跑哪個步驟"`
		Item     string `json:"item" doc:"影音 ID 或短代號（例如 yt:abc123xxxxx）"`
		From     string `json:"from" doc:"範圍開始，HH:MM:SS 或 MM:SS（可帶小數）"`
		To       string `json:"to" doc:"範圍結束（不含）；範圍內最多 200 句，超過回 422"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
}

// PlaygroundCue 是試跑輸出的一句。
type PlaygroundCue struct {
	Index    int    `json:"index"`
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	Text     string `json:"text"`
	Original string `json:"original,omitempty" doc:"輸入的同一句（輸出與輸入一對一時才有）"`
}

// PlaygroundValidation 是驗證結果。
type PlaygroundValidation struct {
	Passed bool     `json:"passed" doc:"false 代表至少一段驗證重試用盡，該段的輸出是步驟的退回內容"`
	Errors []string `json:"errors"`
}

// PlaygroundUsage 是 provider 回報的 token 用量。
type PlaygroundUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// PlaygroundPrompt 是實際用到的提示詞版本。
type PlaygroundPrompt struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

// PlaygroundBody 是一次試跑的結果。
type PlaygroundBody struct {
	Stage      string               `json:"stage" doc:"試跑的步驟（由提示詞的類型決定）"`
	Prompt     PlaygroundPrompt     `json:"prompt"`
	Cues       int                  `json:"cues" doc:"範圍內送出的句數"`
	Output     []PlaygroundCue      `json:"output"`
	Validation PlaygroundValidation `json:"validation"`
	Usage      PlaygroundUsage      `json:"usage"`
	// EstimateUSD 是花費；內建計價表沒有這個 model 的價格時是 null（沒有 M4 的成本帳本，不寫入帳本）。
	EstimateUSD *float64 `json:"estimate_usd" nullable:"true" doc:"內建計價表沒有這個 model 的價格時是 null"`
}

type playgroundOutput struct{ Body PlaygroundBody }

func playgroundBody(r pipeline.PlaygroundResult) PlaygroundBody {
	b := PlaygroundBody{
		Stage:       r.Stage,
		Prompt:      PlaygroundPrompt{ID: r.Prompt.ID, Version: r.Prompt.Version, SHA256: r.Prompt.SHA256},
		Cues:        r.Inputs,
		Output:      make([]PlaygroundCue, 0, len(r.Output)),
		Validation:  PlaygroundValidation{Passed: r.Passed, Errors: append([]string{}, r.Errors...)},
		Usage:       PlaygroundUsage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens},
		EstimateUSD: r.EstimateUSD,
	}
	for _, c := range r.Output {
		b.Output = append(b.Output, PlaygroundCue{Index: c.Index, StartMS: c.StartMS, EndMS: c.EndMS, Text: c.Text, Original: c.Original})
	}
	return b
}

// parseRange 解析 from、to；格式錯誤回 422（逐欄位）。
func parseRange(from, to string) (fromMS, toMS int64, err error) {
	var fields []validate.FieldError
	if fromMS, err = subtitle.ParseTimecode(from); err != nil {
		fields = append(fields, validate.FieldError{Path: "from", Message: "from 必須是 HH:MM:SS 或 MM:SS（可帶小數）。"})
	}
	if toMS, err = subtitle.ParseTimecode(to); err != nil {
		fields = append(fields, validate.FieldError{Path: "to", Message: "to 必須是 HH:MM:SS 或 MM:SS（可帶小數）。"})
	}
	if len(fields) > 0 {
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, fields[0].Path+"："+fields[0].Message)
		e.Errors = fields
		return 0, 0, e
	}
	return fromMS, toMS, nil
}

func (rt *routes) playground(ctx context.Context, in *playgroundInput) (*playgroundOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	fromMS, toMS, err := parseRange(b.From, b.To)
	if err != nil {
		return nil, err
	}
	res, err := rt.d.Pipeline.Playground(ctx, pipeline.PlaygroundRequest{
		Prompt: b.Prompt, Item: b.Item, FromMS: fromMS, ToMS: toMS, Provider: b.Provider, Model: b.Model,
	})
	if err != nil {
		return nil, rt.mapLLMErr(ctx, err)
	}
	return &playgroundOutput{Body: playgroundBody(res)}, nil
}

type compareInput struct {
	ID   string `path:"id" doc:"提示詞 ID"`
	Body struct {
		A        int    `json:"a" minimum:"1" doc:"版本 a"`
		B        int    `json:"b" minimum:"1" doc:"版本 b"`
		Item     string `json:"item,omitempty" doc:"影音 ID 或短代號；指定時另外在同一段字幕上試跑兩個版本（需要 from、to、provider、model）"`
		From     string `json:"from,omitempty"`
		To       string `json:"to,omitempty"`
		Provider string `json:"provider,omitempty"`
		Model    string `json:"model,omitempty"`
	}
}

// PromptDiffLine 是內文差異的一行。
type PromptDiffLine struct {
	Op   string `json:"op" enum:"equal,delete,insert" doc:"equal：兩邊都有；delete：只在 a；insert：只在 b"`
	Text string `json:"text"`
}

// CompareOutputs 是兩個版本在同一段字幕上的輸出。
type CompareOutputs struct {
	A PlaygroundBody `json:"a"`
	B PlaygroundBody `json:"b"`
}

type compareOutput struct {
	Body struct {
		A        int              `json:"a"`
		B        int              `json:"b"`
		BodyDiff []PromptDiffLine `json:"body_diff" doc:"從 a 到 b 的行級差異，依序"`
		Outputs  *CompareOutputs  `json:"outputs,omitempty" doc:"指定影音與範圍時才有"`
	}
}

func (rt *routes) comparePrompts(ctx context.Context, in *compareInput) (*compareOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	refA, refB := fmt.Sprintf("%s@%d", in.ID, b.A), fmt.Sprintf("%s@%d", in.ID, b.B)
	va, err := svc.Resolve(ctx, refA)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	vb, err := svc.Resolve(ctx, refB)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &compareOutput{}
	out.Body.A, out.Body.B = va.Version, vb.Version
	lines := prompts.Diff(va.Body, vb.Body)
	out.Body.BodyDiff = make([]PromptDiffLine, 0, len(lines))
	for _, l := range lines {
		out.Body.BodyDiff = append(out.Body.BodyDiff, PromptDiffLine{Op: string(l.Op), Text: l.Text})
	}
	if b.Item == "" {
		return out, nil
	}

	// 指定影音：在同一段字幕上各試跑一次（依序，不並行：花費與限流都以兩次計）。
	fromMS, toMS, err := parseRange(b.From, b.To)
	if err != nil {
		return nil, err
	}
	run := func(ref string) (PlaygroundBody, error) {
		res, err := rt.d.Pipeline.Playground(ctx, pipeline.PlaygroundRequest{
			Prompt: ref, Item: b.Item, FromMS: fromMS, ToMS: toMS, Provider: b.Provider, Model: b.Model,
		})
		if err != nil {
			return PlaygroundBody{}, err
		}
		return playgroundBody(res), nil
	}
	ra, err := run(refA)
	if err != nil {
		return nil, rt.mapLLMErr(ctx, err)
	}
	rb, err := run(refB)
	if err != nil {
		return nil, rt.mapLLMErr(ctx, err)
	}
	out.Body.Outputs = &CompareOutputs{A: ra, B: rb}
	return out, nil
}

// 試跑與比較的錯誤碼（呼叫 LLM 的同步請求）。
const (
	CodeNeedsAction = "needs_action"
	CodeRateLimited = "rate_limited"
)

// mapLLMErr 把試跑的錯誤對應到 HTTP：外部服務回報的 *errclass.Error 依分類對應
// （缺 API key、模型不存在是 needs_action → 409，並帶訊息與動作；限流 → 429 並帶 retry_after_s；
// provider 失敗或逾時 → 502；其餘永久失敗 → 422）；其他錯誤交給 mapPromptErr 與 mapErr。
func (rt *routes) mapLLMErr(ctx context.Context, err error) error {
	var ce *errclass.Error
	if !errors.As(err, &ce) {
		return rt.mapPromptErr(ctx, err)
	}
	msg := ce.Message
	if msg == "" {
		msg = "LLM 請求失敗。"
	}
	var e *Error
	switch ce.Class {
	case errclass.ClassNeedsAction, errclass.ClassBudgetBlocked:
		e = newError(http.StatusConflict, CodeNeedsAction, msg)
		e.Actions = ce.Actions
	case errclass.ClassWait:
		e = newError(http.StatusTooManyRequests, CodeRateLimited, msg)
		secs := int((ce.RetryAfter + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		e.RetryAfterS = &secs
	case errclass.ClassRetryable:
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "試跑的 LLM 請求失敗", slog.Any("error", err))
		e = newError(http.StatusBadGateway, CodeUpstream, msg)
	default:
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "試跑失敗", slog.Any("error", err))
		e = newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
	}
	return e
}
