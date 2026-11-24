package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/cookies"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/digest"
	"github.com/cxyfer/notesteme/internal/diskusage"
	"github.com/cxyfer/notesteme/internal/hubsettings"
	"github.com/cxyfer/notesteme/internal/inbox"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/providers"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subscription"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
	"github.com/cxyfer/notesteme/internal/webui"
	"github.com/cxyfer/notesteme/internal/worker"
	webclient "github.com/cxyfer/notesteme/web/client"
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
	cfg *config.Config
	// unrestrictedFiles 為 true（standalone）時，程式內讀取本機檔案不受 inbox 與 media_roots 限制（GRD-R8）。
	unrestrictedFiles bool
	log               *slog.Logger
	netw              netguard.Deps
	ln                net.Listener
	http              *http.Server
	store             *store.Store
	keys              *secrets.Keyring
	// services 管理加密存放的服務商 API key（GET／PUT /api/settings/services 與執行期解析）。
	services *secrets.Services
	// providerCatalog 是設定頁的 provider 目錄與測試連線（SEC-R11）；pipeline 建立時才有 LLM 與字幕來源。
	providerCatalog *providers.Catalog
	// hub 是 Client 的 Hub 設定（HSHR）；startPipeline 建立。
	hub *hubsettings.Settings
	// cookies 加密存放平台 cookie，並提供 yt-dlp 用的暫存檔（SEC-R7）；startPipeline 建立。
	cookies *cookies.Store
	auth    *auth.Service
	lock    *store.DirLock

	// sockLn、sockHTTP 是本機 Unix socket（/data/notesteme.sock）與它的 http.Server，只有 serve 有；
	// 它的 handler 是 api.NewLocal，不驗證 cookie 與 CSRF，見 socket.go。路徑太長時略過，三者都是 nil。
	sockLn   net.Listener
	sockHTTP *http.Server
	sockDone chan struct{} // sockHTTP.Serve 結束時關閉

	pipeline *pipeline.Pipeline
	// prompts 是 prompt 庫（PRM）；startPipeline 建立。
	prompts *prompts.Service
	// assStyles 是 ASS 樣式庫（OUT-R6）；startPipeline 建立。
	assStyles *assstyle.Service
	queue     *queue.Queue
	// runner 執行外部工具（yt-dlp、ffmpeg 與 yt-dlp 的安裝）；startPipeline 設定。
	runner toolexec.Runner
	// ytdlp 管理 yt-dlp 的更新與回退（OPS-R6、R7），見 tools.go。
	ytdlp  *ytdlp.Manager
	worker *worker.Worker
	// scheduler 是 M4 的排程器（ADR-0021 第 1 點）：worker 的一個 Periodic。各切片在 worker 開始之前
	// 以 scheduler.Register 登記自己的 kind；M4-S0 的註冊表是空的，不查資料庫、不改變 worker 的喚醒時間。
	scheduler *scheduler.Scheduler
	// notify 管理 notifier 與送出通知 outbox（NOTIFY）；startPipeline 建立。
	notify *notify.Service
	// subscriptions 是訂閱（SUB，M4-S2）；startPipeline 建立，並在排程器登記 subscription.check。
	subscriptions *subscription.Service
	// cost 是成本帳本、計價表與預算（COST）；startPipeline 建立。
	cost *cost.Service
	// disk 統計資料目錄的磁碟用量（總覽，LIB-R22）；startPipeline 建立，TCP 與本機 socket 共用同一份快取。
	disk *diskusage.Meter
	// digests 是訂閱彙整（DIG，M4-S6）；startPipeline 建立，並在排程器登記 digest。
	digests *digest.Service
	// workerDone 在 production 的 worker 迴圈結束時關閉；手動模式（測試）沒有迴圈，為 nil。
	workerDone chan struct{}
	// watchCancel、watchDone 是 inbox 監看迴圈（production 的 serve 才有，ING-05）；沒有時為 nil。
	watchCancel context.CancelFunc
	watchDone   chan struct{}
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
	return StartServerWith(ctx, env, ServeOptions{})
}

