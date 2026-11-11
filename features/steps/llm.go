package steps

import "github.com/cucumber/godog"

// registerLLM 註冊「LLM」的步驟，句型見 docs/specs/steps.md。
func registerLLM(r *Registry) {
	r.Step(`the LLM provider {string} answers {string} with {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} answers {string} with {string} and then {string}`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} answers {string} for chunk {int} with {string}`, func(string, string, int, string) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} fails with HTTP {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} fails with HTTP {int} once`, func(string, int) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} received {int} requests`, func(string, int) error { return godog.ErrPending })
	r.Step(`the LLM provider {string} received {int} requests for {string}`, func(string, int, string) error { return godog.ErrPending })
	r.Step(`request {int} to {string} contains {string}`, func(int, string, string) error { return godog.ErrPending })
	r.Step(`request {int} to {string} does not contain {string}`, func(int, string, string) error { return godog.ErrPending })
	r.Step(`requests {int} and {int} to {string} share the same prefix before the chunk`, func(int, int, string) error { return godog.ErrPending })
	r.Step(`the {string} chunks of {string} are:`, func(string, string, *godog.Table) error { return godog.ErrPending })
}
