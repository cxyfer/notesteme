package pipeline

import (
	"context"
	"slices"

	"github.com/cxyfer/notesteme/internal/store"
)

// unfinishedStatuses 是「還沒結束」的步驟狀態：取消會把它們改成 skipped。
// blocked 也算：使用者完成修正動作後它會自動回到 pending（PIPE-R4），不取消的話，
// 已取消的影音會在上傳 cookie 之後又開始處理。failed、done、skipped 已經結束。
var unfinishedStatuses = []string{store.StatusPending, store.StatusRunning, store.StatusWaiting, store.StatusBlocked, store.StatusAwaitingAgent}

// cancelledState 是被取消的步驟的狀態（PIPE-R15）。skipped 與被重跑取代的步驟相同，原因寫在 block_reason。
func cancelledState() store.StageState {
	return store.StageState{Status: store.StatusSkipped, BlockReason: reasonCancelled, Message: "已取消。"}
}

// CancelItem 取消影音所有未結束的處理（PIPE-R15）：在一個交易中把未結束的步驟改為 skipped（原因 cancelled），
// pending 與 leased 的任務改為 cancelled。回傳被取消的任務 ID；
// 呼叫端要在交易完成之後通知 worker 結束其中正在執行的（worker.Cancel）。
//
// 任務先在資料庫中取消，所以還在執行的任務晚一點做完時，寫回會因為租約失效（store.ErrLeaseLost）
// 整筆丟棄（apply），不會把步驟或任務蓋回 done。
// 沒有未結束的步驟時什麼都不做，回傳空的清單（冪等）。影音不存在時回傳 store.ErrNotFound。
func (p *Pipeline) CancelItem(ctx context.Context, itemID string) ([]string, error) {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, store.ErrNotFound
	}
	var taskIDs []string
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if !slices.Contains(unfinishedStatuses, run.Status) {
				continue
			}
			if err := r.SetStageState(ctx, run.ID, cancelledState()); err != nil {
				return err
			}
		}
		taskIDs, err = r.CancelItemTasks(ctx, it.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return taskIDs, nil
}
