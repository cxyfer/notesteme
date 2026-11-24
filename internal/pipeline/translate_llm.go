package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// translate 走 api executor（S6b）：一個 stage run 處理所有目標語言（S6-Q4 維持一筆 stage run），
// 每個目標各自產生一份 translated 產出（TRN-R1）。目標依 PlanTargets 分成：
//   - translate：LLM 翻譯。翻譯單位是句群（TRN-R2）；句群的譯文依切分標記分回各 cue（TRN-R3），
//     標記數不符時該句群多送一次逐 cue 的請求（TRN-R4，不計入驗證重試）。
//   - localize：先用 OpenCC 轉字形，再由 LLM 修正用詞（TRN-R9），一個 cue 一個單位，prompt 是 localize-<目標>。
//   - convert：OpenCC 轉換（TRN-R8），不呼叫 LLM。
//   - platform：平台有原文與目標語言的人工字幕軌時，採用目標語言的字幕軌、依時間重疊對齊（TRN-R11），不呼叫 LLM。
//
// 任務沿用 LLM 引擎的 plan、chunk、assemble 三種（llm.go）：所有 LLM 目標的 chunk 放在同一個 stage run 的 llm_chunks，
// 以全域編號區分（第一個 LLM 目標是 1..n1，第二個接著 n1+1..），每一列的 plan_json 都帶著完整的目標清單。

// trnTarget 是一個目標的處理方式（寫在每個 chunk 的 plan_json，assemble 依它產出）。
type trnTarget struct {
	Lang  string       `json:"lang"`
	Kind  TargetKind   `json:"kind"`
	Table zhconv.Table `json:"table,omitempty"`
	// Prompt 是 LLM 目標使用的 prompt 版本（<id>@<N>）；localize 的 prompt 在 plan 時解析。
	Prompt string `json:"prompt,omitempty"`
	// Track 是 platform 目標要採用的平台字幕軌。
	Track *transcript.Track `json:"track,omitempty"`
	// Key 是這個目標的快取鍵：現行的譯文來源紀錄的 cache_key 相同時直接沿用（Reuse）。
	Key   string `json:"key,omitempty"`
	Reuse string `json:"reuse,omitempty"`
	// Offset、Chunks 是 LLM 目標的 chunk 在 llm_chunks 的全域編號範圍（Offset+1 .. Offset+Chunks）。
	Offset int `json:"offset,omitempty"`
	Chunks int `json:"chunks,omitempty"`
}

func (t trnTarget) usesLLM() bool { return t.Kind == TargetTranslate || t.Kind == TargetLocalize }

// trnChunkPlan 是 translate 的 llm_chunks.plan_json：這個 chunk 屬於哪個目標，以及這次 stage run 的輸入與全部目標。
type trnChunkPlan struct {
	llm.Chunk
	Chunks  int         `json:"chunks"`
	Input   string      `json:"input"`
	Target  int         `json:"target"`
	Targets []trnTarget `json:"targets"`
}

// trnCueOut 是一句的結果；Reason 非空代表這句退回原文（TRN-R7）。
type trnCueOut struct {
	I      int    `json:"i"`
	Text   string `json:"text"`
	Reason string `json:"reason,omitempty"`
}

// trnChunkOutput 是 translate 的 llm_chunks.output_json：已依切分標記分回各 cue 的結果（句號是 cue 的編號，從 1 起算）。
type trnChunkOutput struct {
	Cues []trnCueOut `json:"cues"`
}

// trnBase 是 plan、chunk、assemble 共用、與目標無關的部分。
type trnBase struct {
	lang       Language
	cfg        StageConfig
	settings   LLMSettings
	input      store.Artifact
	tr         subtitle.Transcript
	sourceLang string
	meta       llm.Metadata
	chapters   []time.Duration
	glossary   []string
	ratio      [2]float64
	extras     map[string]any
}

// trnJob 是一個 LLM 目標的工作內容；由 trnBase 與目標決定，plan、chunk、assemble 重新計算的結果相同。
type trnJob struct {
	target    trnTarget
	opts      llm.TranslateOptions
	spec      *llm.Spec
	prompt    ResolvedPrompt
	srcTexts  []string   // 每個 cue 送出的原文（localize 是 OpenCC 轉換後的文字）
	units     []llm.Unit // 翻譯單位
	cues      []llm.Cue  // 每個單位送出的資料：i 是第一個 cue 的編號，句群的文字以切分標記串接
	context   []llm.Cue  // 每個單位當作前文時的資料（以空白串接，不含標記）
	chunks    []llm.Chunk
	fixed     llm.Fixed
	fixedSHA  string
	paramsSHA string
}

// translateSource 選 translate 的輸入：translateInput 選出的字幕有由它衍生的現行 edited 版本（OUT-R13 匯入、語言相同）時，
// 改用 edited（以使用者的修正為準，OUT-06）；correct 不經過這裡，不以 edited 為輸入。
func translateSource(ctx context.Context, r *store.Repo, itemID, from string) (store.Artifact, bool, error) {
	base, ok, err := translateInputFor(ctx, r, itemID, from)
	if err != nil || !ok {
		return base, ok, err
	}
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return store.Artifact{}, false, err
	}
	for i := len(arts) - 1; i >= 0; i-- {
		a := arts[i]
		if a.Variant == string(subtitle.VariantEdited) && sameLang(a.Lang, base.Lang) && slices.Contains(a.Parents, base.ID) {
			return a, true, nil
		}
	}
	return base, true, nil
}

