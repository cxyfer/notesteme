package steps

import "github.com/cucumber/godog"

// registerEnv 註冊「環境與時間」的步驟，句型見 docs/specs/steps.md。
func registerEnv(r *Registry) {
	r.Step(`the clock is at {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the clock advances by {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the config file contains:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the environment variable {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the environment variable {string} is not set`, func(string) error { return godog.ErrPending })
	r.Step(`the server is running`, func() error { return godog.ErrPending })
	r.Step(`the server is not running`, func() error { return godog.ErrPending })
	r.Step(`the provider {string} has no API key`, func(string) error { return godog.ErrPending })
	r.Step(`the server starts`, func() error { return godog.ErrPending })
	r.Step(`the server restarts`, func() error { return godog.ErrPending })
	r.Step(`the server shuts down gracefully`, func() error { return godog.ErrPending })
	r.Step(`the server fails to start with {string}`, func(string) error { return godog.ErrPending })
}
