package steps

import "github.com/cucumber/godog"

// registerConfig 註冊「Prompt 與處理方案」的步驟，句型見 docs/specs/steps.md。
func registerConfig(r *Registry) {
	r.Step(`the prompt {string} version {int} is:`, func(string, int, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the default prompt for {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the profile {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the default profile is {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the global settings are:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the channel of {string} has the overrides:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the effective config of {string} for {string} contains:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
}
