package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
)

// registerItems 註冊「影音與產出」的步驟，句型見 docs/specs/steps.md。
func registerItems(r *Registry, s *Scenario) {
	r.Step(`the owner resolves {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner ingests {string}`, s.ownerIngests)
	r.Step(`the owner ingests {string} with the profile {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the owner ingests {string} with the overrides:`, s.ownerIngestsWithOverrides)
	r.Step(`the owner uploads {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner reruns {string} from {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the item {string} has been ingested`, s.itemHasBeenIngested)
	r.Step(`the item {string} has been ingested as {word}`, func(string, string) error { return godog.ErrPending })
	r.Step(`{string} is a mixed-language item`, func(string) error { return godog.ErrPending })
	r.Step(`the item {string} has been processed up to {string}`, func(string, string) error { return godog.ErrPending })
	r.Given(`the {string} transcript of {string} is:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Then(`the {string} transcript of {string} is:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Given(`the {string} transcript of {string} in {string} is:`, func(string, string, string, *godog.Table) error { return godog.ErrPending })
	r.Then(`the {string} transcript of {string} in {string} is:`, func(string, string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the worker runs until idle`, s.workerRunsUntilIdle)
	r.Step(`the library contains {int} items`, s.libraryContainsCount)
	r.Step(`the library contains {string}`, s.libraryContains)
	r.Step(`the stage {string} of {string} is {string}`, s.stageIs)
	r.Step(`the stage {string} of {string} is blocked with the reason {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the stage {string} of {string} failed with the error class {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the stage {string} of {string} waits until {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the stage {string} of {string} retries at {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`{string} shows the message {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`{string} offers the actions {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the transcript of {string} came from {string}`, s.transcriptCameFrom)
	r.Step(`the source {string} was rejected for {string} because {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the original language of {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the original language of {string} is marked as mixed`, func(string) error { return godog.ErrPending })
	r.Step(`the {string} transcript of {string} has {int} cues`, s.transcriptHasCues)
	r.Step(`the {string} transcript of {string} in {string} has {int} cues`, s.transcriptInLangHasCues)
	r.Step(`every cue of the {string} transcript of {string} follows the subtitle rules for {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`every cue of the {string} transcript of {string} has the timing {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the {string} artifact of {string} has the provenance:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the {string} artifact of {string} has {int} versions`, func(string, string, int) error { return godog.ErrPending })
	r.Step(`the item {string} has no {string} artifact`, func(string, string) error { return godog.ErrPending })
	r.Step(`the media of {string} is cached`, func(string) error { return godog.ErrPending })
	r.Step(`the trash contains {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the trash does not contain {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the trash contains {int} items`, func(int) error { return godog.ErrPending })
	r.Step(`the cost ledger has an entry of {string} USD for {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the cost ledger has an entry of {string} USD titled {string}`, func(string, string) error { return godog.ErrPending })
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

// ingest 送出 POST /api/items。target 是影音代號或完整網址。
func (s *Scenario) ingest(target string, overrides map[string]any) error {
	body := map[string]any{"url": refToURL(target)}
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
	if err := s.send("POST /api/items", requestOpts{who: asOwner, body: raw}); err != nil {
		return err
	}
	s.recordRefs(s.last)
	return nil
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

// workerRunsUntilIdle 讓程序內 server 的手動 worker 執行到沒有可以執行的任務（ADR-0015）；不使用 sleep。
func (s *Scenario) workerRunsUntilIdle() error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	return s.server.RunUntilIdle(s.ctx)
}

// ---- 影音與步驟 ----

func (s *Scenario) libraryContainsCount(want int) error {
	items, err := s.listItems()
	if err != nil {
		return err
	}
	if len(items) != want {
		refs := make([]string, len(items))
		for i, it := range items {
			refs[i] = it.Ref
		}
		return fmt.Errorf("資料庫有 %d 部影音，預期 %d：%v", len(items), want, refs)
	}
	return nil
}

func (s *Scenario) libraryContains(ref string) error {
	items, err := s.listItems()
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Ref == ref {
			return nil
		}
	}
	return fmt.Errorf("資料庫沒有影音 %q（共 %d 部）", ref, len(items))
}

func (s *Scenario) stageIs(stage, ref, want string) error {
	it, err := s.item(ref)
	if err != nil {
		return err
	}
	for _, st := range it.Stages {
		if st.Stage == stage {
			if st.Status != want {
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
