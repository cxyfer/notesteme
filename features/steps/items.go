package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// registerItems 註冊「影音與產出」的步驟，句型見 docs/specs/steps.md。
func registerItems(r *Registry, s *Scenario) {
	r.Step(`the owner resolves {string}`, s.ownerResolves)
	r.Step(`the owner ingests {string}`, s.ownerIngests)
	r.Step(`the owner ingests {string} with the profile {string}`, s.ownerIngestsWithProfile)
	r.Step(`the owner ingests {string} with the overrides:`, s.ownerIngestsWithOverrides)
	r.Step(`the owner uploads {string}`, s.ownerUploads)
	r.Step(`the owner reruns {string} from {string}`, s.ownerReruns)
	r.Step(`the item {string} has been ingested`, s.itemHasBeenIngested)
	r.Step(`the item {string} has been ingested as {word}`, s.itemHasBeenIngestedAs)
	r.Step(`{string} is a mixed-language item`, s.itemIsMixedLanguage)
	r.Step(`the item {string} has been processed up to {string}`, s.itemProcessedUpTo)
	r.Given(`the {string} transcript of {string} is:`, func(variant, ref string, t *godog.Table) error {
		return s.seedTranscript(variant, ref, "", t)
	})
	r.Then(`the {string} transcript of {string} is:`, func(variant, ref string, t *godog.Table) error {
		return s.transcriptIs(variant, ref, "", t)
	})
	r.Given(`the {string} transcript of {string} in {string} is:`, s.seedTranscript)
	r.Then(`the {string} transcript of {string} in {string} is:`, s.transcriptIs)
	r.Step(`the worker runs until idle`, s.workerRunsUntilIdle)
	r.Step(`the library contains {int} items`, s.libraryContainsCount)
	r.Step(`the library contains {string}`, s.libraryContains)
	r.Step(`the stage {string} of {string} is {string}`, s.stageIs)
	r.Step(`the stage {string} of {string} is blocked with the reason {string}`, s.stageIsBlockedWith)
	r.Step(`the stage {string} of {string} is skipped with the reason {string}`, s.stageIsSkippedWith)
	r.Step(`the stage {string} of {string} failed with the error class {string}`, s.stageFailedWith)
	r.Step(`the stage {string} of {string} waits until {string}`, s.stageWaitsUntil)
	r.Step(`the stage {string} of {string} retries at {string}`, s.stageRetriesAt)
	r.Step(`{string} shows the message {string}`, s.itemShowsMessage)
	r.Step(`{string} offers the actions {string}`, s.itemOffersActions)
	r.Step(`the transcript of {string} came from {string}`, s.transcriptCameFrom)
	r.Step(`the source {string} was rejected for {string} because {string}`, s.sourceRejectedFor)
	r.Step(`the original language of {string} is {string}`, s.originalLanguageIs)
	r.Step(`the original language of {string} is marked as mixed`, s.originalLanguageIsMixed)
	r.Step(`the {string} transcript of {string} has {int} cues`, s.transcriptHasCues)
	r.Step(`the {string} transcript of {string} in {string} has {int} cues`, s.transcriptInLangHasCues)
	r.Step(`every cue of the {string} transcript of {string} follows the subtitle rules for {string}`, s.everyCueFollowsRules)
	r.Step(`every cue of the {string} transcript of {string} has the timing {string}`, s.everyCueHasTiming)
	r.Step(`the {string} artifact of {string} has the provenance:`, s.artifactHasProvenance)
	r.Step(`the {string} artifact of {string} has {int} versions`, s.artifactHasVersions)
	r.Step(`the item {string} has no {string} artifact`, s.itemHasNoArtifact)
}

// ---- 新增 ----

func (s *Scenario) ownerIngests(target string) error { return s.ingest(target, nil) }

// ownerIngestsWithOverrides 帶單次覆寫（YAML）新增。覆寫中的 until 等同 API 的 until 欄位。
func (s *Scenario) ownerIngestsWithOverrides(target string, doc *godog.DocString) error {
	var overrides map[string]any
	if err := yaml.Unmarshal([]byte(doc.Content), &overrides); err != nil {
		return fmt.Errorf("單次覆寫不是合法的 YAML：%w", err)
	}
	return s.ingest(target, overrides)
}

