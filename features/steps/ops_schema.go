package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案放 schema 與 migration 的步驟（OPS-R3、R4）；語意見 docs/specs/steps.md 第 4.12 節。
// 模擬的細節在 harness/schema.go。

// schemaTimeout 只是防止卡死的保險。
const schemaTimeout = 30 * time.Second

// opsDataDirCreatedBySchema 實作 `the data directory was created by schema version {int}`：
// 以指定版本建立 /data 的資料庫，必須在 server 啟動之前。
func (s *Scenario) opsDataDirCreatedBySchema(version int) error {
	if s.server != nil || s.started {
		return errors.New("server 已經啟動過：`the data directory was created by schema version` 必須放在啟動 server 的步驟之前")
	}
	ctx, cancel := context.WithTimeout(s.ctx, schemaTimeout)
	defer cancel()
	return s.World.CreateDBAtSchema(ctx, version)
}

// opsCurrentSchemaIs 實作 `the current schema version is {int}`：之後啟動的 server 只提供版本 1 到 N 的 migration。
func (s *Scenario) opsCurrentSchemaIs(version int) error {
	if version < 1 {
		return fmt.Errorf("schema 版本 %d 不正確", version)
	}
	if s.server != nil {
		return errors.New("server 已經在執行：`the current schema version is` 必須放在啟動 server 的步驟之前")
	}
	prev := s.World.CurrentSchema
	s.World.CurrentSchema = version
	if _, err := s.World.Migrations(); err != nil {
		s.World.CurrentSchema = prev
		return err
	}
	return nil
}

// opsMigrationFails 實作 `migration {int} fails`：第 N 支 migration 換成會失敗的那一支。
func (s *Scenario) opsMigrationFails(version int) error {
	if version < 1 {
		return fmt.Errorf("migration 版本 %d 不正確", version)
	}
	if s.server != nil {
		return errors.New("server 已經在執行：`migration N fails` 必須放在啟動 server 的步驟之前")
	}
	prev := s.World.FailMigration
	s.World.FailMigration = version
	if _, err := s.World.Migrations(); err != nil {
		s.World.FailMigration = prev
		return err
	}
	return nil
}

// opsDatabaseAtSchema 實作 `the database is at schema version {int}`：唯讀地比對版本表與 sqlite_master，
// 不啟動 server（失敗的啟動不會留下 server，隱含啟動只會再次觸發 migration）。
func (s *Scenario) opsDatabaseAtSchema(version int) error {
	ctx, cancel := context.WithTimeout(s.ctx, schemaTimeout)
	defer cancel()
	return s.World.CheckDBSchema(ctx, version)
}

// opsResponseSchemaVersion 實作 `the response field {string} is the current schema version`（OPS-R22）：
// 最近一次回應的頂層欄位是數字，等於這個場景的 server 使用的最新 schema 版本
// （內建的 migration，或 `the current schema version is` 指定的版本）。
func (s *Scenario) opsResponseSchemaVersion(field string) error {
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	var body map[string]any
	if err := json.Unmarshal(s.last.body, &body); err != nil {
		return fmt.Errorf("回應不是 JSON 物件：%w", err)
	}
	got, ok := body[field].(float64)
	if !ok {
		return fmt.Errorf("回應的 %s 不是數字：%v", field, body[field])
	}
	migs, err := s.World.Migrations()
	if err != nil {
		return err
	}
	want, err := store.LatestSchemaVersion(migs)
	if err != nil {
		return err
	}
	if int64(got) != want {
		return fmt.Errorf("回應的 %s 是 %v，預期目前的 schema 版本 %d", field, got, want)
	}
	return nil
}
