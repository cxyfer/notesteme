package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 摘要（DIG-R2～R5）：產出的 kind 與 variant 同名。
const (
	KindSummary = "summary"
	summaryFmt  = "notesteme-summary+json"
	// 預設的上限（DIG-R2）。
	defaultSummaryMaxDuration    = 2 * time.Hour
	defaultSummaryMaxInputTokens = 32000
	// summaryDescriptionRunes 是放進資料區塊的影片說明長度（DIG-R2）。
	summaryDescriptionRunes = 2000
	actionRegenerateSummary = "重新產生摘要"
)

// 摘要後暫停（DIG-R6、R7）。
const (
	BlockAwaitingUser = "awaiting_user"
	msgAwaitingUser   = "摘要完成，等你決定要不要繼續處理。"
	actionContinue    = "繼續處理"
	actionCancel      = "取消"
)

// ErrNotAwaitingUser 是影音沒有 awaiting_user 的步驟（DIG-R7，API 回 409 not_awaiting_user）。
var ErrNotAwaitingUser = errors.New("這部影音沒有在等待你決定是否繼續。")

// Summary 是 summary 產出的內容（DIG-R5）。TitleTranslation 為 nil 時 JSON 是 null。
type Summary struct {
	Text             string   `json:"text"`
	KeyPoints        []string `json:"key_points"`
	TitleTranslation *string  `json:"title_translation"`
	Language         string   `json:"language"`
}

// summarySettings 是 stages.summary 中摘要專屬的欄位（DIG 的「介面」）。
type summarySettings struct {
	Language       string `json:"language"`
	PauseAfter     bool   `json:"pause_after"`
	MaxDuration    string `json:"max_duration"`
	MaxInputTokens *int   `json:"max_input_tokens"`
}

func summarySettingsOf(eff Effective) (summarySettings, error) {
	var s summarySettings
	extras, err := stageExtras(eff, StageSummary)
	if err != nil || extras == nil {
		return s, err
	}
	b, err := json.Marshal(extras)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, invalidf("stages.summary 設定不正確：%v", err)
	}
	return s, nil
}

// limits 回傳片長與輸入 token 的上限；沒有設定或格式不對（處理方案已驗證過）時用預設值。
func (s summarySettings) limits() (time.Duration, int) {
	dur, tokens := defaultSummaryMaxDuration, defaultSummaryMaxInputTokens
	if s.MaxDuration != "" {
		if d, err := auth.ParseTokenDuration(s.MaxDuration); err == nil && d > 0 {
			dur = d
		}
	}
	if s.MaxInputTokens != nil && *s.MaxInputTokens > 0 {
		tokens = *s.MaxInputTokens
	}
	return dur, tokens
}

// summaryLangOf 決定摘要語言（DIG-R3）：stages.summary.language，否則同 NOTE-R5 的筆記語言
// （language.targets 的第一個，沒有目標語言時用原文語言）。
func summaryLangOf(eff Effective, s summarySettings, srcLang string) string {
	if s.Language != "" {
		return s.Language
	}
	if l, err := eff.Language(); err == nil && len(l.Targets) > 0 && l.Targets[0] != "" {
		return l.Targets[0]
	}
	return srcLang
}

// sameLanguage 回報原文語言與摘要語言是不是同一種語言（DIG-R3 的 title_translation）：主語言相同，
// 中文另外要字形相同（zh-Hans 的標題對 zh-Hant 的摘要仍然需要譯文）；不知道字形時視為相同。
func sameLanguage(src, dst string) bool {
	if !langtag.SameBase(src, dst) {
		return false
	}
	if !langtag.IsChinese(src) {
		return true
	}
	a, b := langtag.ChineseScript(src), langtag.ChineseScript(dst)
	return a == "" || b == "" || a == b
}

