package steps

import "github.com/cucumber/godog"

// registerSubscriptions 註冊「訂閱、事後的更好來源與新增入口」的步驟（steps.md 4.17，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerSubscriptions(r *Registry, s *Scenario) {
	r.Step(`the YouTube feeds are served from the registered videos`, func() error { return godog.ErrPending })
	r.Step(`the YouTube feed of the channel {string} fails with HTTP {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the YouTube feed of the channel {string} recovers`, func(string) error { return godog.ErrPending })
	r.Step(`the YouTube feed of the channel {string} was requested {int} times`, func(string, int) error { return godog.ErrPending })
	r.Step(`the subscription {string} for {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the subscription {string} for {string} with:`, func(string, string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the subscription {string} has the entries:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the subscription {string} has no entries`, func(string) error { return godog.ErrPending })
	r.Step(`the next check of the subscription {string} is at {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the next check of the subscription {string} is between {string} and {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the number of subscriptions is {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the podcast feed {string} adds the episode {string} titled {string} with the audio {string}`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the owner imports the subscriptions from {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the owner imports a generated OPML file with {int} feeds`, func(int) error { return godog.ErrPending })
	r.Step(`the platform adds {word} captions in {string} to {string} at {string}`, func(string, string, string, string) error { return godog.ErrPending })
	r.Step(`the browser opens {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the browser opens {string} signed in as the owner`, func(string) error { return godog.ErrPending })
	r.Step(`the browser is sent to {string}`, func(string) error { return godog.ErrPending })
	r.Step(`someone signs in with the password {string} to continue to {string}`, func(string, string) error { return godog.ErrPending })
}
