package steps

import "github.com/cucumber/godog"

// registerLibrary 註冊「全文搜尋、bundle 與術語審核」的步驟（steps.md 4.20，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerLibrary(r *Registry, s *Scenario) {
	r.Step(`the search library has {string} titled {string} with the {string} transcript:`, func(string, string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the search library has {string} titled {string} with the {string} transcript in {string}:`, func(string, string, string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the search runs for {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the search runs for the query:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the search index has no rows for {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the search index has rows for {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the bundle {string} contains the item {string} titled {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the bundle {string} also has the entries:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the bundle {string} has the manifest field {string} set to:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the bundle {string} has the content of {string} changed`, func(string, string) error { return godog.ErrPending })
	r.Step(`the bundle {string} has the file {string} replaced with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the bundle {string} is the text:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the bundle instance ID is {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the bundle {string} is imported`, func(string) error { return godog.ErrPending })
	r.Step(`the bundle {string} is imported with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the bundle {string} is imported with the token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the bundle {string} passes its own checks`, func(string) error { return godog.ErrPending })
	r.Step(`the bundle {string} lists the artifacts:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the bundle {string} has the manifest values:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the bundle {string} contains no local paths`, func(string) error { return godog.ErrPending })
	r.Step(`the bundle {string} does not contain {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the term candidates {string} are pending for the channel of {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the term candidate {string} of the channel of {string} is decided with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the term candidates are decided in one batch:`, func(*godog.Table) error { return godog.ErrPending })
	r.Step(`the glossary {string} has exactly the terms {string}`, func(string, string) error { return godog.ErrPending })
}
