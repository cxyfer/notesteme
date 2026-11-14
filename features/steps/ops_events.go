package steps

import "github.com/cucumber/godog"

// 這個檔案放事件持久化與清理的步驟（OPS-R10）；語意見 docs/specs/steps.md 第 4.12 節。
// 骨架：目前都回 pending，由 OPS-C 實作。

// opsEventExists 實作 `an event from {string} exists`；at 是 RFC 3339 時間。
func (s *Scenario) opsEventExists(at string) error { return godog.ErrPending }

// opsNoEventOlderThan 實作 `no event older than {int} days remains`。
func (s *Scenario) opsNoEventOlderThan(days int) error { return godog.ErrPending }

// opsEventCountIs 實作 `the number of events is {int}`。
func (s *Scenario) opsEventCountIs(n int) error { return godog.ErrPending }
