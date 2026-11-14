package store

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Secret 是一筆加密後的 secret（例如 kind=cookie、name=bilibili）。
// store 只存取密文；加解密由 internal/secrets 負責。
type Secret struct {
	Kind       string
	Name       string
	Nonce      []byte
	Ciphertext []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SecretInfo 是 secret 的中繼資料，不含 nonce 與密文。
type SecretInfo struct {
	Kind      string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertSecret 寫入或覆寫 (kind, name) 的密文；覆寫時保留 created_at。
func (r *Repo) UpsertSecret(ctx context.Context, kind, name string, nonce, ciphertext []byte) error {
	return r.q.UpsertSecret(ctx, sqlcdb.UpsertSecretParams{Kind: kind, Name: name, Nonce: nonce, Ciphertext: ciphertext, Now: r.nowMs()})
}

// Secret 讀取 (kind, name) 的密文；不存在時回傳 ErrNotFound。
func (r *Repo) Secret(ctx context.Context, kind, name string) (Secret, error) {
	row, err := r.q.GetSecret(ctx, sqlcdb.GetSecretParams{Kind: kind, Name: name})
	if err != nil {
		return Secret{}, notFound(err)
	}
	return Secret{
		Kind: row.Kind, Name: row.Name, Nonce: row.Nonce, Ciphertext: row.Ciphertext,
		CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt),
	}, nil
}

// DeleteSecret 刪除 (kind, name)；回傳是否真的刪到一筆。
func (r *Repo) DeleteSecret(ctx context.Context, kind, name string) (bool, error) {
	n, err := r.q.DeleteSecret(ctx, sqlcdb.DeleteSecretParams{Kind: kind, Name: name})
	return n > 0, err
}

// ListSecrets 依 kind、name 排序列出所有 secret 的中繼資料，不含密文。
func (r *Repo) ListSecrets(ctx context.Context) ([]SecretInfo, error) {
	rows, err := r.q.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SecretInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, SecretInfo{Kind: row.Kind, Name: row.Name, CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt)})
	}
	return out, nil
}
