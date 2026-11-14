package steps

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// 步驟的前置條件：這些 Given 只累積設定，server 已啟動就失敗，不默默忽略。
func TestSchemaStepsRequireNoServer(t *testing.T) {
	s := newTestScenario(t)
	if err := s.opsDataDirCreatedBySchema(3); err != nil {
		t.Fatal(err)
	}
	if err := s.opsCurrentSchemaIs(4); err != nil {
		t.Fatal(err)
	}
	if err := s.opsMigrationFails(4); err != nil {
		t.Fatal(err)
	}
	if err := s.opsMigrationFails(9); err == nil || s.World.FailMigration != 4 {
		t.Errorf("migration 9 超過現行版本 4 應該失敗且不改變設定：%v（fail=%d）", err, s.World.FailMigration)
	}
	if err := s.opsCurrentSchemaIs(0); err == nil {
		t.Error("schema 版本 0 不正確")
	}
	if err := s.opsDatabaseAtSchema(3); err != nil {
		t.Errorf("剛建立的資料庫是版本 3：%v", err)
	}
	if err := s.opsDatabaseAtSchema(4); err == nil {
		t.Error("版本不符應該失敗")
	}

	// 啟動過之後不能再改：必須放在啟動 server 的步驟之前。
	s.started = true
	if err := s.opsDataDirCreatedBySchema(1); err == nil || !strings.Contains(err.Error(), "啟動") {
		t.Errorf("啟動之後建立資料庫應該失敗：%v", err)
	}
	s.started = false
	s.server = &app.Server{} // 只需要「有 server 在執行」的狀態
	if s.opsCurrentSchemaIs(5) == nil || s.opsMigrationFails(2) == nil || s.opsDataDirCreatedBySchema(1) == nil {
		t.Error("server 執行中應該拒絕")
	}
	s.server = nil
}

// server 已停止時，library 的斷言改由唯讀的資料庫查詢，不隱含啟動 server。
func TestLibraryReadsDBWhenStopped(t *testing.T) {
	s := newTestScenario(t)
	if err := s.World.CreateDBAtSchema(t.Context(), 5); err != nil {
		t.Fatal(err)
	}
	s.stopped = true
	if err := s.libraryContainsCount(0); err != nil {
		t.Fatal(err)
	}
	if err := s.libraryContainsCount(1); err == nil {
		t.Error("沒有影音時預期 1 應該失敗")
	}
	if err := s.libraryContains("yt:abc123xxxxx"); err == nil {
		t.Error("沒有這部影音應該失敗")
	}
	if s.server != nil || s.started {
		t.Error("不該隱含啟動 server")
	}
}
