package pipeline

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是移到垃圾桶與還原（DEL-R1、R2、R4、R5）。

// DefaultTrashRetention 是垃圾桶預設保留的時間（DEL-R4：30 天）。
const DefaultTrashRetention = 30 * 24 * time.Hour

// cancelWaitTimeout 是移到垃圾桶之後，等待執行中的任務結束的上限。
const cancelWaitTimeout = 30 * time.Second

// TaskCanceller 結束正在執行的任務並等它們結束（*worker.Worker 實作它）。
type TaskCanceller interface {
	Cancel(ctx context.Context, taskIDs []string) error
}

// TrashOptions 是移到垃圾桶的選項。
type TrashOptions struct {
	// DeleteOutputs 為 true 時刪除輸出目標寫出、且沒被改過的檔案；改過的保留並列在 KeptOutputs（DEL-R9）。預設保留全部。
	DeleteOutputs bool
	// Canceller 結束被取消的任務中、正在執行的那些（PIPE-R15）；nil 時用 SetTaskCanceller 設定的，都沒有代表沒有 worker 在執行（例如測試）。
	Canceller TaskCanceller
}

// KeptOutput 是指定 delete_outputs 時保留下來的輸出檔（DEL-R9）；Reason 見 KeptModified 等。
type KeptOutput struct {
	Path   string
	Reason string
}

// TrashResult 是移到垃圾桶的結果。
type TrashResult struct {
	ID        string
	Ref       string
	DeletedAt time.Time
	// PurgeAt 是到期後永久刪除的時間（deleted_at 加保留天數）。
	PurgeAt     time.Time
	KeptOutputs []KeptOutput
}

// TrashedItem 是垃圾桶中的一部影音。
type TrashedItem struct {
	ID        string
	Ref       string
	Title     string
	DeletedAt time.Time
	PurgeAt   time.Time
}

// retention 是垃圾桶保留的時間；沒有設定時用預設值。
func (p *Pipeline) retention() time.Duration {
	if p.trashRetention <= 0 {
		return DefaultTrashRetention
	}
	return p.trashRetention
}

func (p *Pipeline) trashedItem(it store.Item) TrashedItem {
	out := TrashedItem{ID: it.ID, Ref: source.Ref(it.Platform, it.ExternalID, it.Part), Title: it.Title}
	if it.DeletedAt != nil {
		out.DeletedAt = *it.DeletedAt
		out.PurgeAt = it.DeletedAt.Add(p.retention())
	}
	return out
}

// trashedStageState 是移到垃圾桶時被取消的步驟的狀態（DEL-R1、R5）：skipped，原因 cancelled，
// 提供「重試」；還原之後步驟維持這個狀態，使用者按「重試」才重新處理。
func trashedStageState() store.StageState {
	st := cancelledState()
	st.Actions = []string{"重試"}
	return st
}

