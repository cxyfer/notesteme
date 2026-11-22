package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
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
	// artifacts 是同一個步驟額外產生的產出（例如 translate 每個目標語言一份）；寫入順序在 artifact 之後。
	// stage_runs.artifact_id 指向第一份（artifact，沒有時是 artifacts[0]）。
	artifacts []*store.NewArtifact
	// supersede 是這次不再產生、但目前仍是現行版本的產出組：寫入結果時標記為被這次的第一份產出取代
	// （產出的取代以語言為單位，目標語言減少時舊語言的產出不會自己被取代）。
	supersede *supersedeSet
	probes    []store.NewProbe
	meta      *metaUpdate
	// advance 為 true 時，成功後建立下游步驟的任務（until 之前）。
	advance bool
	// startedAt 是這次執行開始的時間（Handle 開頭記下），apply 用它判斷 blocked 的恢復條件是否在執行中已經滿足。
	startedAt time.Time
	// transcriptASR 只用於 metadata：下游的 transcript 任務是否歸為 asr 資源類別。
	transcriptASR bool
	// afterCommit 在結果確實寫入（交易 commit，而且不是過期或租約失效）之後執行，
	// 用來清掉只有舊記錄會用到的檔案（例如 podcast 換了副檔名的舊音檔）。
	afterCommit func()
	// afterDiscard 在結果沒有寫入時執行（過期、租約失效、ctx 取消或寫回失敗），
	// 用來刪掉這次執行已暫存、沒有任何記錄會指向的檔案（例如 podcast 剛下載的音檔）。
	afterDiscard func()
	// keepState 為 true 時不改寫步驟狀態（LLM 步驟的 plan 與 chunk 任務：步驟要等所有 chunk 完成才有結果）。
	keepState bool
	// inTx 在寫回的交易中、任務狀態寫入之後執行（例如寫入 chunk 的結果、建立下一個任務）；回傳 true 代表建立了任務，要喚醒 worker。
	inTx func(ctx context.Context, r *store.Repo) (wake bool, err error)
}

// discard 執行 afterDiscard（有設定的話）。
func (r result) discard() {
	if r.afterDiscard != nil {
		r.afterDiscard()
	}
}

// supersedeSet 是要撤下的現行產出：影音的 (kind, variant) 在這些語言的版本（Repo.WithdrawArtifacts）。
type supersedeSet struct {
	Kind, Variant string
	Langs         []string
}

type metaUpdate struct {
	item    store.ItemMeta
	channel *store.Channel
}

