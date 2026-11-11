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
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
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

	// root 是 server 的根 context；背景工作（之後的 worker、scheduler）都掛在它底下，cancel 會取消它。
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
	lock, err := store.LockDir(cfg.Data)
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, log: log, netw: env.Net, lock: lock, done: make(chan struct{})}
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

	authSvc := auth.New(auth.Config{Store: s.store, Clock: env.Clock, Params: hashParams})
	if err := authSvc.EnsurePassword(ctx, env.Vars[envPassword]); err != nil {
		return nil, err
	}
	if n, err := s.store.DeleteExpiredSessions(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		log.InfoContext(ctx, "已清除過期的 session", "count", n)
	}

	s.ln, err = env.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("監聽 %s 失敗：%w", cfg.Listen, err)
	}
	if err := lock.WriteInfo(store.LockInfo{Holder: "serve", PID: os.Getpid(), Listen: s.ln.Addr().String()}); err != nil {
		return nil, fmt.Errorf("寫入鎖檔失敗：%w", err)
	}

	s.root, s.cancel = context.WithCancel(ctx)
	s.http = &http.Server{
		Handler: api.New(api.Deps{
			Store: s.store, Auth: authSvc, Clock: env.Clock, Log: log, Version: version,
			ReadyChecks: toolChecks(env.Vars),
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
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
		s.cancel()
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
		_ = s.store.Close()
		_ = s.lock.Close()
	})
}

// abort 在啟動途中失敗時釋放已取得的資源。
func (s *Server) abort() {
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
