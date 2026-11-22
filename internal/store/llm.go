package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// LLM chunk 的狀態（llm_chunks.status）。
const (
	ChunkPending  = "pending"
	ChunkDone     = "done"
	ChunkFallback = "fallback"
)

// LLMChunk 是一個 LLM 步驟的 stage run 中的一個 chunk（LLM-R1、R8）。
type LLMChunk struct {
	StageRunID string
	Chunk      int
	ItemID     string
	Stage      string
	CacheKey   string
	Status     string
	Reason     string
	Plan       json.RawMessage
	Output     json.RawMessage // pending 與 fallback 時是 nil
	Usage      json.RawMessage
	// Detail 是退回的補充資料（例如 correct 退回時的相似度）；沒有時是 {}。
	Detail json.RawMessage
	// Reused 為 true 代表這個 chunk 重用了之前相同輸入的結果，沒有呼叫 provider（LLM-R8）。
	Reused bool
	// Executor 是產生輸出的執行方式：api 或 agent（AGT-R10）；Agent 是 token 名稱，ModelReported 是 Agent 自己回報的模型。
	Executor      string
	Agent         string
	ModelReported string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func llmChunkFromRow(row sqlcdb.LlmChunk) LLMChunk {
	c := LLMChunk{
		StageRunID: row.StageRunID, Chunk: int(row.Chunk), ItemID: row.ItemID, Stage: row.Stage,
		CacheKey: row.CacheKey, Status: row.Status, Reason: row.Reason, Plan: json.RawMessage(row.PlanJson),
		Usage: json.RawMessage(row.UsageJson), Detail: json.RawMessage(row.DetailJson), Reused: row.Reused != 0,
		Executor: row.Executor, Agent: row.Agent, ModelReported: row.ModelReported,
		CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt),
	}
	if row.OutputJson != nil {
		c.Output = json.RawMessage(*row.OutputJson)
	}
	return c
}

func rawPtr(b json.RawMessage) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

func rawOr(b json.RawMessage, def string) string {
	if len(b) == 0 {
		return def
	}
	return string(b)
}

// InsertLLMChunk 新增一個 chunk。
func (r *Repo) InsertLLMChunk(ctx context.Context, c LLMChunk) error {
	reused := int64(0)
	if c.Reused {
		reused = 1
	}
	return r.q.InsertLLMChunk(ctx, sqlcdb.InsertLLMChunkParams{
		StageRunID: c.StageRunID, Chunk: int64(c.Chunk), ItemID: c.ItemID, Stage: c.Stage, CacheKey: c.CacheKey,
		Status: c.Status, Reason: c.Reason, PlanJson: rawOr(c.Plan, "{}"), OutputJson: rawPtr(c.Output),
		UsageJson: rawOr(c.Usage, "{}"), DetailJson: rawOr(c.Detail, "{}"), Reused: reused, Now: r.nowMs(),
	})
}

// LLMChunks 依 chunk 編號列出 stage run 的所有 chunk。
func (r *Repo) LLMChunks(ctx context.Context, stageRunID string) ([]LLMChunk, error) {
	rows, err := r.q.LLMChunksByRun(ctx, stageRunID)
	if err != nil {
		return nil, err
	}
	out := make([]LLMChunk, 0, len(rows))
	for _, row := range rows {
		out = append(out, llmChunkFromRow(row))
	}
	return out, nil
}

// LLMChunk 讀取一個 chunk；不存在時回傳 ErrNotFound。
func (r *Repo) LLMChunk(ctx context.Context, stageRunID string, chunk int) (LLMChunk, error) {
	row, err := r.q.LLMChunk(ctx, sqlcdb.LLMChunkParams{StageRunID: stageRunID, Chunk: int64(chunk)})
	if errors.Is(err, sql.ErrNoRows) {
		return LLMChunk{}, ErrNotFound
	}
	if err != nil {
		return LLMChunk{}, err
	}
	return llmChunkFromRow(row), nil
}

// ChunkResult 是寫入 chunk 的結果（FinishLLMChunk）。
type ChunkResult struct {
	Status, Reason        string
	Output, Usage, Detail json.RawMessage
	// By 是產生輸出的執行者；Executor 空字串代表 api。
	By ChunkProducer
}

// ChunkProducer 是 chunk 輸出的來源（AGT-R10）。
type ChunkProducer struct {
	Executor      string // api 或 agent
	Agent         string // agent 的 token 名稱
	ModelReported string // agent 自己回報的模型，未驗證
}

// FinishLLMChunk 寫入 chunk 的結果（done 或 fallback）。chunk 已經不是 pending 時回傳 false，不改變任何資料。
func (r *Repo) FinishLLMChunk(ctx context.Context, stageRunID string, chunk int, res ChunkResult) (bool, error) {
	executor := res.By.Executor
	if executor == "" {
		executor = "api"
	}
	n, err := r.q.FinishLLMChunk(ctx, sqlcdb.FinishLLMChunkParams{
		Status: res.Status, Reason: res.Reason, OutputJson: rawPtr(res.Output), UsageJson: rawOr(res.Usage, "{}"),
		DetailJson: rawOr(res.Detail, "{}"), Executor: executor, Agent: res.By.Agent, ModelReported: res.By.ModelReported,
		Now: r.nowMs(), StageRunID: stageRunID, Chunk: int64(chunk),
	})
	return n > 0, err
}

// CountPendingLLMChunks 回傳 stage run 還沒有結果的 chunk 數。
func (r *Repo) CountPendingLLMChunks(ctx context.Context, stageRunID string) (int, error) {
	n, err := r.q.CountPendingLLMChunks(ctx, stageRunID)
	return int(n), err
}

// LatestLLMChunkNumbers 回傳影音的這個步驟最近一次切分實際存在的 chunk 編號，由小到大；從來沒有切分過時是空的
// （POST /rerun 的 chunks，L12）。編號不一定是 1..n：translate 的編號是各目標的全域編號，被沿用的目標不產生 chunk，會有空洞。
func (r *Repo) LatestLLMChunkNumbers(ctx context.Context, itemID, stage string) ([]int, error) {
	rows, err := r.q.LatestLLMChunkNumbers(ctx, sqlcdb.LatestLLMChunkNumbersParams{ItemID: itemID, Stage: stage})
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(rows))
	for _, c := range rows {
		out = append(out, int(c))
	}
	return out, nil
}

// DoneLLMChunkByCacheKey 回傳同一部影音、同一步驟中，快取鍵相同而且通過驗證的最近一個 chunk（LLM-R8）；沒有時回傳 ErrNotFound。
func (r *Repo) DoneLLMChunkByCacheKey(ctx context.Context, itemID, stage, key string) (LLMChunk, error) {
	row, err := r.q.DoneLLMChunkByCacheKey(ctx, sqlcdb.DoneLLMChunkByCacheKeyParams{ItemID: itemID, Stage: stage, CacheKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return LLMChunk{}, ErrNotFound
	}
	if err != nil {
		return LLMChunk{}, err
	}
	return llmChunkFromRow(row), nil
}