// hmsText 把秒數寫成 H:MM:SS（小時不補零，DIG-R2、R10）。
func hmsText(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

// HMS 是 hmsText 的公開版本（彙整的片長，DIG-R10）。
func HMS(sec int64) string { return hmsText(sec) }

// summaryTooLongError 是 prepareSummary 判斷超過上限（DIG-R2）：plan 不呼叫 LLM，步驟改成 skipped（too_long）。
type summaryTooLongError struct{ message string }

func (e *summaryTooLongError) Error() string { return e.message }

// summaryStage 是 summary 在 LLM 引擎中的登記：一部影音一個請求（prepareSummary），輸出寫成 summary 產出（writeSummary）。
var summaryStage = llmStage{
	spec:    func(map[string]any) *llm.Spec { return llm.SummarySpec() },
	prepare: (*Pipeline).prepareSummary,
	write:   (*Pipeline).writeSummary,
}

// summaryInput 回傳影音現行的 normalized 字幕（DIG-R2：摘要只用正規化的字幕，不用校正或譯文）。
func summaryInput(ctx context.Context, r *store.Repo, itemID, _ string) (store.Artifact, bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return store.Artifact{}, false, err
	}
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Variant == string(subtitle.VariantNormalized) {
			return arts[i], true, nil
		}
	}
	return store.Artifact{}, false, nil
}

// prepareSummary 是 summary 的 llmStage.prepare（DIG-R2、R3）：資料區塊只有標題、頻道與說明的前 2000 字，
// 不放章節與詞彙表；整份字幕一個 chunk；超過片長或 token 上限時回傳 *summaryTooLongError。
func (p *Pipeline) prepareSummary(_ context.Context, it store.Item, job *llmJob, meta *llm.Metadata, vars *llm.TemplateVars) error {
	set, err := summarySettingsOf(job.eff)
	if err != nil {
		return err
	}
	meta.Chapters, meta.Glossary = nil, nil
	if r := []rune(meta.Description); len(r) > summaryDescriptionRunes {
		meta.Description = string(r[:summaryDescriptionRunes])
	}
	lang := summaryLangOf(job.eff, set, vars.SourceLang)
	vars.SummaryLang = lang

	maxDur, maxTokens := set.limits()
	if ms := durationMS(it); ms > maxDur.Milliseconds() {
		return &summaryTooLongError{fmt.Sprintf("影片長度 %s 超過摘要的上限 %s，略過摘要。", hmsText(ms/1000), hmsText(int64(maxDur/time.Second)))}
	}
	tokens := llm.EstimateTokens(meta.Title) + llm.EstimateTokens(meta.Channel) + llm.EstimateTokens(meta.Description)
	for _, c := range job.cues {
		tokens += llm.EstimateTokens(c.Text) + 4
	}
	if tokens > maxTokens {
		return &summaryTooLongError{fmt.Sprintf("字幕太長（約 %d tokens），超過摘要的上限 %d tokens，略過摘要。", roundTokens(tokens), maxTokens)}
	}
	job.chunks = nil
	if len(job.cues) > 0 {
		job.chunks = []llm.Chunk{{Index: 1, From: 1, To: len(job.cues)}}
	}
	// 摘要語言可能來自 language.targets（不在 stages.summary 的 params 中）：併進步驟層的快取鍵。
	job.extraKey = sha256Hex([]byte("summary_lang=" + lang))
	return nil
}

// roundTokens 把估算的 token 數取到百位，訊息只是大約的數字。
func roundTokens(n int) int {
	if n < 1000 {
		return n
	}
	return (n + 50) / 100 * 100
}

// summarySkipTooLong 是超過上限時的結果（DIG-R2）：skipped（too_long）並前進；pause_after 照常套用（DIG-R6）。
func (p *Pipeline) summarySkipTooLong(run store.StageRun, msg string) result {
	stage := &store.StageResult{Executor: stageExecutor(run), ImplVersion: p.implVersion(StageSummary)}
	return result{task: taskDone, advance: true, stage: stage, state: store.StageState{
		Status: store.StatusSkipped, BlockReason: SkipTooLong, Message: msg,
	}}
}

