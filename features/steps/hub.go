package steps

import "github.com/cucumber/godog"

// registerHub 註冊「Hub 實例與帳號」的步驟（steps.md 4.30，M5）。
//
// M5 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M5 的實作切片換成真正的實作（測試替身見 features/internal/fakeoauth，ADR-0030 第 3 點）。
func registerHub(r *Registry, s *Scenario) {
	r.Step(`a Hub is running`, func() error { return godog.ErrPending })
	r.Step(`the Hub config contains:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the OAuth provider {string} has the user {string} with:`, func(string, string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the OAuth provider {string} denies the next sign-in`, func(string) error { return godog.ErrPending })
	r.Step(`{string} starts signing in to the Hub with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the OAuth provider {string} completes the pending sign-in`, func(string) error { return godog.ErrPending })
	r.Step(`the pending sign-in returns with a different state`, func() error { return godog.ErrPending })
	r.Step(`the last sign-in callback is replayed`, func() error { return godog.ErrPending })
	r.Step(`{string} signs in to the Hub with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} signed in with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} links the {string} user {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the OAuth provider {string} received a valid PKCE verifier`, func(string) error { return godog.ErrPending })
	r.Step(`the number of Hub accounts is {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the response does not contain {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the hub member {string} sends {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} sends {string} with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the hub member {string} sends {string} without a CSRF token`, func(string, string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`a Hub token {string} for {string} with the permissions {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string} and:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the Hub client sends {string} with the Hub token {string} from the origin {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} is in the group {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the hub member {string} is suspended`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub MCP client with the Hub token {string} lists the tools`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub MCP client with the Hub token {string} calls {string} with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
}
