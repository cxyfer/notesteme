package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

// 鎖檔中 LockInfo.Holder 的值。
const (
	holderServe = "serve"
	holderCLI   = "cli"
)

// openCore 開啟資料目錄的擁有者共用的部分：載入設定、檢查 master key、鎖定資料目錄、
// 開啟並 migrate DB、解開資料金鑰、組出 pipeline、queue 與 worker。serve 與 standalone 都從這裡開始。
//
// 回傳的 *Server 只有「核心」欄位：沒有 listener、http.Server，也還沒寫入鎖檔的 Listen 與 Socket。
// serve 在它上面接 HTTP；standalone 只用 Handler 與 RunUntilIdle（見 standalone.go）。
// 任何一步失敗時已取得的資源都會釋放，回傳 nil。
//
// 檢查順序（錯誤訊息與場景逐字相同）：
//  1. master key 是否設定（SEC-R5）：serve 最先檢查，還沒碰資料目錄就失敗，不留下任何檔案。
//     standalone（keyAfterLock）改在取得鎖之後檢查，因為鎖被 serve 持有時 CLI 走 client 模式，不需要 master key；
//     資料目錄不存在時（不可能有 serve 持有）仍然先檢查，不建立目錄。
//  2. 資料目錄的 flock，開啟 DB。已被持有時回傳 store.ErrLocked（原樣，由呼叫端決定怎麼說明）。
//     取得鎖之後才把資料目錄設為 0700（NFR-SEC-5），探測鎖的 client 模式不會改動既有目錄的權限。
//  3. master key 是否正確（SEC-R5）：要解開 DB 裡的資料金鑰，所以在開啟 DB 之後。
//  4. opts.check（serve 的登入密碼，AUTH-R3）：要看 DB 裡有沒有密碼，所以在開啟 DB 之後，pipeline 啟動之前。
//
// 兩個檢查同時不滿足時回報 master key；目前沒有場景同時缺兩者。
func openCore(ctx context.Context, env Env, opts coreOpts) (*Server, error) {
	if env.Clock == nil {
		return nil, errors.New("app: Env.Clock 不可為 nil")
	}
	logw := env.Log
	if logw == nil {
		logw = io.Discard
	}
	log := slog.New(slog.NewJSONHandler(logw, nil))

	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return nil, err
	}

	// master key 的值不寫進 log 與 DB；之後用來解開資料金鑰（見 secrets.Open）。
	noKey := env.Vars[envMasterKey] == ""
	if noKey && !opts.keyAfterLock {
		return nil, ErrNoMasterKey
	}

	// 資料目錄不存在時才建立：不存在就不可能有 serve 持有它，所以 standalone 也可以先檢查 master key，
	// 失敗時不留下任何檔案。已存在的目錄在取得鎖之前不動它的權限（見下方）。
	if _, err := os.Stat(cfg.Data); errors.Is(err, os.ErrNotExist) {
		if noKey {
			return nil, ErrNoMasterKey
		}
		if err := os.MkdirAll(cfg.Data, dataDirPerm); err != nil {
			return nil, fmt.Errorf("建立資料目錄 %s 失敗：%w", cfg.Data, err)
		}
	}
	lock, err := store.LockDir(cfg.Data)
	if err != nil {
		if opts.explainLock {
			err = lockedError(cfg.Data, err)
		}
		return nil, err
	}
	// 只有取得鎖的程序才會改資料目錄的權限；client 模式的 CLI 探測到鎖已被持有時不會走到這裡。
	// standalone 的 master key 檢查也在取得鎖之後：資料目錄被 serve 持有時，CLI 改走 client 模式，不需要 master key。
	if noKey {
		_ = lock.Release()
		return nil, ErrNoMasterKey
	}
	// MkdirAll 受 umask 影響，也不會改已存在目錄的權限；NFR-SEC-5 要求一律 0700。
	if err := os.Chmod(cfg.Data, dataDirPerm); err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("設定資料目錄 %s 的權限失敗：%w", cfg.Data, err)
	}
	// 取得鎖之後立刻寫入持有者，讓其他程序讀到的不是上一個持有者（例如被 Kill 的 serve）留下的內容；
	// serve 之後會補上 Listen 與 Socket。
	if err := lock.WriteInfo(store.LockInfo{Holder: opts.holder, PID: os.Getpid()}); err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}

	s := &Server{cfg: cfg, log: log, netw: netDeps(env.Net, cfg), lock: lock, done: make(chan struct{}), grace: shutdownGrace}
	s.unrestrictedFiles = opts.holder == holderCLI
	if env.Test != nil && env.Test.SkipShutdownGrace {
		s.grace = 0
	}
	ok := false
	defer func() {
		if !ok {
			s.abort()
		}
	}()

	// /data/tmp 的內容在重啟後沒有意義（PIPE-R11 之後會放 pipeline 的暫存），啟動時清空。
	tmp := filepath.Join(cfg.Data, "tmp")
	if err := os.RemoveAll(tmp); err != nil {
		return nil, fmt.Errorf("清空 %s 失敗：%w", tmp, err)
	}
	if err := os.MkdirAll(tmp, dataDirPerm); err != nil {
		return nil, fmt.Errorf("建立 %s 失敗：%w", tmp, err)
	}
	if err := os.Chmod(tmp, dataDirPerm); err != nil {
		return nil, fmt.Errorf("設定 %s 的權限失敗：%w", tmp, err)
	}

	var storeOpts store.Options
	if env.Test != nil {
		storeOpts.Migrations = env.Test.Migrations
	}
	s.store, err = store.Open(ctx, cfg.Data, env.Clock, storeOpts)
	if err != nil {
		return nil, err
	}
	// 已取得資料目錄的鎖、還沒處理任何請求：清掉 crash 留下的孤兒上傳目錄。
	cleanOrphanUploads(ctx, s.store, cfg.Data, log)

	hashParams, kdf := auth.ProductionParams, secrets.ProductionKDF
	if env.Test != nil && env.Test.LowCostHashing {
		hashParams, kdf = auth.TestParams, secrets.TestKDF
	}
	// 第一次啟動建立資料金鑰；之後以 master key 解開，錯誤時拒絕啟動（SEC-R5）。
	if s.keys, err = secrets.Open(ctx, s.store, env.Vars[envMasterKey], kdf); err != nil {
		return nil, err
	}
	s.services = secrets.NewServices(s.store, s.keys)

	s.auth = auth.New(auth.Config{Store: s.store, Clock: env.Clock, Params: hashParams, Log: log,
		TrustedProxies: cfg.TrustedProxies})
	if opts.check != nil {
		if err := opts.check(ctx, s); err != nil {
			return nil, err
		}
	}

	// root 是 server 的根 context：worker 與它啟動的子程序都掛在它底下，關閉時一起取消。
	// 不繼承 ctx 的取消：runServe 收到 SIGINT／SIGTERM 時會先取消 ctx 再呼叫 Shutdown，
	// 繼承的話任務在 Drain 之前就被中止，寬限期無效（PIPE-R12）。只由 Shutdown、Kill、Close 取消。
	s.root, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.startEvents()
	if err := s.startPipeline(ctx, env, cfg, log, opts.manualWorker); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

