package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// EnsureSnapshot 存入生效設定的快照；相同內容（sha256）已存在時回傳既有的 ID。
func (r *Repo) EnsureSnapshot(ctx context.Context, sha256, configJSON string) (string, error) {
	id, err := r.q.GetSnapshotIDBySHA(ctx, sha256)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = NewID()
	err = r.q.InsertSnapshot(ctx, sqlcdb.InsertSnapshotParams{ID: id, Sha256: sha256, ConfigJson: configJSON, CreatedAt: r.nowMs()})
	return id, err
}

// Snapshot 回傳快照的 JSON 內容。
func (r *Repo) Snapshot(ctx context.Context, id string) (string, error) {
	cfg, err := r.q.SnapshotConfig(ctx, id)
	return cfg, notFound(err)
}

func stageRunFromRow(row sqlcdb.StageRun) (StageRun, error) {
	s := StageRun{
		ID:               row.ID,
		ItemID:           row.ItemID,
		Stage:            row.Stage,
		Lang:             row.Lang,
		Variant:          row.Variant,
		Status:           row.Status,
		BlockReason:      row.BlockReason,
		ResumeOn:         row.ResumeOn,
		ErrorClass:       row.ErrorClass,
		Message:          row.ErrorMessage,
		Executor:         row.Executor,
		CacheKey:         row.CacheKey,
		ImplVersion:      int(row.ImplVersion),
		ConfigSnapshotID: row.ConfigSnapshotID,
		ArtifactID:       deref(row.ArtifactID),
		NotBefore:        msToTime(row.NotBefore),
		StartedAt:        msToTime(row.StartedAt),
		FinishedAt:       msToTime(row.FinishedAt),
		CreatedAt:        fromMs(row.CreatedAt),
	}
	if err := json.Unmarshal([]byte(row.ActionsJson), &s.Actions); err != nil {
		return StageRun{}, err
	}
	return s, nil
}

// InsertStageRun 新增步驟。
func (r *Repo) InsertStageRun(ctx context.Context, n NewStageRun) (StageRun, error) {
	id := NewID()
	err := r.q.InsertStageRun(ctx, sqlcdb.InsertStageRunParams{
		ID:               id,
		ItemID:           n.ItemID,
		Stage:            n.Stage,
		Lang:             n.Lang,
		Variant:          n.Variant,
		Status:           n.Status,
		ConfigSnapshotID: n.ConfigSnapshotID,
		CreatedAt:        r.nowMs(),
	})
	if err != nil {
		return StageRun{}, err
	}
	if err := r.emitStage(ctx, n.ItemID, n.Stage, n.Status); err != nil {
		return StageRun{}, err
	}
	return r.StageRun(ctx, id)
}

// StageRun 以 ID 查詢步驟。
func (r *Repo) StageRun(ctx context.Context, id string) (StageRun, error) {
	row, err := r.q.StageRunByID(ctx, id)
	if err != nil {
		return StageRun{}, notFound(err)
	}
	return stageRunFromRow(row)
}

// StageRunsByItem 依建立順序列出影音的步驟。
func (r *Repo) StageRunsByItem(ctx context.Context, itemID string) ([]StageRun, error) {
	rows, err := r.q.StageRunsByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	var out []StageRun
	for _, row := range rows {
		s, err := stageRunFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ExecutedRunsByItem 列出影音實際執行過（有開始時間）的步驟，依開始時間由新到舊，附上當時的生效設定（PIPE-R13）。
// 同一毫秒開始的以建立順序的相反順序排列，所以後建立的步驟在前。
func (r *Repo) ExecutedRunsByItem(ctx context.Context, itemID string) ([]ExecutedRun, error) {
	rows, err := r.q.ExecutedRunsByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]ExecutedRun, 0, len(rows))
	for _, row := range rows {
		run := ExecutedRun{
			ID: row.ID, Stage: row.Stage, Status: row.Status, FinishedAt: msToTime(row.FinishedAt),
			CacheKey: row.CacheKey, ImplVersion: int(row.ImplVersion), ConfigJSON: row.ConfigJson,
		}
		if row.StartedAt != nil {
			run.StartedAt = fromMs(*row.StartedAt)
		}
		out = append(out, run)
	}
	return out, nil
}

// SetStageState 寫入步驟的狀態與說明。進入 running 時記錄開始時間，進入 done／failed／skipped 時記錄結束時間。
func (r *Repo) SetStageState(ctx context.Context, id string, st StageState) error {
	actions, err := json.Marshal(nonNil(st.Actions))
	if err != nil {
		return err
	}
	now := r.nowMs()
	var started, finished *int64
	switch st.Status {
	case StatusRunning:
		started = &now
	case StatusDone, StatusFailed, StatusSkipped:
		finished = &now
	}
	row, err := r.q.SetStageState(ctx, sqlcdb.SetStageStateParams{
		Status:       st.Status,
		BlockReason:  st.BlockReason,
		ResumeOn:     st.ResumeOn, // SQL 只在 blocked 時保留，其他狀態會清空
		ErrorClass:   st.ErrorClass,
		ErrorMessage: st.Message,
		ActionsJson:  string(actions),
		NotBefore:    msPtr(st.NotBefore),
		StartedAt:    started,
		FinishedAt:   finished,
		ID:           id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil // 沒有這個步驟：沿用過去 :exec 的行為，不視為錯誤，也沒有狀態可以發布
	}
	if err != nil {
		return err
	}
	return r.emitStage(ctx, row.ItemID, row.Stage, row.Status)
}

// SetStageResult 記錄步驟的快取鍵、版本與產出。
func (r *Repo) SetStageResult(ctx context.Context, id string, res StageResult) error {
	return r.q.SetStageResult(ctx, sqlcdb.SetStageResultParams{
		Executor:    res.Executor,
		CacheKey:    res.CacheKey,
		ImplVersion: int64(res.ImplVersion),
		ArtifactID:  res.ArtifactID,
		ID:          id,
	})
}

// DoneStageByCacheKey 找出同一部影音、同一步驟中快取鍵相同且已完成的步驟（PIPE-R5）；沒有時回傳 ErrNotFound。
func (r *Repo) DoneStageByCacheKey(ctx context.Context, itemID, stage, cacheKey string) (StageRun, error) {
	row, err := r.q.DoneStageByCacheKey(ctx, sqlcdb.DoneStageByCacheKeyParams{ItemID: itemID, Stage: stage, CacheKey: cacheKey})
	if err != nil {
		return StageRun{}, notFound(err)
	}
	return stageRunFromRow(row)
}

// BlockedRunsByResume 列出未刪除影音中，每個 (影音, 步驟) 最新一筆為 blocked 且 resume_on 等於 token 的步驟，
// 依建立順序排列。token 的格式見 errclass.Error.ResumeOn；使用者補上 cookie 後，呼叫端用它找出要恢復的步驟。
func (r *Repo) BlockedRunsByResume(ctx context.Context, token string) ([]StageRun, error) {
	rows, err := r.q.BlockedRunsByResume(ctx, token)
	if err != nil {
		return nil, err
	}
	out := make([]StageRun, 0, len(rows))
	for _, row := range rows {
		s, err := stageRunFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
