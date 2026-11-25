package store

import (
	"context"
	"database/sql"
	"errors"
)

// 這個檔案是 GET /api/items/:id/chunks（LIB-R29）的唯讀查詢。直接寫 SQL、不經過 sqlc（同 SubscriptionNewItems），
// 只讀不寫，所以用唯讀連線池。

// LatestLLMChunkRun 回傳影音的這個步驟最近一次切分所在的 stage run（和 LatestLLMChunkNumbers 是同一個 stage run，
// POST /rerun 的 chunks 也是對照它）；從來沒有切分過時回傳空字串。
func (r *Repo) LatestLLMChunkRun(ctx context.Context, itemID, stage string) (string, error) {
	var id string
	err := r.s.r.QueryRowContext(ctx, `SELECT stage_run_id FROM llm_chunks
WHERE item_id = ? AND stage = ?
ORDER BY created_at DESC, rowid DESC LIMIT 1`, itemID, stage).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ChunkTaskStates 回傳 stage run 每個 chunk 最近一個任務的狀態（chunk 編號 → tasks.state）；沒有任務的 chunk 不在其中。
func (r *Repo) ChunkTaskStates(ctx context.Context, stageRunID string) (map[int]string, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT chunk_index, state FROM tasks
WHERE stage_run_id = ? AND chunk_index IS NOT NULL
ORDER BY created_at, rowid`, stageRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var chunk int
		var state string
		if err := rows.Scan(&chunk, &state); err != nil {
			return nil, err
		}
		out[chunk] = state // 依建立順序，後面的任務蓋過前面的
	}
	return out, rows.Err()
}