// Handle 執行一個已領取的任務，並把結果寫回（worker.Handler）。
// 步驟自己的失敗（工具錯誤、沒有字幕…）依錯誤類別寫進步驟狀態，不會回傳錯誤；
// 回傳的錯誤只代表資料庫或檔案系統出了問題。ctx 取消（關閉中）時不改變任何狀態，租約之後會放回佇列。
func (p *Pipeline) Handle(ctx context.Context, t store.Task) error {
	started := p.clock.Now()
	r := p.st.Read()
	run, err := r.StageRun(ctx, t.StageRunID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, store.ErrNotFound) {
			// 步驟已隨影音永久刪除：沒有東西可以寫回。
			p.log.Info("步驟已不存在，略過任務", "task", t.ID, "stage_run", t.StageRunID)
			return nil
		}
		return fmt.Errorf("讀取步驟 %s：%w", t.StageRunID, err)
	}
	it, err := r.ItemByID(ctx, run.ItemID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, store.ErrNotFound) {
			// 同上：影音已永久刪除。
			p.log.Info("影音已不存在，略過任務", "task", t.ID, "item", run.ItemID)
			return nil
		}
		return fmt.Errorf("讀取影音 %s：%w", run.ItemID, err)
	}
	if it.DeletedAt != nil {
		// 領取之後、取消之前影音被移到垃圾桶：不執行步驟，結束任務（DEL-R1）。
		return p.dropTrashedTask(ctx, it.ID)
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

	// 步驟前進時已記錄解析後的 executor；沒有記錄（M1 的步驟）時依設定解析。
	executor := run.Executor
	if executor == "" {
		if rs, rerr := ResolveExecutor(eff, run.Stage, StageContext{}); rerr == nil {
			executor = rs.Executor
		}
	}
	var res result
	switch {
	case !stageImplemented(run.Stage, executor):
		// 沒有實作的 (步驟, executor)（例如單次覆寫指定了 provider）：任務照常建立，在這裡看得見地失敗，
		// 之後的切片在 stageImplemented 與這個分派登記它的 handler。
		err = &errclass.Error{
			Class: errclass.ClassPermanent, Source: "pipeline", Detail: "步驟尚未實作 " + run.Stage + "／" + executor,
			Message: "這個步驟還沒有實作（" + run.Stage + "／" + executor + "）。",
		}
	case run.Stage == StageMetadata:
		res, err = p.runMetadata(ctx, it, lang)
	case run.Stage == StageTranscript:
		res, err = p.runTranscript(ctx, it, lang)
	case run.Stage == StageNormalize:
		res, err = p.runNormalize(ctx, it)
	case run.Stage == StageTranslate && executor == ExecutorOpenCC:
		res, err = p.runTranslateOpenCC(ctx, it, eff)
	case usesLLMEngine(run.Stage, executor):
		res, err = p.runLLM(ctx, t, run, it, eff)
	default:
		err = &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "不認識的步驟 " + run.Stage}
	}
	if ctx.Err() != nil {
		// 關閉或取消中：不寫回，已寫出的產出檔也不會有記錄指向它。
		p.discardResultFiles(res)
		return nil
	}
	if errors.Is(err, errItemGone) {
		// 影音在執行期間被移到垃圾桶或永久刪除：產出沒有寫出（DEL-R2），任務與步驟比照移到垃圾桶結束，
		// 不能留下 leased 或 running。
		p.log.Info("影音已不在媒體庫中，丟棄這次的結果", "item", it.ID, "stage", run.Stage)
		return p.dropTrashedTask(ctx, it.ID)
	}
	if err != nil {
		p.log.Warn("步驟失敗", "item", it.ID, "stage", run.Stage, "attempt", t.Attempts, "error", err)
		res = p.classify(t, err)
	}
	res.startedAt = started
	if err := p.apply(ctx, t, run, it, res); err != nil {
		return fmt.Errorf("寫回步驟 %s 的結果：%w", run.Stage, err)
	}
	return nil
}

// dropTrashedTask 結束垃圾桶中影音的任務（DEL-R1）：在交易內重新確認它仍在垃圾桶中，
// 然後和移到垃圾桶時一樣把未結束的步驟改成 skipped（原因 cancelled，動作「重試」）、任務改成 cancelled。
// 影音已被永久刪除時步驟與任務已隨之刪除，什麼都不用做；已被還原時不動，任務的租約到期後會重新領取。
func (p *Pipeline) dropTrashedTask(ctx context.Context, itemID string) error {
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		it, err := r.ItemByID(ctx, itemID)
		if err != nil {
			return err
		}
		if it.DeletedAt == nil {
			return nil
		}
		_, err = cancelItemTx(ctx, r, itemID, trashedStageState())
		return err
	})
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("結束垃圾桶中影音的任務：%w", err)
	}
	return nil
}

