package pipeline

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是 SRC-R6 的動作「現在改用語音轉錄」：transcript 正在等 YouTube 的自動字幕（waiting）時，
// 略過剩下的等待，立刻改用下一個來源（fallback）。不消耗重試次數：等待中的任務本來就沒有計入（PIPE-R2），
// 這個動作只把同一個任務提前到現在，並在 payload 記下旗標，之後同一個任務的執行（含重試）都不再等待。

// ActionCodeUseASRNow 是「現在改用語音轉錄」的穩定動作代碼（Stage.action_codes，前端不必比對繁中字串）。
const ActionCodeUseASRNow = "use_asr_now"

// ActionCodes 回傳步驟的動作中有穩定代碼的那些（依 actions 的順序）。動作本身仍是給人看的繁中文字；
// 目前只有「現在改用語音轉錄」有代碼，其他動作沿用文字對照（docs/ui/README.md 第 10 節）。
func ActionCodes(actions []string) []string {
	var out []string
	for _, a := range actions {
		if a == actionUseASRNow {
			out = append(out, ActionCodeUseASRNow)
		}
	}
	return out
}

// ErrNotWaiting 是 transcript 不是在等待自動字幕（已經在執行、已經結束或根本沒有等待）。
var ErrNotWaiting = errors.New("這部影音的字幕來源沒有在等待自動字幕，不能略過等待。")

// taskPayload 是任務 payload 中這裡用到的欄位。
type taskPayload struct {
	SkipAutoWait bool `json:"skip_auto_wait"`
}

type skipAutoWaitKey struct{}

// withTaskFlags 把任務 payload 的旗標放進 ctx（目前只有 skip_auto_wait）；payload 不是合法的 JSON 時當成沒有旗標。
func withTaskFlags(ctx context.Context, t store.Task) context.Context {
	var pl taskPayload
	if len(t.Payload) > 0 && json.Unmarshal(t.Payload, &pl) == nil && pl.SkipAutoWait {
		return context.WithValue(ctx, skipAutoWaitKey{}, true)
	}
	return ctx
}

// skipAutoWait 回報這次執行是否要略過 SRC-R6 的等待。
func skipAutoWait(ctx context.Context) bool {
	v, _ := ctx.Value(skipAutoWaitKey{}).(bool)
	return v
}

// UseASRNow 實作「現在改用語音轉錄」（SRC-R6）：只接受 transcript 目前是 waiting、而且它的任務還沒被領取。
// 任務的 not_before 改成現在並記下旗標，步驟改回 pending（訊息與動作清掉），然後喚醒 worker。
// 其他情況回傳 ErrNotWaiting；影音不存在時是 store.ErrNotFound。
func (p *Pipeline) UseASRNow(ctx context.Context, itemRef string) (store.StageRun, error) {
	it, err := p.itemByIDOrRef(ctx, itemRef)
	if err != nil {
		return store.StageRun{}, err
	}
	var out store.StageRun
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		run, ok := latestRuns(runs)[StageTranscript]
		if !ok || run.Status != store.StatusWaiting {
			return ErrNotWaiting
		}
		task, err := r.TaskByStageRun(ctx, run.ID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotWaiting
		}
		if err != nil {
			return err
		}
		skipped, err := r.SkipTaskWait(ctx, task.ID)
		if err != nil {
			return err
		}
		if !skipped {
			// 任務正在執行（租約中）：執行結束時又會排回 2 小時後，旗標也會失效，所以拒絕，讓使用者稍後再按。
			return ErrNotWaiting
		}
		if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
			return err
		}
		out = run
		out.Status = store.StatusPending
		return nil
	})
	if err != nil {
		return store.StageRun{}, err
	}
	p.wake()
	return out, nil
}