// ownerIngestsWithProfile 以指定的處理方案新增：body 加上 profile。
func (s *Scenario) ownerIngestsWithProfile(target, profile string) error {
	body := s.ingestBody(target)
	body["profile"] = profile
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return s.send("POST /api/items", requestOpts{who: asOwner, body: raw})
}

// ingest 送出 POST /api/items。target 是影音代號或完整網址。
func (s *Scenario) ingest(target string, overrides map[string]any) error {
	body := s.ingestBody(target)
	if until, ok := overrides["until"]; ok {
		body["until"] = until
		delete(overrides, "until")
	}
	if len(overrides) > 0 {
		body["overrides"] = overrides
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return s.send("POST /api/items", requestOpts{who: asOwner, body: raw})
}

// itemHasBeenIngested 以 API 新增影音，worker 不執行（任務留在佇列中）。
func (s *Scenario) itemHasBeenIngested(ref string) error {
	if err := s.ingest(ref, nil); err != nil {
		return err
	}
	// 新增還沒實作（501）時，這個前提無法成立：與斷言回應的步驟一樣視為 pending（ADR-0015）。
	if s.last.status == http.StatusNotImplemented && isNotImplemented(s.last.body) {
		return godog.ErrPending
	}
	if s.last.status != http.StatusCreated && s.last.status != http.StatusOK {
		return fmt.Errorf("新增 %s 失敗：HTTP %d %s", ref, s.last.status, snippet(s.last.body))
	}
	return nil
}

// itemHasBeenIngestedAs 以指定的優先級（manual、subscription、backfill）新增影音。
// 優先級不開放給 API，透過程序內 server 的測試專用介面（ADR-0015）。
func (s *Scenario) itemHasBeenIngestedAs(ref, priority string) error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	return s.server.IngestAs(s.ctx, refToURL(ref), priority)
}

// workerRunsUntilIdle 讓程序內 server 的手動 worker 執行到沒有可以執行的任務（ADR-0015）；不使用 sleep。
func (s *Scenario) workerRunsUntilIdle() error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	// 先前的 worker 輪次可能把任務擋在 gate 前；解除後才會跑到閒置。
	if err := s.World.OpenGate(); err != nil {
		return fmt.Errorf("解除 gate：%w", err)
	}
	return s.server.RunUntilIdle(s.ctx)
}

// ---- 影音與步驟 ----

// libraryRefs 回傳資料庫中的影音代號（不含垃圾桶中的）。server 已明確停下時改用唯讀方式查詢 DB，
// 不隱含啟動 server（steps.md 4.4）；否則走 API。
func (s *Scenario) libraryRefs() ([]string, error) {
	if s.stopped {
		return s.World.DBItemRefs()
	}
	items, err := s.listItems()
	if err != nil {
		return nil, err
	}
	refs := make([]string, len(items))
	for i, it := range items {
		refs[i] = it.Ref
	}
	return refs, nil
}

func (s *Scenario) libraryContainsCount(want int) error {
	refs, err := s.libraryRefs()
	if err != nil {
		return err
	}
	if len(refs) != want {
		return fmt.Errorf("資料庫有 %d 部影音，預期 %d：%v", len(refs), want, refs)
	}
	return nil
}

func (s *Scenario) libraryContains(ref string) error {
	refs, err := s.libraryRefs()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r == ref {
			return nil
		}
	}
	return fmt.Errorf("資料庫沒有影音 %q（共 %d 部）", ref, len(refs))
}

// dbStageStatus 直接讀資料庫中影音某個步驟的狀態，不經過 API：server 已停下（不啟動新的 server），
// 或影音在垃圾桶中（詳情回 404）時使用。影音的 ID 取自新增時記下的 World.Refs。
func (s *Scenario) dbStageStatus(stage, ref, why string) (string, error) {
	id, ok := s.World.Refs[ref]
	if !ok {
		return "", fmt.Errorf("%s，而且場景沒有記下影音 %q 的 ID，無法直接讀資料庫", why, ref)
	}
	got, found, err := s.World.DBStageStatus(id, stage)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s 沒有步驟 %q（讀自資料庫，%s）", ref, stage, why)
	}
	return got, nil
}

