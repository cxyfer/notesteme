package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 筆記的產出與任務（NOTE-R13、ADR-0020 第 4 點）。
const (
	KindNote = "note"
	noteFmt  = "notesteme-note+json"
	// TaskNoteMap 是 map-reduce 中 map 的任務種類（NOTE-R6、N8）；reduce 與一般的筆記是 note。
	TaskNoteMap = "note_map"
	// noteMapPrompt 是 map 用的內建 prompt（NOTE N12）。
	noteMapPrompt = "note-map-default"
	// 預設的 map-reduce 門檻與沒有章節時的視窗（NOTE-R6、N6）。
	defaultMapReduceAfter = 60 * time.Minute
	noteMapWindow         = 20 * time.Minute
	// 動作（NOTE-R3、R7、R10）。
	// ActionRegenerateNote（「重新產生筆記」）定義在 gemini.go，兩種引擎共用。
	actionOpenTemplate = "開啟筆記模板"
	// NoteStaleReason 是字幕在筆記之後更新時的說明（NOTE-R10）。
	NoteStaleReason = "字幕已更新，筆記可能過時。"
)

// noteStage 是 note 在 LLM 引擎中的登記（NOTE-R1、R3、R5、R6）：prepareNote 附上章節與畫面並決定是否 map-reduce，
// writeNote 套用模板、執行自動檢查並寫出 note 產出。
var noteStage = llmStage{
	spec:    func(map[string]any) *llm.Spec { return llm.NoteSpec(llm.NoteRefs{}) },
	prepare: (*Pipeline).prepareNote,
	write:   (*Pipeline).writeNote,
}

// noteJob 是一次 note 的 stage run 由輸入決定的資料；plan、chunk、assemble 都重新計算，結果相同。
type noteJob struct {
	lang string
	// maps 是 map 的 chunk 數；0 代表一個請求寫出整份筆記（NOTE-R6）。
	maps     int
	mapSpec  *llm.Spec
	mapFixed llm.Fixed
	// mapFixedSHA 是 mapFixed 的 sha256（map chunk 的快取鍵）。
	mapFixedSHA string
	// cueSet 是可以引用的句號（輸入中、不在 SponsorBlock 片段內，NOTE-R3）。
	cueSet map[int]bool
	// frames 是選中的畫面：編號 → 時間與圖說（NOTE-R9）。
	frames   map[int]notes.Frame
	chapters *store.Artifact
	frameArt *store.Artifact
	// extraRows 是影片資訊表的附加列（畫面不足、畫質，NOTE-R9）。
	extraRows []notes.InfoRow
	status    string
	template  noteTemplate
}

// noteMaps 回傳 map 的 chunk 數（不是 note 或沒有 map-reduce 時是 0）。
func (j *llmJob) noteMaps() int {
	if j.note == nil {
		return 0
	}
	return j.note.maps
}

// isNoteReduce 回報 c 是不是 note 的 map-reduce 中的 reduce（最後一個 chunk）。
func (j *llmJob) isNoteReduce(c llm.Chunk) bool {
	return j.note != nil && j.note.maps > 0 && c.Index == j.note.maps+1
}

// isNoteMap 回報 c 是不是 note 的 map chunk。
func (j *llmJob) isNoteMap(c llm.Chunk) bool {
	return j.note != nil && j.note.maps > 0 && c.Index <= j.note.maps
}

// taskKind 是第 idx 個 chunk 的任務種類：note 的 map 是 note_map（Agent 依它領取，N8），其他是步驟名稱。
func (j *llmJob) taskKind(idx int) string {
	if j.note != nil && j.note.maps > 0 && idx <= j.note.maps {
		return TaskNoteMap
	}
	return j.stage
}

// specFor 回傳 chunk 的 Spec：note 的 map 用 note_map，其他用步驟的 Spec。
func (j *llmJob) specFor(c llm.Chunk) *llm.Spec {
	if j.isNoteMap(c) {
		return j.note.mapSpec
	}
	return j.spec
}

// fixedFor 回傳 chunk 的固定前綴：note 的 map 用 note-map-default 組出的前綴。
func (j *llmJob) fixedFor(c llm.Chunk) llm.Fixed {
	if j.isNoteMap(c) {
		return j.note.mapFixed
	}
	return j.fixed
}

