package api

import (
	"context"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案是 LLM chunk 的時間範圍（LIB-R29）：GET /api/items/{id}/chunks?stage=&from=&to=。
// 時間帶用它畫出各 chunk 處理到哪裡；拖曳選取的時間範圍用 from、to 換成 POST /rerun 的 chunks。

func (rt *routes) registerChunks(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-chunks", Method: http.MethodGet, Path: "/api/items/{id}/chunks",
		Summary: "列出影音的 LLM 步驟（segment、correct、translate）最近一次切分的每個 chunk 的時間範圍與狀態（LIB-R29）；" +
			"編號和 POST /api/items/{id}/rerun 的 chunks 相同。from、to（秒）只列時間範圍和 [from, to) 重疊的 chunk",
	}, auth.ScopeRead, rt.listChunks)
}

// ChunkSpanBody 是一個 LLM chunk（LIB-R29）。
type ChunkSpanBody struct {
	Stage  string `json:"stage" enum:"segment,correct,translate"`
	Chunk  int    `json:"chunk" doc:"chunk 編號，從 1 起算，就是 POST /api/items/{id}/rerun 的 chunks（translate 是各目標的全域編號）"`
	Status string `json:"status" enum:"pending,running,failed,done,fallback" doc:"done、fallback（驗證失敗後退回，LLM-R5）是 chunk 的結果；還沒有結果時依最近一個任務：running（處理中或等待 Batch）、failed、pending"`
	Lang   string `json:"lang,omitempty" doc:"translate 的目標語言；其他步驟省略"`
	Start  string `json:"start,omitempty" doc:"這個 chunk 負責輸出的第一句的開始時間（HH:MM:SS.mmm；不含開頭和前一個 chunk 重疊的句子）；輸入的字幕讀不到時省略"`
	End    string `json:"end,omitempty" doc:"這個 chunk 負責輸出的最後一句的結束時間（HH:MM:SS.mmm）；輸入的字幕讀不到時省略"`
	// Current 給時間帶判斷要不要畫：false 的切分是更早的執行留下的。
	Current bool `json:"current" doc:"這次切分屬於這個步驟目前（最新）的 stage run；false 是更早的執行的切分（例如之後的執行整個重用了快取，或新的執行還沒有切分）"`
}

type chunksOutput struct {
	Body struct {
		Chunks []ChunkSpanBody `json:"chunks"`
	}
}

type chunksInput struct {
	ID    string  `path:"id" doc:"影音 ID 或代號"`
	Stage string  `query:"stage" doc:"只列這個步驟（segment、correct、translate）；省略時全部"`
	From  float64 `query:"from" minimum:"0" doc:"秒；只列結束時間晚於它的 chunk。省略時是 0"`
	To    float64 `query:"to" minimum:"0" doc:"秒；只列開始時間早於它的 chunk。省略或 0 代表沒有上限；不是 0 時必須大於 from"`
}

func (rt *routes) listChunks(ctx context.Context, in *chunksInput) (*chunksOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	var stages []string
	if in.Stage != "" {
		if !pipeline.IsLLMStage(in.Stage) {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "stage 必須是 segment、correct 或 translate。")
		}
		stages = []string{in.Stage}
	}
	if math.IsNaN(in.From) || math.IsInf(in.From, 0) || math.IsNaN(in.To) || math.IsInf(in.To, 0) || (in.To > 0 && in.To <= in.From) {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "to 必須大於 from。")
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	spans, err := rt.d.Pipeline.ItemChunks(ctx, id, stages)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	fromMS, toMS := int64(math.Round(in.From*1000)), int64(math.Round(in.To*1000))
	filter := in.From > 0 || in.To > 0
	out := &chunksOutput{}
	out.Body.Chunks = make([]ChunkSpanBody, 0, len(spans))
	for _, s := range spans {
		if filter && !s.Overlaps(fromMS, toMS) {
			continue
		}
		b := ChunkSpanBody{Stage: s.Stage, Chunk: s.Chunk, Status: s.Status, Lang: s.Lang, Current: s.Current}
		if s.HasTime {
			b.Start = pipeline.FrameTimeText(s.StartMS)
			b.End = pipeline.FrameTimeText(s.EndMS)
		}
		out.Body.Chunks = append(out.Body.Chunks, b)
	}
	return out, nil
}
