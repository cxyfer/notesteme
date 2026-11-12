package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
)

// registerConfig 註冊「Prompt 與處理方案」的步驟，句型見 docs/specs/steps.md。
func registerConfig(r *Registry, s *Scenario) {
	r.Step(`the prompt {string} version {int} is:`, func(string, int, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the default prompt for {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the profile {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the default profile is {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the global settings are:`, s.globalSettingsAre)
	r.Step(`the channel of {string} has the overrides:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the effective config of {string} for {string} contains:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
}

// globalSettingsAre 累積全域設定（YAML，頂層是區段名稱）；第一個送出請求的步驟才寫入（見 flushSettings）。
func (s *Scenario) globalSettingsAre(doc *godog.DocString) error {
	var sections map[string]any
	if err := yaml.Unmarshal([]byte(s.World.Expand(doc.Content)), &sections); err != nil {
		return fmt.Errorf("全域設定不是合法的 YAML：%w", err)
	}
	if s.givenSettings == nil {
		s.givenSettings = map[string]any{}
	}
	for k, v := range sections {
		s.givenSettings[k] = v
	}
	return nil
}

// flushSettings 對每個區段呼叫 PUT /api/settings/{section}。
// 還沒實作的區段（501）讓步驟成為 pending：這個 Given 無法成立，場景不能繼續。
func (s *Scenario) flushSettings() error {
	if len(s.givenSettings) == 0 {
		return nil
	}
	pending := s.givenSettings
	s.givenSettings = nil
	names := make([]string, 0, len(pending))
	for k := range pending {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := json.Marshal(pending[name])
		if err != nil {
			return fmt.Errorf("全域設定 %s：%w", name, err)
		}
		resp, err := s.do(http.MethodPut, "/api/settings/"+name, requestOpts{who: asOwner, body: body})
		if err != nil {
			return err
		}
		switch {
		case resp.status == http.StatusNotImplemented && isNotImplemented(resp.body):
			return godog.ErrPending
		case resp.status != http.StatusOK:
			return fmt.Errorf("設定全域設定 %s 失敗：HTTP %d %s", name, resp.status, snippet(resp.body))
		}
	}
	return nil
}