// trnPrepare 讀取 translate 的設定與輸入字幕。inputID 非空時用那份產出（chunk、assemble 沿用 plan 選定的輸入）。
func (p *Pipeline) trnPrepare(ctx context.Context, it store.Item, eff Effective, inputID string) (*trnBase, error) {
	b := &trnBase{}
	var err error
	if b.lang, err = eff.Language(); err != nil {
		return nil, err
	}
	if b.cfg, err = eff.Stage(StageTranslate); err != nil {
		return nil, err
	}
	if b.settings, err = eff.LLM(); err != nil {
		return nil, err
	}
	if b.extras, err = stageExtras(eff, StageTranslate); err != nil {
		return nil, err
	}
	b.ratio = lengthRatio(b.extras)
	r := p.st.Read()
	if inputID != "" {
		b.input, err = r.Artifact(ctx, inputID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, errPermanent("輸入的字幕產出已不存在")
		}
		if err != nil {
			return nil, err
		}
	} else {
		art, found, err := translateSource(ctx, r, it.ID, "")
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errPermanent(errNoSource.Error())
		}
		b.input = art
	}
	if b.tr, err = p.readTranscriptArtifact(ctx, b.input); err != nil {
		if errors.Is(err, ErrCorruptArtifact) {
			return nil, errPermanent(err.Error())
		}
		return nil, err
	}
	b.sourceLang = b.input.Lang
	if b.sourceLang == "" {
		b.sourceLang = it.OriginalLang
	}
	if langtag.IsChinese(b.sourceLang) && langtag.ChineseScript(b.sourceLang) == "" {
		b.sourceLang = withDetectedScript(b.sourceLang, b.tr.Cues)
	}
	b.meta, b.chapters = llmMetadata(it)
	// 詞彙表（COR-R4）：與 correct 相同，全域、頻道、處理方案與頻道覆寫合併，再轉成原文字形；放在資料區塊，不是模板變數。
	terms, err := p.glossaryTerms(ctx, it, eff)
	if err != nil {
		return nil, err
	}
	b.glossary = glossaryForSource(terms, b.sourceLang, b.tr.Cues)
	b.meta.Glossary = b.glossary
	return b, nil
}

// llmMetadata 取出 <video_metadata> 的內容與章節的開始時間（與 prepareLLM 相同）。
func llmMetadata(it store.Item) (llm.Metadata, []time.Duration) {
	var md itemMetadata
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md) // 格式不對時當成沒有中繼資料
	}
	meta := llm.Metadata{Title: it.Title, Channel: md.Channel, Description: md.Description}
	var starts []time.Duration
	for _, c := range md.Chapters {
		start := time.Duration(c.Start * float64(time.Second))
		starts = append(starts, start)
		meta.Chapters = append(meta.Chapters, llm.ChapterInfo{Start: clockText(start), Title: c.Title})
	}
	slices.Sort(starts)
	return meta, starts
}

// lengthRatio 讀出 stages.translate.length_ratio（預設 0.3 到 3.0，TRN-R5）。分層各給一端而範圍顛倒時用預設值。
func lengthRatio(extras map[string]any) [2]float64 {
	out := [2]float64{llm.DefaultMinRatio, llm.DefaultMaxRatio}
	m, _ := extras["length_ratio"].(map[string]any)
	if v, ok := m["min"].(float64); ok && v > 0 {
		out[0] = v
	}
	if v, ok := m["max"].(float64); ok && v > 0 {
		out[1] = v
	}
	if out[0] > out[1] {
		out = [2]float64{llm.DefaultMinRatio, llm.DefaultMaxRatio}
	}
	return out
}

// localizePrompt 是 localize 目標的 prompt id（TRN-R9 的 localize-<目標>）：依序找完整的標記，
// 再找同一字形的預設地區（簡體 → zh-hans-cn，繁體而沒有地區 → zh-hant-tw）。
func localizePromptIDs(target string) []string {
	ids := []string{"localize-" + strings.ToLower(langtag.Parse(target).String())}
	t := langtag.Parse(target)
	switch langtag.ChineseScript(target) {
	case "Hans":
		ids = append(ids, "localize-zh-hans-cn")
	case "Hant":
		if t.Region == "" {
			ids = append(ids, "localize-zh-hant-tw")
		}
	}
	return slices.Compact(ids)
}

// trnJobFor 計算一個 LLM 目標的工作內容。
func (p *Pipeline) trnJobFor(ctx context.Context, b *trnBase, target trnTarget) (*trnJob, error) {
	job := &trnJob{target: target}
	mode := llm.ModeTranslate
	if target.Kind == TargetLocalize {
		mode = llm.ModeLocalize
	}
	job.opts = llm.TranslateOptions{
		Target: target.Lang, Mode: mode, MinRatio: b.ratio[0], MaxRatio: b.ratio[1],
		MinSimilarity: llm.DefaultLocalizeSimilarity, Glossary: b.glossary,
	}
	job.spec = llm.TranslateSpec(job.opts)

	var err error
	if p.prompts == nil {
		return nil, errPermanent("沒有 prompt 庫")
	}
	job.prompt, err = p.prompts.ResolvePrompt(ctx, target.Prompt)
	if err != nil {
		if errors.Is(err, ErrPromptNotFound) || errors.Is(err, ErrPromptVersionNotFound) {
			return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: err.Error(),
				Message: fmt.Sprintf("找不到 prompt「%s」，請檢查處理方案的設定。", target.Prompt)}
		}
		return nil, err
	}

	job.srcTexts = make([]string, len(b.tr.Cues))
	for k, c := range b.tr.Cues {
		job.srcTexts[k] = c.Text
		if mode == llm.ModeLocalize {
			if job.srcTexts[k], err = convertText(target.Table, c.Text); err != nil {
				return nil, fmt.Errorf("轉換 cue %d（%s）：%w", k+1, target.Table, err)
			}
		}
	}
	if mode == llm.ModeLocalize {
		job.units = make([]llm.Unit, len(job.srcTexts))
		for k := range job.units {
			job.units[k] = llm.Unit{First: k, Count: 1}
		}
	} else {
		job.units = llm.BuildUnits(job.srcTexts)
	}
	planCues := make([]llm.PlanCue, len(job.units))
	job.cues = make([]llm.Cue, len(job.units))
	job.context = make([]llm.Cue, len(job.units))
	for k, u := range job.units {
		parts := job.srcTexts[u.First : u.First+u.Count]
		job.cues[k] = llm.Cue{I: u.First + 1, Text: strings.Join(parts, llm.SplitMarker)}
		job.context[k] = llm.Cue{I: u.First + 1, Text: strings.Join(parts, " ")}
		planCues[k] = llm.PlanCue{Start: time.Duration(b.tr.Cues[u.First].StartMS) * time.Millisecond, Text: job.cues[k].Text}
	}
	job.chunks = llm.PlanChunks(planCues, b.chapters, b.settings.Chunk)

	job.fixed, err = llm.BuildFixed(job.spec, job.prompt.Body,
		llm.TemplateVars{SourceLang: b.sourceLang, TargetLang: target.Lang}, b.meta)
	if err != nil {
		return nil, errPermanent("prompt 無法渲染：" + err.Error())
	}
	fixedJSON, _ := json.Marshal(job.fixed)
	job.fixedSHA = sha256Hex(fixedJSON)
	params := map[string]any{
		"chunk": b.settings.Chunk, "glossary": b.glossary, "stage": b.extras, "target": target.Lang, "mode": mode,
		"ratio": b.ratio, "similarity": job.opts.MinSimilarity, "max_group": llm.MaxGroupCues,
	}
	if mode == llm.ModeLocalize {
		params["table"], params["opencc_data"] = target.Table, zhconv.DataVersion
	}
	job.paramsSHA = paramsSHA(params)
	return job, nil
}

