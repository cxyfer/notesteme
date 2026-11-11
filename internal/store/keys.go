package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// ActiveWrappedKey 回傳現行資料金鑰（已由 master key 加密）；還沒有時 ok 為 false。
func (s *Store) ActiveWrappedKey(ctx context.Context) (wrapped string, ok bool, err error) {
	err = s.r.QueryRowContext(ctx,
		`SELECT wrapped_key FROM encryption_keys WHERE retired_at IS NULL`).Scan(&wrapped)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return wrapped, true, nil
}

// InsertWrappedKey 在沒有現行金鑰時寫入；已有現行金鑰時回傳 false（不覆寫）。
func (s *Store) InsertWrappedKey(ctx context.Context, wrapped string) (bool, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO encryption_keys (id, wrapped_key, created_at)
		 SELECT ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM encryption_keys WHERE retired_at IS NULL)`,
		id.String(), wrapped, s.nowMs())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
