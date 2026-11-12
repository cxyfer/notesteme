package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/worker"
)

// 環境變數名稱（設定檔相關的在 config 套件）。
const (
	envMasterKey = "NOTESTEME_MASTER_KEY"
	envPassword  = "NOTESTEME_PASSWORD"
)

// ErrNoMasterKey 表示沒有設定 master key（SEC-R5）。
var ErrNoMasterKey = errors.New("尚未設定 NOTESTEME_MASTER_KEY。")

const (
	dataDirPerm = 0o700
	// readHeaderTimeout 防止 slowloris；其他逾時由各 handler 決定（SSE 需要長連線）。
	readHeaderTimeout = 10 * time.Second
	// readTimeout 是讀完整個請求（含 body）的上限。
	readTimeout = 2 * time.Minute
	// idleTimeout 是 keep-alive 連線閒置多久後關閉。
	idleTimeout = 2 * time.Minute
	// 刻意不設 WriteTimeout：下載字幕與 SSE 是串流回應，固定的寫入期限會截斷慢速 client 的正常下載。
)

// Server 是執行中的 serve。
type Server struct {
	cfg   *config.Config
	log   *slog.Logger
	netw  netguard.Deps
	ln    net.Listener
	http  *http.Server
	store *store.Store
	keys  *secrets.Keyring
	lock  *store.DirLock

	pipeline *pipeline.Pipeline
	queue    *queue.Queue
	worker   *worker.Worker
	// workerDone 在 production 的 worker 迴圈結束時關閉；手動模式（測試）沒有迴圈，為 nil。
	workerDone chan struct{}
	// grace 是正常關閉時等待執行中任務的寬限期（PIPE-R12）。
	grace time.Duration
	// events 是 SSE 的事件匯流排與狀態（PIPE-R14），見 events.go。
	events eventsState

	// root 是 server 的根 context；背景工作（worker、之後的 scheduler）都掛在它底下，cancel 會取消它。
	root   context.Context
	cancel context.CancelFunc

	done     chan struct{} // Serve 結束時關閉
	serveErr error         // Serve 結束的原因（正常關閉時為 nil）

	closeOnce sync.Once
	closeErr  error
}