// key 是 LLM 目標的快取鍵（譯文來源紀錄的 cache_key）。
func (j *trnJob) key(p *Pipeline, it store.Item, b *trnBase) string {
	return cacheKey{
		Item: it.ID, Stage: StageTranslate, Impl: p.implVersion(StageTranslate), Upstream: b.input.SHA256 + "+" + j.fixedSHA,
		Provider: b.cfg.Provider, Model: b.cfg.Model, Prompt: j.prompt.SHA256, Params: j.paramsSHA,
	}.String() + "/" + j.target.Lang
}

// chunkInput 組出第 c 個 chunk（目標內的編號）送給模型的資料。
func (j *trnJob) chunkInput(c llm.Chunk) llm.ChunkInput {
	in := llm.ChunkInput{Chunk: c, Chunks: len(j.chunks), Cues: j.cues[c.From-1 : c.To], Context: []llm.Cue{}}
	if c.HasContext() {
		in.Context = j.context[c.ContextFrom-1 : c.ContextTo]
	}
	return in
}

// chunkKey 是 chunk 層的快取鍵（LLM-R8）。
func (j *trnJob) chunkKey(impl int, b *trnBase, c llm.Chunk) (string, error) {
	in := j.chunkInput(c)
	data, err := llm.DataBlock(c.Index, len(j.chunks), in.Context, in.Cues)
	if err != nil {
		return "", err
	}
	return paramsSHA(map[string]any{
		"stage": StageTranslate, "impl_version": impl, "provider": b.cfg.Provider, "model": b.cfg.Model,
		"fixed": j.fixedSHA, "params": j.paramsSHA, "data": data, "target": j.target.Lang,
	}), nil
}

// convertKey 是 convert 目標的快取鍵。
func convertKey(p *Pipeline, it store.Item, b *trnBase, t trnTarget) string {
	return cacheKey{
		Item: it.ID, Stage: StageTranslate, Impl: p.implVersion(StageTranslate), Upstream: b.input.SHA256, Provider: "opencc",
		Params: paramsSHA(openccTarget{Lang: t.Lang, Table: t.Table}) + "+" + zhconv.DataVersion,
	}.String() + "/" + t.Lang
}

// runTranslateLLM 是 translate 走 api executor 的任務，依 payload 的 phase 分派。
func (p *Pipeline) runTranslateLLM(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, pl llmPayload) (result, error) {
	switch {
	case t.ChunkIndex != nil:
		return p.trnChunk(ctx, t, run, it, eff, pl)
	case pl.Phase == llmPhaseAssemble:
		rows, err := p.st.Read().LLMChunks(ctx, run.ID)
		if err != nil {
			return result{}, err
		}
		if len(rows) == 0 {
			return result{}, errPermanent("找不到這個步驟的 chunk 記錄")
		}
		var first trnChunkPlan
		if err := json.Unmarshal(rows[0].Plan, &first); err != nil {
			return result{}, errPermanent("chunk 的記錄不正確：" + err.Error())
		}
		b, err := p.trnPrepare(ctx, it, eff, first.Input)
		if err != nil {
			return result{}, err
		}
		return p.trnAssemble(ctx, it, b, first.Targets, rows)
	default:
		return p.trnPlan(ctx, t, run, it, eff, pl.Chunks)
	}
}

