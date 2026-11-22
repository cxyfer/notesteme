package steps

import "github.com/cucumber/godog"

// registerSignin 註冊「進階登入與工作階段」的步驟（steps.md 4.21，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerSignin(r *Registry, s *Scenario) {
	r.Step(`the reverse proxy at {string} signs users in with the header {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the reverse proxy allows the users {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the reverse proxy at {string} forwards {string} with the header {string} set to {string}`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the reverse proxy at {string} forwards {string} with the header {string} set to {string} and the CSRF token`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the reverse proxy at {string} forwards {string} with the token {string} and the header {string} set to {string}`, func(string, string, string, string, string) error { return godog.ErrPending })
	r.Step(`the auth log contains {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC provider {string} signs in the subject {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the OIDC provider allows the subjects {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC provider issues ID tokens with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC provider denies the sign-in with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC sign-in starts`, func() error { return godog.ErrPending })
	r.Step(`the OIDC sign-in starts with the next page {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC sign-in is completed`, func() error { return godog.ErrPending })
	r.Step(`the OIDC sign-in is completed with the state {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC callback is replayed`, func() error { return godog.ErrPending })
	r.Step(`the OIDC authorization request has the parameter {string} set to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the OIDC sign-in redirects to {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC callback set no session cookie`, func() error { return godog.ErrPending })
	r.Step(`the OIDC token request used PKCE with {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the OIDC session sends {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the session {string} is signed in from {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the session {string} sends {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the session {string} is revoked by the owner`, func(string) error { return godog.ErrPending })
	r.Step(`the session list does not contain the secret of the session {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the token {string} is rotated as {string}`, func(string, string) error { return godog.ErrPending })
}
