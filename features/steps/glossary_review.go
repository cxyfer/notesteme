package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerGlossaryReview 註冊術語審核（GLOS）的步驟（steps.md 4.20「術語審核」）。
// 這四個句型只有 GLOS 的場景使用，不需要依 tag 放行。
func registerGlossaryReview(r *Registry, s *Scenario) {
	r.Step(`the term candidates {string} are pending for the channel of {string}`, s.termCandidatesPending)
	r.Step(`the term candidate {string} of the channel of {string} is decided with:`, s.termCandidateDecided)
	r.Step(`the term candidates are decided in one batch:`, s.termCandidatesBatch)
	r.Step(`the glossary {string} has exactly the terms {string}`, s.glossaryHasExactly)
}

// pendingTermCandidate 是 `the term candidates … are pending` 累積、尚未寫入的候選（見 flushTermCandidates）。
type pendingTermCandidate struct {
	ref, term string
}

// splitTerms 以逗號切開、去掉前後空白；空字串代表沒有任何詞。
func splitTerms(list string) []string {
	if strings.TrimSpace(list) == "" {
		return []string{}
	}
	parts := strings.Split(list, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// termCandidatesPending 只累積；第一個送出請求的步驟才寫入（steps.md 4.20：在處理方案之後、影音的 seeding 之後）。
func (s *Scenario) termCandidatesPending(list, ref string) error {
	if _, err := s.channelKeyFor(ref); err != nil {
		return err
	}
	terms := splitTerms(list)
	if len(terms) == 0 {
		return fmt.Errorf("至少要有一個術語候選")
	}
	for _, t := range terms {
		s.termCandidates = append(s.termCandidates, pendingTermCandidate{ref: ref, term: t})
	}
	return nil
}

// flushTermCandidates 依登記順序寫入累積的術語候選；created_at 是 fake clock 的現在加上序號毫秒（GLOS-R5 的排序）。
// 清單先清空再處理：ensureItemForSeed 會再次經過 prepareOwner。
func (s *Scenario) flushTermCandidates() error {
	pending := s.termCandidates
	s.termCandidates = nil
	for i, c := range pending {
		id, err := s.ensureItemForSeed(c.ref)
		if err != nil {
			return err
		}
		at := s.World.Clock.Now().Add(time.Duration(i+1) * time.Millisecond)
		if err := s.server.SeedTermCandidate(s.ctx, id, c.term, at); err != nil {
			return fmt.Errorf("寫入 %s 頻道的術語候選 %q：%w", c.ref, c.term, err)
		}
	}
	return nil
}

// termCandidateID 以 owner 的 session 讀取頻道的術語候選，回傳 term 完全相同的候選 id 與頻道的 natural key。
func (s *Scenario) termCandidateID(ref, term string) (id, channel string, err error) {
	channel, err = s.channelKeyFor(ref)
	if err != nil {
		return "", "", err
	}
	var got struct {
		Candidates []struct {
			ID   string `json:"id"`
			Term string `json:"term"`
		} `json:"candidates"`
	}
	if err := s.apiJSON(http.MethodGet, "/api/channels/"+url.PathEscape(channel)+"/term-candidates", nil, &got); err != nil {
		return "", "", err
	}
	for _, c := range got.Candidates {
		if c.Term == term {
			return c.ID, channel, nil
		}
	}
	return "", "", fmt.Errorf("頻道 %s 沒有術語候選 %q", channel, term)
}

// termCandidateDecided 送出 PATCH /api/channels/<頻道>/term-candidates/<候選 id>；回應成為最近一次的回應。
func (s *Scenario) termCandidateDecided(term, ref string, doc *godog.DocString) error {
	id, channel, err := s.termCandidateID(ref, term)
	if err != nil {
		return err
	}
	path := "/api/channels/" + url.PathEscape(channel) + "/term-candidates/" + url.PathEscape(id)
	return s.send(http.MethodPatch+" "+path, requestOpts{who: asOwner, body: s.requestBody(doc)})
}

// termCandidatesBatch 依表格組成 {decisions: [...]}，送出 POST /api/term-candidates/decide；回應成為最近一次的回應。
// 欄位：item、term、state，可加 decided_term、scope（空白代表省略）。
func (s *Scenario) termCandidatesBatch(table *godog.Table) error {
	if table == nil || len(table.Rows) < 2 {
		return fmt.Errorf("表格至少要有標題列與一列決定")
	}
	header := make([]string, 0, len(table.Rows[0].Cells))
	for _, c := range table.Rows[0].Cells {
		header = append(header, c.Value)
	}
	for _, need := range []string{"item", "term", "state"} {
		if !slices.Contains(header, need) {
			return fmt.Errorf("表格缺少欄位 %q", need)
		}
	}
	for _, h := range header {
		if !slices.Contains([]string{"item", "term", "state", "decided_term", "scope"}, h) {
			return fmt.Errorf("表格有不認得的欄位 %q", h)
		}
	}
	decisions := make([]map[string]any, 0, len(table.Rows)-1)
	for _, row := range table.Rows[1:] {
		v := map[string]string{}
		for i, c := range row.Cells {
			v[header[i]] = c.Value
		}
		id, _, err := s.termCandidateID(v["item"], v["term"])
		if err != nil {
			return err
		}
		d := map[string]any{"candidate": id, "state": v["state"]}
		if v["decided_term"] != "" {
			d["term"] = v["decided_term"]
		}
		if v["scope"] != "" {
			d["scope"] = v["scope"]
		}
		decisions = append(decisions, d)
	}
	body, err := json.Marshal(map[string]any{"decisions": decisions})
	if err != nil {
		return err
	}
	return s.send(http.MethodPost+" /api/term-candidates/decide", requestOpts{who: asOwner, body: body})
}

// glossaryHasExactly 讀取 global 或 channel-of:<代號> 的詞彙表，terms 必須依序完全相等；不改寫最近一次的回應。
func (s *Scenario) glossaryHasExactly(which, list string) error {
	var path string
	switch {
	case which == "global":
		path = "/api/glossaries/global/default"
	case strings.HasPrefix(which, "channel-of:"):
		key, err := s.channelKeyFor(strings.TrimPrefix(which, "channel-of:"))
		if err != nil {
			return err
		}
		path = "/api/glossaries/channel/" + url.PathEscape(key)
	default:
		return fmt.Errorf("詞彙表 %q 應該是 global 或 channel-of:<代號>", which)
	}
	var got struct {
		Terms []string `json:"terms"`
	}
	if err := s.apiJSON(http.MethodGet, path, nil, &got); err != nil {
		return err
	}
	if want := splitTerms(list); !slices.Equal(got.Terms, want) {
		return fmt.Errorf("詞彙表 %s 的 terms 是 %q，預期 %q", which, got.Terms, want)
	}
	return nil
}
