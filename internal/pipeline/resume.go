package pipeline

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// ResumeBlocked 恢復因 token 而 blocked 的步驟（PIPE-R4）：使用者補上 cookie 之後，API 以 "cookie:<platform>" 呼叫。
//
// 在同一個交易中，把未刪除影音裡每個 (影音, 步驟) 最新一筆、resume_on 等於 token 的 blocked 步驟改回 pending，
// 並各建立一個新任務：優先順序沿用原本的任務（找不到時當成手動新增），嘗試次數從 0 開始。
// 回傳恢復的步驟數。
//
// 冪等：恢復後步驟已不是 blocked，再呼叫一次找不到任何步驟，不會重複建立任務。
// token 為空字串時不恢復任何步驟（沒有 resume_on 的 blocked 需要使用者手動處理）。
func (p *Pipeline) ResumeBlocked(ctx context.Context, token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	n := 0
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		n = 0 // 交易可能重試
		runs, err := r.BlockedRunsByResume(ctx, token)
		if err != nil {
			return err
		}
		for _, run := range runs {
			priority, asr := queue.PriorityManual, false
			switch old, err := r.TaskByStageRun(ctx, run.ID); {
			case err == nil:
				priority = old.Priority
				// 沿用原本任務的資源類別：transcript 要不要歸為 asr 是建立任務時的推定，不重新判斷。
				asr = old.ResourceClass == queue.ResourceASR
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
			// 清除 blocked 的說明與 resume_on（SQL 在非 blocked 時一併清空）。
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
				return err
			}
			if _, err := insertStageTask(ctx, r, run.ItemID, run, priority, asr); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if n > 0 {
		p.wake()
	}
	return n, nil
}