// summaryFallbackMessage 是重試用盡時的訊息（DIG-R4）。
func summaryFallbackMessage(fallbacks []fallbackRecord) string {
	reason := "格式不正確"
	if len(fallbacks) > 0 {
		if s, ok := fallbacks[0].Detail["error"].(string); ok && s != "" {
			reason = s
		} else if fallbacks[0].Reason != "" {
			reason = fallbacks[0].Reason
		}
	}
	return fmt.Sprintf("無法產生摘要：模型回傳的內容沒有通過驗證（%s）。", reason)
}

// reuseArtifactField 是 write 通知 assembleLLM「內容與現行產出相同，沿用它」的欄位（不寫進來源紀錄）。
const reuseArtifactField = "\x00reuse_artifact"

// writeSummary 是 summary 的 llmStage.write（DIG-R3～R5）。重試用盡時步驟仍是 done、不寫入產出，附上訊息與動作；
// 內容與現行產出完全相同時沿用現行產出，不建立新版本。
func (p *Pipeline) writeSummary(ctx context.Context, it store.Item, job *llmJob, outputs []json.RawMessage, fallbacks []fallbackRecord, fields map[string]any) (*store.NewArtifact, store.StageState, error) {
	state := store.StageState{Status: store.StatusDone}
	if len(fallbacks) > 0 || len(outputs) == 0 || outputs[0] == nil {
		state.Message = summaryFallbackMessage(fallbacks)
		state.Actions = []string{actionRegenerateSummary, actionOpenPrompt}
		return nil, state, nil
	}
	out, verr := llm.CheckSummary(outputs[0])
	if verr != nil {
		return nil, state, errPermanent("摘要的輸出不正確：" + verr.Message)
	}
	set, err := summarySettingsOf(job.eff)
	if err != nil {
		return nil, state, err
	}
	srcLang := job.tr.Lang
	if srcLang == "" {
		srcLang = it.OriginalLang
	}
	doc := Summary{Text: out.Summary, KeyPoints: out.KeyPoints, TitleTranslation: out.TitleTranslation, Language: summaryLangOf(job.eff, set, srcLang)}
	if doc.KeyPoints == nil {
		doc.KeyPoints = []string{}
	}
	if sameLanguage(srcLang, doc.Language) {
		doc.TitleTranslation = nil // 原文與摘要同語言時丟掉模型回傳的譯文（DIG-R3）
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, state, err
	}
	if cur, ok, err := p.currentSummaryArtifact(ctx, it.ID); err != nil {
		return nil, state, err
	} else if ok && cur.SHA256 == sha256Hex(body) {
		fields[reuseArtifactField] = cur.ID
		return nil, state, nil
	}
	fields["summary"] = map[string]any{"language": doc.Language}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return nil, state, err
	}
	art, err := p.newSummaryArtifact(it.ID, body, []string{job.input.ID}, prov)
	return art, state, err
}

// newSummaryArtifact 寫出 summary 產出檔，回傳要寫入的產出記錄。
func (p *Pipeline) newSummaryArtifact(itemID string, body []byte, parents []string, prov json.RawMessage) (*store.NewArtifact, error) {
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(p.dataDir, itemID, id, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return nil, fmt.Errorf("寫入產出檔：%w", err)
	}
	return &store.NewArtifact{
		ID: id, ItemID: itemID, Kind: KindSummary, Variant: KindSummary, Format: summaryFmt,
		Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
	}, nil
}

// currentSummaryArtifact 回傳影音現行的 summary 產出。
func (p *Pipeline) currentSummaryArtifact(ctx context.Context, itemID string) (store.Artifact, bool, error) {
	return currentSummaryIn(ctx, p.st.Read(), itemID)
}