// classify 把步驟的錯誤依 plan 第 5.2 節的錯誤類別對應到步驟狀態與任務的處置。
// 沒有分類的錯誤（解析失敗、檔案系統…）視為 retryable。
func (p *Pipeline) classify(t store.Task, err error) result {
	// 被防護擋下（內部網路、轉址太多、回應太大、轉址到不支援的 scheme）時重試也不會成功，歸為 permanent 並告訴使用者原因。
	// 要在 ClassOf 之前判斷：來源會把連線錯誤包成 retryable，例如 Supadata。
	if msg, ok := netguard.PolicyMessage(err); ok {
		return result{task: taskFailed, state: store.StageState{
			Status: store.StatusFailed, ErrorClass: string(errclass.ClassPermanent), Message: msg,
		}}
	}
	class := errclass.ClassRetryable
	if c, ok := errclass.ClassOf(err); ok {
		class = c
	}
	switch class {
	case errclass.ClassPermanent:
		st := store.StageState{
			Status: store.StatusFailed, ErrorClass: string(class), Message: "這個步驟無法完成，重試也不會成功。",
		}
		// 錯誤自己帶了給使用者看的訊息時採用（例如本機影音「找不到這個檔案。」）。
		var ce *errclass.Error
		if errors.As(err, &ce) && ce.Message != "" {
			st.Message = ce.Message
		}
		return result{task: taskFailed, state: st}
	case errclass.ClassNeedsAction, errclass.ClassBudgetBlocked:
		reason := "needs_action"
		if class == errclass.ClassBudgetBlocked {
			reason = "budget_blocked"
		}
		st := store.StageState{
			Status: store.StatusBlocked, BlockReason: reason, ErrorClass: string(class), Message: "這個步驟需要你處理才能繼續。",
		}
		// 錯誤自己帶了給使用者看的訊息與修正動作時採用（SRC-R9：403 → 更新 yt-dlp、上傳 cookie）。
		var ce *errclass.Error
		if errors.As(err, &ce) && ce.Message != "" {
			st.Message, st.Actions = ce.Message, ce.Actions
		}
		// 錯誤指定了恢復條件（例如 cookie:bilibili）時一併記錄，補上 cookie 後可依它恢復。
		if ce != nil {
			st.ResumeOn = ce.ResumeOn
		}
		return result{task: taskFailed, state: st}
	case errclass.ClassWait:
		// wait 類的錯誤由執行者自己決定何時再檢查（見 runMetadata）；走到這裡時用錯誤指定的等待時間（例如 LLM 的限流，LLM-R7），
		// 沒有指定就用預設值。稍後再試且不計入次數。
		d, msg := queue.WaitRecheckDefault, "還沒有結果，稍後會再檢查。"
		var ce *errclass.Error
		if errors.As(err, &ce) {
			if ce.RetryAfter > 0 {
				d = ce.RetryAfter
			}
			if ce.Message != "" {
				msg = ce.Message
			}
		}
		at := p.clock.Now().Add(d)
		return result{task: taskRequeue, notBefore: &at, countAttempt: false, state: store.StageState{
			Status: store.StatusWaiting, ErrorClass: string(class), Message: msg, NotBefore: &at,
		}}
	}
	// retryable：指數退避，次數用盡後失敗（PIPE-R3）。來源指定了 Retry-After 時依它（LLM-R6）。
	if d, ok := queue.RetryDelay(t.Attempts, t.MaxAttempts); ok {
		var ce *errclass.Error
		if errors.As(err, &ce) && ce.RetryAfter > 0 {
			d = ce.RetryAfter
		}
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
//
// 重跑之後同一個步驟有多筆 stage run；寫入前先確認這一筆仍是該步驟最新的。
// 不是的話代表較新的重跑已經取代了它，結果整份丟棄（含已寫出的產出檔），
// 否則較晚完成的舊任務會蓋掉較新的現行版本。
func (p *Pipeline) apply(ctx context.Context, t store.Task, run store.StageRun, it store.Item, res result) error {
	var wake, stale bool
	var purged []store.Artifact
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		purged = nil
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		latest := latestRuns(runs)
		if latest[run.Stage].ID != run.ID {
			stale = true
			return discardStale(ctx, r, t, run)
		}
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
			outs := res.allArtifacts()
			for i, na := range outs {
				a, err := r.InsertArtifact(ctx, *na)
				if err != nil {
					return err
				}
				if i == 0 {
					sr.ArtifactID = a.ID
				}
				// 新版本寫入後，同一組只保留最近幾版（PIPE-R7）；檔案在 commit 之後才刪。
				old, err := r.PurgeOldArtifacts(ctx, a.ItemID, a.Kind, a.Variant, a.Lang, keepArtifactVersions)
				if err != nil {
					return err
				}
				purged = append(purged, old...)
			}
			if len(outs) == 0 && sr.ArtifactID != "" {
				// 快取命中重用既有產出（PIPE-R5）。它可能已被後來的版本取代
				// （設定 A → B → 改回 A）：選擇「恢復」而不是重新產生，
				// 因為快取鍵相同就是不要重複付費；恢復讓它重新成為現行版本，原本的現行版本改指向它。
				if err := r.RestoreArtifact(ctx, sr.ArtifactID); err != nil {
					return err
				}
			}
			if g := res.supersede; g != nil && len(g.Langs) > 0 {
				if err := r.WithdrawArtifacts(ctx, it.ID, g.Kind, g.Variant, g.Langs); err != nil {
					return err
				}
			}
			if err := r.SetStageResult(ctx, run.ID, sr); err != nil {
				return err
			}
		}
		// 執行中使用者已經補上了恢復條件（例如上傳 cookie）時，ResumeBlocked 找不到這個還在 running 的步驟，
		// 若照常寫成 blocked 就永遠卡住；改為 requeue：pending、新任務、不計入嘗試次數。
		resolved, err := resumeAlreadyResolved(ctx, r, res)
		if err != nil {
			return err
		}
		if resolved {
			if err := requeueResolved(ctx, r, t, run); err != nil {
				return err
			}
			wake = true
			return nil
		}
		if !res.keepState {
			if err := r.SetStageState(ctx, run.ID, res.state); err != nil {
				return err
			}
		}
		switch res.task {
		case taskDone:
			if err := r.CompleteTask(ctx, t.ID, t.LeaseToken); err != nil {
				return err
			}
		case taskFailed:
			if err := r.FailTask(ctx, t.ID, t.LeaseToken); err != nil {
				return err
			}
			if t.ChunkIndex != nil {
				// 一個 chunk 讓步驟停下（blocked、failed）：同一個步驟其他還沒領取的 chunk 不再執行，
				// 否則下一次領取會把步驟改回 running，蓋掉 blocked 的訊息與動作。
				if _, err := r.CancelPendingTasks(ctx, []string{run.ID}); err != nil {
					return err
				}
			}
		case taskRequeue:
			if err := r.RequeueTask(ctx, t.ID, t.LeaseToken, res.notBefore, res.countAttempt); err != nil {
				return err
			}
			wake = true
		}
		if res.inTx != nil {
			w, err := res.inTx(ctx, r)
			if err != nil {
				return err
			}
			wake = wake || w
		}
		if res.advance {
			// metadata 完成、頻道確定之後，把頻道層併進同一批還沒開始的下游步驟（D6）。
			// 要在 nextStage 之前（until=metadata 時也要改綁），而且會同步更新 latest：下面前進時讀的是它。
			if run.Stage == StageMetadata {
				if err := p.rebindChannel(ctx, r, it.ID, run, latest); err != nil {
					return err
				}
			}
			next, ok := nextStage(run.Stage, it.UntilStage)
			if !ok {
				return nil
			}
			// 走到這裡時這一筆一定是最新的（上面已檢查），舊的結果不會觸發下游。
			nr, ok := latest[next]
			if !ok {
				return nil
			}
			// 依下一步的設定快照解析執行方式：可以略過的步驟直接 skipped 並繼續往下，
			// 直到第一個要執行的步驟或 until 為止（until 之後的步驟維持 pending）。
			snap, err := r.Snapshot(ctx, nr.ConfigSnapshotID)
			if err != nil {
				return fmt.Errorf("讀取設定快照 %s：%w", nr.ConfigSnapshotID, err)
			}
			eff, err := ParseEffective(snap)
			if err != nil {
				return err
			}
			sc, err := p.stageContext(ctx, r, it, "")
			if err != nil {
				return err
			}
			chain, err := planChain(eff, next, it.UntilStage, sc)
			if err != nil {
				return err
			}
			created, err := p.applyChain(ctx, r, it.ID, chain, latest, t.Priority, res.transcriptASR)
			if err != nil {
				return err
			}
			wake = wake || created
		}
		return nil
	})
	// 租約已經失效（被放回、到期後被別人領走，或任務已被取消）：寫回的交易已整個回復，
	// 這次的結果不算數。不是錯誤；產出檔沒有記錄指向它，一併丟棄。
	lost := errors.Is(err, store.ErrLeaseLost)
	if lost {
		p.log.Warn("任務的租約已失效，丟棄這次的結果", "task", t.ID, "stage", run.Stage)
		err = nil
	}
	if err != nil {
		// 交易沒有 commit（例如 ctx 取消、資料庫錯誤）：沒有記錄會指向已寫出的產出檔。
		p.discardResultFiles(res)
		return err
	}
	if stale || lost {
		// 結果已丟棄，沒有記錄會指向這些檔案；清掉避免留下孤兒檔。
		p.discardResultFiles(res)
	} else {
		// 已 commit：被清除版本的記錄不在了，檔案也一併刪除。
		for _, a := range purged {
			p.discardArtifactFile(&store.NewArtifact{Path: a.Path})
		}
		if res.afterCommit != nil {
			res.afterCommit()
		}
	}
	if wake {
		p.wake()
	}
	return nil
}

