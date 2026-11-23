package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ErrNoteTemplateExists 表示已經有同 id 的筆記模板。
var ErrNoteTemplateExists = errors.New("store: 已經有同 id 的筆記模板")

// NoteTemplate 是筆記模板的一個版本（NOTE-R11、ADR-0020 第 5 點）。版本建立後不再修改；PATCH 會新增下一個版本。
// RulesJSON 是模板規則（read_button、要執行的檢查），JSON 物件。
type NoteTemplate struct {
	ID        string
	Version   int
	Name      string
	Body      string
	RulesJSON string
	SHA256    string
	CreatedAt time.Time
}

func noteTemplateFromRow(row sqlcdb.NoteTemplate) NoteTemplate {
	return NoteTemplate{
		ID: row.ID, Version: int(row.Version), Name: row.Name, Body: row.Body, RulesJSON: row.RulesJson,
		SHA256: row.Sha256, CreatedAt: fromMs(row.CreatedAt),
	}
}

// NoteTemplates 依 id 排序列出每個模板的最新版本。
func (r *Repo) NoteTemplates(ctx context.Context) ([]NoteTemplate, error) {
	rows, err := r.q.ListLatestNoteTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NoteTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, noteTemplateFromRow(row))
	}
	return out, nil
}

// LatestNoteTemplate 回傳模板的最新版本；不存在時回傳 ErrNotFound。
func (r *Repo) LatestNoteTemplate(ctx context.Context, id string) (NoteTemplate, error) {
	row, err := r.q.LatestNoteTemplate(ctx, id)
	if err != nil {
		return NoteTemplate{}, notFound(err)
	}
	return noteTemplateFromRow(row), nil
}

// NoteTemplateVersion 回傳模板的指定版本；不存在時回傳 ErrNotFound。
func (r *Repo) NoteTemplateVersion(ctx context.Context, id string, version int) (NoteTemplate, error) {
	row, err := r.q.GetNoteTemplateVersion(ctx, sqlcdb.GetNoteTemplateVersionParams{ID: id, Version: int64(version)})
	if err != nil {
		return NoteTemplate{}, notFound(err)
	}
	return noteTemplateFromRow(row), nil
}

// AddNoteTemplateVersion 在同一個交易中新增模板的下一個版本。create 為 true 時建立新模板（版本 1），
// id 已存在回傳 ErrNoteTemplateExists；為 false 時模板必須已經存在，否則回傳 ErrNotFound。
func (r *Repo) AddNoteTemplateVersion(ctx context.Context, create bool, t NoteTemplate) (NoteTemplate, error) {
	var out NoteTemplate
	err := r.inTx(ctx, func(tx *Repo) error {
		next := 1
		cur, err := tx.q.LatestNoteTemplate(ctx, t.ID)
		switch {
		case err == nil && create:
			return ErrNoteTemplateExists
		case err == nil:
			next = int(cur.Version) + 1
		case !errors.Is(err, sql.ErrNoRows):
			return err
		case !create:
			return ErrNotFound
		}
		if err := tx.q.InsertNoteTemplate(ctx, sqlcdb.InsertNoteTemplateParams{
			ID: t.ID, Version: int64(next), Name: t.Name, Body: t.Body, RulesJson: t.RulesJSON, Sha256: t.SHA256, Now: tx.nowMs(),
		}); err != nil {
			return err
		}
		out, err = tx.NoteTemplateVersion(ctx, t.ID, next)
		return err
	})
	return out, err
}