// chunkInputFor 組出 chunk 送出的資料：note 的 map 只算 map 的段數；reduce 的 cues 是各段的要點（句號是要點開始的 cue）。
func (p *Pipeline) chunkInputFor(ctx context.Context, r *store.Repo, runID string, j *llmJob, c llm.Chunk) (llm.ChunkInput, error) {
	in := j.chunkInput(c)
	switch {
	case j.isNoteMap(c):
		in.Chunks = j.note.maps
	case j.isNoteReduce(c):
		rows, err := r.LLMChunks(ctx, runID)
		if err != nil {
			return in, err
		}
		points := []llm.Cue{}
		for _, row := range rows {
			if row.Chunk > j.note.maps {
				continue
			}
			if row.Status != store.ChunkDone {
				return in, errPermanent(fmt.Sprintf("第 %d 段的要點還沒有結果", row.Chunk))
			}
			var o llm.NoteMapOutput
			if err := json.Unmarshal(row.Output, &o); err != nil {
				return in, errPermanent(fmt.Sprintf("第 %d 段的要點不正確：%v", row.Chunk, err))
			}
			for _, pt := range o.Points {
				points = append(points, llm.Cue{I: pt.Cue, Text: pt.Text})
			}
		}
		in.Chunk = llm.Chunk{Index: 1, From: 1, To: len(points)}
		in.Chunks, in.Cues, in.Context = 1, points, []llm.Cue{}
	}
	return in, nil
}

// noteSettings 是 stages.note 中筆記專屬的欄位（NOTE 介面）。
type noteSettings struct {
	Template           string `json:"template"`
	Lang               string `json:"lang"`
	IncludeTranslation bool   `json:"include_translation"`
	MapReduceAfter     string `json:"map_reduce_after"`
}

func noteSettingsOf(eff Effective) (noteSettings, error) {
	var s noteSettings
	extras, err := stageExtras(eff, StageNote)
	if err != nil || extras == nil {
		return s, err
	}
	b, err := json.Marshal(extras)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, invalidf("stages.note 設定不正確：%v", err)
	}
	return s, nil
}

// noteLangOf 決定筆記語言（NOTE-R5）：stages.note.lang，否則 language.targets 的第一個，再否則原文語言。
func noteLangOf(eff Effective, s noteSettings, srcLang string) string {
	if s.Lang != "" {
		return s.Lang
	}
	if l, err := eff.Language(); err == nil && len(l.Targets) > 0 && l.Targets[0] != "" {
		return l.Targets[0]
	}
	return srcLang
}

// parseNoteDuration 解析 map_reduce_after（例如 60m、1h30m）；沒有設定時用預設值。
func parseNoteDuration(s string) (time.Duration, error) {
	if s == "" {
		return defaultMapReduceAfter, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, invalidf("stages.note.map_reduce_after 必須是正的時間長度（例如 60m）。")
	}
	return d, nil
}

// cueAtOrBefore 回傳時間 ms 所在（或之前最近）的輸入句號；ms 在第一句之前時回傳第一句。
func cueAtOrBefore(tr subtitle.Transcript, cueSet map[int]bool, ms int64) int {
	best := 0
	for k, c := range tr.Cues {
		i := k + 1
		if !cueSet[i] {
			continue
		}
		if best == 0 || c.StartMS <= ms {
			best = i
		}
		if c.StartMS > ms {
			break
		}
	}
	return best
}

