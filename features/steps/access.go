package steps

import "github.com/cucumber/godog"

// registerAccess 註冊「存取」的步驟，句型見 docs/specs/steps.md。
func registerAccess(r *Registry) {
	r.Step(`the owner password is {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner is signed in`, func() error { return godog.ErrPending })
	r.Step(`a token {string} with scopes {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the token {string} expired`, func(string) error { return godog.ErrPending })
	r.Step(`someone signs in with the password {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner sends {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner sends {string} with:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the owner sends {string} without a CSRF token`, func(string) error { return godog.ErrPending })
	r.Step(`the client sends {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the client sends {string} with the token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the client sends {string} with the token {string} and:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the client sends {string} with the token {string} from the origin {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the owner sends {string} with the header {string} set to {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the owner is listening to events`, func() error { return godog.ErrPending })
	r.Step(`an event is received:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the response status is {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the response contains:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the response header {string} contains {string}`, func(string, string) error { return godog.ErrPending })
}