// trnPlan 是協調任務：決定每個目標的處理方式、切 chunk、查快取並建立 chunk 任務；沒有要送出的 chunk 時直接組出產出。
func (p *Pipeline) trnPlan(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, force []int) (result, error) {
	b, err := p.trnPrepare(ctx, it, eff, "")
	if err != nil {
		return result{}, err
	}
	if len(b.tr.Cues) == 0 {
		return result{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "輸入字幕沒有任何句子",
			Message: "字幕是空的，沒有可以處理的內容。"}
	}
	arts, err := p.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return result{}, err
	}
	var targets []trnTarget
	for _, tp := range PlanTargets(b.sourceLang, b.lang) {
		if tp.Kind != TargetSame {
			targets = append(targets, trnTarget{Lang: tp.Lang, Kind: tp.Kind, Table: tp.Table})
		}
	}
	if len(targets) == 0 {
		// 執行時才發現每個目標都與原文相同：比照 opencc，略過並撤下不再要求的譯文。
		return result{
			task: taskDone, advance: true, stage: &store.StageResult{Executor: ExecutorAPI, ImplVersion: p.implVersion(StageTranslate)},
			state: skippedState(SkipSameLanguage), supersede: translatedSupersede(removedTranslatedLangs(arts, nil)),
		}, nil
	}
	if err := p.trnPlatformTracks(ctx, it, b, targets); err != nil {
		return result{}, err
	}

	impl := p.implVersion(StageTranslate)
	var rows []store.LLMChunk
	var pending []int
	offset := 0
	for i := range targets {
		tg := &targets[i]
		switch {
		case tg.Kind == TargetConvert:
			tg.Key = convertKey(p, it, b, *tg)
		case tg.usesLLM():
			if tg.Prompt, err = p.trnPromptRef(ctx, b, *tg); err != nil {
				return result{}, err
			}
			job, err := p.trnJobFor(ctx, b, *tg)
			if err != nil {
				return result{}, err
			}
			tg.Key = job.key(p, it, b)
			tg.Offset, tg.Chunks = offset, len(job.chunks)
			offset += len(job.chunks)
		}
		if tg.Key != "" && len(force) == 0 {
			tg.Reuse = p.currentTranslation(arts, tg.Lang, tg.Key)
		}
	}
	existing, err := runChunks(ctx, p.st.Read(), run.ID)
	if err != nil {
		return result{}, err
	}
	for i, tg := range targets {
		if !tg.usesLLM() || tg.Reuse != "" {
			continue
		}
		job, err := p.trnJobFor(ctx, b, tg)
		if err != nil {
			return result{}, err
		}
		for _, c := range job.chunks {
			g := tg.Offset + c.Index
			if ex, ok := existing[g]; ok {
				// 恢復 blocked 的步驟（COST-R9）：沿用這次執行已有的 chunk（同 llmPlan）。
				if ex.Status == store.ChunkPending {
					pending = append(pending, g)
				}
				rows = append(rows, ex)
				continue
			}
			key, err := job.chunkKey(impl, b, c)
			if err != nil {
				return result{}, err
			}
			plan, err := json.Marshal(trnChunkPlan{Chunk: c, Chunks: len(job.chunks), Input: b.input.ID, Target: i, Targets: targets})
			if err != nil {
				return result{}, err
			}
			row := store.LLMChunk{StageRunID: run.ID, Chunk: g, ItemID: it.ID, Stage: run.Stage, CacheKey: key, Status: store.ChunkPending, Plan: plan}
			if !slices.Contains(force, g) {
				if hit, err := p.st.Read().DoneLLMChunkByCacheKey(ctx, it.ID, run.Stage, key); err == nil {
					row.Status, row.Output, row.Reused = store.ChunkDone, hit.Output, true
				} else if !errors.Is(err, store.ErrNotFound) {
					return result{}, err
				}
			}
			if row.Status == store.ChunkPending {
				pending = append(pending, g)
			}
			rows = append(rows, row)
		}
	}
	insertRows := func(ctx context.Context, tx *store.Repo) error {
		for _, row := range rows {
			if _, ok := existing[row.Chunk]; ok {
				continue
			}
			if err := tx.InsertLLMChunk(ctx, row); err != nil {
				return err
			}
		}
		return nil
	}
	if len(pending) == 0 {
		res, err := p.trnAssemble(ctx, it, b, targets, rows)
		if err != nil {
			return result{}, err
		}
		if len(rows) > 0 {
			res.inTx = func(ctx context.Context, tx *store.Repo) (bool, error) { return false, insertRows(ctx, tx) }
		}
		return res, nil
	}
	return result{task: taskDone, keepState: true, inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
		if err := insertRows(ctx, tx); err != nil {
			return false, err
		}
		payload, err := json.Marshal(llmPayload{ItemID: it.ID, Phase: llmPhaseChunk})
		if err != nil {
			return false, err
		}
		for _, k := range pending {
			idx := k
			if _, err := tx.InsertTask(ctx, store.NewTask{
				StageRunID: run.ID, Kind: run.Stage, Executor: queue.ExecutorBuiltin, ResourceClass: queue.ResourceLLM,
				Priority: t.Priority, Payload: payload, ChunkIndex: &idx, MaxAttempts: queue.DefaultMaxAttempts,
			}); err != nil {
				return false, err
			}
		}
		return true, nil
	}}, nil
}

// trnPromptRef 決定 LLM 目標的 prompt 版本：translate 用快照記下的 stages.translate.prompt；
// localize 用 localize-<目標> 目前的預設版本（TRN-R9），找不到時退回同字形的預設地區。
func (p *Pipeline) trnPromptRef(ctx context.Context, b *trnBase, t trnTarget) (string, error) {
	if t.Kind != TargetLocalize {
		rp, err := p.stagePrompt(ctx, b.cfg)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s@%d", rp.ID, rp.Version), nil
	}
	if p.prompts == nil {
		return "", errPermanent("沒有 prompt 庫")
	}
	ids := localizePromptIDs(t.Lang)
	for _, id := range ids {
		rp, err := p.prompts.ResolvePrompt(ctx, id+"@default")
		if errors.Is(err, ErrPromptNotFound) || errors.Is(err, ErrPromptVersionNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s@%d", rp.ID, rp.Version), nil
	}
	return "", &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "沒有 " + ids[0],
		Message: fmt.Sprintf("找不到在地化的 prompt「%s」，請新增它，或改用 variant_strategy: convert。", ids[0])}
}

// currentTranslation 回傳語言為 lang、來源紀錄的 cache_key 為 key 的現行譯文（檔案也要完好）；沒有時回傳空字串。
func (p *Pipeline) currentTranslation(arts []store.Artifact, lang, key string) string {
	for _, a := range arts {
		if a.Kind != "transcript" || a.Variant != string(subtitle.VariantTranslated) || a.Lang != lang {
			continue
		}
		var prov struct {
			CacheKey string `json:"cache_key"`
		}
		if json.Unmarshal(a.Provenance, &prov) == nil && prov.CacheKey == key {
			if _, err := readTranscript(p.dataDir, a.Path, a.SHA256); err == nil {
				return a.ID
			}
		}
	}
	return ""
}

// trnLimiter 是 chunk 請求的限流（與 llmChunk 相同，LLM-R7）。always 為 true 時一律透支、不等待：
// 用於逐 cue 的補送（TRN-R4）——等待會讓整個 chunk 放回佇列、第一個請求被重複送出。
func (p *Pipeline) trnLimiter(provider string, always bool) func(context.Context, int, llm.Request) error {
	prov, _ := p.llm.Provider(provider)
	limiter := p.llm.Limiter
	return func(_ context.Context, attempt int, req llm.Request) error {
		tokens := llm.RequestTokens(req)
		if attempt > 0 || always {
			limiter.Charge(prov.ID, prov.Limits, tokens)
			return nil
		}
		if wait := limiter.Reserve(prov.ID, prov.Limits, tokens); wait > 0 {
			return &errclass.Error{
				Class: errclass.ClassWait, Source: "llm:" + prov.ID, Detail: "速率限制", RetryAfter: wait,
				Message: fmt.Sprintf("等待 LLM provider %s 的速率限制，稍後會繼續。", prov.ID),
			}
		}
		return nil
	}
}