// prepareNote 是 note 的 llmStage.prepare：附上章節、選中的畫面與（設定要求時的）譯文，決定筆記語言、模板與是否 map-reduce。
func (p *Pipeline) prepareNote(ctx context.Context, it store.Item, job *llmJob, meta *llm.Metadata, vars *llm.TemplateVars) error {
	set, err := noteSettingsOf(job.eff)
	if err != nil {
		return err
	}
	nj := &noteJob{cueSet: map[int]bool{}, frames: map[int]notes.Frame{}, status: notes.StatusComplete}
	job.note = nj
	nj.lang = noteLangOf(job.eff, set, vars.SourceLang)
	vars.NoteLang = nj.lang
	for _, c := range job.cues {
		nj.cueSet[c.I] = true
	}
	if nj.template, err = p.resolveNoteTemplate(ctx, set.Template); err != nil {
		return err
	}

	// 章節（CHAP-R7）：以起點的句號表示；平台章節沒有句號時用起點所在的句子。
	r := p.st.Read()
	if art, ok, err := p.currentChaptersArtifact(ctx, it.ID); err != nil {
		return err
	} else if ok {
		doc, _, err := p.ItemChapters(ctx, it.ID)
		if err != nil {
			return errPermanent("讀取章節失敗：" + err.Error())
		}
		for _, ch := range doc.Chapters {
			cue := ch.Cue
			if !nj.cueSet[cue] {
				cue = cueAtOrBefore(job.tr, nj.cueSet, ch.StartMS)
			}
			if cue > 0 {
				meta.NoteChapters = append(meta.NoteChapters, llm.NoteChapter{Cue: cue, Title: ch.Title})
			}
		}
		if len(doc.Chapters) > 0 {
			nj.chapters = &art
		}
	}

	// 選中的畫面（FRM）：編號、最接近的句號與圖說，不給時間與圖片（NOTE-R5）。
	if art, man, ok, err := p.currentFrames(ctx, it.ID); err != nil {
		return errPermanent("讀取關鍵畫面失敗：" + err.Error())
	} else if ok {
		for i, c := range man.Candidates {
			if !c.Selected {
				continue
			}
			n := i + 1
			nj.frames[n] = notes.Frame{AtMS: c.AtMS, Caption: c.Caption}
			meta.Frames = append(meta.Frames, llm.NoteFrame{N: n, Cue: cueAtOrBefore(job.tr, nj.cueSet, c.AtMS), Caption: c.Caption})
		}
		if len(nj.frames) > 0 {
			nj.frameArt = &art
		}
		if man.Shortfall {
			nj.status = notes.StatusPartial
			nj.extraRows = append(nj.extraRows, notes.InfoRow{Label: "畫面", Value: fmt.Sprintf("只找到 %d 張有意義的畫面", len(nj.frames))})
		}
		if man.Height > 0 && man.Height < 1080 {
			nj.extraRows = append(nj.extraRows, notes.InfoRow{Label: "畫質", Value: fmt.Sprintf("%dp（來源的最高畫質）", man.Height)})
		}
	}

	// 降級的結果（NOTE-R2）：章節產生失敗或 SponsorBlock 查詢失敗時 partial。
	if degraded, err := p.noteDegraded(ctx, r, it.ID); err != nil {
		return err
	} else if degraded {
		nj.status = notes.StatusPartial
	}

	// 譯文（NOTE-R5、N5）：只在設定要求時附上筆記語言的譯文。
	if set.IncludeTranslation {
		tr, ok, err := p.noteTranslation(ctx, r, it.ID, nj.lang)
		if err != nil {
			return err
		}
		if ok {
			for _, c := range job.cues {
				if k := c.I - 1; k < len(tr.Cues) && tr.Cues[k].Text != "" {
					meta.Translations = append(meta.Translations, llm.Cue{I: c.I, Text: tr.Cues[k].Text})
				}
			}
		}
	}

	// 切 chunk（NOTE-R6）：片長超過門檻或字幕超過 chunk 上限時 map-reduce，否則一個請求。
	after, err := parseNoteDuration(set.MapReduceAfter)
	if err != nil {
		return err
	}
	tokens := 0
	for _, c := range job.cues {
		tokens += llm.EstimateTokens(c.Text) + 4
	}
	job.spec = llm.NoteSpec(llm.NoteRefs{Cues: nj.cueSet, Frames: selectedSet(nj.frames)})
	job.chunks = nil
	if len(job.cues) > 0 {
		if durationMS(it) > after.Milliseconds() || tokens > job.settings.Chunk.MaxTokens {
			job.chunks = p.noteMapChunks(job, meta)
			nj.maps = len(job.chunks)
		}
		job.chunks = append(job.chunks, llm.Chunk{Index: len(job.chunks) + 1, From: 1, To: len(job.cues)})
	}
	if nj.maps > 0 {
		rp, err := p.prompts.ResolvePrompt(ctx, noteMapPrompt)
		if err != nil {
			return errPermanent("找不到內建的 prompt " + noteMapPrompt + "：" + err.Error())
		}
		nj.mapSpec = llm.NoteMapSpec()
		if nj.mapFixed, err = llm.BuildFixed(nj.mapSpec, rp.Body, *vars, *meta); err != nil {
			return errPermanent("prompt 無法渲染：" + err.Error())
		}
		fixedJSON, _ := json.Marshal(nj.mapFixed)
		nj.mapFixedSHA = sha256Hex(fixedJSON)
		job.extraKey = rp.SHA256
	}
	key, err := json.Marshal(map[string]any{
		"template": nj.template.SHA256, "rules": nj.template.Rules, "lang": nj.lang, "status": nj.status,
		"map": job.extraKey, "extra": nj.extraRows,
	})
	if err != nil {
		return err
	}
	job.extraKey = sha256Hex(key)
	return nil
}

