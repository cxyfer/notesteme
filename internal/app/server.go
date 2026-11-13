package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
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

// Server 是執行中的 serve。openCore 建立的核心欄位（設定、DB、pipeline、worker 等）
// 也是 standalone 使用的部分（見 standalone.go），所以 Standalone 同樣包一個 *Server。
type Server struct {
	cfg   *config.Config
	log   *slog.Logger
	netw  netguard.Deps
	ln    net.Listener
	http  *http.Server
	store *store.Store
	keys  *secrets.Keyring
	auth  *auth.Service
	lock  *store.DirLock

	// sockLn、sockHTTP 是本機 Unix socket（/data/notesteme.sock）與它的 http.Server，只有 serve 有；
	// 它的 handler 是 api.NewLocal，不驗證 cookie 與 CSRF，見 socket.go。路徑太長時略過，三者都是 nil。
	sockLn   net.Listener
	sockHTTP *http.Server
	sockDone chan struct{} // sockHTTP.Serve 結束時關閉

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

// StartServer 依 env 啟動 serve：先做 openCore（載入設定、檢查啟動條件、鎖定資料目錄、
// 開啟並 migrate DB、組 pipeline 與 worker），再建立 listener 與本機 Unix socket 後開始服務。
// 回傳時 listener 已經在接受連線。檢查順序與錯誤訊息見 openCore。
func StartServer(ctx context.Context, env Env) (*Server, error) {
	if env.Listen == nil {
		return nil, errors.New("app: Env.Listen 不可為 nil")
	}
	s, err := openCore(ctx, env, coreOpts{
		holder:       holderServe,
		explainLock:  true,
		manualWorker: env.Test != nil && env.Test.ManualWorker,
		check: func(ctx context.Context, s *Server) error {
			if err := s.auth.EnsurePassword(ctx, env.Vars[envPassword]); err != nil {
				return err
			}
			if n, err := s.store.DeleteExpiredSessions(ctx); err != nil {
				return err
			} else if n > 0 {
				s.log.InfoContext(ctx, "已清除過期的 session", "count", n)
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			s.abort()
		}
	}()

	cfg, log := s.cfg, s.log
	s.ln, err = env.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("監聽 %s 失敗：%w", cfg.Listen, err)
	}
	// 本機 Unix socket 在 TCP 之後建立。路徑太長時 sockLn 為 nil（已 log Warn）：略過 socket，鎖檔的 Socket 留空。
	if s.sockLn, err = listenSocket(env, cfg.Data, log); err != nil {
		return nil, err
	}
	info := store.LockInfo{Holder: holderServe, PID: os.Getpid(), Listen: s.ln.Addr().String()}
	if s.sockLn != nil {
		info.Socket = SocketPath(cfg.Data)
	}
	if err := s.lock.WriteInfo(info); err != nil {
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}

	deps := s.apiDeps(env)
	s.http = s.newHTTPServer(api.New(deps))
	go func() {
		defer close(s.done)
		if err := s.http.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()
	if s.sockLn != nil {
		s.sockHTTP = s.newHTTPServer(api.NewLocal(deps))
		s.sockDone = make(chan struct{})
		go func() {
			defer close(s.sockDone)
			if err := s.sockHTTP.Serve(s.sockLn); !errors.Is(err, http.ErrServerClosed) {
				log.Error("本機 socket 停止服務", "error", err)
			}
		}()
	}

	started = true
	log.InfoContext(ctx, "server started", "listen", s.ln.Addr().String(), "data", cfg.Data)
	return s, nil
}

// newHTTPServer 建立 http.Server：TCP 與 Unix socket 的逾時設定相同。
func (s *Server) newHTTPServer(h http.Handler) *http.Server {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	// http.Server.Shutdown 不會結束長連線的 SSE 串流，要主動通知它們結束。
	srv.RegisterOnShutdown(s.stopEvents)
	return srv
}

// startPipeline 組出 pipeline 與 worker。啟動時先把別的 worker 留下的租約放回佇列（PIPE-R11）；
// 預設在背景執行 worker；manual 為 true（測試的手動模式與 standalone）時由呼叫端用 RunRound、
// RunUntilIdle 驅動（ADR-0015）。
func (s *Server) startPipeline(ctx context.Context, env Env, cfg *config.Config, log *slog.Logger, manual bool) error {
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
	if env.Test != nil {
		deps.ImplVersions = env.Test.ImplVersions
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
	if manual {
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

// Shutdown 正常關閉（PIPE-R12）：
//  1. 先讓 worker 停止領取新任務；之後才關閉 HTTP，避免關閉期間又開始新任務。
//  2. 停止接受新連線並等待處理中的請求；ctx 只限制這一段。
//  3. 讓執行中的任務在寬限期內完成，之後取消剩下的並把租約放回佇列。這一段有獨立的期限
//     （寬限期加放回租約的上限），不受 ctx 與 HTTP 關閉已經花掉的時間影響。
//  4. 關閉 DB（SQLite 會做 WAL checkpoint）並清空、釋放資料目錄的鎖。
//
// 可重複呼叫，只有第一次生效。
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() {
		var errs []error
		s.worker.StopClaiming()
		for _, srv := range []*http.Server{s.http, s.sockHTTP} {
			if srv == nil {
				continue
			}
			if err := srv.Shutdown(ctx); err != nil {
				errs = append(errs, err)
				_ = srv.Close() // 等不到就強制中斷，避免資源殘留
			}
		}
		<-s.done
		if s.sockLn != nil {
			<-s.sockDone
			removeSocket(s.cfg.Data) // 正常關閉才移除 socket 檔；Kill 不移除，下次啟動時清掉
		}
		errs = append(errs, s.drainAndClose(ctx))
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// drainAndClose 是 Shutdown 的後半（serve 與 standalone 共用）：讓執行中的任務在寬限期內完成，
// 之後取消剩下的並把租約放回佇列，再關閉 DB 並清空、釋放資料目錄的鎖。
// 這一段有獨立的期限（寬限期加放回租約的上限），不受 ctx 已經花掉的時間影響。
// 呼叫之前 worker 必須已經 StopClaiming。
func (s *Server) drainAndClose(ctx context.Context) error {
	var errs []error
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.grace+worker.ReleaseTimeout)
	defer cancel()
	if err := s.worker.Drain(drainCtx, s.grace); err != nil {
		errs = append(errs, err)
	}
	s.stopWorker()
	errs = append(errs, s.store.Close(), s.lock.Release())
	return errors.Join(errs...)
}

// Kill 模擬程序中斷（ADR-0015）：不等待處理中的請求，不清空鎖檔內容，
// 但會關閉 listener、DB 與檔案，釋放 flock，讓同一程序內可以接著重新啟動。
func (s *Server) Kill() {
	s.closeOnce.Do(func() {
		s.cancel()
		keepSocket(s.sockLn) // 模擬程序中斷：socket 檔留著，下次啟動清掉
		_ = s.http.Close()
		<-s.done
		if s.sockLn != nil {
			_ = s.sockHTTP.Close()
			<-s.sockDone
		}
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
	if s.sockLn != nil {
		_ = s.sockLn.Close() // 已建立的 Unix listener 會在關閉時刪除 socket 檔
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