// trnChunkWork 是一個 translate chunk 任務送出之前算好的內容；一般 API（trnChunk）與 Batch API（submitBatch、
// finishBatch）共用。
type trnChunkWork struct {
	row  store.LLMChunk
	plan trnChunkPlan
	b    *trnBase
	job  *trnJob
}

// trnPrepareChunk 讀出 chunk 的記錄並組出這個目標的工作內容。chunk 已經有結果時 done 為 true（不再送出）。
func (p *Pipeline) trnPrepareChunk(ctx context.Context, r *store.Repo, t store.Task, run store.StageRun, it store.Item, eff Effective) (*trnChunkWork, bool, error) {
	row, err := r.LLMChunk(ctx, run.ID, *t.ChunkIndex)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, errPermanent(fmt.Sprintf("找不到 chunk %d 的記錄", *t.ChunkIndex))
		}
		return nil, false, err
	}
	if row.Status != store.ChunkPending {
		return nil, true, nil
	}
	var plan trnChunkPlan
	if err := json.Unmarshal(row.Plan, &plan); err != nil {
		return nil, false, errPermanent("chunk 的記錄不正確：" + err.Error())
	}
	if plan.Target < 0 || plan.Target >= len(plan.Targets) || !plan.Targets[plan.Target].usesLLM() {
		return nil, false, errPermanent(fmt.Sprintf("chunk %d 的目標不正確", row.Chunk))
	}
	b, err := p.trnPrepare(ctx, it, eff, plan.Input)
	if err != nil {
		return nil, false, err
	}
	job, err := p.trnJobFor(ctx, b, plan.Targets[plan.Target])
	if err != nil {
		return nil, false, err
	}
	if plan.To > len(job.units) || plan.From < 1 || plan.From > plan.To || plan.Chunks != len(job.chunks) {
		return nil, false, errPermanent(fmt.Sprintf("chunk %d 的範圍與輸入字幕不符", row.Chunk))
	}
	return &trnChunkWork{row: row, plan: plan, b: b, job: job}, false, nil
}

// runOptions 是這個 chunk 第一個請求的 RunChunk 輸入（不含 Client 與 hook）；一般 API 與 Batch API 共用（BATCH-R2）。
func (w *trnChunkWork) runOptions() llm.RunOptions {
	return llm.RunOptions{
		Spec: w.job.spec, Model: w.b.cfg.Model, Fixed: w.job.fixed, Input: w.job.chunkInput(w.plan.Chunk),
		MaxRetries: w.b.settings.MaxValidationRetries,
	}
}

func (w *trnChunkWork) batchWork() *batchWork {
	return &batchWork{provider: w.b.cfg.Provider, model: w.b.cfg.Model, opts: w.runOptions(), trn: w}
}

// units 是這個 chunk 的翻譯單位。
func (w *trnChunkWork) units() []llm.Unit { return w.job.units[w.plan.From-1 : w.plan.To] }

// trnPrior 是 translate 經 Batch API 取得、已經付費的第一個請求的結果（BATCH-R4）：句群的切分標記不符時，
// 一般 API 的任務沿用它，只補送逐 cue 的請求（TRN-R4）。Output 是通過驗證的輸出；沒有驗證重試額度時是 nil，
// Fallback 是驗證錯誤（其中通過驗證的句子照樣採用，TRN-R7）。
type trnPrior struct {
	Output   json.RawMessage      `json:"output,omitempty"`
	Fallback *llm.ValidationError `json:"fallback,omitempty"`
	Usage    llm.Usage            `json:"usage"`
}

// batchOutcome 把 batch 的結果分回各 cue（BATCH-R4）。out 是通過驗證的輸出；沒有通過時 verr 是驗證錯誤（呼叫端只在
// 沒有驗證重試額度時呼叫，照 TRN-R7 只退回沒有通過的句子）。句群的切分標記不符時回傳 prior（要補送逐 cue 的請求）。
func (w *trnChunkWork) batchOutcome(out json.RawMessage, verr *llm.ValidationError, usage llm.Usage) (*store.ChunkResult, *trnPrior, error) {
	res := llm.ChunkResult{Output: out}
	if out == nil {
		res.Fallback = verr
	}
	units := w.units()
	results := map[int]trnCueOut{}
	var redo []llm.Unit
	w.job.collect(units, w.job.chunkInput(w.plan.Chunk).Cues, res, results, &redo)
	if len(redo) > 0 {
		return nil, &trnPrior{Output: res.Output, Fallback: res.Fallback, Usage: usage}, nil
	}
	cues, err := w.job.chunkOutput(w.plan.Chunk, units, results)
	if err != nil {
		return nil, nil, err
	}
	cr, err := trnChunkResult(cues, usage)
	if err != nil {
		return nil, nil, err
	}
	return &cr, nil, nil
}

// trnChunkResult 是寫進 llm_chunks 的結果：任一句退回原文時整個 chunk 是 fallback（TRN-R7）。
func trnChunkResult(out trnChunkOutput, usage llm.Usage) (store.ChunkResult, error) {
	output, err := json.Marshal(out)
	if err != nil {
		return store.ChunkResult{}, err
	}
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return store.ChunkResult{}, err
	}
	res := store.ChunkResult{Status: store.ChunkDone, Output: output, Usage: usageJSON}
	for _, c := range out.Cues {
		if c.Reason != "" {
			res.Status, res.Reason = store.ChunkFallback, c.Reason
			break
		}
	}
	return res, nil
}

