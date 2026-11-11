package steps

import "github.com/cucumber/godog"

// registerSources 註冊「平台與來源的 fixture」的步驟，句型見 docs/specs/steps.md。
func registerSources(r *Registry, s *Scenario) {
	r.Step(`a YouTube video {string} titled {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`a YouTube video {string} with:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`a Bilibili video {string} with {int} parts`, func(string, int) error { return godog.ErrPending })
	r.Step(`a podcast feed {string} with the episodes:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`a local file {string} in the inbox`, func(string) error { return godog.ErrPending })
	r.Step(`{string} has {word} captions in {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`{string} has no caption tracks`, func(string) error { return godog.ErrPending })
	r.Step(`the auto captions of {string} are not ready yet`, func(string) error { return godog.ErrPending })
	r.Step(`the source {string} returns {string} for {string}`, func(string, string, string) error { return godog.ErrPending })
	r.Step(`the source {string} has nothing for {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the source {string} requires the cookie {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the source {string} accepts files up to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the audio of {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`yt-dlp fails for {string} with {string}`, func(string, string) error { return godog.ErrPending })
}
