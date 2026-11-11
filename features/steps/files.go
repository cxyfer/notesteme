package steps

import "github.com/cucumber/godog"

// registerFiles 註冊「檔案」的步驟，句型見 docs/specs/steps.md。
func registerFiles(r *Registry) {
	r.Step(`the file {string} contains:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the file {string} exists`, func(string) error { return godog.ErrPending })
	r.Step(`the file {string} does not exist`, func(string) error { return godog.ErrPending })
	r.Step(`the file {string} contains {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the file {string} does not contain {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the data directory does not contain the created token`, func() error { return godog.ErrPending })
	r.Step(`the file {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the file {string} has the mode {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the data directory does not contain {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the data directory has no files of {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the media cache has no files of {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the logs do not contain {string}`, func(string) error { return godog.ErrPending })
}
