package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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

// PreviewOutput 是刪除預覽中的一個輸出檔（DEL-R3）：指定 delete_outputs 時，Modified 為 false 的會刪除，
// 為 true 的保留（Reason 是原因，modified 之外還有 outside_roots、unreadable）。
type PreviewOutput struct {
	Path     string
	Modified bool
	Reason   string
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
// purgeWriteTimeout 是永久刪除在結束任務之後，清除資料庫與刪除檔案的上限（不受請求 ctx 取消影響）。
const purgeWriteTimeout = 30 * time.Second

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
	// KeptOutputs 是指定 DeleteOutputs 時保留下來的輸出檔（DEL-R9）；沒有指定時是空的。
	KeptOutputs []KeptOutput
}

// PurgeOptions 是永久刪除的選項。
type PurgeOptions struct {
	// DeleteOutputs 為 true 時，在清除資料庫之前刪除輸出目標寫出、且沒被改過的檔案（DEL-R9）：
	// outputs 紀錄隨影音 cascade 刪除，之後就無法再比對雜湊了。
	DeleteOutputs bool
}

// PurgeItem 同 Purge，另外回傳被刪除的影音的 ID、代號與標題。
func (p *Pipeline) PurgeItem(ctx context.Context, id string) (PurgedItem, error) {
	return p.PurgeItemWith(ctx, id, PurgeOptions{})
}

// PurgeItemWith 同 PurgeItem，可以指定選項。
func (p *Pipeline) PurgeItemWith(ctx context.Context, id string, opts PurgeOptions) (PurgedItem, error) {
	return p.purge(ctx, id, nil, opts)
}

// purge 是 Purge 的實作；notAfter 不為 nil 時，影音必須在那個時間或之前刪除（到期清理用）。
func (p *Pipeline) purge(ctx context.Context, id string, notAfter *time.Time, opts PurgeOptions) (PurgedItem, error) {
	ids, dirs, err := p.stopTasks(ctx, id, notAfter)
	if err != nil {
		return PurgedItem{}, err
	}
	// 所有任務都交給 worker：它會略過不在執行中的 ID；資料庫中已是 cancelled 的任務可能仍在執行。
	// 等待不受請求的 ctx 取消影響：用戶端中途斷線不該留下一半的永久刪除，逾時仍沒結束就放棄這次。
	// 任務已經取消之後，資料庫的清除也用同樣不受請求取消影響的 ctx：否則斷線時停在「任務取消了、影音還在垃圾桶」。
	if p.tasks != nil && len(ids) > 0 {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelWaitTimeout)
		defer cancel()
		if err := p.tasks.Cancel(wctx, ids); err != nil {
			return PurgedItem{}, fmt.Errorf("等待任務結束：%w", err)
		}
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), purgeWriteTimeout)
	defer cancel()
	kept := []KeptOutput{}
	if opts.DeleteOutputs {
		// 要在清除資料庫之前：outputs 紀錄會 cascade 刪除。讀不到紀錄就不繼續，免得留下無法辨識的檔案。
		if kept, err = p.deleteOutputs(dctx, id); err != nil {
			return PurgedItem{}, fmt.Errorf("刪除輸出檔：%w", err)
		}
	}
	done, err := p.st.Write().PurgeItem(dctx, id, notAfter)
	if err != nil {
		return PurgedItem{}, err
	}
	p.removeDirs(dctx, dirs)
	return PurgedItem{ID: done.ID, Ref: done.Ref, Title: done.Title, KeptOutputs: kept}, nil
}

