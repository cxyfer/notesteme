package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ErrPromptExists 表示已經有同 id 的 prompt。
var ErrPromptExists = errors.New("store: 已經有同 id 的 prompt")

// Prompt 是使用者 prompt 的摘要（PRM）。DefaultVersion 只有 SetPromptDefault 會改變；
// LatestVersion 是最新儲存的版本。
type Prompt struct {
	ID             string
	Kind           string
	DefaultVersion int
	LatestVersion  int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PromptVersion 是 prompt 的一個版本（不可修改，PRM-R2）。VariablesJSON 是內文用到的變數（JSON 字串陣列）。
type PromptVersion struct {
	PromptID      string
	Version       int
	Body          string
	SHA256        string
	VariablesJSON string
	CreatedAt     time.Time
}

func promptFromRow(row sqlcdb.Prompt) Prompt {
	return Prompt{
		ID: row.ID, Kind: row.Kind, DefaultVersion: int(row.DefaultVersion), LatestVersion: int(row.LatestVersion),
		CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt),
	}
}

func promptVersionFromRow(row sqlcdb.PromptVersion) PromptVersion {
	return PromptVersion{
		PromptID: row.PromptID, Version: int(row.Version), Body: row.Body, SHA256: row.Sha256,
		VariablesJSON: row.VariablesJson, CreatedAt: fromMs(row.CreatedAt),
	}
}

// Prompts 依 id 排序列出所有使用者的 prompt。
func (r *Repo) Prompts(ctx context.Context) ([]Prompt, error) {
	rows, err := r.q.ListPrompts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Prompt, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptFromRow(row))
	}
	return out, nil
}

// Prompt 以 id 查詢；不存在時回傳 ErrNotFound。
func (r *Repo) Prompt(ctx context.Context, id string) (Prompt, error) {
	row, err := r.q.GetPrompt(ctx, id)
	if err != nil {
		return Prompt{}, notFound(err)
	}
	return promptFromRow(row), nil
}

// CreatePrompt 在同一個交易中建立 prompt 與它的版本 1（也是預設版本）。id 已存在回傳 ErrPromptExists。
func (r *Repo) CreatePrompt(ctx context.Context, id, kind, body, sha, variablesJSON string) (Prompt, PromptVersion, error) {
	var (
		p Prompt
		v PromptVersion
	)
	err := r.inTx(ctx, func(tx *Repo) error {
		if _, err := tx.q.GetPrompt(ctx, id); err == nil {
			return ErrPromptExists
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := tx.nowMs()
		if err := tx.q.InsertPrompt(ctx, sqlcdb.InsertPromptParams{ID: id, Kind: kind, Now: now}); err != nil {
			return err
		}
		if err := tx.q.InsertPromptVersion(ctx, sqlcdb.InsertPromptVersionParams{
			PromptID: id, Version: 1, Body: body, Sha256: sha, VariablesJson: variablesJSON, Now: now,
		}); err != nil {
			return err
		}
		var err error
		if p, err = tx.Prompt(ctx, id); err != nil {
			return err
		}
		v, err = tx.PromptVersion(ctx, id, 1)
		return err
	})
	if err != nil {
		return Prompt{}, PromptVersion{}, err
	}
	return p, v, nil
}

// AddPromptVersion 在同一個交易中新增下一個版本，不改變 default_version（PRM-R5）。prompt 不存在回傳 ErrNotFound。
func (r *Repo) AddPromptVersion(ctx context.Context, id, body, sha, variablesJSON string) (PromptVersion, error) {
	var v PromptVersion
	err := r.inTx(ctx, func(tx *Repo) error {
		now := tx.nowMs()
		next, err := tx.q.BumpPromptLatest(ctx, sqlcdb.BumpPromptLatestParams{ID: id, Now: now})
		if err != nil {
			return notFound(err)
		}
		if err := tx.q.InsertPromptVersion(ctx, sqlcdb.InsertPromptVersionParams{
			PromptID: id, Version: next, Body: body, Sha256: sha, VariablesJson: variablesJSON, Now: now,
		}); err != nil {
			return err
		}
		v, err = tx.PromptVersion(ctx, id, int(next))
		return err
	})
	return v, err
}

// PromptVersion 讀取一個版本；prompt 或版本不存在時回傳 ErrNotFound。
func (r *Repo) PromptVersion(ctx context.Context, id string, version int) (PromptVersion, error) {
	row, err := r.q.GetPromptVersion(ctx, sqlcdb.GetPromptVersionParams{PromptID: id, Version: int64(version)})
	if err != nil {
		return PromptVersion{}, notFound(err)
	}
	return promptVersionFromRow(row), nil
}

// PromptVersions 依版本遞增列出 prompt 的所有版本；prompt 不存在時是空的。
func (r *Repo) PromptVersions(ctx context.Context, id string) ([]PromptVersion, error) {
	rows, err := r.q.ListPromptVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]PromptVersion, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptVersionFromRow(row))
	}
	return out, nil
}

// SetPromptDefault 把 default_version 設成 version；prompt 或版本不存在時回傳 ErrNotFound，資料不變。
func (r *Repo) SetPromptDefault(ctx context.Context, id string, version int) error {
	n, err := r.q.SetPromptDefault(ctx, sqlcdb.SetPromptDefaultParams{ID: id, Version: int64(version), Now: r.nowMs()})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("prompt %s 的版本 %d：%w", id, version, ErrNotFound)
	}
	return nil
}

// DeletePrompt 硬刪除 prompt 與它的所有版本；回傳是否真的刪到一筆。
func (r *Repo) DeletePrompt(ctx context.Context, id string) (bool, error) {
	n, err := r.q.DeletePrompt(ctx, id)
	return n > 0, err
}
