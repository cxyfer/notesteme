package steps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/config"
)

// registerEnv 註冊「環境與時間」的步驟，句型見 docs/specs/steps.md。
func registerEnv(r *Registry, s *Scenario) {
	r.Step(`the clock is at {string}`, s.clockIsAt)
	r.Step(`the clock advances by {string}`, s.clockAdvances)
	r.Step(`the config file contains:`, s.configFileContains)
	r.Step(`the environment variable {string} is {string}`, s.setEnv)
	r.Step(`the environment variable {string} is not set`, s.unsetEnv)
	r.Step(`the server is running`, s.serverIsRunning)
	r.Step(`the server is not running`, s.serverIsNotRunning)
	r.Step(`the provider {string} has no API key`, s.providerHasNoAPIKey)
	r.Step(`the server starts`, s.serverStarts)
	r.Step(`the server restarts`, s.serverRestarts)
	r.Step(`the server shuts down gracefully`, s.serverShutsDown)
	r.Step(`the server fails to start with {string}`, s.serverFailsToStart)
}

// configFileContains 把內容（虛擬路徑換成實際路徑）寫成設定檔，下一次啟動 server 時生效。
// server 已在執行時設定檔不會重新載入，寫入也不會生效，所以讓步驟失敗，而不是默默忽略。
func (s *Scenario) configFileContains(doc *godog.DocString) error {
	if s.server != nil {
		return errors.New("server 已經在執行，設定檔只在啟動時讀取：`the config file contains:` 必須放在啟動 server 的步驟之前")
	}
	path := s.World.Expand(s.World.Env[config.EnvConfig])
	if path == "" {
		return errors.New("Env 沒有設定 NOTESTEME_CONFIG，無法寫入設定檔")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("建立設定檔目錄：%w", err)
	}
	if err := os.WriteFile(path, []byte(s.World.Expand(doc.Content)), 0o600); err != nil {
		return fmt.Errorf("寫入設定檔：%w", err)
	}
	return nil
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

// subtitleSourceIDs 是字幕來源 ID（steps.md 第 3 節）。
var subtitleSourceIDs = []string{
	"youtube-manual", "youtube-auto", "bilibili-cc", "bilibili-ai", "podcast-transcript",
	"local-sidecar", "local-embedded", "supadata", "groq", "whisper-1", "voxtral",
}

// providerHasNoAPIKey 移除 provider 的 API key（fake provider 預設有 key）。
//   - 平台字幕來源（youtube-*、bilibili-*）：沒有 API key，明確失敗。
//   - 其他字幕來源 ID：先確認產品已實作這個來源（source.Implemented），否則 pending（允許的種類之一）；
//     再移除環境變數與 fake origin 的 key 要求，產品若沒檢查 key 就呼叫來源，會拿到結果而讓場景失敗。
//   - LLM provider（anthropic、openai，M3 另有 gemini）：移除 NOTESTEME_<ID>_API_KEY，並取消 fakellm（或 fakegemini）的 key 要求；
//     產品沒檢查 key 就呼叫 provider 會拿到正常的回答，場景因此失敗。
//   - 其他：不認識的 provider，明確失敗。
func (s *Scenario) providerHasNoAPIKey(provider string) error {
	if fakellm.IsProvider(provider) || provider == geminiProvider {
		if s.server != nil {
			return errors.New("server 已經在執行，環境變數只在啟動時讀取：`the provider … has no API key` 必須放在啟動 server 的步驟之前")
		}
		return s.World.RemoveAPIKey(provider)
	}
	if !slices.Contains(subtitleSourceIDs, provider) {
		return fmt.Errorf("不認識的 provider %q（字幕來源 ID 見 steps.md 第 3 節，LLM provider 只有 anthropic、openai）", provider)
	}
	// 平台字幕來源沒有 API key，需要的是平台的 cookie：明確失敗，不是 pending。
	if ps, platform := platformSources[provider]; platform {
		return fmt.Errorf("來源 %q 是平台字幕，需要 %s 的 cookie（POST /api/cookies），沒有 API key 可以移除", provider, ps.login)
	}
	if err := scriptableSource(provider); err != nil {
		return err
	}
	// 環境變數只在 server 啟動時讀取；已在執行時移除 fake 的要求，會讓還握著 key 的 server 照樣通過。
	if s.server != nil {
		return errors.New("server 已經在執行，環境變數只在啟動時讀取：`the provider … has no API key` 必須放在啟動 server 的步驟之前")
	}
	return s.World.RemoveAPIKey(provider)
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
	// 中斷後，卡在 gate 的程序已被結束；解除 gate，重啟後的任務才能執行。
	if err := s.World.OpenGate(); err != nil {
		return fmt.Errorf("解除 gate：%w", err)
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
	// 場景明確讓 server 停下：之後讀取狀態的步驟直接查資料庫，不隱含啟動新的 server。
	s.stopped = true
	if err := srv.Shutdown(ctx); err != nil {
		srv.Kill()
		return fmt.Errorf("正常關閉失敗：%w", err)
	}
	// 測試中的寬限期是 0，關閉應該立即完成。Drain 沒有取消仍在執行的任務時，會一直等到 ctx 的期限才放回租約
	// （Shutdown 不回報錯誤），這裡用期限是否用完來抓出它。
	if ctx.Err() != nil {
		return fmt.Errorf("正常關閉用盡了時間上限：執行中的任務沒有被取消？（%w）", ctx.Err())
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
