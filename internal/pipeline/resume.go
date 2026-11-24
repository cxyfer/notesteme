package pipeline

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// resumeSecret 解析 resume_on 的 token（"<種類>:<名稱>"）：回傳它對應的 secret 的 (kind, name)。
// 目前只有 "cookie:<platform>"，對應 secrets 表的 (cookie, <platform>)；之後有新種類時在這裡擴充。
func resumeSecret(token string) (kind, name string, ok bool) {
	k, n, found := strings.Cut(token, ":")
	if !found || n == "" {
		return "", "", false
	}
	switch k {
	case "cookie":
		return "cookie", n, true
	}
	return "", "", false
}

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
			priority := queue.PriorityManual
			var prev *store.Task
			switch old, err := r.TaskByStageRun(ctx, run.ID); {
			case err == nil:
				priority = old.Priority
				// 沿用原本任務的資源類別：transcript 要不要歸為 asr、publish 要不要歸為 download 是建立任務時的推定，不重新判斷。
				prev = &old
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
			// 清除 blocked 的說明與 resume_on（SQL 在非 blocked 時一併清空）。
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
				return err
			}
			if _, err := insertStageTaskLike(ctx, r, run.ItemID, run, priority, prev); err != nil {
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

// resumeAlreadyResolved 判斷 blocked 的結果所等待的條件，是否在這次執行開始之後就已經滿足。
//
// 競態：yt-dlp 在沒有 cookie 時開始執行，使用者此時上傳 cookie，ResumeBlocked 找不到步驟（還在 running）；
// 之後步驟回報需要 cookie，若寫成 blocked 就沒有人會再恢復它。
// 所以 secret 的 updated_at 晚於這次執行的開始時間時，視為條件已滿足。
// 時間以毫秒比較（secrets 表只存到毫秒），而且要嚴格晚於：開始當下就已存在的 cookie（例如過期被拒絕）不算，
// 否則會無限重跑。不是 blocked、沒有 resume_on，或 token 不認識時都回傳 false。
func resumeAlreadyResolved(ctx context.Context, r *store.Repo, res result) (bool, error) {
	if res.state.Status != store.StatusBlocked || res.startedAt.IsZero() {
		return false, nil
	}
	kind, name, ok := resumeSecret(res.state.ResumeOn)
	if !ok {
		return false, nil
	}
	sec, err := r.Secret(ctx, kind, name)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return sec.UpdatedAt.After(res.startedAt.Truncate(time.Millisecond)), nil
}

// requeueResolved 把步驟改回 pending 並建立新任務（沿用原任務的優先順序與資源類別），原任務結束；
// 不計入嘗試次數，也不寫 blocked 的說明與 resume_on。
func requeueResolved(ctx context.Context, r *store.Repo, t store.Task, run store.StageRun) error {
	if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
		return err
	}
	if err := r.FailTask(ctx, t.ID, t.LeaseToken); err != nil {
		return err
	}
	_, err := insertStageTaskLike(ctx, r, run.ItemID, run, t.Priority, &t)
	return err
}
