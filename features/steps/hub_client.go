package steps

import "github.com/cucumber/godog"

// registerHubClient 註冊「Client 與 Hub」的步驟（steps.md 4.32，M5）。
//
// M5 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M5 的實作切片換成真正的實作（測試替身見 features/internal/fakehub，ADR-0030 第 3 點）。
func registerHubClient(r *Registry, s *Scenario) {
	r.Step(`the owner's client is connected to a Hub`, func() error { return godog.ErrPending })
	r.Step(`the client's Hub token has the permissions {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the client's Hub token is not set`, func() error { return godog.ErrPending })
	r.Step(`the Hub fails with HTTP {int}`, func(int) error { return godog.ErrPending })
	r.Step(`the Hub does not respond`, func() error { return godog.ErrPending })
	r.Step(`the Hub serves tampered content for the version {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub received {int} requests`, func(int) error { return godog.ErrPending })
	r.Step(`the Hub was asked for a lookup {int} times`, func(int) error { return godog.ErrPending })
	r.Step(`the Hub received a share of {string} with the artifacts:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the last bundle sent to the Hub has the manifest fields:`, func(*godog.Table) error { return godog.ErrPending })
	r.Step(`the last bundle sent to the Hub does not contain {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the Hub never received the video ID of {string}`, func(string) error { return godog.ErrPending })
}
