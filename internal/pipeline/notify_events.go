package pipeline

import (
	"context"

	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/store"
)

// emitRunEvent 在 applyResult 的交易最後判斷這次寫回的結果要不要通知（NOTIFY-R3），在同一個交易寫進 outbox。
// stage 是這次寫回的步驟。沒有設定通知（Deps.Notify 為 nil）時什麼都不做。
func (p *Pipeline) emitRunEvent(ctx context.Context, r *store.Repo, it store.Item, stage string) error {
	if p.notify == nil {
		return nil
	}
	runs, err := r.StageRunsByItem(ctx, it.ID)
	if err != nil {
		return err
	}
	ev, ok := runEvent(it, latestRuns(runs), stage)
	if !ok {
		return nil
	}
	if want, err := p.notify.Wants(ctx, r, ev.Name); err != nil || !want {
		return err
	}
	if ev.Item, err = notify.ItemOf(ctx, r, it.ID); err != nil {
		return err
	}
	_, err = p.notify.Emit(ctx, r, ev)
	return err
}

// runEvent 依影音各步驟最新的狀態決定事件（NOTIFY-R3、R10）：
//   - 這次寫回的步驟 failed：item.failed（鍵：影音＋步驟）；
//   - 這次寫回的步驟 blocked，原因 needs_action 或 budget_blocked：item.blocked（awaiting_user 交給 DIG 的彙整）；
//   - 最後一個步驟是 done 或 skipped（這次執行的步驟都做完了），而且沒有用 until 提前結束：item.done
//     （鍵含最後一個步驟的執行 id，重跑一次就通知一次）。seeding 與 MCP 的 until: transcript 不會通知。
func runEvent(it store.Item, latest map[string]store.StageRun, stage string) (notify.Event, bool) {
	cur, ok := latest[stage]
	if !ok {
		return notify.Event{}, false
	}
	switch cur.Status {
	case store.StatusFailed:
		return notify.Event{
			Name: notify.EventItemFailed, DedupeKey: notify.DedupeItemFailed(it.ID, stage),
			Stage: stage, ErrorClass: cur.ErrorClass, Message: cur.Message, Actions: cur.Actions,
		}, true
	case store.StatusBlocked:
		if cur.BlockReason != "needs_action" && cur.BlockReason != "budget_blocked" {
			return notify.Event{}, false
		}
		return notify.Event{
			Name: notify.EventItemBlocked, DedupeKey: notify.DedupeItemBlocked(it.ID, stage, cur.BlockReason),
			Stage: stage, ErrorClass: cur.ErrorClass, Message: cur.Message, Actions: cur.Actions,
		}, true
	}
	if it.UntilStage != "" && it.UntilStage != Stages[len(Stages)-1] {
		return notify.Event{}, false
	}
	// 鏈是線性的：失敗、blocked 或 until 都會讓鏈停在前面，所以最後一個步驟是 done 或 skipped 就代表這次執行做完了。
	// 不檢查更早的步驟：從後面的步驟重跑時（例如 seeding 之後從 publish 重跑），更早的步驟可能從來沒有執行過。
	lastRun := ""
	for i := len(Stages) - 1; i >= 0; i-- {
		run, ok := latest[Stages[i]]
		if !ok {
			continue // M2、M3 之前建立的影音沒有後來才加的步驟
		}
		if run.Status != store.StatusDone && run.Status != store.StatusSkipped {
			return notify.Event{}, false
		}
		lastRun = run.ID
		break
	}
	if lastRun == "" {
		return notify.Event{}, false
	}
	return notify.Event{Name: notify.EventItemDone, DedupeKey: notify.DedupeItemDone(it.ID, lastRun)}, true
}
