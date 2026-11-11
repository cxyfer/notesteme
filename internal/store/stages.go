package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// EnsureSnapshot 存入生效設定的快照；相同內容（sha256）已存在時回傳既有的 ID。
func (r *Repo) EnsureSnapshot(ctx context.Context, sha256, configJSON string) (string, error) {
	var id string
	err := r.q.QueryRowContext(ctx, `SELECT id FROM config_snapshots WHERE sha256 = ?`, sha256).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = NewID()
	_, err = r.q.ExecContext(ctx,
		`INSERT INTO config_snapshots (id, sha256, config_json, created_at) VALUES (?, ?, ?, ?)`,
		id, sha256, configJSON, r.nowMs())
	return id, err
}

// Snapshot 回傳快照的 JSON 內容。
func (r *Repo) Snapshot(ctx context.Context, id string) (string, error) {
	var cfg string
	return cfg, scanOne(r.q.QueryRowContext(ctx, `SELECT config_json FROM config_snapshots WHERE id = ?`, id), &cfg)
}

const stageColumns = `id, item_id, stage, lang, variant, status, block_reason, error_class, error_message,
	actions_json, executor, cache_key, impl_version, config_snapshot_id, COALESCE(artifact_id, ''),
	not_before, started_at, finished_at, created_at`

func scanStageRun(sc scanner) (StageRun, error) {
	var (
		s                            StageRun
		actions                      string
		notBefore, started, finished *int64
		created                      int64
	)
	err := sc.Scan(&s.ID, &s.ItemID, &s.Stage, &s.Lang, &s.Variant, &s.Status, &s.BlockReason, &s.ErrorClass,
		&s.Message, &actions, &s.Executor, &s.CacheKey, &s.ImplVersion, &s.ConfigSnapshotID, &s.ArtifactID,
		&notBefore, &started, &finished, &created)
	if err != nil {
		return StageRun{}, err
	}
	if err := json.Unmarshal([]byte(actions), &s.Actions); err != nil {
		return StageRun{}, err
	}
	s.NotBefore, s.StartedAt, s.FinishedAt = msToTime(notBefore), msToTime(started), msToTime(finished)
	s.CreatedAt = fromMs(created)
	return s, nil
}

// InsertStageRun 新增步驟。
func (r *Repo) InsertStageRun(ctx context.Context, n NewStageRun) (StageRun, error) {
	id := NewID()
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO stage_runs (id, item_id, stage, lang, variant, status, config_snapshot_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.ItemID, n.Stage, n.Lang, n.Variant, n.Status, n.ConfigSnapshotID, r.nowMs())
	if err != nil {
		return StageRun{}, err
	}
	return r.StageRun(ctx, id)
}

// StageRun 以 ID 查詢步驟。
func (r *Repo) StageRun(ctx context.Context, id string) (StageRun, error) {
	s, err := scanStageRun(r.q.QueryRowContext(ctx, `SELECT `+stageColumns+` FROM stage_runs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StageRun{}, ErrNotFound
	}
	return s, err
}

// StageRunsByItem 依建立順序列出影音的步驟。
func (r *Repo) StageRunsByItem(ctx context.Context, itemID string) ([]StageRun, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+stageColumns+` FROM stage_runs WHERE item_id = ? ORDER BY created_at, rowid`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StageRun
	for rows.Next() {
		s, err := scanStageRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetStageState 寫入步驟的狀態與說明。進入 running 時記錄開始時間，進入 done／failed／skipped 時記錄結束時間。
func (r *Repo) SetStageState(ctx context.Context, id string, st StageState) error {
	actions, err := json.Marshal(nonNil(st.Actions))
	if err != nil {
		return err
	}
	now := r.nowMs()
	var started, finished any
	switch st.Status {
	case StatusRunning:
		started = now
	case StatusDone, StatusFailed, StatusSkipped:
		finished = now
	}
	_, err = r.q.ExecContext(ctx,
		`UPDATE stage_runs SET status = ?, block_reason = ?, error_class = ?, error_message = ?, actions_json = ?,
		   not_before = ?, started_at = COALESCE(?, started_at), finished_at = ?
		 WHERE id = ?`,
		st.Status, st.BlockReason, st.ErrorClass, st.Message, string(actions),
		msPtr(st.NotBefore), started, finished, id)
	return err
}

// SetStageResult 記錄步驟的快取鍵、版本與產出。
func (r *Repo) SetStageResult(ctx context.Context, id string, res StageResult) error {
	_, err := r.q.ExecContext(ctx,
		`UPDATE stage_runs SET executor = ?, cache_key = ?, impl_version = ?, artifact_id = NULLIF(?, '') WHERE id = ?`,
		res.Executor, res.CacheKey, res.ImplVersion, res.ArtifactID, id)
	return err
}

// DoneStageByCacheKey 找出同一部影音、同一步驟中快取鍵相同且已完成的步驟（PIPE-R5）；沒有時回傳 ErrNotFound。
func (r *Repo) DoneStageByCacheKey(ctx context.Context, itemID, stage, cacheKey string) (StageRun, error) {
	s, err := scanStageRun(r.q.QueryRowContext(ctx,
		`SELECT `+stageColumns+` FROM stage_runs
		 WHERE item_id = ? AND stage = ? AND cache_key = ? AND status = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		itemID, stage, cacheKey, StatusDone))
	if err == sql.ErrNoRows {
		return StageRun{}, ErrNotFound
	}
	return s, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
