package steps

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/harness"
)

// harnessFailures 收集場景結束階段發現的 harness 違規（未攔截的對外連線、log 中的 secret 等）。
//
// 不能只靠 After hook 的回傳值：godog 會把 hook 的錯誤與步驟的錯誤合併，
// 只要其中一邊是 godog.ErrPending，非 strict 模式下整個場景就不算失敗，違規會被吞掉。
// 所以另外記在這裡，由 TestFeatures 在 suite.Run() 之後檢查。
var harnessFailures struct {
	mu   sync.Mutex
	list []string
}

// HarnessFailures 回傳目前為止收集到的 harness 違規（複本，每項已附上場景名稱）。並行安全。
func HarnessFailures() []string {
	harnessFailures.mu.Lock()
	defer harnessFailures.mu.Unlock()
	return append([]string(nil), harnessFailures.list...)
}

// recordHarnessFailure 記錄一項違規，附上場景名稱。err 為 nil 時不記錄。
func recordHarnessFailure(scenario string, err error) {
	if err == nil {
		return
	}
	harnessFailures.mu.Lock()
	defer harnessFailures.mu.Unlock()
	harnessFailures.list = append(harnessFailures.list, fmt.Sprintf("場景 %q：%v", scenario, err))
}

// installHooks 在 sc 上掛 Before 與 After hook，管理 s 的 World 與清理。
func installHooks(sc *godog.ScenarioContext, s *Scenario) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w, err := harness.New()
		if err != nil {
			return ctx, fmt.Errorf("建立場景環境：%w", err)
		}
		s.reset(w)
		return ctx, nil
	})
	// 每個步驟開始時遞增序號，讓斷言能分辨「緊接在前一個步驟送出的請求」（見 lastFromPreviousStep）。
	sc.StepContext().Before(func(ctx context.Context, _ *godog.Step) (context.Context, error) {
		s.step++
		return ctx, nil
	})
	sc.After(func(ctx context.Context, sn *godog.Scenario, err error) (context.Context, error) {
		return ctx, finishScenario(s, sn.Name, err)
	})
}

// finishScenario 是 After hook 的本體：停止 server、檢查違規、清掉場景環境。
// 違規會記進 HarnessFailures，同時作為錯誤回傳（讓已通過的場景也標示失敗）。
func finishScenario(s *Scenario, name string, stepErr error) error {
	if s.World == nil {
		return stepErr
	}
	stopErr := s.close()
	checkErr := s.World.Check()
	leakErr := errors.Join(s.checkLogs(), s.checkDataDirSecrets(), s.checkBackupSecrets()) // server 已關閉，/data 不會再變
	closeErr := s.World.Close()
	s.World = nil

	for _, e := range []error{stopErr, checkErr, leakErr, closeErr} {
		recordHarnessFailure(name, e)
	}
	// 步驟本身失敗時 godog 已經記錄那個錯誤；這裡只回報清理與檢查階段的額外問題，避免重複。
	return errors.Join(stopErr, checkErr, leakErr, closeErr)
}
