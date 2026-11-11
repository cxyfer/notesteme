package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// taskOutcome 是任務的結局。
type taskOutcome int

const (
	taskDone    taskOutcome = iota // 完成
	taskFailed                     // 失敗，不再重試（permanent、blocked、重試次數用盡）
	taskRequeue                    // 放回佇列，notBefore 之後再執行
)

// result 是執行一個任務的結果，由 apply 在同一個交易中寫回：
// 步驟狀態、產出、影音欄位、Probe 紀錄與任務狀態要一起成功或一起失敗。
type result struct {
	state store.StageState
	task  taskOutcome
	// notBefore 與 countAttempt 只用於 taskRequeue。
	// countAttempt 為 false 時這次領取不計入重試次數（wait，PIPE-R2）。
	notBefore    *time.Time
	countAttempt bool

	stage    *store.StageResult
	artifact *store.NewArtifact
	probes   []store.NewProbe
	meta     *metaUpdate
	// advance 為 true 時，成功後建立下游步驟的任務（until 之前）。
	advance bool
}

type metaUpdate struct {
	item    store.ItemMeta
	channel *store.Channel
}

// Handle 執行一個已領取的任務，並把結果寫回（worker.Handler）。
// 步驟自己的失敗（工具錯誤、沒有字幕…）依錯誤類別寫進步驟狀態，不會回傳錯誤；
// 回傳的錯誤只代表資料庫或檔案系統出了問題。ctx 取消（關閉中）時不改變任何狀態，租約之後會放回佇列。
func (p *Pipeline) Handle(ctx context.Context, t store.Task) error {
	r := p.st.Read()
	run, err := r.StageRun(ctx, t.StageRunID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("讀取步驟 %s：%w", t.StageRunID, err)
	}
	it, err := r.ItemByID(ctx, run.ItemID)
	if err != nil {
		return fmt.Errorf("讀取影音 %s：%w", run.ItemID, err)
	}
	cfgJSON, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return fmt.Errorf("讀取設定快照 %s：%w", run.ConfigSnapshotID, err)
	}
	eff, err := ParseEffective(cfgJSON)
	if err != nil {
		return err
	}
	lang, err := eff.Language()
	if err != nil {
		return err
	}

	var res result
	switch run.Stage {
	case StageMetadata:
		res, err = p.runMetadata(ctx, it)
	case StageTranscript:
		res, err = p.runTranscript(ctx, it, lang)
	case StageNormalize:
		res, err = p.runNormalize(ctx, it)
	default:
		err = &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "不認識的步驟 " + run.Stage}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		p.log.Warn("步驟失敗", "item", it.ID, "stage", run.Stage, "attempt", t.Attempts, "error", err)
		res = p.classify(t, err)
	}
	if err := p.apply(ctx, t, run, it, res); err != nil {
		return fmt.Errorf("寫回步驟 %s 的結果：%w", run.Stage, err)
	}
	return nil
}

// classify 把步驟的錯誤依 plan 第 5.2 節的錯誤類別對應到步驟狀態與任務的處置。
// 沒有分類的錯誤（解析失敗、檔案系統…）視為 retryable。
func (p *Pipeline) classify(t store.Task, err error) result {
	class := errclass.ClassRetryable
	if c, ok := errclass.ClassOf(err); ok {
		class = c
	}
	switch class {
	case errclass.ClassPermanent:
		return result{task: taskFailed, state: store.StageState{
			Status: store.StatusFailed, ErrorClass: string(class), Message: "這個步驟無法完成，重試也不會成功。",
		}}
	case errclass.ClassNeedsAction, errclass.ClassBudgetBlocked:
		// TODO(SRC-R8、SRC-R9)：依原因給出具體的訊息與修正動作（上傳 cookie、更新 yt-dlp、設定 API key）。
		reason := "needs_action"
		if class == errclass.ClassBudgetBlocked {
			reason = "budget_blocked"
		}
		return result{task: taskFailed, state: store.StageState{
			Status: store.StatusBlocked, BlockReason: reason, ErrorClass: string(class), Message: "這個步驟需要你處理才能繼續。",
		}}
	case errclass.ClassWait:
		// wait 類的錯誤由執行者自己決定何時再檢查（見 runMetadata）；走到這裡代表沒有指定時間，稍後再試且不計入次數。
		at := p.clock.Now().Add(queue.WaitRecheckDefault)
		return result{task: taskRequeue, notBefore: &at, countAttempt: false, state: store.StageState{
			Status: store.StatusWaiting, ErrorClass: string(class), Message: "還沒有結果，稍後會再檢查。", NotBefore: &at,
		}}
	}
	// retryable：指數退避，次數用盡後失敗（PIPE-R3）。
	if d, ok := queue.RetryDelay(t.Attempts, t.MaxAttempts); ok {
		at := p.clock.Now().Add(d)
		return result{task: taskRequeue, notBefore: &at, countAttempt: true, state: store.StageState{
			Status: store.StatusPending, ErrorClass: string(class), Message: "暫時失敗，系統會自動重試。", NotBefore: &at,
		}}
	}
	return result{task: taskFailed, state: store.StageState{
		Status: store.StatusFailed, ErrorClass: string(class), Message: "重試多次仍然失敗。",
	}}
}

// apply 在一個交易中寫回任務的結果；成功後視需要建立下游任務。
func (p *Pipeline) apply(ctx context.Context, t store.Task, run store.StageRun, it store.Item, res result) error {
	var wake bool
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		if len(res.probes) > 0 {
			if err := r.InsertProbes(ctx, it.ID, res.probes); err != nil {
				return err
			}
		}
		if res.meta != nil {
			m := res.meta.item
			if c := res.meta.channel; c != nil {
				id, err := r.UpsertChannel(ctx, *c)
				if err != nil {
					return err
				}
				m.ChannelID = id
			}
			if err := r.UpdateItemMeta(ctx, it.ID, m); err != nil {
				return err
			}
		}
		if res.stage != nil {
			sr := *res.stage
			if res.artifact != nil {
				a, err := r.InsertArtifact(ctx, *res.artifact)
				if err != nil {
					return err
				}
				sr.ArtifactID = a.ID
			}
			if err := r.SetStageResult(ctx, run.ID, sr); err != nil {
				return err
			}
		}
		if err := r.SetStageState(ctx, run.ID, res.state); err != nil {
			return err
		}
		switch res.task {
		case taskDone:
			if err := r.CompleteTask(ctx, t.ID); err != nil {
				return err
			}
		case taskFailed:
			if err := r.FailTask(ctx, t.ID); err != nil {
				return err
			}
		case taskRequeue:
			if err := r.RequeueTask(ctx, t.ID, res.notBefore, res.countAttempt); err != nil {
				return err
			}
			wake = true
		}
		if res.advance {
			next, ok := nextStage(run.Stage, it.UntilStage)
			if !ok {
				return nil
			}
			runs, err := r.StageRunsByItem(ctx, it.ID)
			if err != nil {
				return err
			}
			for _, nr := range runs {
				if nr.Stage == next {
					if _, err := insertStageTask(ctx, r, it.ID, nr, t.Priority); err != nil {
						return err
					}
					wake = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if wake {
		p.wake()
	}
	return nil
}

// errPermanent 建立 permanent 錯誤。
func errPermanent(detail string) error {
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: detail}
}

var errNoSource = errors.New("沒有原始字幕")