// noteDegraded 回報筆記的輸入是不是降級的結果（NOTE-R2）：sponsorblock 或 chapters 最新一次是 done 但帶有訊息。
func (p *Pipeline) noteDegraded(ctx context.Context, r *store.Repo, itemID string) (bool, error) {
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	latest := latestRuns(runs)
	for _, s := range []string{StageSponsorBlock, StageChapters} {
		if run, ok := latest[s]; ok && run.Status == store.StatusDone && run.Message != "" {
			return true, nil
		}
	}
	return false, nil
}

func selectedSet(frames map[int]notes.Frame) map[int]bool {
	out := make(map[int]bool, len(frames))
	for n := range frames {
		out[n] = true
	}
	return out
}

// noteMapChunks 切出 map 的 chunk（NOTE-R6）：每個章節一段，沒有章節時每 20 分鐘一段；沒有句子的段落略過。
// 範圍是 job.cues 中的位置（從 1 起算）。
func (p *Pipeline) noteMapChunks(job *llmJob, meta *llm.Metadata) []llm.Chunk {
	// 每句的開始時間（job.cues 的順序）。
	starts := make([]int64, len(job.cues))
	for k, c := range job.cues {
		starts[k] = job.tr.Cues[c.I-1].StartMS
	}
	var bounds []int64 // 每一段的開始時間
	if len(meta.NoteChapters) > 0 {
		for _, ch := range meta.NoteChapters {
			bounds = append(bounds, job.tr.Cues[ch.Cue-1].StartMS)
		}
		bounds[0] = 0
	} else {
		last := starts[len(starts)-1]
		for t := int64(0); t <= last; t += noteMapWindow.Milliseconds() {
			bounds = append(bounds, t)
		}
	}
	slices.Sort(bounds)
	var out []llm.Chunk
	from := 0
	for b := range bounds {
		end := len(starts)
		if b+1 < len(bounds) {
			end = from
			for end < len(starts) && starts[end] < bounds[b+1] {
				end++
			}
		}
		if end > from {
			out = append(out, llm.Chunk{Index: len(out) + 1, From: from + 1, To: end})
		}
		from = end
	}
	return out
}

// noteTranslation 讀取筆記語言的現行譯文；沒有時 ok 為 false。
func (p *Pipeline) noteTranslation(ctx context.Context, r *store.Repo, itemID, lang string) (subtitle.Transcript, bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return subtitle.Transcript{}, false, err
	}
	for i := len(arts) - 1; i >= 0; i-- {
		a := arts[i]
		if a.Variant != string(subtitle.VariantTranslated) || !sameLang(a.Lang, lang) {
			continue
		}
		tr, err := readTranscript(p.dataDir, a.Path, a.SHA256)
		if err != nil {
			return subtitle.Transcript{}, false, errPermanent("讀取譯文失敗：" + err.Error())
		}
		return tr, true, nil
	}
	return subtitle.Transcript{}, false, nil
}

