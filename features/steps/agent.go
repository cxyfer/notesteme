package steps

import "github.com/cucumber/godog"

// registerAgent 註冊「Agent 與 MCP」的步驟，句型見 docs/specs/steps.md。
func registerAgent(r *Registry, s *Scenario) {
	r.Step(`an agent {string} using the token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the agent {string} claims up to {int} tasks of kind {string}`, func(string, int, string) error { return godog.ErrPending })
	r.Step(`the agent {string} holds {int} tasks`, func(string, int) error { return godog.ErrPending })
	r.Step(`the agent {string} submits {string} for its task {int}`, func(string, string, int) error { return godog.ErrPending })
	r.Step(`the agent {string} submits {string} for all its tasks`, func(string, string) error { return godog.ErrPending })
	r.Step(`no task is held by both {string} and {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the agent {string} extends the lease of its task {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the agent {string} releases its task {int} with the reason {string}`, func(string, int, string) error { return godog.ErrPending })
	r.Step(`the submission is accepted`, func() error { return godog.ErrPending })
	r.Step(`the submission is rejected with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the MCP client with the token {string} calls {string} with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the MCP client with the token {string} lists the tools`, func(string) error { return godog.ErrPending })
	r.Step(`the MCP client with the token {string} lists the prompts`, func(string) error { return godog.ErrPending })
	r.Step(`the tool result contains:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the tool call fails with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the listed names include {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the listed names do not include {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the description of the tool {string} contains {string}`, func(string, string) error { return godog.ErrPending })
}
