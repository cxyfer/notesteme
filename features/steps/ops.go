package steps

// registerOps 註冊「維運」的步驟，句型見 docs/specs/steps.md。
// handler 依主題放在三個檔案：ops_schema.go（schema、migration）、ops_tools.go（yt-dlp）、ops_events.go（事件）。
func registerOps(r *Registry, s *Scenario) {
	r.Step(`the data directory was created by schema version {int}`, s.opsDataDirCreatedBySchema)
	r.Step(`the current schema version is {int}`, s.opsCurrentSchemaIs)
	r.Step(`migration {int} fails`, s.opsMigrationFails)
	r.Step(`the database is at schema version {int}`, s.opsDatabaseAtSchema)
	r.Step(`the response field {string} is the current schema version`, s.opsResponseSchemaVersion)
	r.Step(`yt-dlp {string} is installed`, s.opsYtDlpInstalled)
	r.Step(`the yt-dlp release {string} passes the self-check`, s.opsYtDlpReleasePasses)
	r.Step(`the yt-dlp release {string} fails the self-check`, s.opsYtDlpReleaseFails)
	r.Step(`yt-dlp {string} is the active version`, s.opsYtDlpIsActive)
	r.Step(`an event from {string} exists`, s.opsEventExists)
	r.Step(`no event older than {int} days remains`, s.opsNoEventOlderThan)
	r.Step(`the number of events is {int}`, s.opsEventCountIs)
}
