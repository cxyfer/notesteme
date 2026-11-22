package steps

import "github.com/cucumber/godog"

// registerFrames 註冊「關鍵畫面」的步驟（steps.md 4.14，M3）。
//
// M3 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M3 的實作切片換成真正的實作（假服務見 features/internal/fakeframes）。
func registerFrames(r *Registry, s *Scenario) {
	r.Step(`the video of {string} has the scenes:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the best video format of {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the frames of {string} are:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the tool result has an image of type {string}`, func(string) error { return godog.ErrPending })
}
