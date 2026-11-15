package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是永久刪除、刪除預覽與到期清理（DEL-R3、R4、R6、R7、R11）。

// 永久刪除前要確定沒有任務還在使用這部影音的檔案（TaskCanceller 定義在 trash.go）；
// worker 在 pipeline 之後才建立，所以用 SetTaskCanceller 接上。
// SetTaskCanceller 設定永久刪除時用來結束殘留任務的 canceller；必須在開始處理請求之前呼叫。
// 沒有設定時（例如單元測試）只在資料庫中取消任務。
func (p *Pipeline) SetTaskCanceller(c TaskCanceller) { p.tasks = c }

// PreviewOutput 是刪除預覽中的一個輸出檔（DEL-R3）；M1 還沒有輸出目標，清單一律是空的。
type PreviewOutput struct {
	Path     string
	Modified bool
}

// Preview 是刪除影音的影響範圍（DEL-R3）。
type Preview struct {
	// Artifacts 是所有版本的產出數。
	Artifacts int
	// Bytes 是產出加上 media、uploads 的大小。
	Bytes int64
	// SpentUSD 是這部影音實際花費（狀態為 actual）的總和。
	SpentUSD float64
	Outputs  []PreviewOutput
}

// itemDataDirs 是資料目錄下依影音 ID 分目錄存放的子目錄；永久刪除時整個目錄都要刪掉，包括目錄本身。
var itemDataDirs = []string{"artifacts", "media", "uploads"}

// itemDir 回傳影音在資料目錄 sub 子目錄下的目錄。路徑只能用資料庫中的影音 ID 組出，而且它必須是標準形式的 UUID：
// 其他形式的字串（含 ".."、路徑分隔符號）一律拒絕，不會組出指向資料目錄以外的路徑。
// inbox 與 media_roots 不在這些目錄之下，永遠不會被碰到。
func itemDir(dataDir, sub, itemID string) (string, error) {
	u, err := uuid.Parse(itemID)
	if err != nil || u.String() != itemID {
		return "", fmt.Errorf("影音 ID %q 不是標準形式的 UUID，拒絕組出檔案路徑", itemID)
	}
	return filepath.Join(dataDir, sub, itemID), nil
}

// itemDirs 回傳影音在 itemDataDirs 各子目錄下的目錄。
func itemDirs(dataDir, itemID string) ([]string, error) {
	dirs := make([]string, 0, len(itemDataDirs))
	for _, sub := range itemDataDirs {
		d, err := itemDir(dataDir, sub, itemID)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, d)
	}
	return dirs, nil
}

// Purge 永久刪除垃圾桶中的影音（DEL-R6、R7、R11）。
//
// 影音不在垃圾桶中（包含不存在）時回傳 store.ErrNotFound（API 回 404 not_found），與還原一致：
// 這個端點只處理垃圾桶，不讓呼叫端用它直接刪除還在媒體庫裡的影音，要刪除請先移到垃圾桶。
//
// 順序：先結束殘留的任務，再用一個交易清除資料庫（見 store.Repo.PurgeItem），commit 之後才刪檔案：
// DB 失敗時不會留下指向已刪檔案的紀錄。刪檔失敗只記 log，不讓已經完成的永久刪除變成失敗。
func (p *Pipeline) Purge(ctx context.Context, id string) error {
	_, err := p.PurgeItem(ctx, id)
	return err
}

// PurgedItem 是被永久刪除的影音：影音已經不存在，API 回應用它帶出內部 ID 與代號（CLI 輸出代號）。
type PurgedItem struct {
	ID    string
	Ref   string
	Title string
}

// PurgeItem 同 Purge，另外回傳被刪除的影音的 ID、代號與標題。
func (p *Pipeline) PurgeItem(ctx context.Context, id string) (PurgedItem, error) {
	return p.purge(ctx, id, nil)
}

// purge 是 Purge 的實作；notAfter 不為 nil 時，影音必須在那個時間或之前刪除（到期清理用）。
func (p *Pipeline) purge(ctx context.Context, id string, notAfter *time.Time) (PurgedItem, error) {
	it, err := p.st.Read().ItemByID(ctx, id)
	if err != nil {
		return PurgedItem{}, err
	}
	if it.DeletedAt == nil || (notAfter != nil && it.DeletedAt.After(*notAfter)) {
		return PurgedItem{}, store.ErrNotFound
	}
	// 先確認可以安全組出檔案路徑：不行就不動資料庫，免得留下刪不掉的檔案。
	dirs, err := itemDirs(p.dataDir, it.ID)
	if err != nil {
		return PurgedItem{}, err
	}
	if err := p.stopTasks(ctx, it.ID); err != nil {
		return PurgedItem{}, err
	}
	done, err := p.st.Write().PurgeItem(ctx, it.ID, notAfter)
	if err != nil {
		return PurgedItem{}, err
	}
	p.removeDirs(ctx, dirs)
	return PurgedItem(done), nil
}

