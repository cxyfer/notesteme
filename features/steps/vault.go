package steps

import "github.com/cucumber/godog"

// registerVault 註冊「Vault 與 hook」的步驟（steps.md 4.16，M3）。
//
// M3 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M3 的實作切片換成真正的實作（假服務見 features/internal/fakegit、fakehook）。
func registerVault(r *Registry, s *Scenario) {
	r.Step(`the vault remote rejects pushes as {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the vault remote fails the next push with a network error`, func() error { return godog.ErrPending })
	r.Step(`the vault remote received a commit with the files:`, func(*godog.Table) error { return godog.ErrPending })
	r.Step(`the vault remote received a single new commit`, func() error { return godog.ErrPending })
	r.Step(`the vault remote received no push`, func() error { return godog.ErrPending })
	r.Step(`every git command for the vault ran with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`no git command for the vault contained {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the hook {string} ran with the arguments:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the hook {string} did not run`, func(string) error { return godog.ErrPending })
	r.Step(`the hook {string} saw the environment variable {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hook {string} did not see the environment variable {string}`, func(string, string) error { return godog.ErrPending })
}
