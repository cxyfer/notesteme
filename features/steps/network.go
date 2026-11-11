package steps

import "github.com/cucumber/godog"

// registerNetwork 註冊「網路」的步驟，句型見 docs/specs/steps.md。
func registerNetwork(r *Registry, s *Scenario) {
	r.Step(`the host {string} resolves to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the host {string} resolves to {string} and then {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the URL {string} redirects to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the URL {string} serves {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the URL {string} serves {int} bytes`, func(string, int) error { return godog.ErrPending })
	r.Step(`no connection was made to {string}`, func(string) error { return godog.ErrPending })
}