// stopTasks 取消影音殘留的任務並等執行中的結束。移到垃圾桶時已經做過一次；這裡再做是為了
// 到期清理與重啟之後留下的任務：永久刪除不能在子程序還在寫檔案時刪除目錄。
func (p *Pipeline) stopTasks(ctx context.Context, itemID string) error {
	ids, err := p.st.Write().CancelItemTasks(ctx, itemID)
	if err != nil {
		return fmt.Errorf("取消殘留的任務：%w", err)
	}
	if p.tasks != nil && len(ids) > 0 {
		if err := p.tasks.Cancel(ctx, ids); err != nil {
			return fmt.Errorf("等待任務結束：%w", err)
		}
	}
	return nil
}

// removeDirs 刪除影音的資料目錄（包含目錄本身）；失敗只記 log。
// os.RemoveAll 不會跟隨目錄中的符號連結，連結指向的檔案不會被刪除。
func (p *Pipeline) removeDirs(ctx context.Context, dirs []string) {
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			p.log.LogAttrs(ctx, slog.LevelWarn, "永久刪除後清除檔案失敗", slog.String("dir", d), slog.String("error", err.Error()))
		}
	}
}

// PurgeAll 清空垃圾桶，回傳永久刪除的影音數（DEL-R4）。每部影音各用一個交易；
// 其中一部失敗時繼續處理其他的，最後回傳已經刪除的數量與所有錯誤。
func (p *Pipeline) PurgeAll(ctx context.Context) (int, error) {
	ids, err := p.st.Read().TrashIDs(ctx)
	if err != nil {
		return 0, err
	}
	return p.purgeEach(ctx, ids, nil)
}

// PurgeExpired 永久刪除 deleted_at 不晚於 now 減去 retention 的影音，回傳永久刪除的影音數（DEL-R4）。
// now 來自注入的 clock，由 housekeeping 呼叫。
func (p *Pipeline) PurgeExpired(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	cutoff := now.Add(-retention)
	ids, err := p.st.Read().ExpiredTrashIDs(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	return p.purgeEach(ctx, ids, &cutoff)
}

// purgeEach 依序永久刪除 ids。名單產生之後才被還原或已經被別人刪除的影音（store.ErrNotFound）略過，不算失敗。
func (p *Pipeline) purgeEach(ctx context.Context, ids []string, notAfter *time.Time) (int, error) {
	var errs []error
	n := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		switch _, err := p.purge(ctx, id, notAfter); {
		case err == nil:
			n++
		case errors.Is(err, store.ErrNotFound):
		default:
			errs = append(errs, fmt.Errorf("永久刪除 %s：%w", id, err))
		}
	}
	return n, errors.Join(errs...)
}

// DeletionPreview 回傳刪除影音的影響範圍，唯讀：不寫入任何資料，也不產生事件（DEL-R3）。
// 影音在垃圾桶中時回傳 *InTrashError（DEL-R1），不存在時回傳 store.ErrNotFound。
func (p *Pipeline) DeletionPreview(ctx context.Context, id string) (Preview, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	if it.DeletedAt != nil {
		return Preview{}, &InTrashError{ItemID: it.ID}
	}
	stats, err := r.DeletionStats(ctx, it.ID)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{Artifacts: stats.Artifacts, Bytes: stats.Bytes, SpentUSD: stats.SpentUSD, Outputs: []PreviewOutput{}}
	// artifacts 的大小以資料庫記錄的為準（上面已經加總），只量 media 與 uploads。
	for _, sub := range []string{"media", "uploads"} {
		d, err := itemDir(p.dataDir, sub, it.ID)
		if err != nil {
			return Preview{}, err
		}
		out.Bytes += dirSize(d)
	}
	return out, nil
}

// dirSize 加總目錄下一般檔案的大小；目錄不存在時是 0。不跟隨符號連結。預覽只是估計，讀不到的項目略過。
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
