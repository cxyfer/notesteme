package steps

import "github.com/cucumber/godog"

// 這個檔案放 schema 與 migration 的步驟（OPS-R3、R4）；語意見 docs/specs/steps.md 第 4.12 節。
// 骨架：目前都回 pending，由 OPS-A 實作。

// opsDataDirCreatedBySchema 實作 `the data directory was created by schema version {int}`。
func (s *Scenario) opsDataDirCreatedBySchema(version int) error { return godog.ErrPending }

// opsCurrentSchemaIs 實作 `the current schema version is {int}`。
func (s *Scenario) opsCurrentSchemaIs(version int) error { return godog.ErrPending }

// opsMigrationFails 實作 `migration {int} fails`。
func (s *Scenario) opsMigrationFails(version int) error { return godog.ErrPending }

// opsDatabaseAtSchema 實作 `the database is at schema version {int}`。
func (s *Scenario) opsDatabaseAtSchema(version int) error { return godog.ErrPending }