// finishNoteMapsTx 在一個 note 的 chunk 寫回之後執行（finishChunkTx）：map-reduce 的 map 都有結果、只剩 reduce 時，
// 建立 reduce 的任務；有 map 重試用盡時不送出 reduce，把它記成退回並組出結果（步驟以 permanent 失敗，NOTE-R3）。
// 不是 map-reduce 或還有 map 沒有結果時 handled 為 false。
func finishNoteMapsTx(ctx context.Context, tx *store.Repo, itemID string, run store.StageRun, priority, pending int) (wake, handled bool, err error) {
	if pending != 1 {
		return false, false, nil
	}
	rows, err := tx.LLMChunks(ctx, run.ID)
	if err != nil || len(rows) == 0 {
		return false, false, err
	}
	var plan chunkPlan
	if err := json.Unmarshal(rows[0].Plan, &plan); err != nil || plan.Maps == 0 {
		return false, false, nil
	}
	reduce := rows[len(rows)-1]
	if reduce.Chunk != plan.Maps+1 || reduce.Status != store.ChunkPending {
		return false, false, nil
	}
	for _, row := range rows[:len(rows)-1] {
		if row.Status == store.ChunkFallback {
			detail, _ := json.Marshal(map[string]any{"error": fmt.Sprintf("第 %d 段的要點沒有通過驗證", row.Chunk)})
			w, err := finishChunkTx(ctx, tx, itemID, run, priority, reduce.Chunk, store.ChunkResult{
				Status: store.ChunkFallback, Reason: llm.ReasonNoteStructure, Usage: json.RawMessage(`{}`), Detail: detail,
			})
			return w, true, err
		}
	}
	payload, err := json.Marshal(llmPayload{ItemID: itemID, Phase: llmPhaseChunk})
	if err != nil {
		return false, true, err
	}
	executor, deadline := queue.ExecutorBuiltin, (*time.Time)(nil)
	if run.Executor == ExecutorAgent {
		snap, err := tx.Snapshot(ctx, run.ConfigSnapshotID)
		if err != nil {
			return false, true, err
		}
		eff, err := ParseEffective(snap)
		if err != nil {
			return false, true, err
		}
		as, err := eff.Agent()
		if err != nil {
			return false, true, err
		}
		at := tx.Now().Add(as.Timeout)
		executor, deadline = ExecutorAgent, &at
	}
	idx := reduce.Chunk
	if _, err := tx.InsertTask(ctx, store.NewTask{
		StageRunID: run.ID, Kind: StageNote, Executor: executor, ResourceClass: queue.ResourceLLM,
		Priority: priority, Payload: payload, ChunkIndex: &idx, MaxAttempts: queue.DefaultMaxAttempts, DeadlineAt: deadline,
	}); err != nil {
		return false, true, err
	}
	if executor == ExecutorAgent {
		return false, true, awaitAgentIfIdle(ctx, tx, run)
	}
	return true, true, nil
}

// noteDoc 是 note 產出檔的內容（ADR-0020 第 4 點：Markdown 本文與 {frontmatter, checks, engine, template}）。
// sections 與 self_check 是模型通過驗證的輸出，預覽模板時重新套用（NOTE-R12）。
type noteDoc struct {
	Lang        string            `json:"lang"`
	Markdown    string            `json:"markdown"`
	Frontmatter map[string]string `json:"frontmatter"`
	Checks      []notes.Check     `json:"checks"`
	Engine      string            `json:"engine"`
	Template    string            `json:"template"`
	Sections    []notes.Section   `json:"sections"`
	SelfCheck   []string          `json:"self_check"`
	// Images 是筆記嵌入的畫面檔名與畫面編號（VAULT 寫入圖片用）。
	Images []noteImage `json:"images"`
	// Render 是重新套用模板需要的資料（預覽，NOTE-R12）。
	Render noteRender `json:"render"`
}

type noteImage struct {
	N    int    `json:"n"`
	File string `json:"file"`
}

// noteFailureMessage 是筆記驗證重試用盡時的訊息（NOTE-R3）。
func noteFailureMessage(fallbacks []fallbackRecord) string {
	reason := "格式不正確"
	if len(fallbacks) > 0 {
		if s, ok := fallbacks[0].Detail["error"].(string); ok && s != "" {
			reason = s
		} else if fallbacks[0].Reason != "" {
			reason = fallbacks[0].Reason
		}
	}
	return fmt.Sprintf("筆記的內容沒有通過驗證（%s）。", reason)
}

