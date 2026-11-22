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
	Reused    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func llmChunkFromRow(row sqlcdb.LlmChunk) LLMChunk {
	c := LLMChunk{
		StageRunID: row.StageRunID, Chunk: int(row.Chunk), ItemID: row.ItemID, Stage: row.Stage,
		CacheKey: row.CacheKey, Status: row.Status, Reason: row.Reason, Plan: json.RawMessage(row.PlanJson),
		Usage: json.RawMessage(row.UsageJson), Detail: json.RawMessage(row.DetailJson), Reused: row.Reused != 0,
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

// FinishLLMChunk 寫入 chunk 的結果（done 或 fallback）。chunk 已經不是 pending 時回傳 false，不改變任何資料。
func (r *Repo) FinishLLMChunk(ctx context.Context, stageRunID string, chunk int, status, reason string, output, usage, detail json.RawMessage) (bool, error) {
	n, err := r.q.FinishLLMChunk(ctx, sqlcdb.FinishLLMChunkParams{
		Status: status, Reason: reason, OutputJson: rawPtr(output), UsageJson: rawOr(usage, "{}"), DetailJson: rawOr(detail, "{}"),
		Now: r.nowMs(), StageRunID: stageRunID, Chunk: int64(chunk),
	})
	return n > 0, err
}

// CountPendingLLMChunks 回傳 stage run 還沒有結果的 chunk 數。
func (r *Repo) CountPendingLLMChunks(ctx context.Context, stageRunID string) (int, error) {
	n, err := r.q.CountPendingLLMChunks(ctx, stageRunID)
	return int(n), err
}

// LatestLLMChunkCount 回傳影音的這個步驟最近一次切分的 chunk 數；從來沒有切分過時是 0（POST /rerun 的 chunks，L12）。
func (r *Repo) LatestLLMChunkCount(ctx context.Context, itemID, stage string) (int, error) {
	n, err := r.q.LatestLLMChunkCount(ctx, sqlcdb.LatestLLMChunkCountParams{ItemID: itemID, Stage: stage})
	return int(n), err
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
