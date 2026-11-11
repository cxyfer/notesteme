package steps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/app"
)

// registerEnv 註冊「環境與時間」的步驟，句型見 docs/specs/steps.md。
func registerEnv(r *Registry, s *Scenario) {
	r.Step(`the clock is at {string}`, s.clockIsAt)
	r.Step(`the clock advances by {string}`, s.clockAdvances)
	r.Step(`the config file contains:`, func(*godog.DocString) error { return godog.ErrPending })
	r.Step(`the environment variable {string} is {string}`, s.setEnv)
	r.Step(`the environment variable {string} is not set`, s.unsetEnv)
	r.Step(`the server is running`, s.serverIsRunning)
	r.Step(`the server is not running`, s.serverIsNotRunning)
	r.Step(`the provider {string} has no API key`, func(string) error { return godog.ErrPending })
	r.Step(`the server starts`, s.serverStarts)
	r.Step(`the server restarts`, s.serverRestarts)
	r.Step(`the server shuts down gracefully`, s.serverShutsDown)
	r.Step(`the server fails to start with {string}`, s.serverFailsToStart)
}

// setEnv 只累積設定：之後啟動的 server 才會看到（已在執行的 server 不受影響）。
func (s *Scenario) setEnv(name, value string) error {
	s.World.Env[name] = value
	return nil
}

func (s *Scenario) unsetEnv(name string) error {
	delete(s.World.Env, name)
	return nil
}

func (s *Scenario) clockIsAt(ts string) error {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339：%w", ts, err)
	}
	s.World.Clock.Set(t)
	return nil
}

func (s *Scenario) clockAdvances(d string) error {
	dur, err := parseDuration(d)
	if err != nil {
		return err
	}
	s.World.Clock.Advance(dur)
	return nil
}

// parseDuration 解析 "61s"、"25h" 這類寫法；另外接受以 "d" 結尾的天數（"2d"）。
func parseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err == nil && fmt.Sprint(n) == days {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("時間長度 %q 不正確：%w", s, err)
	}
	return d, nil
}

// serverIsRunning 以目前的設定啟動 server（已在執行時不動作）；啟動失敗讓步驟失敗。
func (s *Scenario) serverIsRunning() error {
	if s.server != nil {
		return nil
	}
	if err := s.startServer(); err != nil {
		return fmt.Errorf("server 啟動失敗：%w", err)
	}
	return nil
}

// serverIsNotRunning 確認沒有 serve 持有資料目錄。
// TODO: CLI 步驟實作後，同時確認 /data/notesteme.lock 沒有被其他持有者鎖住。
func (s *Scenario) serverIsNotRunning() error {
	if s.server != nil {
		return errors.New("server 正在執行")
	}
	return nil
}

// serverStarts 啟動 serve，等到 /readyz 回應或啟動失敗；啟動失敗不讓步驟失敗，
// 由 `Then the server fails to start with` 檢查。
// StartServer 回傳時 listener 已經在接受連線，所以 /readyz 的請求不需要等待或輪詢。
func (s *Scenario) serverStarts() error {
	if s.server != nil {
		return s.startSecondServer()
	}
	if err := s.startServer(); err != nil {
		return nil
	}
	resp, err := s.do(http.MethodGet, "/readyz", requestOpts{who: asClient})
	if err != nil {
		return fmt.Errorf("等待 /readyz：%w", err)
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("/readyz 回應 %d：%s", resp.status, snippet(resp.body))
	}
	return nil
}

// startSecondServer 在已有 server 執行時再啟動一個（同一個資料目錄），預期因 flock 被拒絕（CLI-R1）。
// 第一個 server 不受影響；拒絕的原因記在 startErr，供 `Then the server fails to start with` 檢查。
func (s *Scenario) startSecondServer() error {
	s.started = true
	srv, err := app.StartServer(s.ctx, s.World.AppEnv())
	if err != nil {
		s.startErr = err
		return nil
	}
	srv.Kill()
	return errors.New("server 已經在執行，第二個 server 卻啟動成功了")
}

// serverRestarts 模擬程序中斷（Kill，不經過正常關閉）後以目前的設定重新啟動。
func (s *Scenario) serverRestarts() error {
	if s.server != nil {
		s.server.Kill()
		s.server = nil
	}
	if err := s.startServer(); err != nil {
		return fmt.Errorf("重新啟動失敗：%w", err)
	}
	return nil
}

// serverShutsDown 正常關閉 server 並等待完成。
func (s *Scenario) serverShutsDown() error {
	if s.server == nil {
		return errors.New("server 沒有在執行")
	}
	// 只是防止卡死的保險；正常關閉在本機是立即的。
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	srv := s.server
	s.server = nil
	if err := srv.Shutdown(ctx); err != nil {
		srv.Kill()
		return fmt.Errorf("正常關閉失敗：%w", err)
	}
	return nil
}

// serverFailsToStart 確認最近一次啟動失敗，且錯誤訊息包含指定文字。
func (s *Scenario) serverFailsToStart(want string) error {
	if !s.started {
		return errors.New("這個場景還沒有啟動過 server")
	}
	if s.startErr == nil {
		return errors.New("server 啟動成功了，預期啟動失敗")
	}
	got := s.World.Unexpand(s.startErr.Error())
	if !strings.Contains(got, want) {
		return fmt.Errorf("啟動錯誤訊息不含 %q\n實際：%s", want, got)
	}
	return nil
}
