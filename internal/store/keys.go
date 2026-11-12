package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ActiveWrappedKey 回傳現行資料金鑰（已由 master key 加密）；還沒有時 ok 為 false。
func (s *Store) ActiveWrappedKey(ctx context.Context) (wrapped string, ok bool, err error) {
	wrapped, err = s.rq.ActiveWrappedKey(ctx)
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
	n, err := s.wq.InsertWrappedKey(ctx, sqlcdb.InsertWrappedKeyParams{ID: id.String(), WrappedKey: wrapped, CreatedAt: s.nowMs()})
	return n > 0, err
}
