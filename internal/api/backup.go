package api

import (
	"context"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// BackupSource 產生備份（OPS-R1）。server 不寫入 client 指定的路徑：備份只以串流回傳給呼叫端。
type BackupSource interface {
	// Begin 先完成一致的資料庫快照，成功才回傳；之後才開始寫出，所以快照失敗時還能回正常的錯誤回應。
	Begin(ctx context.Context) (Backup, error)
}

// Backup 是一份準備好的備份；用完要 Close（刪除暫存的快照）。
type Backup interface {
	// Name 是建議的檔名，例如 notesteme-backup-20261112T100000Z.tar.gz。
	Name() string
	// WriteTo 把備份以 tar.gz 寫到 w。
	WriteTo(w io.Writer) (int64, error)
	Close() error
}

func (rt *routes) registerBackup(api huma.API) {
	registerOwnerOnly(api, huma.Operation{
		OperationID: "backup-create", Method: http.MethodPost, Path: "/api/backup",
		Summary: "備份：回傳 tar.gz 串流，包含資料庫快照與設定檔，不含 master key（OPS-R1）",
	}, "備份", rt.createBackup)
}

// createBackup 只有 owner（session 或本機）可以使用：備份含有整個資料庫，token 即使帶了 scope 也不行。
// session 的 CSRF 由 authMiddleware 檢查（AUTH-R5）。
func (rt *routes) createBackup(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
	if err := requireOwner(ctx, "備份"); err != nil {
		return nil, err
	}
	if rt.d.Backup == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	b, err := rt.d.Backup.Begin(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "備份失敗", "error", err.Error())
		return nil, asError(err)
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		defer b.Close()
		hc.SetHeader("Content-Type", "application/gzip")
		hc.SetHeader("Content-Disposition", `attachment; filename="`+b.Name()+`"`)
		hc.SetHeader("Cache-Control", "no-store")
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetStatus(http.StatusOK)
		if _, err := b.WriteTo(hc.BodyWriter()); err != nil {
			// 標頭已經送出：記錄後中斷連線，讓 client 看到不完整的串流，而不是一份被截斷卻「成功」的備份。
			rt.d.Log.ErrorContext(hc.Context(), "輸出備份時中斷", "error", err.Error())
			panic(http.ErrAbortHandler)
		}
	}}, nil
}