// coreOpts 是 openCore 在 serve 與 standalone 之間不同的部分。
type coreOpts struct {
	// holder 寫進鎖檔，是 holderServe 或 holderCLI。
	holder string
	// keyAfterLock 為 true 時，沒有 master key 的錯誤等到取得資料目錄的鎖之後才回報（standalone）：
	// 鎖已被 serve 持有時，CLI 要改走 client 模式，client 模式不需要 master key。
	// serve 為 false：SEC-R5 要求先檢查 master key，還沒碰資料目錄就失敗。
	keyAfterLock bool
	// explainLock 為 true 時，資料目錄被 CLI 持有的錯誤改用說明持有者的訊息（見 lockedError）；
	// 否則原樣回傳 store.ErrLocked，讓呼叫端（standalone）自己讀 LockInfo 決定怎麼辦。
	explainLock bool
	// manualWorker 為 true 時不在背景執行 worker，任務只在 RunRound、RunUntilIdle 時執行。
	// standalone 一律如此；serve 只有測試（TestControls.ManualWorker）才會。
	manualWorker bool
	// check 在 DB 與資料金鑰都就緒、pipeline 啟動之前呼叫；回傳錯誤時整個啟動失敗。
	check func(ctx context.Context, s *Server) error
}

// fileGuard 是 pipeline 程式內讀取本機檔案（雜湊、sidecar、媒體串流）用的 Guard：
// serve 限制在 inbox 與 media_roots（與 API 的 Paths 相同），standalone 不受限制（GRD-R8）；
// 兩者都加上 <data>/uploads（上傳的檔案放在資料目錄底下）。
func (s *Server) fileGuard() *pathguard.Guard {
	g := pathguard.New(append([]string{s.cfg.Inbox}, s.cfg.MediaRoots...))
	if s.unrestrictedFiles {
		g = pathguard.Unrestricted()
	}
	return g.With(filepath.Join(s.cfg.Data, "uploads"))
}

