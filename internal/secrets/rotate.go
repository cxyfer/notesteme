package secrets

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/cxyfer/notesteme/internal/store"
)

var (
	// ErrEmptyMasterKey 表示呼叫時提供的 master key 為空字串。
	ErrEmptyMasterKey = errors.New("master key 不可為空。")
	// ErrNoDataKey 表示資料庫尚未建立現行資料金鑰，無法輪替。
	ErrNoDataKey = errors.New("資料庫尚未建立資料金鑰。")
)

func kdfFor(lowCost bool) KDFParams {
	if lowCost {
		return TestKDF
	}
	return ProductionKDF
}

// RotateMasterKey 以 currentMaster 解開現行資料金鑰，改用 newMaster 重新包裝後，
// 在單一資料庫交易中以新的包裝覆寫舊的，不保留舊 master key 包裝的資料金鑰（SEC-R8）。
// currentMaster 不正確時回傳 ErrWrongMasterKey，資料庫不做任何變更。
func RotateMasterKey(ctx context.Context, st *store.Store, currentMaster, newMaster string, lowCost bool) error {
	if currentMaster == "" || newMaster == "" {
		return ErrEmptyMasterKey
	}
	wrapped, ok, err := st.ActiveWrappedKey(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoDataKey
	}
	dk, err := unwrap(currentMaster, wrapped)
	if err != nil {
		return err
	}
	newWrapped, err := wrap(newMaster, dk, kdfFor(lowCost))
	if err != nil {
		return err
	}
	return st.RotateWrappedKey(ctx, newWrapped)
}

// ResetSecrets 刪除所有加密 secrets 與舊資料金鑰，並以 master 重新建立一把新的資料金鑰（SEC-R9）。
// 單一資料庫交易執行，其他非 secret 資料表（影音、產出、設定、session、token 等）皆保留。
func ResetSecrets(ctx context.Context, st *store.Store, master string, lowCost bool) error {
	if master == "" {
		return ErrEmptyMasterKey
	}
	dk := make([]byte, keyLen)
	if _, err := rand.Read(dk); err != nil {
		return err
	}
	wrapped, err := wrap(master, dk, kdfFor(lowCost))
	if err != nil {
		return err
	}
	return st.ResetSecretsAndKey(ctx, wrapped)
}