// stageIsFromDB 比對步驟狀態：直接讀資料庫（見 dbStageStatus）。
func (s *Scenario) stageIsFromDB(stage, ref, want, why string) error {
	got, err := s.dbStageStatus(stage, ref, why)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期 %q（%s，讀自資料庫）", ref, stage, got, want, why)
	}
	return nil
}

func (s *Scenario) stageIs(stage, ref, want string) error {
	if s.stopped {
		return s.stageIsFromDB(stage, ref, want, "server 已停止")
	}
	it, err := s.item(ref)
	if _, trashed := inTrashBody(err); trashed {
		// 影音在垃圾桶中：詳情回 404，步驟狀態改讀資料庫（steps.md 4.4）。
		return s.stageIsFromDB(stage, ref, want, "影音在垃圾桶中")
	}
	if err != nil {
		return err
	}
	for _, st := range it.Stages {
		if st.Stage == stage {
			if st.Status != want {
				// 緊接在前一個步驟送出的請求是還沒實作的路由（例如取消、刪除）：步驟狀態不符是因為那個動作沒有發生。
				if s.previousRequestNotImplemented() {
					return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
				}
				// GEM：Gemini 的筆記通過驗證，但 NOTE 的模板還沒有實作（M3-S4）；不是 GEM 的退步（GEM G9）。
				if stage == pipeline.StageNote && st.Message == pipeline.GeminiNoteRendererMissing {
					return fmt.Errorf("note 的筆記模板還沒有實作（M3-S4）：%w", godog.ErrPending)
				}
				return fmt.Errorf("%s 的步驟 %s 是 %q，預期 %q（%s）", ref, stage, st.Status, want, describeStage(st))
			}
			return nil
		}
	}
	return fmt.Errorf("%s 沒有步驟 %q", ref, stage)
}

func describeStage(st stageJSON) string {
	var parts []string
	for _, kv := range [][2]string{{"block_reason", st.BlockReason}, {"error_class", st.ErrorClass}, {"message", st.Message}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if st.NotBefore != nil {
		parts = append(parts, "not_before="+*st.NotBefore)
	}
	return strings.Join(parts, " ")
}

// transcriptCameFrom 比對採用的字幕來源：影音詳情的 sources 中狀態為 used 的那一個。
func (s *Scenario) transcriptCameFrom(ref, want string) error {
	it, err := s.item(ref)
	if err != nil {
		return err
	}
	for _, src := range it.Sources {
		if src.State == "used" {
			if src.Source != want {
				return fmt.Errorf("%s 的字幕來自 %q，預期 %q", ref, src.Source, want)
			}
			return nil
		}
	}
	return fmt.Errorf("%s 沒有採用任何字幕來源（sources：%+v）", ref, it.Sources)
}

// ---- 字幕 ----

func (s *Scenario) transcriptHasCues(variant, ref string, want int) error {
	return s.cueCount(variant, ref, "", want)
}

func (s *Scenario) transcriptInLangHasCues(variant, ref, lang string, want int) error {
	return s.cueCount(variant, ref, lang, want)
}

// cueCount 取得字幕（GET /api/items/{id}/cues）並比對 cue 數。
func (s *Scenario) cueCount(variant, ref, lang string, want int) error {
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	q := url.Values{"variant": {variant}}
	if lang != "" {
		q.Set("lang", lang)
	}
	var out struct {
		Variant string            `json:"variant"`
		Lang    string            `json:"lang"`
		Cues    []json.RawMessage `json:"cues"`
	}
	if err := s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id)+"/cues?"+q.Encode(), nil, &out); err != nil {
		return fmt.Errorf("取得 %s 的 %s 字幕：%w", ref, variant, err)
	}
	if len(out.Cues) != want {
		return fmt.Errorf("%s 的 %s 字幕（%s）有 %d 句，預期 %d 句", ref, variant, out.Lang, len(out.Cues), want)
	}
	return nil
}

// ---- 辨識網址與重跑 ----

// ownerResolves 送出 POST /api/resolve。target 是影音代號或完整網址。
func (s *Scenario) ownerResolves(target string) error {
	raw, err := json.Marshal(map[string]string{"url": refToURL(target)})
	if err != nil {
		return err
	}
	return s.send("POST /api/resolve", requestOpts{who: asOwner, body: raw})
}

