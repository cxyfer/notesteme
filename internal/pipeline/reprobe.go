package pipeline

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 事後出現更好的來源（SRC-10，SRC-R18～R23）。
const (
	// ReprobeInterval 是重新 Probe 的間隔（SRC-R18，SRC 待決問題 R1）。
	ReprobeInterval = 12 * time.Hour
	// reprobeWindow 是發布後重新 Probe 的期間。
	reprobeWindow = 7 * 24 * time.Hour

	actionRerunBetter   = "改用更好的字幕重跑"
	msgSponsorUpdated   = "SponsorBlock 片段有更新，從章節重跑才會套用到章節、關鍵畫面與筆記。"
	actionRerunChapters = "從章節重跑"
	// betterActionNotify、betterActionRerun 是 source.better_available 的 action。
	betterActionNotify = "notify"
	betterActionRerun  = "rerun"
)

// sourceNames 是訊息中的來源名稱（SRC-R21）；沒有列出的來源用 ID。
var sourceNames = map[string]string{
	transcript.SourceYouTubeManual: "YouTube 人工字幕",
	transcript.SourceYouTubeAuto:   "YouTube 自動字幕",
	transcript.SourceBilibiliCC:    "Bilibili CC 字幕",
	transcript.SourceBilibiliAI:    "Bilibili AI 字幕",
}

// betterMessage 是發現更好的來源時 transcript 步驟的訊息（SRC-R21）。
func betterMessage(src, lang string, estimate *float64) string {
	name, ok := sourceNames[src]
	if !ok {
		name = src
	}
	tail := "目前無法估算金額。"
	if estimate != nil {
		tail = fmt.Sprintf("預估 $%.2f。", *estimate)
	}
	return fmt.Sprintf("發現更好的字幕來源：%s（%s）。重跑會再花一次 LLM 費用，%s", name, lang, tail)
}

