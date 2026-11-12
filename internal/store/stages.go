package store

import (
	"context"
	"database/sql"
	"encoding/json"

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
	return r.q.SetStageState(ctx, sqlcdb.SetStageStateParams{
		Status:       st.Status,
		BlockReason:  st.BlockReason,
		ErrorClass:   st.ErrorClass,
		ErrorMessage: st.Message,
		ActionsJson:  string(actions),
		NotBefore:    msPtr(st.NotBefore),
		StartedAt:    started,
		FinishedAt:   finished,
		ID:           id,
	})
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

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