// StartServerWith 同 StartServer，帶 serve 的旗標（ServeOptions）。
func StartServerWith(ctx context.Context, env Env, opts ServeOptions) (*Server, error) {
	if env.Listen == nil {
		return nil, errors.New("app: Env.Listen 不可為 nil")
	}
	s, err := openCore(ctx, env, coreOpts{
		holder:       holderServe,
		explainLock:  true,
		manualWorker: env.Test != nil && env.Test.ManualWorker,
		check: func(ctx context.Context, s *Server) error {
			// 旗標的檢查在密碼之前：vps 部署拒絕 --insecure-no-auth 時，訊息要說明原因（OPS-R21）。
			if err := opts.check(s.cfg, env.Vars[envPassword]); err != nil {
				return err
			}
			if opts.InsecureNoAuth {
				// 無驗證模式（AUTH-R3）：DB 已經有密碼時拒絕，不默默停用 owner 設好的驗證。
				if has, err := s.auth.HasPassword(ctx); err != nil {
					return err
				} else if has {
					return ErrInsecureWithStoredPassword
				}
			} else if err := s.auth.EnsurePassword(ctx, env.Vars[envPassword]); err != nil {
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
	// 無驗證模式只套用在 TCP listener（api.New）；本機 socket 本來就不驗證。
	deps.InsecureNoAuth = opts.InsecureNoAuth
	// Web UI 只掛在 TCP listener（api.New 才會使用 Deps.UI）；本機 socket 只給 CLI。
	deps.UI = webui.New(webclient.FS())
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
	if opts.InsecureNoAuth {
		s.warnInsecure(env, s.ln.Addr().String())
	}
	if cfg.M4.Deployment == config.DeploymentVPS && len(cfg.TrustedProxies) == 0 {
		log.WarnContext(ctx, VPSNoTrustedProxiesWarning)
	}
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
	s.runner = runner
	// 工具的尋找順序在實際執行、/readyz、/api/system 與 doctor 之間一致（OPS-R8），見 ToolResolver。
	tools := ToolResolver(env.Vars, cfg)
	// cookie 存放在 secrets 表（資料金鑰加密）；每次呼叫 yt-dlp 時寫成 <data>/tmp 下的暫存檔（SEC-R7）。
	s.cookies = cookies.New(s.store, s.keys, cfg.Data)
	// yt-dlp 的呼叫結果與 cookie 的成功使用記進統計（OPS-R17、R18）。
	calls := s.opsCalls(env, log)
	yt := &youtube.Client{Runner: runner, Tools: tools, Cookies: s.cookies, Calls: calls}
	s.queue = queue.New(s.store, env.Clock, store.NewID())
	if env.Test != nil && env.Test.SerialLLM {
		// llm 類別一次最多執行一個任務（不論設定的上限），請求順序才有確定性；不影響 GET /api/queue 顯示的上限。
		s.queue.Control().SetCap(queue.ResourceLLM, 1)
	}
	deps := pipeline.Deps{
		Store: s.store, Clock: env.Clock, DataDir: cfg.Data, Log: log, YouTube: yt, Wake: s.queue.Wake, Version: version,
		// 垃圾桶列表的 purge_at 與定期清理用同一個保留期限（DEL-R4）。
		TrashRetention: trashRetention(cfg),
		// Bilibili 與 YouTube 共用 runner、工具解析與 cookie 存放；Probe 不帶 --no-warnings，才看得到需要登入的警告（SRC-R8）。
		Bilibili: &bilibili.Client{Runner: runner, Tools: tools, Cookies: s.cookies, Log: log, Calls: calls},
		// 對外請求一律經過 netguard（ADR-0014），不使用 http.DefaultClient；逾時與大小由各呼叫端限制。
		HTTP: netguard.NewClient(s.netw),
		// 新增 Podcast 單集時預檢音檔的位址（不連線，GRD-R2）；用同一份 netguard 依賴，allowlist 的判斷與連線時一致。
		CheckURL: func(ctx context.Context, rawURL string) error { return netguard.CheckURL(ctx, s.netw, rawURL) },
		// 本機影音（ING-05）：ffprobe／ffmpeg 與 yt-dlp 共用 runner 與工具位置。
		Media: media.New(runner, tools),
		Files: s.fileGuard(),
	}
	if env.Test != nil {
		deps.ImplVersions = env.Test.ImplVersions
	}
	if err := s.configureSources(ctx, &deps, env, cfg); err != nil {
		return err
	}
	// prompts 要問 Pipeline「誰在用這個 prompt」，Pipeline 要經由 prompts 解析 prompt 引用：互相依賴，用 closure 解開。
	var pl *pipeline.Pipeline
	s.prompts = prompts.New(prompts.Deps{
		Store: s.store, Clock: env.Clock,
		// 刪除在同一個寫入交易內檢查：傳進來的 Repo 是那個交易（P7）。
		UsedBy: func(ctx context.Context, r *store.Repo, id string) ([]string, error) {
			return pl.PromptUsersIn(ctx, r, id)
		},
	})
	s.assStyles = assstyle.New(s.store)
	// LLM provider：內建的加上設定檔 llm.providers 的；同一個 Registry 也回答處理方案驗證的「provider 存在嗎」（PRF-R4）。
	deps.LLM = s.llmRegistry(env, cfg, deps.HTTP)
	deps.Providers, deps.Prompts = deps.LLM, promptResolver{s.prompts}
	s.providerCatalog = newProviderCatalog(cfg, deps)
	// vault 與 hook 只能在設定檔定義（VAULT-R1、R10）：處理方案只能引用這些 id。
	deps.Outputs = outputCatalog{vaults: cfg.Vaults, hooks: cfg.Hooks}
	deps.Vaults, deps.Hooks = vaultDefs(cfg)
	// git 與 hook 經同一個 runner（process group、逾時、輸出上限）；環境變數由 vault 套件依規則挑選（VAULT-R8、R11）。
	deps.Runner, deps.Env = runner, env.Vars
	// SponsorBlock 經同一個 netguard client（SPB-R2）；base_url 只能寫在設定檔。
	// 沒有經過設定檔載入的 Config（測試、standalone 的預設值）沒有 base_url，用公開伺服器。
	sbBase := cfg.SponsorBlockBaseURL
	if sbBase == "" {
		sbBase = config.DefaultSponsorBlockBase
	}
	deps.SponsorBlock = &sponsorblock.Client{HTTP: deps.HTTP, BaseURL: sbBase}
	deps.SponsorBlockBaseURL = sbBase
	// Hub（HSHR）：經同一個 netguard client；hub.url 只能寫在設定檔。
	s.configureHub(env, cfg, &deps)
	// 通知（NOTIFY）：pipeline 在步驟狀態的交易中寫入 outbox，排程工作 notify.deliver 送出。
	var outbox *notify.Outbox
	s.notify, outbox = s.newNotify(env.Clock, cfg, log)
	deps.Notify = outbox
	// 成本與預算（COST）：pipeline 在送出付費請求前後記帳，預算的提醒經同一個 outbox。
	var costOpts cost.Options
	if env.Test != nil {
		costOpts = cost.Options{Estimates: env.Test.CostEstimates, PricesAsOf: env.Test.PricesAsOf}
	}
	s.cost = cost.New(env.Clock, outbox, costOpts)
	deps.Cost = s.cost
	s.disk = diskusage.New(cfg.Data, env.Clock)
	pl = pipeline.New(deps)
	s.pipeline = pl
	s.worker = worker.New(s.queue, s.pipeline, env.Clock, log)
	// 永久刪除要結束殘留的任務，所以 pipeline 需要 worker（worker 在 pipeline 之後才建立）。
	s.pipeline.SetTaskCanceller(s.worker)
	// 每輪開頭清理過期事件（OPS-R10），以及永久刪除到期的垃圾桶影音（DEL-R4）。
	hk := newHousekeeping(s.store, env.Clock, log)
	hk.addTrashCleanup(s.pipeline, trashRetention(cfg))
	// Agent 領取的上限讀 llm 類別設定的上限（AGT-R2），不受測試的 SerialLLM 影響（那只限制內建 worker）；
	// 暫停整個佇列或 llm 類別時不發出新任務（PIPE-R10）。
	ctl := s.queue.Control()
	s.pipeline.SetAgentLimit(func(class string) int {
		if ctl.Paused() || ctl.ClassPaused(class) {
			return 0
		}
		return ctl.Limit(class)
	})
	// Agent 任務的租約到期與逾時（AGT-R4、R9）：每一輪開頭檢查；production 閒置時睡到最早的到期時間。
	s.scheduler = scheduler.New(s.store, env.Clock, log, cfg.M4.JitterMax)
	if err := s.notify.Register(s.scheduler); err != nil {
		return err
	}
	bc, _ := deps.Bilibili.(*bilibili.Client)
	if err := s.startSubscriptions(env.Clock, cfg, log, deps.HTTP, runner, tools, yt, bc); err != nil {
		return err
	}
	if err := s.registerBudgetMonth(ctx); err != nil {
		return err
	}
	if err := s.registerBatchPoll(); err != nil {
		return err
	}
	// 彙整（DIG-R8～R11）：排程工作 digest 經同一個 outbox 送出。
	s.digests = digest.New(digest.Deps{Store: s.store, Clock: env.Clock, Outbox: outbox, Summaries: pl, PublicURL: cfg.M4.PublicURL, Log: log})
	if err := s.digests.Register(s.scheduler); err != nil {
		return err
	}
	// SRC-10：重新 Probe（SRC-R18～R22）。
	if err := s.registerReprobe(); err != nil {
		return err
	}
	// OPS-R11～R18：每日備份與無人值守告警（只有 serve）。
	if !s.unrestrictedFiles {
		if err := s.registerOps(ctx, env, cfg, log, outbox); err != nil {
			return err
		}
	}
	var periodic worker.Periodic = periodics{hk, s.pipeline.AgentSweeper(), s.scheduler}
	// inbox 監看（ING-05）只在 serve 啟動：standalone 的 CLI 是短命的程序，不自動掃描。
	var watcher *inbox.Watcher
	if !s.unrestrictedFiles {
		watcher = &inbox.Watcher{
			Clock: env.Clock, Inbox: cfg.Inbox, Files: deps.Files, Media: deps.Media,
			Store: s.store, Ingester: s.pipeline, Log: log,
		}
		if manual {
			// 手動模式：RunUntilIdle 的每一輪都掃描（測試用 fake clock 推進時間）。
			periodic = periodics{hk, s.pipeline.AgentSweeper(), s.scheduler, watcher}
		}
	}
	s.worker.SetPeriodic(periodic)

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
	if watcher != nil {
		// production：獨立的迴圈每 5 秒掃描一次；關閉時與 worker 一起停止（見 stopWatcher）。
		wctx, cancel := context.WithCancel(s.root)
		s.watchCancel, s.watchDone = cancel, make(chan struct{})
		go func() {
			defer close(s.watchDone)
			watcher.Loop(wctx)
		}()
	}
	return nil
}

// stopWatcher 停止 inbox 監看並等它結束；可重複呼叫，沒有監看時什麼都不做。
// 正常關閉一開始就要呼叫（與 StopClaiming 同時）：draining 時不該再新增影音。
func (s *Server) stopWatcher() {
	if s.watchCancel == nil {
		return
	}
	s.watchCancel()
	<-s.watchDone
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

// ActiveTasks 回傳 worker 執行中的任務數。只供手動模式（測試）使用。
func (s *Server) ActiveTasks() int { return s.worker.ActiveCount() }

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
	s.stopWatcher()
	if s.workerDone != nil {
		<-s.workerDone
	}
	_ = s.worker.Wait()
}

// Addr 回傳實際監聽的位址。
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Config 回傳生效的設定。
func (s *Server) Config() *config.Config { return s.cfg }

// ExpireToken 把指定名稱的有效 API token 設為在目前時鐘時間到期（測試與驗收 harness 使用）。
func (s *Server) ExpireToken(ctx context.Context, name string) error {
	if s.auth == nil {
		return errors.New("app: auth 尚未初始化")
	}
	return s.auth.ExpireTokenByName(ctx, name)
}

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
		s.stopWatcher()
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
		s.stopWatcher()
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

// netDeps 把設定檔的 allowlist 併入 env 提供的 netguard 依賴（GRD-R3）；不改動 d 本身。
func netDeps(d netguard.Deps, cfg *config.Config) netguard.Deps {
	d.AllowPrivate = netguard.Allowlist{
		Prefixes: append(slices.Clone(d.AllowPrivate.Prefixes), cfg.AllowPrivate.Prefixes...),
		Hosts:    append(slices.Clone(d.AllowPrivate.Hosts), cfg.AllowPrivate.Hosts...),
	}
	return d
}