// trnChunk 送出一個 chunk：驗證重試（TRN-R5）、句群切分（TRN-R3）、標記數不符時逐 cue 補送（TRN-R4），
// 重試用盡時只把沒有通過驗證的句子退回原文（TRN-R7）。最後一個完成的 chunk 建立 assemble 任務。
// 背景的工作改用 Batch API（BATCH-R1、R2），送出之後任務等待結果（BATCH-R3）。
func (p *Pipeline) trnChunk(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, pl llmPayload) (result, error) {
	if p.llm == nil {
		return result{}, errPermanent("沒有設定 LLM provider")
	}
	w, done, err := p.trnPrepareChunk(ctx, p.st.Read(), t, run, it, eff)
	if err != nil {
		return result{}, err
	}
	if done {
		return result{task: taskDone, keepState: true}, nil
	}
	if p.batchEligible(t, run, w.b.cfg, eff, pl) {
		if handled, err := p.submitBatch(ctx, t, run, it, w.batchWork()); handled || err != nil {
			return result{parked: err == nil}, err
		}
	}
	client, err := p.llm.Client(ctx, w.b.cfg.Provider)
	if err != nil {
		return result{}, err
	}
	m := p.newMeter(t, run, it, w.b.cfg.Provider, w.b.cfg.Model)
	out, usage, err := p.trnRunChunk(ctx, w, client, p.chunkTimeout(t), m, pl.Batch)
	if err != nil {
		return result{}, err
	}
	res, err := trnChunkResult(out, usage)
	if err != nil {
		return result{}, err
	}
	if res.Status == store.ChunkFallback {
		p.log.WarnContext(ctx, "譯文驗證重試用盡，退回原文", "item", it.ID, "chunk", w.row.Chunk, "reason", res.Reason)
	}
	fb := pl.Batch
	return result{task: taskDone, keepState: true, inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
		if fb != nil {
			// 來源紀錄的 mode 與 fallback_reason（BATCH-R8）。
			if err := tx.SetLLMChunkBatch(ctx, run.ID, w.row.Chunk, fb.record()); err != nil {
				return false, err
			}
		}
		return finishChunkTx(ctx, tx, it.ID, run, t.Priority, w.row.Chunk, res)
	}}, nil
}

// trnRunChunk 執行一個 chunk 的請求，回傳每個 cue 的結果與用量。m 為每次請求記帳（COST），可以是 nil。
// fb 是從 Batch API 改走一般 API 的原因（沒有經過 batch 時是 nil）：驗證失敗的 batch 結果附上錯誤說明並計入驗證重試
// （LLM-R5）；帶著 batch 結果（Prior）時不再送第一個請求，只補送逐 cue 的請求（TRN-R4）。
func (p *Pipeline) trnRunChunk(ctx context.Context, w *trnChunkWork, client llm.Client, timeout time.Duration, m *meter, fb *batchFallback) (trnChunkOutput, llm.Usage, error) {
	job, b, c := w.job, w.b, w.plan.Chunk
	opts := w.runOptions()
	in := opts.Input
	units := w.units()
	started := p.clock.Now()
	var usage llm.Usage
	var res llm.ChunkResult
	prior := fb != nil && fb.Prior != nil
	if prior {
		res = llm.ChunkResult{Output: fb.Prior.Output, Fallback: fb.Prior.Fallback}
		usage.Add(fb.Prior.Usage)
	} else {
		if fb != nil && fb.Retry != "" {
			opts.Retry, opts.MaxRetries = fb.Retry, max(opts.MaxRetries-1, 0)
		}
		opts.Client, opts.Timeout = client, timeout
		opts.BeforeRequest, opts.AfterRequest = m.then(p.trnLimiter(b.cfg.Provider, false)), m.afterFunc()
		var err error
		if res, err = llm.RunChunk(ctx, opts); err != nil {
			return trnChunkOutput{}, usage, err
		}
		usage.Add(res.Usage)
	}

	results := map[int]trnCueOut{} // cue 的位置（從 0 起算）→ 結果
	var redo []llm.Unit
	job.collect(units, in.Cues, res, results, &redo)
	if len(redo) > 0 {
		// TRN-R4：標記數不符的句群改成逐 cue 翻譯，多送一次請求；它有自己的驗證重試，不計入第一個請求的次數。
		var cues []llm.Cue
		var singles []llm.Unit
		for _, u := range redo {
			for k := u.First; k < u.First+u.Count; k++ {
				cues = append(cues, llm.Cue{I: k + 1, Text: job.srcTexts[k]})
				singles = append(singles, llm.Unit{First: k, Count: 1})
			}
		}
		left := timeout
		if timeout > 0 {
			if left = timeout - p.clock.Now().Sub(started); left <= 0 {
				left = -1
			}
		}
		in2 := llm.ChunkInput{Chunk: c, Chunks: in.Chunks, Context: in.Context, Cues: cues}
		// 一般情況下補送一律透支、不等待（見 trnLimiter）；沿用 batch 結果時補送是這個任務的第一個請求，可以等待。
		res2, err := llm.RunChunk(ctx, llm.RunOptions{
			Client: client, Spec: job.spec, Model: b.cfg.Model, Fixed: job.fixed, Input: in2,
			MaxRetries: b.settings.MaxValidationRetries, Timeout: left, BeforeRequest: m.then(p.trnLimiter(b.cfg.Provider, !prior)), AfterRequest: m.afterFunc(),
		})
		if err != nil {
			return trnChunkOutput{}, usage, err
		}
		usage.Add(res2.Usage)
		var again []llm.Unit
		job.collect(singles, cues, res2, results, &again)
	}
	out, err := job.chunkOutput(c, units, results)
	return out, usage, err
}

// chunkOutput 依單位順序取出每一句的結果；缺少任何一句都是程式錯誤。
func (j *trnJob) chunkOutput(c llm.Chunk, units []llm.Unit, results map[int]trnCueOut) (trnChunkOutput, error) {
	out := trnChunkOutput{Cues: make([]trnCueOut, 0, len(results))}
	for _, u := range units {
		for k := u.First; k < u.First+u.Count; k++ {
			r, ok := results[k]
			if !ok {
				return trnChunkOutput{}, errPermanent(fmt.Sprintf("chunk %d 缺少第 %d 句的結果", c.Index, k+1))
			}
			out.Cues = append(out.Cues, r)
		}
	}
	return out, nil
}