// StartServer 依 env 啟動 serve：載入設定、檢查啟動條件、鎖定資料目錄、
// 開啟並 migrate DB、建立 listener 後開始服務。回傳時 listener 已經在接受連線。
//
// 檢查順序（錯誤訊息與場景逐字相同）：
//  1. master key 是否設定（SEC-R5）：最先檢查，還沒碰資料目錄就失敗，不留下任何檔案。
//  2. 資料目錄的 flock，開啟 DB。
//  3. master key 是否正確（SEC-R5）：要解開 DB 裡的資料金鑰，所以在開啟 DB 之後。
//  4. 登入密碼（AUTH-R3）：要看 DB 裡有沒有密碼，所以在開啟 DB 之後。
//
// 兩個檢查同時不滿足時回報 master key；目前沒有場景同時缺兩者。
func StartServer(ctx context.Context, env Env) (*Server, error) {
	if env.Clock == nil {
		return nil, errors.New("app: Env.Clock 不可為 nil")
	}
	if env.Listen == nil {
		return nil, errors.New("app: Env.Listen 不可為 nil")
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
	if env.Vars[envMasterKey] == "" {
		return nil, ErrNoMasterKey
	}

	if err := os.MkdirAll(cfg.Data, dataDirPerm); err != nil {
		return nil, fmt.Errorf("建立資料目錄 %s 失敗：%w", cfg.Data, err)
	}
	// MkdirAll 受 umask 影響，也不會改已存在目錄的權限；NFR-SEC-5 要求一律 0700。
	if err := os.Chmod(cfg.Data, dataDirPerm); err != nil {
		return nil, fmt.Errorf("設定資料目錄 %s 的權限失敗：%w", cfg.Data, err)
	}
	lock, err := store.LockDir(cfg.Data)
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, log: log, netw: env.Net, lock: lock, done: make(chan struct{}), grace: shutdownGrace}
	if env.Test != nil && env.Test.SkipShutdownGrace {
		s.grace = 0
	}
	started := false
	defer func() {
		if !started {
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

	s.store, err = store.Open(ctx, cfg.Data, env.Clock)
	if err != nil {
		return nil, err
	}

	hashParams, kdf := auth.ProductionParams, secrets.ProductionKDF
	if env.Test != nil && env.Test.LowCostHashing {
		hashParams, kdf = auth.TestParams, secrets.TestKDF
	}
	// 第一次啟動建立資料金鑰；之後以 master key 解開，錯誤時拒絕啟動（SEC-R5）。
	if s.keys, err = secrets.Open(ctx, s.store, env.Vars[envMasterKey], kdf); err != nil {
		return nil, err
	}

	authSvc := auth.New(auth.Config{Store: s.store, Clock: env.Clock, Params: hashParams, Log: log,
		TrustedProxies: cfg.TrustedProxies})
	if err := authSvc.EnsurePassword(ctx, env.Vars[envPassword]); err != nil {
		return nil, err
	}
	if n, err := s.store.DeleteExpiredSessions(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		log.InfoContext(ctx, "已清除過期的 session", "count", n)
	}

	// root 是 server 的根 context：worker 與它啟動的子程序都掛在它底下，關閉時一起取消。
	s.root, s.cancel = context.WithCancel(ctx)
	s.startEvents()
	if err := s.startPipeline(ctx, env, cfg, log); err != nil {
		return nil, err
	}

	s.ln, err = env.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("監聽 %s 失敗：%w", cfg.Listen, err)
	}
	if err := lock.WriteInfo(store.LockInfo{Holder: "serve", PID: os.Getpid(), Listen: s.ln.Addr().String()}); err != nil {
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}

	s.http = &http.Server{
		Handler: api.New(api.Deps{
			Store: s.store, Auth: authSvc, Clock: env.Clock, Log: log, Version: version,
			ReadyChecks: toolChecks(toolexec.Resolver{Vars: env.Vars, Config: cfg.Tools}),
			Pipeline:    s.pipeline,
			Queue:       s.queue,
			Events:      s.eventsHandler(),
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	// http.Server.Shutdown 不會結束長連線的 SSE 串流，要主動通知它們結束。
	s.http.RegisterOnShutdown(s.stopEvents)
	go func() {
		defer close(s.done)
		if err := s.http.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()

	started = true
	log.InfoContext(ctx, "server started", "listen", s.ln.Addr().String(), "data", cfg.Data)
	return s, nil
}

// startPipeline 組出 pipeline 與 worker。啟動時先把別的 worker 留下的租約放回佇列（PIPE-R11）；
// production 模式在背景執行 worker，手動模式（測試）由測試呼叫 RunRound、RunUntilIdle 驅動（ADR-0015）。
func (s *Server) startPipeline(ctx context.Context, env Env, cfg *config.Config, log *slog.Logger) error {
	runner := env.Runner
	if runner == nil {
		runner = toolexec.New(env.Vars)
	}
	yt := &youtube.Client{Runner: runner, Tools: toolexec.Resolver{Vars: env.Vars, Config: cfg.Tools}}
	s.queue = queue.New(s.store, env.Clock, store.NewID())
	deps := pipeline.Deps{
		Store: s.store, Clock: env.Clock, DataDir: cfg.Data, Log: log, YouTube: yt, Wake: s.queue.Wake, Version: version,
		HTTP: outboundClient(env.Net),
	}
	if err := s.configureSources(ctx, &deps, env, cfg); err != nil {
		return err
	}
	s.pipeline = pipeline.New(deps)
	s.worker = worker.New(s.queue, s.pipeline, env.Clock, log)

	if n, err := s.queue.RecoverStale(ctx); err != nil {
		return fmt.Errorf("放回上次留下的租約失敗：%w", err)
	} else if n > 0 {
		log.InfoContext(ctx, "已放回上次執行留下的租約", "count", n)
	}
	// 上限與暫停存在資料庫，重啟後保留；worker 開始領取之前先讀入。
	if err := s.queue.Control().Load(ctx); err != nil {
		return err
	}
	if env.Test != nil && env.Test.ManualWorker {
		return nil
	}
	s.workerDone = make(chan struct{})
	go func() {
		defer close(s.workerDone)
		s.worker.Run(s.root)
	}()
	return nil
}

// RunRound 讓 worker 領取一輪任務並開始執行，不等待完成。只供手動模式（測試）使用。
// 開始執行的任務在 RunRound 回傳之後還要繼續，所以它們的 context 不能在這裡取消：
// 只在 server 關閉（root 取消）或呼叫端的 ctx 取消時才結束。
func (s *Server) RunRound(ctx context.Context) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	context.AfterFunc(s.root, cancel)
	return s.worker.RunRound(ctx)
}

// RunUntilIdle 讓 worker 執行到沒有可以執行的任務為止。只供手動模式（測試）使用。
func (s *Server) RunUntilIdle(ctx context.Context) error {
	ctx, stop := s.withRoot(ctx)
	defer stop()
	return s.worker.RunUntilIdle(ctx)
}

// WaitWorker 等到 worker 執行中的任務都結束。只供手動模式（測試）使用。
func (s *Server) WaitWorker() error { return s.worker.Wait() }

// withRoot 讓 ctx 在 server 關閉（root 取消）時也一起取消，worker 啟動的子程序才會被結束。
func (s *Server) withRoot(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.root, cancel)
	return ctx, func() { stop(); cancel() }
}

// stopWorker 取消 root，等 worker 結束（production）或執行中的任務結束（手動模式）。
func (s *Server) stopWorker() {
	s.cancel()
	if s.workerDone != nil {
		<-s.workerDone
	}
	_ = s.worker.Wait()
}

// Addr 回傳實際監聽的位址。
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Config 回傳生效的設定。
func (s *Server) Config() *config.Config { return s.cfg }

// Done 在 Serve 結束時關閉。
func (s *Server) Done() <-chan struct{} { return s.done }

// Err 回傳 Serve 非正常結束的原因；Done 關閉之前為 nil。
func (s *Server) Err() error {
	select {
	case <-s.done:
		return s.serveErr
	default:
		return nil
	}
}

// Shutdown 正常關閉：停止接受新連線並等待處理中的請求（受 ctx 限制），
// 之後關閉 DB（SQLite 會做 WAL checkpoint）並清空、釋放資料目錄的鎖。
// 可重複呼叫，只有第一次生效。
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() {
		var errs []error
		if err := s.http.Shutdown(ctx); err != nil {
			errs = append(errs, err)
			_ = s.http.Close() // 等不到就強制中斷，避免資源殘留
		}
		<-s.done
		// 先停止領取，等執行中的任務在寬限期內完成，之後取消剩下的並把租約放回佇列（PIPE-R12）。
		if err := s.worker.Drain(ctx, s.grace); err != nil {
			errs = append(errs, err)
		}
		s.stopWorker()
		errs = append(errs, s.store.Close(), s.lock.Release())
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// Kill 模擬程序中斷（ADR-0015）：不等待處理中的請求，不清空鎖檔內容，
// 但會關閉 listener、DB 與檔案，釋放 flock，讓同一程序內可以接著重新啟動。
func (s *Server) Kill() {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.http.Close()
		<-s.done
		s.stopWorker() // 不釋放租約：模擬程序被中斷
		_ = s.store.Close()
		_ = s.lock.Close()
	})
}

// abort 在啟動途中失敗時釋放已取得的資源。
func (s *Server) abort() {
	if s.cancel != nil {
		s.cancel()
		if s.workerDone != nil {
			<-s.workerDone
		}
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.store != nil {
		_ = s.store.Close()
	}
	if s.lock != nil {
		_ = s.lock.Release()
	}
}

// maxRedirects 是對外請求最多跟隨的轉址次數。
const maxRedirects = 5

// outboundClient 建立對外 HTTP 的 client：連線一律經過 netguard（ADR-0014），不使用 http.DefaultClient。
// 逾時與內容大小由各呼叫端自己限制。
func outboundClient(d netguard.Deps) *http.Client {
	return &http.Client{
		Transport: netguard.Transport(d),
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("轉址次數過多")
			}
			return nil
		},
	}
}
