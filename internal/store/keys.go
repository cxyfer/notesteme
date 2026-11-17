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

// RotateWrappedKey 在單一交易中將現行資料金鑰標記為已淘汰，並寫入重新加密後的新資料金鑰。
// 沒有現行金鑰時回傳 ErrNotFound。
func (s *Store) RotateWrappedKey(ctx context.Context, wrapped string) error {
	return s.Tx(ctx, func(tx *Repo) error {
		n, err := tx.q.RetireActiveKey(ctx, tx.nowMs())
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return tx.q.InsertActiveKey(ctx, sqlcdb.InsertActiveKeyParams{
			ID:         NewID(),
			WrappedKey: wrapped,
			CreatedAt:  tx.nowMs(),
		})
	})
}

// ResetSecretsAndKey 在單一交易中刪除所有 secrets 與既有資料金鑰，並寫入新的現行資料金鑰（SEC-R9）。
// 其他所有資料表（影音、產出、步驟、設定、session、token、成本、事件等）皆維持原樣。
func (s *Store) ResetSecretsAndKey(ctx context.Context, wrapped string) error {
	return s.Tx(ctx, func(tx *Repo) error {
		if err := tx.q.DeleteAllSecrets(ctx); err != nil {
			return err
		}
		if err := tx.q.DeleteAllEncryptionKeys(ctx); err != nil {
			return err
		}
		return tx.q.InsertActiveKey(ctx, sqlcdb.InsertActiveKeyParams{
			ID:         NewID(),
			WrappedKey: wrapped,
			CreatedAt:  tx.nowMs(),
		})
	})
}
