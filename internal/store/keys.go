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

// RotateWrappedKey 在單一交易中把現行資料金鑰的包裝換成 wrapped（SEC-R8），不保留舊的包裝：
// 留著的話，拿到資料庫與舊 master key 的人仍能解開同一把資料金鑰，輪替就沒有撤銷舊 key 的效果。
// commit 之後 checkpoint 並截斷 WAL：新的頁面寫回主檔、蓋掉舊的包裝，舊的頁面也不會留在 -wal 檔中。
// 輪替之前的備份與 migration 快照仍保有舊的包裝。
// 沒有現行金鑰時回傳 ErrNotFound。
func (s *Store) RotateWrappedKey(ctx context.Context, wrapped string) error {
	err := s.Tx(ctx, func(tx *Repo) error {
		n, err := tx.q.RewrapActiveKey(ctx, wrapped)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return tx.q.DeleteRetiredKeys(ctx)
	})
	if err != nil {
		return err
	}
	// 盡力而為：有讀取中的連線時 checkpoint 可能無法截斷，WAL 的舊 frame 之後也會被新的寫入覆蓋。
	_, _ = s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return nil
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
