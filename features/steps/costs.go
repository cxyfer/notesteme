package steps

import "github.com/cucumber/godog"

// registerCosts 註冊「成本、預算與 Batch API」的步驟（steps.md 4.19，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerCosts(r *Registry, s *Scenario) {
	r.Step(`the price of {string} for {string} is {string} USD per {string}`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the budget is:`, func(*godog.Table) error { return godog.ErrPending })
	r.Step(`the built-in prices were checked on {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the cost estimate of every {string} task is {string} USD`, func(string, string) error { return godog.ErrPending })
	r.Step(`the cost ledger has an entry of {string} USD for {string} at {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the cost ledger has a reservation of {string} USD for {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the LLM usage reported by {string} is {int} input and {int} output tokens`, func(string, int, int) error { return godog.ErrPending })
	r.Step(`the LLM cost reported by {string} is {string} USD`, func(string, string) error { return godog.ErrPending })
	r.Step(`the cost ledger entries of {string} are:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the cost export has a row with {string} set to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the batch API of {string} completes after {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the batch API of {string} never completes`, func(string) error { return godog.ErrPending })
	r.Step(`the batch API of {string} fails the requests for chunk {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the batch API of {string} points its results to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the batch count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the batch request count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the cancelled batch count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
}
