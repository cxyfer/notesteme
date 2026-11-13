package steps

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// 步驟 impl_version 的覆寫在 server 已啟動時也要記進 World，重啟後才會帶入新的 server。
func TestImplementationVersionSurvivesRestart(t *testing.T) {
	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.implementationVersionIs("normalize", 2); err != nil {
		t.Fatal(err)
	}
	if got := s.World.ImplVersions["normalize"]; got != 2 {
		t.Fatalf("World.ImplVersions[normalize] = %d，預期 2", got)
	}
	if err := s.serverRestarts(); err != nil {
		t.Fatal(err)
	}
	if got := s.World.AppEnv().Test.ImplVersions["normalize"]; got != 2 {
		t.Fatalf("重啟後的 AppEnv 覆寫 = %d，預期 2", got)
	}
}

// stageIs 只把「緊接在前一個步驟送出的 501 請求」當成 pending 的理由。
func TestLastFromPreviousStep(t *testing.T) {
	s := &Scenario{}
	if s.lastFromPreviousStep() {
		t.Error("還沒有請求")
	}
	s.step = 3
	s.last = &response{status: http.StatusNotImplemented, step: 3}
	s.step = 4
	if !s.lastFromPreviousStep() {
		t.Error("前一個步驟送出的請求")
	}
	s.step = 5
	if s.lastFromPreviousStep() {
		t.Error("更早的步驟送出的請求已經過期")
	}
}

// 場景讓 server 停下之後，讀取狀態的步驟直接查資料庫，不隱含啟動新的 server：
// 新 server 啟動時的 RecoverStale 會把租約放回，蓋掉關閉的結果（PIPE-R12）。
func TestStateStepsReadDatabaseAfterShutdown(t *testing.T) {
	s := newTestScenario(t)
	s.World.Video("abc123xxxxx").Title = "t"
	if err := s.World.SyncTools(); err != nil {
		t.Fatal(err)
	}
	if err := s.itemHasBeenIngested("yt:abc123xxxxx"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.startRound(); err != nil {
		t.Fatal(err)
	}
	if err := s.checkRunning("download", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.serverShutsDown(); err != nil {
		t.Fatal(err)
	}

	if err := s.stageIs("metadata", "yt:abc123xxxxx", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := s.noTaskIsRunning(); err != nil {
		t.Fatal(err)
	}
	if s.server != nil {
		t.Fatal("讀取狀態的步驟不應該啟動新的 server")
	}
	if err := s.stageIs("metadata", "yt:abc123xxxxx", "done"); err == nil || !strings.Contains(err.Error(), "讀自資料庫") {
		t.Fatalf("狀態不符應該失敗，實際：%v", err)
	}
	if s.server != nil {
		t.Fatal("讀取狀態的步驟不應該啟動新的 server")
	}

	// 重新啟動後（stopped 解除），一樣的步驟回到走 API。
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if s.stopped {
		t.Fatal("啟動後 stopped 應該解除")
	}
	if err := s.stageIs("metadata", "yt:abc123xxxxx", "pending"); err != nil {
		t.Fatal(err)
	}
}

// 資料庫沒有被領取的任務、程序卻還活著（取消沒有真的停止工作）時，「沒有任務在執行」要失敗。
func TestNoTaskIsRunningNeedsProcessesGone(t *testing.T) {
	old := processWaitLimit
	processWaitLimit = 50 * time.Millisecond
	defer func() { processWaitLimit = old }()

	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.World.ArmGate(); err != nil {
		t.Fatal(err)
	}
	if err := s.noTaskIsRunning(); err != nil {
		t.Fatalf("沒有任務也沒有程序：%v", err)
	}
	// 用測試程序自己代表一個停在 gate 前、仍存活的假工具程序。
	mark := fmt.Sprintf("%s.waiting.%d", s.World.GatePath(), os.Getpid())
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := s.noTaskIsRunning()
	if err == nil || !strings.Contains(err.Error(), "存活的程序數不符") {
		t.Fatalf("程序還活著應該失敗，實際：%v", err)
	}
}