// allArtifacts 依寫入順序列出結果帶的所有產出。
func (r result) allArtifacts() []*store.NewArtifact {
	var out []*store.NewArtifact
	if r.artifact != nil {
		out = append(out, r.artifact)
	}
	return append(out, r.artifacts...)
}

// discardResultFiles 刪除結果帶的所有產出檔（沒有 commit 或結果被丟棄）。
func (p *Pipeline) discardResultFiles(res result) {
	for _, a := range res.allArtifacts() {
		p.discardArtifactFile(a)
	}
	res.discard() // 這次暫存的其他檔案（例如 podcast 的音檔）
}

// discardArtifactFile 刪除已寫出但不會被任何記錄指向的產出檔（沒有 commit 或結果被丟棄）。a 為 nil 時不做事。
// 刪除失敗只記錄：檔案留下來不影響正確性，只是多一個孤兒檔。
func (p *Pipeline) discardArtifactFile(a *store.NewArtifact) {
	if a == nil {
		return
	}
	if err := os.Remove(filepath.Join(p.dataDir, filepath.FromSlash(a.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
		p.log.Warn("清除被丟棄的產出檔失敗", "path", a.Path, "error", err)
	}
}

// keepArtifactVersions 是同一組 (item, kind, variant, lang) 保留的最近版本數（PIPE-R7）。
const keepArtifactVersions = 3

// discardStale 處理已被較新重跑取代的 stage run 所領取的任務：
// 任務完成（不重試、不放回佇列），步驟標為 skipped 而不是留在 running／pending。
// skipped 是既有狀態值中最貼近「被取消、不再處理」的終止狀態（PIPE-R15）。
func discardStale(ctx context.Context, r *store.Repo, t store.Task, run store.StageRun) error {
	if err := r.SetStageState(ctx, run.ID, supersededState()); err != nil {
		return err
	}
	return r.CompleteTask(ctx, t.ID, t.LeaseToken)
}

// supersededState 是被重跑取代的步驟的狀態。
func supersededState() store.StageState {
	return store.StageState{Status: store.StatusSkipped, BlockReason: SkipCancelled, Message: "已被較新的重跑取代。"}
}

// errPermanent 建立 permanent 錯誤。
func errPermanent(detail string) error {
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: detail}
}

var errNoSource = errors.New("沒有原始字幕")

// errItemGone 表示影音在步驟執行期間被移到垃圾桶或已被永久刪除：產出要丟棄，不寫檔（DEL-R2）。
var errItemGone = errors.New("影音已在垃圾桶中或不存在")

// writeCuesArtifact 在寫出產出檔之前，先確認影音不在垃圾桶中而且還存在，否則回傳 errItemGone，不寫任何檔案。
// 檢查與寫檔之間仍有空隙：影音在這之後才被刪除時，任務已在資料庫中取消，寫回會因租約失效而丟棄產出檔（apply）。
func (p *Pipeline) writeCuesArtifact(ctx context.Context, itemID string, tr subtitle.Transcript, parents []string, prov json.RawMessage) (*store.NewArtifact, error) {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, errItemGone
	case err != nil:
		return nil, err
	case it.DeletedAt != nil:
		return nil, errItemGone
	}
	return p.newCuesArtifact(itemID, tr, parents, prov)
}
