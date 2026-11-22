package steps

import "github.com/cucumber/godog"

// registerNotes 註冊「筆記與 Gemini」的步驟（steps.md 4.15，M3）。
//
// M3 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M3 的實作切片換成真正的實作（假服務見 features/internal/fakegemini）。
func registerNotes(r *Registry, s *Scenario) {
	r.Step(`the note of {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the note of {string} contains {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the note of {string} does not contain {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the note of {string} has the frontmatter:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`Gemini accepts YouTube videos up to {string}`, func(string) error { return godog.ErrPending })
}