// collect 把一次 RunChunk 的結果分回各 cue。通過驗證的輸出：單句直接採用，句群依標記切分，切不開的句群放進 redo。
// 重試用盡（Output 是 nil）時，最後一次符合 schema 的輸出中通過驗證的句子照樣採用，其餘退回原文（TRN-R7）。
func (j *trnJob) collect(units []llm.Unit, sent []llm.Cue, res llm.ChunkResult, results map[int]trnCueOut, redo *[]llm.Unit) {
	fallback := func(u llm.Unit, reason string) {
		for k := u.First; k < u.First+u.Count; k++ {
			results[k] = trnCueOut{I: k + 1, Text: j.fallbackText(k), Reason: reason}
		}
	}
	take := func(u llm.Unit, text string) {
		if u.Count == 1 {
			results[u.First] = trnCueOut{I: u.First + 1, Text: strings.TrimSpace(text)}
			return
		}
		parts, ok := llm.SplitParts(text, u.Count)
		if !ok {
			*redo = append(*redo, u)
			return
		}
		for n, part := range parts {
			results[u.First+n] = trnCueOut{I: u.First + n + 1, Text: part}
		}
	}
	raw := res.Output
	if raw == nil && res.Fallback != nil {
		raw = res.Fallback.Output
	}
	var got []llm.Cue
	if raw != nil {
		got, _ = llm.DecodeCues(raw)
	}
	for k, u := range units {
		if len(got) != len(units) || got[k].I != sent[k].I {
			reason := llm.ReasonSchema
			if res.Fallback != nil {
				reason = res.Fallback.Reason
			}
			fallback(u, reason)
			continue
		}
		if res.Output == nil {
			if reason, _ := j.opts.CheckUnit(sent[k].Text, got[k].Text); reason != "" {
				fallback(u, reason)
				continue
			}
		}
		take(u, got[k].Text)
	}
}

// fallbackText 是退回時這句的文字：translate 保留原文；localize 保留 OpenCC 轉換後的文字（字形已是目標）。
func (j *trnJob) fallbackText(k int) string { return j.srcTexts[k] }

// trnRecord 是譯文來源紀錄的 fallbacks[]、warnings[] 的一項。
type trnRecord struct {
	Cue    int      `json:"cue"`
	Reason string   `json:"reason"`
	Chunk  int      `json:"chunk,omitempty"`
	CPS    *float64 `json:"cps,omitempty"`
}

// trnAssemble 依目標清單產出每個目標的譯文。rows 是這次 stage run 的 chunk（全域編號順序）。
func (p *Pipeline) trnAssemble(ctx context.Context, it store.Item, b *trnBase, targets []trnTarget, rows []store.LLMChunk) (result, error) {
	impl := p.implVersion(StageTranslate)
	stage := &store.StageResult{Executor: ExecutorAPI, ImplVersion: impl}
	var outs []*store.NewArtifact
	discard := func() {
		for _, a := range outs {
			p.discardArtifactFile(a)
		}
	}
	keys := make([]string, 0, len(targets))
	clean := true
	firstReuse := ""
	notice := "" // BATCH-R11 的提醒
	for _, tg := range targets {
		keys = append(keys, tg.Key)
		if tg.Reuse != "" {
			if firstReuse == "" {
				firstReuse = tg.Reuse
			}
			continue
		}
		var art *store.NewArtifact
		var err error
		var fellBack bool
		switch {
		case tg.Kind == TargetConvert:
			art, err = p.trnConvert(ctx, it, b, tg)
		case tg.Kind == TargetPlatform:
			art, fellBack, err = p.trnPlatform(ctx, it, b, tg)
		case tg.usesLLM():
			var n string
			art, fellBack, n, err = p.trnAssembleLLM(ctx, it, b, tg, rows)
			if notice == "" {
				notice = n
			}
		default:
			err = errPermanent("不認識的目標處理方式 " + string(tg.Kind))
		}
		if err != nil {
			discard()
			return result{}, err
		}
		if fellBack || tg.Kind == TargetPlatform {
			clean = false
		}
		outs = append(outs, art)
	}
	if clean {
		stage.CacheKey = paramsSHA(keys)
	}
	langs := make([]openccJob, 0, len(targets))
	for _, tg := range targets {
		langs = append(langs, openccJob{lang: tg.Lang})
	}
	// 步驟沒有其他訊息時，BATCH-R11 的提醒成為步驟的訊息（同 assembleLLM）。
	res := result{task: taskDone, advance: true, stage: stage, state: store.StageState{Status: store.StatusDone, Message: notice},
		supersede: translatedSupersede(removedTranslatedLangs(currentArtifactsOrNil(ctx, p, it.ID), langs))}
	if len(outs) == 0 {
		stage.ArtifactID = firstReuse
		return res, nil
	}
	res.artifacts = outs
	return res, nil
}

func currentArtifactsOrNil(ctx context.Context, p *Pipeline, itemID string) []store.Artifact {
	arts, err := p.st.Read().CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return nil
	}
	return arts
}

// trnConvert 產出 convert 目標的譯文（TRN-R8，與 runTranslateOpenCC 相同的轉換）。
func (p *Pipeline) trnConvert(ctx context.Context, it store.Item, b *trnBase, tg trnTarget) (*store.NewArtifact, error) {
	cues := make([]subtitle.Cue, len(b.tr.Cues))
	for i, c := range b.tr.Cues {
		text, err := convertText(tg.Table, c.Text)
		if err != nil {
			return nil, fmt.Errorf("轉換 cue %d（%s）：%w", c.Index, tg.Table, err)
		}
		cues[i] = subtitle.Cue{Index: c.Index, StartMS: c.StartMS, EndMS: c.EndMS, Text: text, Timing: c.Timing, Speaker: c.Speaker}
	}
	prov, err := p.provenance(map[string]any{
		"stage": StageTranslate, "impl_version": p.implVersion(StageTranslate), "executor": ExecutorOpenCC,
		"table": string(tg.Table), "strategy": string(zhconv.StrategyConvert), "opencc_data": zhconv.DataVersion,
		"source_lang": b.sourceLang, "target": tg.Lang,
		"derived_from": derivedFrom(b.input), "cache_key": tg.Key,
	}, nil)
	if err != nil {
		return nil, err
	}
	return p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: tg.Lang, Variant: subtitle.VariantTranslated, Speakers: b.tr.Speakers, Cues: cues},
		[]string{b.input.ID}, prov)
}

