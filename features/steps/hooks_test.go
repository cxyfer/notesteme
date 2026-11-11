package steps

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// runHookSuite 以 in-memory 的 feature 跑一個小的 godog suite：
// 掛上與 Initialize 相同的 hook，步驟由 extra 註冊。回傳 godog 的結束碼與新增的 harness 違規。
func runHookSuite(t *testing.T, feature string, extra func(sc *godog.ScenarioContext, s *Scenario)) (code int, added []string) {
	t.Helper()
	before := len(HarnessFailures())
	suite := godog.TestSuite{
		Name: "hooks",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &Scenario{}
			installHooks(sc, s)
			extra(sc, s)
		},
		Options: &godog.Options{
			Format:          "pretty",
			Output:          io.Discard,
			NoColors:        true,
			Strict:          false, // 與 TestFeatures 相同：pending 不算失敗
			FeatureContents: []godog.Feature{{Name: "hooks.feature", Contents: []byte(feature)}},
		},
	}
	code = suite.Run()
	return code, HarnessFailures()[before:]
}

func hookSteps(sc *godog.ScenarioContext, s *Scenario) {
	sc.Step(`^the program dials an unregistered host$`, func() error {
		_, err := s.World.Net.Dial(context.Background(), netguard.Target{Network: "tcp", Host: "evil.test", Port: 80})
		_ = err // 連線被擋下是預期的；違規由 fakenet 記錄
		return nil
	})
	sc.Step(`^the program logs the password$`, func() error {
		_, err := io.WriteString(s.World.Log, "password="+harness.TestPassword+"\n")
		return err
	})
	sc.Step(`^something not implemented yet$`, func() error { return godog.ErrPending })
	sc.Step(`^everything is fine$`, func() error { return nil })
}

// 違規發生後，後面的步驟是 pending：godog 把 hook 錯誤與 ErrPending 合併後，
// 非 strict 模式不算失敗（suite 結束碼為 0）；收集器仍要有紀錄。
func TestViolationInPendingScenarioIsRecorded(t *testing.T) {
	code, added := runHookSuite(t, `Feature: hooks
  Scenario: dial then pending
    Given the program dials an unregistered host
    Then something not implemented yet
`, hookSteps)
	if code != 0 {
		t.Fatalf("godog 結束碼 = %d；此測試的前提是 godog 吞掉 pending 場景的 hook 錯誤（結束碼 0）", code)
	}
	if len(added) != 1 || !strings.Contains(added[0], "dial then pending") || !strings.Contains(added[0], "evil.test") {
		t.Fatalf("收集器應記錄含場景名稱的違規，實際：%q", added)
	}
}

func TestSecretLeakInPendingScenarioIsRecorded(t *testing.T) {
	code, added := runHookSuite(t, `Feature: hooks
  Scenario: leak then pending
    Given the program logs the password
    Then something not implemented yet
`, hookSteps)
	if code != 0 {
		t.Fatalf("godog 結束碼 = %d，前提不成立", code)
	}
	if len(added) != 1 || !strings.Contains(added[0], "leak then pending") || !strings.Contains(added[0], "NFR-SEC-3") {
		t.Fatalf("收集器應記錄 secret 洩漏，實際：%q", added)
	}
}

// 場景通過時，hook 的錯誤仍會讓 godog 回報失敗，同時也記進收集器。
func TestViolationInPassedScenarioFailsAndIsRecorded(t *testing.T) {
	code, added := runHookSuite(t, `Feature: hooks
  Scenario: dial then pass
    Given the program dials an unregistered host
    Then everything is fine
`, hookSteps)
	if code == 0 {
		t.Fatal("通過的場景有違規時，godog 應回報失敗")
	}
	if len(added) != 1 || !strings.Contains(added[0], "dial then pass") {
		t.Fatalf("收集器應記錄違規，實際：%q", added)
	}
}

// 沒有違規的 pending 場景不留紀錄；每個場景各自記錄、名稱不混淆。
func TestCleanScenariosLeaveNoRecord(t *testing.T) {
	code, added := runHookSuite(t, `Feature: hooks
  Scenario: clean pending
    Given everything is fine
    Then something not implemented yet

  Scenario: clean pass
    Given everything is fine

  Scenario: dirty pending
    Given the program dials an unregistered host
    Then something not implemented yet
`, hookSteps)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(added) != 1 || !strings.Contains(added[0], "dirty pending") {
		t.Fatalf("只有 dirty pending 應有紀錄，實際：%q", added)
	}
}