func currentSummaryIn(ctx context.Context, r *store.Repo, itemID string) (store.Artifact, bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, KindSummary)
	if err != nil || len(arts) == 0 {
		return store.Artifact{}, false, err
	}
	return arts[len(arts)-1], true, nil
}

// ItemSummary 回傳影音現行的摘要（DIG-R5）；沒有摘要產出時 ok 為 false。
// 產出檔讀不到或內容與 sha256 不符時回傳錯誤，由呼叫端決定是否略過。
func (p *Pipeline) ItemSummary(ctx context.Context, itemID string) (Summary, bool, error) {
	art, ok, err := p.currentSummaryArtifact(ctx, itemID)
	if err != nil || !ok {
		return Summary{}, false, err
	}
	return p.readSummary(art)
}

func (p *Pipeline) readSummary(art store.Artifact) (Summary, bool, error) {
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(art.Path)))
	if err != nil {
		return Summary{}, false, err
	}
	if sha256Hex(b) != art.SHA256 {
		return Summary{}, false, fmt.Errorf("%s：%w", art.Path, ErrCorruptArtifact)
	}
	var doc Summary
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		return Summary{}, false, err
	}
	if doc.KeyPoints == nil {
		doc.KeyPoints = []string{}
	}
	return doc, true, nil
}

// pauseAfterSummary 是 DIG-R6：summary 結束（done，或 skipped 的 too_long）而且 summary 那一筆的設定有 pause_after 時，
// 下一個步驟 next 改成 blocked（awaiting_user），不建任務。在 applyResult 的交易中呼叫；回傳是否暫停。
func pauseAfterSummary(ctx context.Context, r *store.Repo, summaryRun, next store.StageRun) (bool, error) {
	snap, err := r.Snapshot(ctx, summaryRun.ConfigSnapshotID)
	if err != nil {
		return false, fmt.Errorf("讀取設定快照 %s：%w", summaryRun.ConfigSnapshotID, err)
	}
	eff, err := ParseEffective(snap)
	if err != nil {
		return false, err
	}
	set, err := summarySettingsOf(eff)
	if err != nil || !set.PauseAfter {
		return false, err
	}
	return true, r.SetStageState(ctx, next.ID, awaitingUserState())
}

func awaitingUserState() store.StageState {
	return store.StageState{
		Status: store.StatusBlocked, BlockReason: BlockAwaitingUser, Message: msgAwaitingUser,
		Actions: []string{actionContinue, actionCancel},
	}
}

// Continue 繼續處理摘要後暫停的影音（DIG-R7）：awaiting_user 的步驟改回 pending，
// 以 summary 任務原本的優先級建立任務（費用照 COST 的規則在執行時預留）。回傳被繼續的步驟。
// 錯誤：store.ErrNotFound、*InTrashError、ErrNotAwaitingUser。
func (p *Pipeline) Continue(ctx context.Context, itemID string) (string, error) {
	var stage string
	var wake bool
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		stage, wake = "", false
		it, err := r.ItemByID(ctx, itemID)
		if err != nil {
			return err
		}
		if it.DeletedAt != nil {
			return &InTrashError{ItemID: it.ID}
		}
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		latest := latestRuns(runs)
		var run store.StageRun
		found := false
		for _, s := range Stages {
			if lr, ok := latest[s]; ok && lr.Status == store.StatusBlocked && lr.BlockReason == BlockAwaitingUser {
				run, found = lr, true
				break
			}
		}
		if !found {
			return ErrNotAwaitingUser
		}
		stage = run.Stage
		priority := priorityOfRun(ctx, r, latest[StageSummary])
		if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
			return err
		}
		snap, err := r.Snapshot(ctx, run.ConfigSnapshotID)
		if err != nil {
			return fmt.Errorf("讀取設定快照 %s：%w", run.ConfigSnapshotID, err)
		}
		eff, err := ParseEffective(snap)
		if err != nil {
			return err
		}
		sc, err := p.planContext(ctx, r, it, "")
		if err != nil {
			return err
		}
		chain, err := planChainFor(eff, run.Stage, it.UntilStage, sc, func(s string) bool { _, ok := latest[s]; return ok })
		if err != nil {
			return err
		}
		if wake, err = p.applyChain(ctx, r, it.ID, chain, latest, priority, false); err != nil {
			return err
		}
		// 鏈上的步驟全部直接略過時沒有任務會寫回結果：在這裡判斷通知（NOTIFY-R3）。
		return p.emitRunEvent(ctx, r, it, chain[len(chain)-1].Stage)
	})
	if err != nil {
		return "", err
	}
	if wake {
		p.wake()
	}
	return stage, nil
}

