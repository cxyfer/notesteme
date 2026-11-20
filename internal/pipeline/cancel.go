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
// 沒有未結束的步驟時什麼都不做，回傳空的清單（冪等）。影音不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) CancelItem(ctx context.Context, itemID string) ([]string, error) {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	var taskIDs []string
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		var err error
		taskIDs, err = cancelItemTx(ctx, r, it.ID, cancelledState())
		return err
	})
	if err != nil {
		return nil, err
	}
	return taskIDs, nil
}

// cancelItemTx 在交易 r 中把影音未結束的步驟改成 state，並把它的 pending 與 leased 任務改為 cancelled，
// 回傳被取消的任務 ID。取消與移到垃圾桶（Trash）共用。
func cancelItemTx(ctx context.Context, r *store.Repo, itemID string, state store.StageState) ([]string, error) {
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !slices.Contains(unfinishedStatuses, run.Status) {
			continue
		}
		if err := r.SetStageState(ctx, run.ID, state); err != nil {
			return nil, err
		}
	}
	return r.CancelItemTasks(ctx, itemID)
}