// Trash 把影音移到垃圾桶（DEL-R1、R2）。
//
// 在一個交易中：設定 deleted_at、未結束的步驟改成 skipped（原因 cancelled，動作「重試」）、
// 任務改成 cancelled、寫入 item.deleted 事件。commit 之後，依 PIPE-R15 讓 worker 結束執行中的任務
// 並等它們結束（opts.Canceller），再刪除媒體快取；等不到任務結束時保留快取，到永久刪除時再刪。
//
// 已經在垃圾桶中時回傳 *InTrashError；影音不存在時回傳 store.ErrNotFound。
func (p *Pipeline) Trash(ctx context.Context, id string, opts TrashOptions) (TrashResult, error) {
	// 從交易到刪除媒體快取都持有這部影音的鎖：等待任務結束的期間若被還原、再重試，
	// 新的任務可能已經在使用 media/<id>，這時不能再刪它（Restore 也要取得同一把鎖）。
	unlock, err := p.trashLocks.lock(ctx, id)
	if err != nil {
		return TrashResult{}, err
	}
	defer unlock()

	var it store.Item
	var taskIDs []string
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		taskIDs = nil
		var changed bool
		var err error
		if it, changed, err = r.TrashItem(ctx, id); err != nil {
			return err
		}
		if !changed {
			return &InTrashError{ItemID: it.ID}
		}
		// 要等的是這部影音所有的任務，不只這次改成 cancelled 的：同時進行的「取消」可能已經把執行中的任務
		// 改成 cancelled（cancelItemTx 不會再回傳它），但它的子程序還在用媒體快取。worker 會略過沒在執行的 ID。
		if taskIDs, err = r.ItemTaskIDs(ctx, it.ID); err != nil {
			return err
		}
		_, err = cancelItemTx(ctx, r, it.ID, trashedStageState())
		return err
	})
	if err != nil {
		return TrashResult{}, err
	}

	// 已經 commit：影音確定在垃圾桶中。client 中途斷線也要把善後做完，所以不受請求 ctx 取消影響。
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelWaitTimeout)
	defer cancel()
	// 呼叫端沒有指定時用 SetTaskCanceller 設定的那個；兩者都沒有（例如單元測試）才不結束執行中的任務。
	canceller := opts.Canceller
	if canceller == nil {
		canceller = p.tasks
	}
	cancelled := true
	if canceller != nil && len(taskIDs) > 0 {
		if err := canceller.Cancel(cctx, taskIDs); err != nil {
			// 逾時仍沒結束：任務可能還在讀寫媒體快取，所以保留它，永久刪除時再一起刪除（itemDataDirs）。
			// 刪除本身仍然成功：任務在資料庫中已經是 cancelled，晚點寫回會因租約失效被丟棄。
			p.log.WarnContext(ctx, "等待被取消的任務結束時失敗，保留媒體快取到永久刪除", "item", it.ID, "error", err)
			cancelled = false
		}
	}
	if cancelled {
		p.removeMediaCache(ctx, it.ID)
	}

	// 輸出檔（DEL-R9）：預設保留；指定時刪除沒被改過的。已經 commit，失敗只記 log，不讓刪除變成失敗。
	kept := []KeptOutput{}
	if opts.DeleteOutputs {
		if k, err := p.deleteOutputs(cctx, it); err != nil {
			p.log.WarnContext(ctx, "刪除輸出檔失敗", "item", it.ID, "error", err)
		} else {
			kept = k
		}
	}

	t := p.trashedItem(it)
	return TrashResult{ID: t.ID, Ref: t.Ref, DeletedAt: t.DeletedAt, PurgeAt: t.PurgeAt, KeptOutputs: kept}, nil
}

// removeMediaCache 刪除影音的媒體快取目錄（DEL-R2）。只在 itemID 是標準形式 UUID 時才組出路徑，
// 失敗只記錄：快取可以重新下載，不影響刪除的結果。
func (p *Pipeline) removeMediaCache(ctx context.Context, itemID string) {
	if u, err := uuid.Parse(itemID); err != nil || u.String() != itemID {
		p.log.WarnContext(ctx, "影音 ID 不是標準形式，略過刪除媒體快取", "item", itemID)
		return
	}
	if err := os.RemoveAll(MediaCacheDir(p.dataDir, itemID)); err != nil {
		p.log.WarnContext(ctx, "刪除媒體快取失敗", "item", itemID, "error", err)
	}
}

// Restore 從垃圾桶還原影音（DEL-R5）：deleted_at 改回 NULL，被取消的步驟維持 skipped 並保留「重試」，
// 寫入 item.restored 事件。影音不在垃圾桶中（或不存在）時回傳 store.ErrNotFound。
// 同一部影音的 Trash 還在善後（等待任務結束、刪除媒體快取）時，等它做完才還原。
//
// 被刪除的輸出檔（有 outputs 紀錄、檔案已不存在，DEL-R9）在還原之後從 publish 重跑寫回。
func (p *Pipeline) Restore(ctx context.Context, id string) (TrashedItem, error) {
	t, err := p.restore(ctx, id)
	if err != nil {
		return TrashedItem{}, err
	}
	// 在鎖之外：重跑自己會讀寫影音，不需要（也不能）和 Trash 的善後互斥。
	p.republishMissingOutputs(ctx, id)
	return t, nil
}

func (p *Pipeline) restore(ctx context.Context, id string) (TrashedItem, error) {
	unlock, err := p.trashLocks.lock(ctx, id)
	if err != nil {
		return TrashedItem{}, err
	}
	defer unlock()
	it, changed, err := p.st.Write().RestoreItem(ctx, id)
	if err != nil {
		return TrashedItem{}, err
	}
	if !changed {
		return TrashedItem{}, store.ErrNotFound
	}
	return p.trashedItem(it), nil
}

// TrashList 列出垃圾桶中的影音，刪除時間由新到舊（DEL-R4）。
func (p *Pipeline) TrashList(ctx context.Context) ([]TrashedItem, error) {
	items, err := p.st.Read().ListTrash(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TrashedItem, 0, len(items))
	for _, it := range items {
		out = append(out, p.trashedItem(it))
	}
	return out, nil
}