// reprobeLang 檢查平台回報的語言（不可信輸入）：正規化後只接受 BCP-47 形狀的短代碼，才會寫進訊息與事件。
var reprobeLang = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,3}$`)

// reprobePlatform 回報平台會不會重新 Probe（SRC-R18：只有 YouTube 與 Bilibili）。
func reprobePlatform(platform string) bool {
	return platform == source.PlatformYouTube || platform == source.PlatformBilibili
}

// reprobeUntil 是重新 Probe 的期限：發布時間＋7 天，沒有發布時間時用影音的 created_at（SRC-R18）。
// 發布時間來自平台（不可信）：比 created_at 晚時用 created_at，否則未來的發布時間會讓期限延到好幾年後。
func reprobeUntil(it store.Item) time.Time {
	base := it.CreatedAt
	if it.PublishedAt != nil && it.PublishedAt.Before(base) {
		base = *it.PublishedAt
	}
	return base.Add(reprobeWindow)
}

// evalRank 回傳來源在評估順序中的位置（SRC 待決問題 R3）：caption_priority 的各條規則，再 fallback 的各個來源。
// 平台字幕用 transcript.RuleIndex；fallback 來源排在全部規則之後；都不是時排在最後。0 代表第一條。
func evalRank(platform string, p store.Probe, originalLang string, lang Language) int {
	rules := len(lang.CaptionPriority)
	if src := transcript.PlatformSource(platform, p.Kind); p.Kind != "" && src == p.SourceID {
		return transcript.RuleIndex(transcript.Track{Lang: p.Lang, Kind: p.Kind}, originalLang, lang.CaptionPriority, lang.AllowPlatformTranslated)
	}
	if i := slices.Index(lang.Fallback, p.SourceID); i >= 0 {
		return rules + i
	}
	return rules + len(lang.Fallback)
}

// usedProbe 回傳採用的那一列；沒有時 ok 為 false。
func usedProbe(ps []store.Probe) (store.Probe, bool) {
	i := slices.IndexFunc(ps, func(p store.Probe) bool { return p.State == transcript.StateUsed })
	if i < 0 {
		return store.Probe{}, false
	}
	return ps[i], true
}

func newProbesToProbes(ps []store.NewProbe) []store.Probe {
	out := make([]store.Probe, 0, len(ps))
	for _, p := range ps {
		out = append(out, store.Probe{SourceID: p.SourceID, Lang: p.Lang, Kind: p.Kind, State: p.State, Reason: p.Reason})
	}
	return out
}

// withReprobe 讓 transcript 與 sponsorblock 完成時，在寫回的交易中判斷要不要排定 source.reprobe（SRC-R18、R20）。
func (p *Pipeline) withReprobe(res result, run store.StageRun, eff Effective, lang Language) result {
	if res.task != taskDone || res.keepState || res.state.Status != store.StatusDone {
		return res
	}
	var hook func(ctx context.Context, r *store.Repo) error
	switch run.Stage {
	case StageTranscript:
		probes := newProbesToProbes(res.probes)
		hook = func(ctx context.Context, r *store.Repo) error {
			return p.reprobeAfterTranscript(ctx, r, run.ItemID, probes, lang)
		}
	case StageSponsorBlock:
		hook = func(ctx context.Context, r *store.Repo) error {
			return p.reprobeAfterSponsorBlock(ctx, r, run.ItemID, eff)
		}
	default:
		return res
	}
	prev := res.inTx
	res.inTx = func(ctx context.Context, r *store.Repo) (bool, error) {
		wake := false
		if prev != nil {
			w, err := prev(ctx, r)
			if err != nil {
				return w, err
			}
			wake = w
		}
		return wake, hook(ctx, r)
	}
	return res
}

// loadReprobe 讀取影音的重新 Probe 狀態；沒有時回傳新的一列。
func loadReprobe(ctx context.Context, r *store.Repo, itemID string) (store.Reprobe, error) {
	rp, err := r.Reprobe(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Reprobe{ItemID: itemID}, nil
	}
	return rp, err
}

// reprobeAfterTranscript 依 transcript 的新結果重新判斷字幕部分（SRC-R18）：
// 平台是 YouTube 或 Bilibili、better_source 不是 off、採用的來源不是評估順序的第一條、還在發布後 7 天內。
// 更好的來源與執行次數重設（重跑 transcript 之後依新的結果重新判斷，SRC-R23）。
func (p *Pipeline) reprobeAfterTranscript(ctx context.Context, r *store.Repo, itemID string, probes []store.Probe, lang Language) error {
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	rp, err := loadReprobe(ctx, r, itemID)
	if err != nil {
		return err
	}
	now := p.clock.Now()
	rp.Until, rp.Runs, rp.Better = reprobeUntil(it), 0, nil
	cur, ok := usedProbe(probes)
	rp.Sources = ok && reprobePlatform(it.Platform) && lang.BetterSource != BetterSourceOff &&
		now.Before(rp.Until) && evalRank(it.Platform, cur, it.OriginalLang, lang) > 0
	return p.syncReprobe(ctx, r, rp, now)
}

// reprobeAfterSponsorBlock 依 sponsorblock 的完成判斷 SponsorBlock 部分（SRC-R20）：YouTube、enabled、發布後 7 天內。
func (p *Pipeline) reprobeAfterSponsorBlock(ctx context.Context, r *store.Repo, itemID string, eff Effective) error {
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	rp, err := loadReprobe(ctx, r, itemID)
	if err != nil {
		return err
	}
	now := p.clock.Now()
	if rp.Until.IsZero() {
		rp.Until = reprobeUntil(it)
	}
	sb, err := eff.SponsorBlock()
	if err != nil {
		return err
	}
	rp.SponsorBlock = it.Platform == source.PlatformYouTube && sb.Enabled && now.Before(reprobeUntil(it))
	return p.syncReprobe(ctx, r, rp, now)
}

// syncReprobe 寫入狀態，並讓排程與狀態一致：有任何部分要做時，還沒有排定就排在 12 小時後（不晚於期限）；
// 都不用做時結束排程工作。已經排定的不改時間（第一次從 transcript 完成起算，SRC-R18）。
func (p *Pipeline) syncReprobe(ctx context.Context, r *store.Repo, rp store.Reprobe, now time.Time) error {
	if err := r.SaveReprobe(ctx, rp); err != nil {
		return err
	}
	if !rp.Sources && !rp.SponsorBlock {
		_, err := r.DeleteScheduledJob(ctx, store.ReprobeKind, rp.ItemID)
		return err
	}
	first := now.Add(ReprobeInterval)
	if first.After(rp.Until) {
		first = rp.Until
	}
	_, err := r.ScheduleJobIfAbsent(ctx, store.ReprobeKind, rp.ItemID, first)
	return err
}

// endReprobeOnRerun 在重跑的交易中結束受影響的部分（SRC-R18）：從 transcript 以前重跑時結束字幕部分並清除更好的來源；
// 從 sponsorblock 以前重跑時結束 SponsorBlock 部分。步驟重新完成後依新的結果重新判斷。自動重跑的次數保留（SRC-R22）。
func (p *Pipeline) endReprobeOnRerun(ctx context.Context, r *store.Repo, itemID, from string) error {
	i := slices.Index(Stages, from)
	if i > slices.Index(Stages, StageSponsorBlock) {
		return nil
	}
	rp, err := r.Reprobe(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	rp.SponsorBlock = false
	if i <= slices.Index(Stages, StageTranscript) {
		rp.Sources, rp.Better, rp.Runs = false, nil, 0
	}
	if err := r.SaveReprobe(ctx, rp); err != nil {
		return err
	}
	if !rp.Sources {
		_, err = r.DeleteScheduledJob(ctx, store.ReprobeKind, itemID)
	}
	return err
}

// ReprobeView 是 GET /api/items/:id 的 reprobe（SRC-R18）。
type ReprobeView struct {
	NextAt       time.Time
	Until        time.Time
	Runs         int
	Sources      bool
	SponsorBlock bool
}

// reprobeDetail 回傳影音的排程（沒有排定時為 nil）與更好的來源（沒有時為 nil）。
func reprobeDetail(ctx context.Context, r *store.Repo, itemID string) (*ReprobeView, *store.BetterSource, error) {
	rp, err := r.Reprobe(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	job, err := r.ScheduledJob(ctx, store.ReprobeKind, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, rp.Better, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &ReprobeView{NextAt: job.NextRunAt, Until: rp.Until, Runs: rp.Runs, Sources: rp.Sources, SponsorBlock: rp.SponsorBlock}, rp.Better, nil
}

// ReprobeOptions 是排程器傳給 Reprobe 的參數。
type ReprobeOptions struct {
	// Take 取一顆平台的 rate limit 額度（SUB-R10）；沒有額度時 ok 為 false，retryAt 是下一顆的時間。nil 代表不限。
	Take func(platform string) (ok bool, retryAt time.Time)
	// JitterMax 是設定檔的 scheduler.jitter_max。
	JitterMax time.Duration
}

// ReprobeResult 是一次 source.reprobe 的結果（對應 scheduler.Result）。
type ReprobeResult struct {
	Next     time.Time
	Done     bool
	Deferred bool
}

// Reprobe 執行一部影音到期的重新 Probe（SRC-R18～R22），由排程器在 now 呼叫。
func (p *Pipeline) Reprobe(ctx context.Context, itemID string, now time.Time, opts ReprobeOptions) (ReprobeResult, error) {
	r0 := p.st.Read()
	it, err := r0.ItemByID(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return ReprobeResult{Done: true}, nil
	}
	if err != nil {
		return ReprobeResult{}, err
	}
	if it.DeletedAt != nil {
		// 移到垃圾桶或永久刪除：結束（SRC-R18）。
		return ReprobeResult{Done: true}, nil
	}
	rp, err := r0.Reprobe(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return ReprobeResult{Done: true}, nil
	}
	if err != nil {
		return ReprobeResult{}, err
	}
	if now.After(rp.Until) {
		// 停機後補跑時已經超過期限的不再執行。
		return ReprobeResult{Done: true}, nil
	}
	runs, err := r0.StageRunsByItem(ctx, itemID)
	if err != nil {
		return ReprobeResult{}, err
	}
	latest := latestRuns(runs)
	if rp.Sources {
		if opts.Take != nil {
			if ok, at := opts.Take(it.Platform); !ok {
				return ReprobeResult{Deferred: true, Next: at}, nil
			}
		}
		rerun, err := p.reprobeSources(ctx, it, &rp, latest, now)
		if err != nil {
			return ReprobeResult{}, err
		}
		if rerun {
			// 自動重跑已經結束這個工作（endReprobeOnRerun），transcript 完成後依新的結果重新判斷。
			return ReprobeResult{Done: true}, nil
		}
	}
	if rp.SponsorBlock {
		if err := p.reprobeSponsorBlock(ctx, it, &rp, latest); err != nil {
			return ReprobeResult{}, err
		}
	}
	next := scheduler.NextWithJitter(store.ReprobeKind, itemID, now, ReprobeInterval, opts.JitterMax)
	done := false
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		// 執行期間使用者可能重跑了 transcript 或 sponsorblock（endReprobeOnRerun 已重設並結束對應部分）：
		// 以交易中最新的一列為準，只把這次關掉的部分關掉並累加次數，不蓋掉重跑的重設。
		cur, err := r.Reprobe(ctx, itemID)
		if errors.Is(err, store.ErrNotFound) {
			done = true
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := r.ScheduledJob(ctx, store.ReprobeKind, itemID); errors.Is(err, store.ErrNotFound) {
			done = true
			return nil
		} else if err != nil {
			return err
		}
		cur.Runs++
		cur.Sources = cur.Sources && rp.Sources
		cur.SponsorBlock = cur.SponsorBlock && rp.SponsorBlock
		done = !cur.Sources && !cur.SponsorBlock
		if !done && next.After(cur.Until) {
			// 最後一次不晚於期限。
			if now.Before(cur.Until) {
				next = cur.Until
			} else {
				done = true
			}
		}
		return r.SaveReprobe(ctx, cur)
	})
	if err != nil {
		return ReprobeResult{}, err
	}
	if done {
		return ReprobeResult{Done: true}, nil
	}
	return ReprobeResult{Next: next}, nil
}

// runSettings 讀取一筆 stage run 的設定快照。
func (p *Pipeline) runSettings(ctx context.Context, r *store.Repo, run store.StageRun) (Effective, error) {
	cfgJSON, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return nil, err
	}
	return ParseEffective(cfgJSON)
}

// reprobeSources 重新列出平台字幕軌（SRC-R19），不下載字幕、不呼叫 fallback 來源。
// 發現更好的來源時寫入事件、通知與步驟訊息（SRC-R21），設定為 rerun 時自動重跑（SRC-R22）；回傳是否已自動重跑。
func (p *Pipeline) reprobeSources(ctx context.Context, it store.Item, rp *store.Reprobe, latest map[string]store.StageRun, now time.Time) (bool, error) {
	tr, ok := latest[StageTranscript]
	if !ok || tr.Status != store.StatusDone {
		// transcript 正在重跑或沒有完成：這次略過，等它完成後重新判斷。
		return false, nil
	}
	r0 := p.st.Read()
	eff, err := p.runSettings(ctx, r0, tr)
	if err != nil {
		return false, err
	}
	lang, err := eff.Language()
	if err != nil {
		return false, err
	}
	probes, err := r0.LatestProbes(ctx, it.ID)
	if err != nil {
		return false, err
	}
	cur, ok := usedProbe(probes)
	if !ok || lang.BetterSource == BetterSourceOff || !reprobePlatform(it.Platform) {
		rp.Sources = false
		return false, nil
	}
	curRank := evalRank(it.Platform, cur, it.OriginalLang, lang)
	if curRank == 0 {
		rp.Sources = false
		return false, nil
	}
	ad, err := p.adapterFor(it.Platform)
	if err != nil {
		return false, err
	}
	info, err := ad.probe(ctx, it)
	if err != nil {
		return false, err
	}
	origLang, _ := inferOriginalLang(it.Platform, it.OriginalLang, info.Tracks, lang)
	chosen, decisions := selectTracks(it.Platform, info.Tracks, origLang, lang)
	var better *store.BetterSource
	if chosen != nil {
		b := store.Probe{SourceID: transcript.PlatformSource(it.Platform, chosen.Kind), Lang: transcript.NormalizeLang(chosen.Lang), Kind: chosen.Kind}
		if reprobeLang.MatchString(b.Lang) && evalRank(it.Platform, b, origLang, lang) < curRank {
			better = &store.BetterSource{Source: b.SourceID, Lang: b.Lang, Kind: b.Kind, FoundAt: now.UTC(), EstimateUSD: p.betterEstimate(ctx, it)}
		}
	}
	action := ""
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		if err := r.InsertProbesFor(ctx, it.ID, store.ProbeTriggerReprobe, toProbes(decisions)); err != nil {
			return err
		}
		if better == nil {
			return nil
		}
		// 交易中重新確認 transcript 仍是同一筆、仍然完成（期間可能被重跑）。
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		if cur := latestRuns(runs)[StageTranscript]; cur.ID != tr.ID || cur.Status != store.StatusDone {
			better = nil
			return nil
		}
		action = betterActionNotify
		if lang.BetterSource == BetterSourceRerun && rp.AutoReruns == 0 {
			edited, err := hasEditedTranscript(ctx, r, it.ID)
			if err != nil {
				return err
			}
			if !edited {
				action = betterActionRerun
				rp.AutoReruns++
			}
		}
		rp.Better, rp.Sources = better, false
		if err := r.SaveReprobe(ctx, *rp); err != nil {
			return err
		}
		msg := betterMessage(better.Source, better.Lang, better.EstimateUSD)
		actions := []string{actionRerunBetter}
		if err := r.SetStageMessage(ctx, tr, msg, actions); err != nil {
			return err
		}
		curRef := &events.SourceRef{Source: cur.SourceID, Lang: cur.Lang, Kind: cur.Kind}
		if err := r.Emit(ctx, events.Event{
			Type: notify.EventBetterAvailable, Item: source.Ref(it.Platform, it.ExternalID, it.Part), ItemID: it.ID,
			Current: curRef, Better: &events.SourceRef{Source: better.Source, Lang: better.Lang, Kind: better.Kind},
			Action: action, EstimateUSD: better.EstimateUSD,
		}); err != nil {
			return err
		}
		return p.emitBetterNotify(ctx, r, it.ID, better, msg, actions)
	})
	if err != nil || action != betterActionRerun {
		return false, err
	}
	// SRC-R22：沿用影音記錄的處理方案從 transcript 重跑，backfill 優先級，做到影音原本的 until。
	if _, err := p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Until: it.UntilStage, Priority: queue.PriorityBackfill}); err != nil {
		p.log.WarnContext(ctx, "改用更好的字幕來源自動重跑失敗，只通知", "item", it.ID, "error", err)
		return false, nil
	}
	return true, nil
}

// betterEstimate 是改用更好的來源重跑的估算（SRC-R23）：從 transcript 重跑到最後一個步驟、影音現行的設定，
// 與批次重跑的 dry_run（LIB-R14）相同的 COST-R5 估算（EstimateRerun）。更好的來源一定是平台字幕，ASR 費用為 0。
// 有任何付費任務無法估算（找不到單價、vision 等沒有估算方法的呼叫）、沒有任何付費任務、或估算失敗時為 nil，不是 0（SRC 待決問題 R10）。
func (p *Pipeline) betterEstimate(ctx context.Context, it store.Item) *float64 {
	// 更好的來源一定是平台字幕，transcript 不用錢：不估目前來源（可能是 ASR）的費用。
	est, err := p.estimateRerun(ctx, it.ID, StageTranscript, "", true)
	if err != nil {
		if ctx.Err() == nil {
			p.log.DebugContext(ctx, "估算改用更好的來源重跑的費用失敗，視為無法估算", "item", it.ID, "error", err.Error())
		}
		return nil
	}
	if est.Unknown > 0 || est.Priced == 0 {
		// 沒有任何付費的 LLM 任務（步驟都是 local、off 或沒有設定 provider）時也是 nil：
		// 訊息說「重跑會再花一次 LLM 費用」，估出「$0.00」反而誤導（SRC 待決問題 R10）。
		return nil
	}
	return usd(est.USD.USD())
}

// emitBetterNotify 在同一個交易中寫入通知 outbox（SRC-R21）；鍵含來源與語言，每部影音每個更好的來源只通知一次。
func (p *Pipeline) emitBetterNotify(ctx context.Context, r *store.Repo, itemID string, b *store.BetterSource, msg string, actions []string) error {
	if p.notify == nil {
		return nil
	}
	if want, err := p.notify.Wants(ctx, r, notify.EventBetterAvailable); err != nil || !want {
		return err
	}
	item, err := notify.ItemOf(ctx, r, itemID)
	if err != nil {
		return err
	}
	_, err = p.notify.Emit(ctx, r, notify.Event{
		Name: notify.EventBetterAvailable, DedupeKey: notify.DedupeBetterSource(itemID, b.Source+":"+b.Lang),
		Item: item, Stage: StageTranscript, Message: msg, Actions: actions,
	})
	return err
}

// hasEditedTranscript 回報影音有沒有使用者手動修改過的字幕（edited 變體，SRC-R22）。
func hasEditedTranscript(ctx context.Context, r *store.Repo, itemID string) (bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(arts, func(a store.Artifact) bool { return a.Variant == string(subtitle.VariantEdited) }), nil
}

// reprobeSponsorBlock 重新查詢 SponsorBlock（SRC-R20）：結果不同時建立新版本並在步驟帶提醒；相同時不建立；
// 查詢失敗時保留現行產出、不顯示訊息。
func (p *Pipeline) reprobeSponsorBlock(ctx context.Context, it store.Item, rp *store.Reprobe, latest map[string]store.StageRun) error {
	run, ok := latest[StageSponsorBlock]
	if !ok || run.Status != store.StatusDone {
		return nil
	}
	eff, err := p.runSettings(ctx, p.st.Read(), run)
	if err != nil {
		return err
	}
	if sb, err := eff.SponsorBlock(); err != nil || !sb.Enabled || it.Platform != source.PlatformYouTube {
		rp.SponsorBlock = false
		return err
	}
	res, err := p.runSponsorBlock(ctx, it, eff)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.log.WarnContext(ctx, "重新查詢 SponsorBlock 失敗，保留現行的片段", "item", it.ID, "error", err)
		return nil
	}
	if res.state.Message != "" || res.artifact == nil {
		// 查詢失敗（runSponsorBlock 只在 outcome 為 error 時帶訊息）或結果相同：不建立新版本。
		p.discardResultFiles(ctx, res)
		return nil
	}
	var purged []store.Artifact
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		if cur := latestRuns(runs)[StageSponsorBlock]; cur.ID != run.ID || cur.Status != store.StatusDone {
			return errReprobeStale
		}
		a, err := r.InsertArtifact(ctx, *res.artifact)
		if err != nil {
			return err
		}
		if purged, err = r.PurgeOldArtifacts(ctx, a.ItemID, a.Kind, a.Variant, a.Lang, keepArtifactVersions); err != nil {
			return err
		}
		sr := *res.stage
		sr.ArtifactID, sr.CacheKey = a.ID, run.CacheKey
		if err := r.SetStageResult(ctx, run.ID, sr); err != nil {
			return err
		}
		return r.SetStageMessage(ctx, run, msgSponsorUpdated, []string{actionRerunChapters})
	})
	if err != nil {
		p.discardResultFiles(ctx, res)
		if errors.Is(err, errReprobeStale) {
			return nil
		}
		return err
	}
	for _, a := range purged {
		p.discardArtifactFile(ctx, &store.NewArtifact{ID: a.ID, ItemID: a.ItemID, Kind: a.Kind, Path: a.Path})
	}
	return nil
}

// errReprobeStale 是寫入前發現步驟已經被重跑：丟棄這次的結果。
var errReprobeStale = errors.New("步驟已經被重跑")
