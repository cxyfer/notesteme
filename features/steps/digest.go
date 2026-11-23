package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// DIG 的句型（steps.md 4.18，M4-S6）：摘要的處理方案、摘要的 seeding 與彙整設定。

// summaryProfileName 是 `the summary profile is:` 累積的處理方案名稱（steps.md 4.18）。
const summaryProfileName = "summary-test"

// digGate 是 DIG 場景的判斷（有 @DIG-R 開頭的 tag）：NOTIFY 與 SUB 的閘門句型對它們放行（M4-S6）。
func (s *Scenario) digGate() bool {
	return slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@DIG-R") })
}

// summaryProfileIs 累積處理方案 summary-test 並設為預設處理方案；YAML 必須含 stages.summary。
func (s *Scenario) summaryProfileIs(doc *godog.DocString) error {
	if !pipeline.StageAvailable(pipeline.StageSummary) {
		return fmt.Errorf("summary 還沒有實作（M4）：%w", godog.ErrPending)
	}
	cfg, err := s.yamlMap(doc.Content, "處理方案 "+summaryProfileName)
	if err != nil {
		return err
	}
	stages, _ := cfg["stages"].(map[string]any)
	if _, ok := stages["summary"]; !ok {
		return fmt.Errorf("the summary profile is: 的 YAML 必須含 stages.summary")
	}
	s.pendingProfiles = append(s.pendingProfiles, pendingProfile{name: summaryProfileName, config: cfg})
	s.pendingDefaultProfile = summaryProfileName
	return nil
}

// summaryOfIs 直接寫入影音現行的摘要（seeding）：影音不存在時先新增並做到 metadata。
func (s *Scenario) summaryOfIs(ref string, doc *godog.DocString) error {
	if !pipeline.StageAvailable(pipeline.StageSummary) {
		return fmt.Errorf("summary 還沒有實作（M4）：%w", godog.ErrPending)
	}
	if !json.Valid([]byte(doc.Content)) {
		return fmt.Errorf("%s 的摘要不是合法的 JSON", ref)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	return s.server.SeedSummary(s.ctx, id, []byte(doc.Content))
}

// pendingDigest 是 `the digest … is:` 累積的彙整設定（接在 notifier 之後寫入，見 flushDigests）。
type pendingDigest struct {
	id   string
	body map[string]any
}

// digestIs 累積一個彙整設定：doc string 是 POST /api/digests 的 JSON（不含 id）。
func (s *Scenario) digestIs(id string, doc *godog.DocString) error {
	var body map[string]any
	if err := json.Unmarshal([]byte(doc.Content), &body); err != nil {
		return fmt.Errorf("彙整 %s 的 JSON 不正確：%w", id, err)
	}
	if _, ok := body["id"]; ok {
		return fmt.Errorf("彙整 %s：doc string 不含 id（id 是參數）", id)
	}
	body["id"] = id
	s.digests = append(s.digests, pendingDigest{id: id, body: body})
	return nil
}

// flushDigests 依宣告順序寫入累積的彙整設定（steps.md 4.18：接在 notifier 之後）；必須回 201，501 時 pending。
func (s *Scenario) flushDigests() error {
	pending := s.digests
	s.digests = nil
	for _, d := range pending {
		if _, err := s.configWrite(http.MethodPost, "/api/digests", d.body, http.StatusCreated, "建立彙整 "+d.id); err != nil {
			return err
		}
	}
	return nil
}
