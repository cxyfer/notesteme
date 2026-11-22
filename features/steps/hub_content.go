package steps

import "github.com/cucumber/godog"

// registerHubContent 註冊「Hub 的內容」的步驟（steps.md 4.31，M5）。
//
// M5 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M5 的實作切片換成真正的實作（測試替身見 真 Hub 與 features/internal/fakehub，ADR-0030 第 3 點）。
func registerHubContent(r *Registry, s *Scenario) {
	r.Step(`the Hub has the version {string} of {string} with:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the Hub version {string} has the cues:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`a bundle {string} of {string} with the artifacts:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the bundle {string} has the manifest fields:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the bundle {string} has a note containing:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the bundle {string} is made unsafe with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} shares the bundle {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} shares the bundle {string} with:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the Hub version of {string} for {string} is known as {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the Hub has {int} versions of {string}`, func(int, string) error { return godog.ErrPending })
	r.Step(`the Hub version {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the Hub version {string} is derived from {string} as {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the Hub stores the content {string} once`, func(string) error { return godog.ErrPending })
}
