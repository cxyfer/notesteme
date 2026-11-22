package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// AssStyle 是使用者自訂的 ASS 樣式（OUT-R6）；StyleJSON 是原文行、譯文行與版面。
type AssStyle struct {
	Name      string
	StyleJSON string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func assStyleFromRow(row sqlcdb.AssStyle) AssStyle {
	return AssStyle{Name: row.Name, StyleJSON: row.StyleJson, CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt)}
}

// AssStyles 依名稱排序列出自訂樣式。
func (r *Repo) AssStyles(ctx context.Context) ([]AssStyle, error) {
	rows, err := r.q.ListAssStyles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AssStyle, 0, len(rows))
	for _, row := range rows {
		out = append(out, assStyleFromRow(row))
	}
	return out, nil
}

// AssStyle 以名稱查詢自訂樣式；不存在時回傳 ErrNotFound。
func (r *Repo) AssStyle(ctx context.Context, name string) (AssStyle, error) {
	row, err := r.q.GetAssStyle(ctx, name)
	if err != nil {
		return AssStyle{}, notFound(err)
	}
	return assStyleFromRow(row), nil
}

// CreateAssStyle 建立自訂樣式；已有同名的樣式時回傳 ErrConflict。
func (r *Repo) CreateAssStyle(ctx context.Context, name, styleJSON string) (st AssStyle, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		if _, gerr := tx.q.GetAssStyle(ctx, name); gerr == nil {
			return fmt.Errorf("%w: 已經有同名的樣式 %q", ErrConflict, name)
		} else if !errors.Is(gerr, sql.ErrNoRows) {
			return gerr
		}
		if err := tx.q.InsertAssStyle(ctx, sqlcdb.InsertAssStyleParams{Name: name, StyleJson: styleJSON, Now: tx.nowMs()}); err != nil {
			return err
		}
		var gerr error
		st, gerr = tx.AssStyle(ctx, name)
		return gerr
	})
	return st, err
}

// UpdateAssStyle 取代自訂樣式的內容；不存在時回傳 ErrNotFound。
func (r *Repo) UpdateAssStyle(ctx context.Context, name, styleJSON string) (st AssStyle, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		n, uerr := tx.q.UpdateAssStyle(ctx, sqlcdb.UpdateAssStyleParams{Name: name, StyleJson: styleJSON, Now: tx.nowMs()})
		if uerr != nil {
			return uerr
		}
		if n == 0 {
			return ErrNotFound
		}
		var gerr error
		st, gerr = tx.AssStyle(ctx, name)
		return gerr
	})
	return st, err
}