// noteInputFor 組出渲染筆記的輸入（不含 sections）：frontmatter、資訊表、時間連結與畫面。
func (p *Pipeline) noteInputFor(it store.Item, lang, status string, tr subtitle.Transcript, cueSet map[int]bool, frames map[int]notes.Frame, extra []notes.InfoRow) notes.Input {
	var md struct {
		Channel string `json:"channel"`
		Podcast struct {
			Enclosure struct {
				URL string `json:"url"`
			} `json:"enclosure"`
		} `json:"podcast"`
	}
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md) // 格式不對時當成沒有中繼資料
	}
	published := ""
	if it.PublishedAt != nil {
		published = it.PublishedAt.UTC().Format("2006-01-02")
	}
	src := it.URL
	if src == "" {
		// 本機檔案沒有網址：用影音代號（NOTE N13）。
		src = source.Ref(it.Platform, it.ExternalID, it.Part)
	}
	starts := make(map[int]int64, len(cueSet))
	for i := range cueSet {
		if i >= 1 && i <= len(tr.Cues) {
			starts[i] = tr.Cues[i-1].StartMS
		}
	}
	return notes.Input{
		Front: notes.Frontmatter{
			Title: it.Title, Source: src, Channel: md.Channel, VideoPublished: published, ItemID: it.ID, Status: status,
		},
		Title: it.Title, Channel: md.Channel, URL: it.URL, Platform: platformLabel(it.Platform), Lang: lang,
		DurationMS: durationMS(it),
		Linker:     notes.Linker{Platform: it.Platform, ID: it.ExternalID, Part: it.Part, AudioURL: md.Podcast.Enclosure.URL},
		CueStart:   starts, Frames: frames, Extra: extra,
	}
}

// platformLabel 是資訊表「原文」的顯示名稱。
func platformLabel(platform string) string {
	switch platform {
	case source.PlatformYouTube:
		return "YouTube"
	case source.PlatformBilibili:
		return "Bilibili"
	case source.PlatformPodcast:
		return "Podcast"
	}
	return ""
}

// renderNote 以模板與模型的輸出產生筆記並執行自動檢查（NOTE-R7、R11）。
func renderNote(in notes.Input, out llm.NoteOutput, tpl noteTemplate) (noteDoc, error) {
	for _, s := range out.Sections {
		in.Sections = append(in.Sections, notes.Section{Level: s.Level, Title: s.Title, Cue: s.Cue, Body: s.Body, Frames: s.Frames})
	}
	in.SelfCheck = out.SelfCheck
	in.ReadButton = tpl.Rules.ReadButton
	md, err := notes.Render(tpl.Body, notes.BuildData(in))
	if err != nil {
		return noteDoc{}, err
	}
	doc := noteDoc{
		Lang: in.Lang, Markdown: md, Frontmatter: in.Front.Map(), Engine: "pipeline", Template: tpl.Ref(),
		Sections: in.Sections, SelfCheck: in.SelfCheck, Images: []noteImage{},
	}
	if doc.Sections == nil {
		doc.Sections = []notes.Section{}
	}
	doc.Render = noteRender{Status: in.Front.Status, CueStart: in.CueStart, Frames: map[int]noteFrameAt{}, Extra: in.Extra}
	for n, f := range in.Frames {
		doc.Render.Frames[n] = noteFrameAt{AtMS: f.AtMS, Caption: f.Caption}
	}
	images := in.Images()
	for _, s := range in.Sections {
		for _, n := range s.Frames {
			if f, ok := in.Frames[n]; ok {
				doc.Images = append(doc.Images, noteImage{N: n, File: notes.FrameFileName(in.Title, f.AtMS, n)})
			}
		}
	}
	doc.Checks = notes.RunChecks(md, notes.CheckOptions{Enabled: tpl.Rules.Checks, Images: images, Title: in.Title})
	return doc, nil
}