func derivedFrom(a store.Artifact) []map[string]string {
	return []map[string]string{{"artifact": a.ID, "variant": a.Variant, "sha256": a.SHA256}}
}

// trnAssembleLLM 合併一個 LLM 目標的 chunk 結果：重疊區以前一個 chunk 為準，記下退回（TRN-R7）與閱讀速度的警告（TRN-R6）。
// 經過 Batch API 的 chunk 記下 mode 等欄位（BATCH-R8）；回傳的 notice 是 BATCH-R11 的提醒。
func (p *Pipeline) trnAssembleLLM(ctx context.Context, it store.Item, b *trnBase, tg trnTarget, rows []store.LLMChunk) (art *store.NewArtifact, fellBack bool, notice string, err error) {
	job, err := p.trnJobFor(ctx, b, tg)
	if err != nil {
		return nil, false, "", err
	}
	if len(job.chunks) != tg.Chunks {
		return nil, false, "", errPermanent(fmt.Sprintf("%s 的切分結果有 %d 個 chunk，記錄有 %d 個", tg.Lang, len(job.chunks), tg.Chunks))
	}
	viaBatch := false
	texts := make([]*trnCueOut, len(b.tr.Cues))
	chunkOf := make([]int, len(b.tr.Cues))
	records := make([]chunkRecord, 0, tg.Chunks)
	for _, c := range job.chunks {
		g := tg.Offset + c.Index
		i := slices.IndexFunc(rows, func(r store.LLMChunk) bool { return r.Chunk == g })
		if i < 0 {
			return nil, false, "", errPermanent(fmt.Sprintf("找不到 chunk %d 的記錄", g))
		}
		row := rows[i]
		if row.Status != store.ChunkDone && row.Status != store.ChunkFallback {
			return nil, false, "", errPermanent(fmt.Sprintf("chunk %d 還沒有結果", g))
		}
		var out trnChunkOutput
		if err := json.Unmarshal(row.Output, &out); err != nil {
			return nil, false, "", errPermanent(fmt.Sprintf("chunk %d 的輸出不正確：%v", g, err))
		}
		for _, cue := range out.Cues {
			k := cue.I - 1
			if k < 0 || k >= len(texts) {
				return nil, false, "", errPermanent(fmt.Sprintf("chunk %d 的輸出有不存在的第 %d 句", g, cue.I))
			}
			if texts[k] == nil { // 重疊區：前一個 chunk 已經輸出
				cp := cue
				texts[k], chunkOf[k] = &cp, c.Index
			}
		}
		rec := chunkRecord{Chunk: c.Index, Cues: [2]int{c.From, c.To}, Reused: row.Reused}
		if c.HasContext() {
			rec.Context = &[2]int{c.ContextFrom, c.ContextTo}
		}
		if !row.Reused && len(row.Usage) > 0 {
			_ = json.Unmarshal(row.Usage, &rec.Usage)
		}
		via, n := rec.applyBatch(row)
		viaBatch = viaBatch || via
		if notice == "" {
			notice = n
		}
		records = append(records, rec)
	}
	limit := llm.ReadingSpeedLimit(tg.Lang)
	cues := make([]subtitle.Cue, len(b.tr.Cues))
	fallbacks, warnings := []trnRecord{}, []trnRecord{}
	for k, c := range b.tr.Cues {
		if texts[k] == nil {
			return nil, false, "", errPermanent(fmt.Sprintf("%s 的譯文缺少第 %d 句", tg.Lang, k+1))
		}
		out := texts[k]
		cues[k] = subtitle.Cue{Index: c.Index, StartMS: c.StartMS, EndMS: c.EndMS, Text: out.Text, Timing: c.Timing, Speaker: c.Speaker}
		if out.Reason != "" {
			fallbacks = append(fallbacks, trnRecord{Cue: k + 1, Reason: out.Reason, Chunk: chunkOf[k]})
			continue
		}
		if dur := c.EndMS - c.StartMS; dur > 0 {
			cps := float64(llm.ReadingChars(out.Text)) * 1000 / float64(dur)
			if cps > limit {
				v := float64(int(cps*10+0.5)) / 10
				warnings = append(warnings, trnRecord{Cue: k + 1, Reason: llm.ReasonReadingSpeed, CPS: &v})
			}
		}
	}
	fields := map[string]any{
		"stage": StageTranslate, "impl_version": p.implVersion(StageTranslate),
		"executor": ExecutorAPI, "provider": b.cfg.Provider, "model": b.cfg.Model,
		"prompt":        map[string]any{"id": job.prompt.ID, "version": job.prompt.Version, "sha256": job.prompt.SHA256},
		"params_sha256": job.paramsSHA, "mode": job.opts.Mode, "source_lang": b.sourceLang, "target": tg.Lang,
		"chunks": records, "fallbacks": fallbacks, "warnings": warnings,
		"derived_from": derivedFrom(b.input),
	}
	if tg.Kind == TargetLocalize {
		fields["table"], fields["opencc_data"] = string(tg.Table), zhconv.DataVersion
	}
	if viaBatch {
		fields["batch"] = true
	}
	if len(fallbacks) == 0 {
		// 有退回的譯文不寫快取鍵：下一次重跑要重新送出（LLM-R8）。
		fields["cache_key"] = tg.Key
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return nil, false, "", err
	}
	art, err = p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: tg.Lang, Variant: subtitle.VariantTranslated, Speakers: b.tr.Speakers, Cues: cues},
		[]string{b.input.ID}, prov)
	return art, len(fallbacks) > 0, notice, err
}
