package steps

import "github.com/cucumber/godog"

// registerNotify 註冊「摘要、通知與無人值守」的步驟（steps.md 4.18，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerNotify(r *Registry, s *Scenario) {
	r.Step(`the summary profile is:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the summary of {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the digest {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the notification endpoints are reachable`, func() error { return godog.ErrPending })
	r.Step(`the notifier {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the notification endpoint {string} answers HTTP {int} for the next {int} requests`, func(string, int, int) error { return godog.ErrPending })
	r.Step(`the notification endpoint {string} answers HTTP {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the notification endpoint {string} received {int} requests`, func(string, int) error { return godog.ErrPending })
	r.Step(`the last request to the notification endpoint {string} contains:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the last request to the notification endpoint {string} has the header {string} containing {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the last request to the notification endpoint {string} is signed with {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the notification endpoint {string} received a request containing {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`no request to a notification endpoint contains {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the backup directory {string} contains:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the backup directory {string} contains exactly:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the data volume is {int} percent full`, func(int) error { return godog.ErrPending })
	r.Step(`the cookie for {string} was last used successfully at {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`yt-dlp recorded {int} failures out of {int} calls in the last hour`, func(int, int) error { return godog.ErrPending })
	r.Step(`the instance is deployed with the {string} profile`, func(string) error { return godog.ErrPending })
	r.Step(`the server starts with the flag {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the compose .env file contains:`, func(*godog.Table) error { return godog.ErrPending })
}
