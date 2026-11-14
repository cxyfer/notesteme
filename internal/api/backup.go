package api

import (
	"context"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// BackupSource 產生備份：把 tar.gz 寫到 w（OPS-R1）。server 不寫入 client 指定的路徑。
// 由 OPS-A 擴充：方法簽名可以在實作時調整（例如回傳檔名與 manifest）。
type BackupSource interface {
	Backup(ctx context.Context, w io.Writer) error
}

func (rt *routes) registerBackup(api huma.API) {
	// 骨架：Deps.Backup 還沒有實作，一律回 501。
	huma.Register(api, huma.Operation{
		OperationID: "backup-create", Method: http.MethodPost, Path: "/api/backup",
		Summary: "備份：回傳 tar.gz 串流（尚未實作）",
	}, notImplemented)
}