// stopTasks 在一個交易中確認影音仍在垃圾桶中（而且符合 notAfter），再取消它殘留的任務，回傳這部影音
// 所有任務的 ID 與要刪除的目錄。確認與取消在同一個交易：影音先被還原、使用者又按了「重試」之後，
// 新的任務不會被這次永久刪除取消。移到垃圾桶時已經做過一次；這裡再做是為了到期清理與重啟之後留下的任務：
// 永久刪除不能在子程序還在寫檔案時刪除目錄。
//
// 不在垃圾桶中（含不存在）回傳 store.ErrNotFound；先確認可以安全組出檔案路徑，不行就不動資料庫。
func (p *Pipeline) stopTasks(ctx context.Context, id string, notAfter *time.Time) (ids, dirs []string, err error) {
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		it, err := r.ItemByID(ctx, id)
		if err != nil {
			return err
		}
		if it.DeletedAt == nil || (notAfter != nil && it.DeletedAt.After(*notAfter)) {
			return store.ErrNotFound
		}
		if dirs, err = itemDirs(p.dataDir, it.ID); err != nil {
			return err
		}
		if ids, err = r.ItemTaskIDs(ctx, it.ID); err != nil {
			return fmt.Errorf("列出影音的任務：%w", err)
		}
		if _, err := r.CancelItemTasks(ctx, it.ID); err != nil {
			return fmt.Errorf("取消殘留的任務：%w", err)
		}
		return nil
	})
	return ids, dirs, err
}

// removeDirs 刪除影音的資料目錄（見 removeDir）。有備份進行中時，上傳原始檔的目錄等備份結束才刪（見 HoldUploads）。
func (p *Pipeline) removeDirs(ctx context.Context, dirs []string) {
	for _, d := range dirs {
		if p.isUploadDir(d) && p.uploadHolds.deferUpload(d) {
			p.log.LogAttrs(ctx, slog.LevelInfo, "備份進行中，上傳原始檔等備份結束才刪除", slog.String("dir", d))
			continue
		}
		p.removeDir(ctx, d)
	}
}

// PurgeAll 清空垃圾桶，回傳永久刪除的影音數（DEL-R4）。每部影音各用一個交易；
// 其中一部失敗時繼續處理其他的，最後回傳已經刪除的數量與所有錯誤。
func (p *Pipeline) PurgeAll(ctx context.Context) (int, error) {
	n, _, err := p.PurgeAllWith(ctx, PurgeOptions{})
	return n, err
}

// PurgeAllWith 同 PurgeAll，可以指定選項；另外回傳所有影音保留下來的輸出檔（DEL-R9）。
func (p *Pipeline) PurgeAllWith(ctx context.Context, opts PurgeOptions) (int, []KeptOutput, error) {
	ids, err := p.st.Read().TrashIDs(ctx)
	if err != nil {
		return 0, nil, err
	}
	return p.purgeEach(ctx, ids, nil, opts)
}

// PurgeExpired 永久刪除 deleted_at 不晚於 now 減去 retention 的影音，回傳永久刪除的影音數（DEL-R4）。
// now 來自注入的 clock，由 housekeeping 呼叫。
func (p *Pipeline) PurgeExpired(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	cutoff := now.Add(-retention)
	ids, err := p.st.Read().ExpiredTrashIDs(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	n, _, err := p.purgeEach(ctx, ids, &cutoff, PurgeOptions{})
	return n, err
}

// purgeEach 依序永久刪除 ids。名單產生之後才被還原或已經被別人刪除的影音（store.ErrNotFound）略過，不算失敗。
func (p *Pipeline) purgeEach(ctx context.Context, ids []string, notAfter *time.Time, opts PurgeOptions) (int, []KeptOutput, error) {
	var errs []error
	kept := []KeptOutput{}
	n := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		switch done, err := p.purge(ctx, id, notAfter, opts); {
		case err == nil:
			n++
			kept = append(kept, done.KeptOutputs...)
		case errors.Is(err, store.ErrNotFound):
		default:
			errs = append(errs, fmt.Errorf("永久刪除 %s：%w", id, err))
		}
	}
	return n, kept, errors.Join(errs...)
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
	out := Preview{Artifacts: stats.Artifacts, Bytes: stats.Bytes, SpentUSD: stats.SpentUSD}
	if out.Outputs, err = p.previewOutputs(ctx, it.ID); err != nil {
		return Preview{}, err
	}
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