// apiDeps 組出 API 的依賴。serve 的 TCP 與 Unix socket、standalone 的程序內 handler 共用同一份，
// 差別只在 api.New 或 api.NewLocal。
//
// Paths 預設限制在 inbox 與 media_roots 之內（GRD-R7）：serve 的 TCP 與本機 socket（client 模式）都由 server 端檢查。
// 獨立模式不受限制（GRD-R8），由 OpenStandalone 改成 pathguard.Unrestricted。
func (s *Server) apiDeps(env Env) api.Deps {
	return api.Deps{
		Paths: pathguard.New(append([]string{s.cfg.Inbox}, s.cfg.MediaRoots...)),
		// 本機影音（ING-05）：inbox 決定識別鍵，資料目錄放上傳的暫存與檔案。
		Inbox: s.cfg.Inbox, DataDir: s.cfg.Data, UploadMaxBytes: s.cfg.UploadMaxBytes,
		Store: s.store, Auth: s.auth, Clock: env.Clock, Log: s.log, Version: version,
		ReadyChecks: toolChecks(ToolResolver(env.Vars, s.cfg)),
		Pipeline:    s.pipeline,
		Prompts:     s.prompts,
		AssStyles:   s.assStyles,
		// cookie API（POST /api/cookies）：儲存後由 pipeline 恢復等待 cookie 的 blocked 步驟（PIPE-R4）。
		Cookies: s.cookies,
		Resumer: s.pipeline,
		Queue:   s.queue,
		Events:  s.eventsHandler(),
		// MCP（MCP-R5、AUTH-R11）：get_video_context 等待狀態變更；HTTP 端的 Origin 與 Host 檢查。
		EventBus:          s.events.bus,
		MCPAllowedOrigins: s.cfg.MCPAllowedOrigins,
		MCPAllowedHosts:   s.cfg.MCPAllowedHosts,
		Tasks:             s.worker,
		// OPS 切片的依賴；各自在 system.go、tools.go、backup.go、providers.go 實作，還沒實作時為 nil。
		System:    s.systemInfo(env),
		Tools:     s.toolManager(env),
		Backup:    s.backupSource(env),
		Providers: s.providerRegistry(env),
		Services:  s.services,
		Notify:    s.notify,
	}
}

// lockedByCLIError 表示資料目錄正被獨立模式的 CLI 使用。它仍然是 store.ErrLocked。
type lockedByCLIError struct{ pid int }

func (e *lockedByCLIError) Error() string {
	return fmt.Sprintf("資料目錄正被 notesteme 指令使用中（%d），請等它結束。", e.pid)
}

func (e *lockedByCLIError) Is(target error) bool { return target == store.ErrLocked }

// lockedError 依鎖檔的持有者說明 serve 為什麼不能啟動：持有者是 CLI 時說明是哪個程序，
// 其他情況（含讀不到鎖檔）維持 store.ErrLocked 的訊息。err 不是 ErrLocked 時原樣回傳。
func lockedError(dir string, err error) error {
	if !errors.Is(err, store.ErrLocked) {
		return err
	}
	if info, ierr := store.ReadLockInfo(dir); ierr == nil && info != nil && info.Holder == holderCLI {
		return &lockedByCLIError{pid: info.PID}
	}
	return err
}
