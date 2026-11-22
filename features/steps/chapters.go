package steps

import "github.com/cucumber/godog"

// registerChapters 註冊「SponsorBlock 與章節」的步驟（steps.md 4.13，M3）。
//
// M3 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M3 的實作切片換成真正的實作（假服務見 features/internal/fakesponsorblock）。
func registerChapters(r *Registry, s *Scenario) {
	r.Step(`SponsorBlock has the segments for {string}:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`SponsorBlock fails for {string} with HTTP {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`SponsorBlock received {int} requests for {string}`, func(int, string) error { return godog.ErrPending })
	r.Step(`SponsorBlock never received the video ID of {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the chapters of {string} are:`, func(string, *godog.Table) error { return godog.ErrPending })
}
