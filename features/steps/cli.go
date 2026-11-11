package steps

import "github.com/cucumber/godog"

// registerCLI 註冊「CLI」的步驟，句型見 docs/specs/steps.md。
func registerCLI(r *Registry, s *Scenario) {
	r.Step(`the owner runs {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the command exits with {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the command output contains {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the command output is JSON containing:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the command ran in {word} mode`, func(string) error { return godog.ErrPending })
}
