package steps

import "github.com/cucumber/godog"

// registerQueue 註冊「Pipeline 與佇列」的步驟，句型見 docs/specs/steps.md。
func registerQueue(r *Registry) {
	r.Step(`the concurrency limit of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the queue is paused`, func() error { return godog.ErrPending })
	r.Step(`the resource class {string} is paused`, func(string) error { return godog.ErrPending })
	r.Step(`the owner pauses the queue`, func() error { return godog.ErrPending })
	r.Step(`the owner resumes the queue`, func() error { return godog.ErrPending })
	r.Step(`the worker starts one round`, func() error { return godog.ErrPending })
	r.Step(`{int} tasks of {string} are running`, func(int, string) error { return godog.ErrPending })
	r.Step(`1 task of {string} is running`, func(string) error { return godog.ErrPending })
	r.Step(`the implementation version of the stage {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the next task belongs to {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the task of {string} for {string} has {int} attempts`, func(string, string, int) error { return godog.ErrPending })
	r.Step(`the source {string} was called {int} times`, func(string, int) error { return godog.ErrPending })
	r.Step(`no task is running`, func() error { return godog.ErrPending })
}
