package app

import (
	"context"
	"net/http"
	"sync"

	"github.com/cxyfer/notesteme/internal/api"
)

// Standalone 是獨立模式（CLI-R2）：CLI 程序自己持有資料目錄，不經過 serve。
//
// 它與 serve 共用 openCore，所以載入設定、啟動檢查、migration、pipeline 與 worker 完全相同；
// 差別是沒有 listener，也不啟動 scheduler（CLI-R4，目前還沒有 scheduler）。
// CLI 一律透過 HTTP 呼叫 API：獨立模式用 Handler 加上程序內的 RoundTripper，
// 所以三種模式（獨立、本機 client、--server）只差在 transport。
type Standalone struct {
	core *Server
	h    http.Handler

	closeOnce sync.Once
	closeErr  error
}

// OpenStandalone 取得資料目錄的鎖（鎖檔寫入 Holder "cli"）並開啟核心。
//
// 鎖已被持有時回傳 store.ErrLocked（原樣，不加說明）；呼叫端用 store.ReadLockInfo 讀出持有者，
// 持有者是 serve 時改走 client 模式（Socket 是本機 Unix socket 的路徑）。
// 其他啟動檢查的錯誤與 serve 相同，例如 ErrNoMasterKey。
func OpenStandalone(ctx context.Context, env Env) (*Standalone, error) {
	core, err := openCore(ctx, env, coreOpts{holder: holderCLI, manualWorker: true})
	if err != nil {
		return nil, err
	}
	return &Standalone{core: core, h: api.NewLocal(core.apiDeps(env))}, nil
}

// Handler 回傳以 owner 身分執行的 API handler（api.NewLocal），不需要 cookie 或 CSRF。
// 只能在程序內使用，不能掛到任何 listener。
func (s *Standalone) Handler() http.Handler { return s.h }

// RunUntilIdle 領取並執行任務，直到沒有可以執行的任務為止；測試的手動 worker 與 production 都用這個入口。
// 還沒到 not_before 的任務不算可以執行。ctx 取消時，執行中的任務也會被取消。
func (s *Standalone) RunUntilIdle(ctx context.Context) error { return s.core.RunUntilIdle(ctx) }

// Close 停止領取、讓執行中的任務在寬限期內結束（超過則取消並放回租約，PIPE-R12），
// 再關閉 DB 並釋放資料目錄的鎖。可重複呼叫，只有第一次生效。
func (s *Standalone) Close() error {
	s.closeOnce.Do(func() {
		s.core.worker.StopClaiming()
		s.core.stopEvents() // 結束程序內的 SSE 串流
		s.closeErr = s.core.drainAndClose(context.Background())
	})
	return s.closeErr
}