// writeNote 是 note 的 llmStage.write（NOTE-R2～R4、R7～R9、R13）：套用模板、執行自動檢查，寫出 note 產出。
// 驗證重試用盡時步驟 failed（permanent）；自動檢查不通過時仍寫出產出，但步驟 blocked（needs_action），不會前進到 publish。
func (p *Pipeline) writeNote(ctx context.Context, it store.Item, job *llmJob, outputs []json.RawMessage, fallbacks []fallbackRecord, fields map[string]any) (*store.NewArtifact, store.StageState, error) {
	nj := job.note
	if nj == nil || len(job.chunks) == 0 {
		return nil, store.StageState{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "筆記沒有輸入",
			Message: "字幕的每一句都在 SponsorBlock 片段內，沒有可以寫成筆記的內容。"}
	}
	if len(fallbacks) > 0 {
		return nil, store.StageState{
			Status: store.StatusFailed, ErrorClass: string(errclass.ClassPermanent),
			Message: noteFailureMessage(fallbacks), Actions: []string{ActionRegenerateNote, actionOpenPrompt},
		}, nil
	}
	raw := outputs[len(outputs)-1]
	last := job.chunks[len(job.chunks)-1]
	out, verr := llm.CheckNote(job.chunkInput(last), raw, llm.NoteRefs{Cues: nj.cueSet, Frames: selectedSet(nj.frames)})
	if verr != nil {
		return nil, store.StageState{}, errPermanent("筆記的輸出不正確：" + verr.Message)
	}
	in := p.noteInputFor(it, nj.lang, nj.status, job.tr, nj.cueSet, nj.frames, nj.extraRows)
	doc, err := renderNote(in, out, nj.template)
	if err != nil {
		var te *notes.TemplateError
		if errors.As(err, &te) {
			return nil, store.StageState{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: te.Message,
				Message: "筆記模板無法套用：" + te.Message}
		}
		return nil, store.StageState{}, err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, store.StageState{}, err
	}
	passed := notes.Passed(doc.Checks)
	fields["engine"] = "pipeline"
	fields["template"] = nj.template.Ref()
	fields["template_sha256"] = nj.template.SHA256
	fields["checks"] = map[string]any{"passed": passed}
	fields["lang"] = nj.lang
	fields["note_status"] = nj.status
	if nj.maps > 0 {
		fields["map_reduce"] = map[string]any{"maps": nj.maps}
	}
	// parents（NOTE-R13）：輸入字幕、chapters、frames、sponsor_segments，依序、只列實際用到的。
	parents := []string{job.input.ID}
	df, _ := fields["derived_from"].([]map[string]string)
	for _, a := range []*store.Artifact{nj.chapters, nj.frameArt, noteSponsor(job)} {
		if a == nil {
			continue
		}
		parents = append(parents, a.ID)
		df = append(df, map[string]string{"artifact": a.ID, "variant": a.Variant, "sha256": a.SHA256})
	}
	if df != nil {
		fields["derived_from"] = df
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return nil, store.StageState{}, err
	}
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(p.dataDir, it.ID, id, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return nil, store.StageState{}, fmt.Errorf("寫入產出檔：%w", err)
	}
	art := &store.NewArtifact{
		ID: id, ItemID: it.ID, Kind: KindNote, Variant: KindNote, Lang: nj.lang, Format: noteFmt,
		Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
	}
	state := store.StageState{Status: store.StatusDone}
	if !passed {
		// NOTE-R7：不通過的筆記不會寫進 vault；步驟停下等使用者處理。
		state = store.StageState{
			Status: store.StatusBlocked, BlockReason: reasonNeedsAction, ErrorClass: string(errclass.ClassNeedsAction),
			Message: notes.FailureMessage(doc.Checks), Actions: []string{ActionRegenerateNote, actionOpenTemplate},
		}
	}
	return art, state, nil
}

// noteSponsor 回傳筆記實際用到的 sponsor_segments 產出：有句子因為在片段內而沒有放進輸入時才算（NOTE-R13 只列實際用到的）。
func noteSponsor(job *llmJob) *store.Artifact {
	if job.sponsor == nil || len(job.cues) == len(job.tr.Cues) {
		return nil
	}
	return job.sponsor
}