// priorityOfRun 回傳 stage run 最近一個任務的優先級；找不到時當成手動新增。
func priorityOfRun(ctx context.Context, r *store.Repo, run store.StageRun) int {
	if run.ID != "" {
		if t, err := r.TaskByStageRun(ctx, run.ID); err == nil {
			return t.Priority
		}
	}
	return queue.PriorityManual
}

// SeedSummary 直接寫入影音現行的 summary 產出（驗收 harness 的 `the summary of … is:`，steps.md 4.18）：
// doc 是 {summary, key_points, title_translation, language}（language 省略時是 zh-Hant），
// summary 步驟最新的一筆改成 done，結束時間是現在。內容不通過 DIG-R3 的驗證時回傳錯誤。
func (p *Pipeline) SeedSummary(ctx context.Context, itemID string, doc []byte) error {
	in := struct {
		Language *string `json:"language"`
	}{}
	if err := json.Unmarshal(doc, &in); err != nil {
		return fmt.Errorf("摘要的 JSON 不正確：%w", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(doc, &raw)
	delete(raw, "language")
	stripped, _ := json.Marshal(raw)
	out, verr := llm.CheckSummary(stripped)
	if verr != nil {
		return errors.New("摘要沒有通過驗證：" + verr.Message)
	}
	lang := "zh-Hant"
	if in.Language != nil && strings.TrimSpace(*in.Language) != "" {
		lang = *in.Language
	}
	s := Summary{Text: out.Summary, KeyPoints: out.KeyPoints, TitleTranslation: out.TitleTranslation, Language: lang}
	if s.KeyPoints == nil {
		s.KeyPoints = []string{}
	}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	prov, err := p.provenance(map[string]any{
		"stage": StageSummary, "impl_version": p.implVersion(StageSummary), "seeded": true,
		"checks": map[string]any{"passed": true}, "summary": map[string]any{"language": lang},
	}, nil)
	if err != nil {
		return err
	}
	art, err := p.newSummaryArtifact(itemID, body, nil, prov)
	if err != nil {
		return err
	}
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		runs, err := r.StageRunsByItem(ctx, itemID)
		if err != nil {
			return err
		}
		run, ok := latestRuns(runs)[StageSummary]
		if !ok {
			return fmt.Errorf("影音 %s 沒有 summary 步驟", itemID)
		}
		a, err := r.InsertArtifact(ctx, *art)
		if err != nil {
			return err
		}
		if err := r.SetStageResult(ctx, run.ID, store.StageResult{Executor: ExecutorAPI, ArtifactID: a.ID, ImplVersion: p.implVersion(StageSummary)}); err != nil {
			return err
		}
		return r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusDone})
	})
	if err != nil {
		p.discardArtifactFile(ctx, art)
	}
	return err
}

// AwaitingUser 回報影音有沒有在等使用者決定是否繼續（任何步驟最新的一筆是 blocked、原因 awaiting_user，DIG-R10）。
func AwaitingUser(ctx context.Context, r *store.Repo, itemID string) (bool, error) {
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	for _, run := range latestRuns(runs) {
		if run.Status == store.StatusBlocked && run.BlockReason == BlockAwaitingUser {
			return true, nil
		}
	}
	return false, nil
}
