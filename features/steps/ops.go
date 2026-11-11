package steps

import "github.com/cucumber/godog"

// registerOps 註冊「維運」的步驟，句型見 docs/specs/steps.md。
func registerOps(r *Registry) {
	r.Step(`the data directory was created by schema version {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the current schema version is {int}`, func(int) error { return godog.ErrPending })
	r.Step(`migration {int} fails`, func(int) error { return godog.ErrPending })
	r.Step(`the database is at schema version {int}`, func(int) error { return godog.ErrPending })
	r.Step(`yt-dlp {string} is installed`, func(string) error { return godog.ErrPending })
	r.Step(`the yt-dlp release {string} passes the self-check`, func(string) error { return godog.ErrPending })
	r.Step(`the yt-dlp release {string} fails the self-check`, func(string) error { return godog.ErrPending })
	r.Step(`an event from {string} exists`, func(string) error { return godog.ErrPending })
	r.Step(`no event older than {int} days remains`, func(int) error { return godog.ErrPending })
}