// ownerReruns 送出 POST /api/items/{id}/rerun。步驟還沒實作（501）時，前提無法成立，視為 pending（ADR-0015）；
// 其他回應（包括錯誤）記錄下來，由後面的步驟斷言。
func (s *Scenario) ownerReruns(ref, stage string) error {
	raw, err := json.Marshal(map[string]string{"from": stage})
	if err != nil {
		return err
	}
	if err := s.send("POST /api/items/"+ref+"/rerun", requestOpts{who: asOwner, body: raw}); err != nil {
		return err
	}
	if s.last.status == http.StatusNotImplemented && isNotImplemented(s.last.body) {
		return godog.ErrPending
	}
	return nil
}

// ---- 字幕的 seeding 與比對 ----

// transcriptRow 是字幕表格的一列；只記錄表格中出現的欄位。
type transcriptRow struct {
	cells map[string]string
}

// tableRows 把表格（第一列是欄名）轉成各列的欄位；欄名只能是 #、start、end、text、words、timing。
func tableRows(t *godog.Table) ([]transcriptRow, error) {
	if t == nil || len(t.Rows) == 0 {
		return nil, fmt.Errorf("字幕表格沒有內容")
	}
	var cols []string
	for _, c := range t.Rows[0].Cells {
		switch c.Value {
		case "#", "start", "end", "text", "words", "timing":
			cols = append(cols, c.Value)
		default:
			return nil, fmt.Errorf("字幕表格不支援欄位 %q（只有 #、start、end、text、words、timing）", c.Value)
		}
	}
	rows := make([]transcriptRow, 0, len(t.Rows)-1)
	for _, r := range t.Rows[1:] {
		row := transcriptRow{cells: map[string]string{}}
		for i, c := range r.Cells {
			row.cells[cols[i]] = c.Value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ensureItemForSeed 回傳影音的 ID；影音還不存在時依代號新增（做到 metadata 為止）並執行 worker 完成它，
// 這樣影音有標題與原文語言，之後的步驟再由 seeding 的產出接手。
func (s *Scenario) ensureItemForSeed(ref string) (string, error) {
	items, err := s.listItems()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.Ref == ref {
			return it.ID, nil
		}
	}
	body := s.ingestBody(ref)
	body["until"] = pipeline.StageMetadata
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	resp, err := s.apiCall(http.MethodPost, "/api/items", raw)
	if err != nil {
		return "", err
	}
	if resp.status == http.StatusNotImplemented && isNotImplemented(resp.body) {
		return "", godog.ErrPending
	}
	if resp.status != http.StatusCreated {
		return "", fmt.Errorf("為 seeding 新增 %s 失敗：HTTP %d %s", ref, resp.status, snippet(resp.body))
	}
	s.recordRefs(resp)
	if err := s.server.RunUntilIdle(s.ctx); err != nil {
		return "", err
	}
	return s.itemID(ref)
}

// seedTranscript 把指定變體的字幕直接寫成產出（透過程序內 server 的測試專用介面，ADR-0015）。
// 沒有指定語言時，用影音的原文語言，影音沒有原文語言就用 en（見 pipeline.SeedTranscript）。
func (s *Scenario) seedTranscript(variant, ref, lang string, table *godog.Table) error {
	rows, err := tableRows(table)
	if err != nil {
		return err
	}
	cues := make([]subtitle.Cue, 0, len(rows))
	for i, row := range rows {
		c := subtitle.Cue{Index: i + 1, Text: row.cells["text"], Timing: subtitle.TimingOrigin(row.cells["timing"])}
		if v, ok := row.cells["#"]; ok {
			if c.Index, err = strconv.Atoi(v); err != nil {
				return fmt.Errorf("第 %d 列的 # 不是整數：%q", i+1, v)
			}
		}
		if c.StartMS, err = subtitle.ParseTimecode(row.cells["start"]); err != nil {
			return fmt.Errorf("第 %d 列的 start：%w", i+1, err)
		}
		if c.EndMS, err = subtitle.ParseTimecode(row.cells["end"]); err != nil {
			return fmt.Errorf("第 %d 列的 end：%w", i+1, err)
		}
		if c.Timing != "" && !c.Timing.Valid() {
			return fmt.Errorf("第 %d 列的 timing %q 不正確", i+1, c.Timing)
		}
		if c.Words, err = subtitle.ParseWords(row.cells["words"]); err != nil {
			return fmt.Errorf("第 %d 列的 words：%w", i+1, err)
		}
		cues = append(cues, c)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	if err := s.server.SeedTranscript(s.ctx, pipeline.SeedRequest{ItemID: id, Variant: subtitle.Variant(variant), Lang: lang, Cues: cues}); err != nil {
		return err
	}
	// 只有 source 變體才算「場景已準備字幕來源」（steps.md 4.4 規則 (1)）：corrected 等後面的變體不是來源，
	// 之後的 `processed up to` 仍要走 fixture（OUT-R9 的預覽影音沒有任何字幕軌，只在 Background 寫了 corrected）。
	if subtitle.Variant(variant) == subtitle.VariantSource {
		if s.seeded == nil {
			s.seeded = map[string]bool{}
		}
		s.seeded[ref] = true
	}
	return nil
}

// 字幕 API 回應的形狀。
type cueJSON struct {
	Index  int    `json:"index"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Text   string `json:"text"`
	Timing string `json:"timing"`
	Words  []struct {
		Text  string `json:"text"`
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"words"`
}

type cuesJSON struct {
	Variant string    `json:"variant"`
	Lang    string    `json:"lang"`
	Cues    []cueJSON `json:"cues"`
}

// transcriptIs 透過 GET /api/items/{id}/cues 取得字幕，只比對表格中出現的欄位。
// 表格有 `#` 欄，而且 `#` 不是從 1 起連號時，是「部分表格」：依 `#` 對應第 N 個 cue 比對，不檢查總句數
// （SRC-R12 只列出接縫處的兩句）；否則 cue 數必須與表格相同，並依序比對。
func (s *Scenario) transcriptIs(variant, ref, lang string, table *godog.Table) error {
	want, err := tableRows(table)
	if err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	q := url.Values{"variant": {variant}}
	if lang != "" {
		q.Set("lang", lang)
	}
	var got cuesJSON
	if err := s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id)+"/cues?"+q.Encode(), nil, &got); err != nil {
		return fmt.Errorf("取得 %s 的 %s 字幕：%w", ref, variant, err)
	}
	return checkTranscript(ref, variant, got, want)
}

// checkTranscript 比對取得的字幕與表格（規則見 transcriptIs）。
func checkTranscript(ref, variant string, got cuesJSON, want []transcriptRow) error {
	partial, err := isPartialTable(want)
	if err != nil {
		return err
	}
	if !partial && len(got.Cues) != len(want) {
		return fmt.Errorf("%s 的 %s 字幕（%s）有 %d 句，預期 %d 句：%s", ref, variant, got.Lang, len(got.Cues), len(want), describeCues(got))
	}
	for i, w := range want {
		pos := i // 對應的 cue 在字幕中的位置
		if partial {
			n, _ := strconv.Atoi(w.cells["#"])
			if n < 1 || n > len(got.Cues) {
				return fmt.Errorf("%s 的 %s 字幕（%s）沒有第 %d 句（共 %d 句）", ref, variant, got.Lang, n, len(got.Cues))
			}
			pos = n - 1
		}
		if err := compareCueRow(ref, variant, pos+1, w, got.Cues[pos]); err != nil {
			return err
		}
	}
	return nil
}

// isPartialTable 判斷表格是不是「部分表格」：有 `#` 欄，而且各列的 `#` 不是 1、2、3… 的連號。
// 沒有 `#` 欄、或 `#` 從 1 起連號的表格代表完整的字幕（要求句數相同）。`#` 不是整數時回傳錯誤。
func isPartialTable(rows []transcriptRow) (bool, error) {
	partial := false
	hasIndex := false
	for i, r := range rows {
		v, ok := r.cells["#"]
		if !ok {
			continue
		}
		hasIndex = true
		n, err := strconv.Atoi(v)
		if err != nil {
			return false, fmt.Errorf("表格第 %d 列的 # 不是整數：%q", i+1, v)
		}
		if n != i+1 {
			partial = true
		}
	}
	return hasIndex && partial, nil
}

// compareCueRow 比對一個 cue 與表格的一列（只比對表格中出現的欄位）；pos 是 cue 在字幕中的第幾句（從 1 起），用在錯誤訊息。
func compareCueRow(ref, variant string, pos int, w transcriptRow, g cueJSON) error {
	fail := func(field, have, exp string) error {
		return fmt.Errorf("%s 的 %s 字幕第 %d 句：%s 是 %q，預期 %q", ref, variant, pos, field, have, exp)
	}
	for field, exp := range w.cells {
		switch field {
		case "#":
			if n, err := strconv.Atoi(exp); err != nil || g.Index != n {
				return fail("#", strconv.Itoa(g.Index), exp)
			}
		case "text":
			if g.Text != exp {
				return fail("text", g.Text, exp)
			}
		case "timing":
			if g.Timing != exp {
				return fail("timing", g.Timing, exp)
			}
		case "start", "end":
			have := g.Start
			if field == "end" {
				have = g.End
			}
			// 時間以毫秒比對，表格寫 00:00:01 或 00:00:01.000 都可以。
			hm, err1 := subtitle.ParseTimecode(have)
			em, err2 := subtitle.ParseTimecode(exp)
			if err1 != nil || err2 != nil || hm != em {
				return fail(field, have, exp)
			}
		case "words":
			expWords, err := subtitle.ParseWords(exp)
			if err != nil {
				return fmt.Errorf("表格第 %d 句的 words：%w", pos, err)
			}
			var haveWords []string
			for _, hw := range g.Words {
				haveWords = append(haveWords, hw.Text+"@"+hw.Start+"-"+hw.End)
			}
			if h, e := strings.Join(haveWords, " "), subtitle.FormatWords(expWords); h != e {
				return fail("words", h, e)
			}
		}
	}
	return nil
}

func describeCues(c cuesJSON) string {
	parts := make([]string, len(c.Cues))
	for i, cue := range c.Cues {
		parts[i] = fmt.Sprintf("%d %s-%s %q", cue.Index, cue.Start, cue.End, cue.Text)
	}
	return strings.Join(parts, "; ")
}

// itemHasNoArtifact 確認影音沒有指定名稱的產出：種類、變體或格式都不叫這個名字。
// 例如 srt 只是下載時即時渲染的格式，不會存成產出（OUT-R1）。
func (s *Scenario) itemHasNoArtifact(ref, name string) error {
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	arts, err := s.server.Artifacts(s.ctx, id)
	if err != nil {
		return err
	}
	for _, a := range arts {
		if strings.EqualFold(a.Kind, name) || strings.EqualFold(a.Variant, name) || strings.EqualFold(a.Format, name) {
			return fmt.Errorf("%s 有 %q 產出：%s/%s（%s）", ref, name, a.Kind, a.Variant, a.Format)
		}
	}
	return nil
}

// originalLanguageIs 比對影音詳情的原文語言（original_lang，SRC-R11）。server 已停止時明確失敗，不隱含啟動新的 server。
// originalLanguageIsMixed 檢查影音詳情的 lang_mixed（SRC-R11）。
func (s *Scenario) originalLanguageIsMixed(ref string) error {
	it, err := s.item(ref)
	if err != nil {
		return err
	}
	if !it.LangMixed {
		return fmt.Errorf("%s 的原文語言沒有標記為 mixed（original_lang %q）", ref, it.OriginalLang)
	}
	return nil
}

func (s *Scenario) originalLanguageIs(ref, want string) error {
	if s.stopped {
		return fmt.Errorf("server 已停止，還不支援從 DB 讀原文語言")
	}
	it, err := s.item(ref)
	if err != nil {
		return err
	}
	if it.OriginalLang != want {
		return fmt.Errorf("%s 的原文語言是 %q，預期 %q", ref, it.OriginalLang, want)
	}
	return nil
}

// itemIsMixedLanguage 把影音的原文語言標記為 mixed（steps.md：Given {string} is a mixed-language item）。
// 這是前置條件（seeding）：產品的判斷由 transcript 步驟依字幕文字做（SRC-R11），這裡直接寫入（harness 可以存取 Core，ADR-0015）。
func (s *Scenario) itemIsMixedLanguage(ref string) error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	return s.server.SeedLangMixed(s.ctx, id)
}
