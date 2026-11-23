// Package hub 組合 Hub 的各個部分（ADR-0030 第 1 點）：設定（hubconfig）、儲存（hubstore、hubblob）與 HTTP 層（hubapi）。
// cmd/notesteme-hub 的 serve 與驗收 harness（程序內的真 Hub）都經由 Open 建立 Server。
//
// Hub 不 import internal/pipeline、queue、worker、api、app：Hub 不處理影音、不執行外部工具，
// 唯一的對外連線是 OAuth 供應商（經 netguard，HREP-R12）。hub_test.go 的 TestImportBoundary 守住這條界線。
package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubapi"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubblob"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// version 由 -ldflags "-X github.com/cxyfer/notesteme/internal/hub.version=…" 設定（goreleaser、Dockerfile）。
var version = "dev"

// Version 回傳 notesteme-hub 的版本。
func Version() string { return version }

// BlobsDirName 是資料目錄下 fs blob 的目錄（ADR-0030 第 2 點）。
const BlobsDirName = "blobs"

// Options 是 Open 的輸入。
type Options struct {
	Config *hubconfig.Config
	Clock  clock.Clock
	Log    *slog.Logger
	// Net 是對外連線（OAuth 供應商）的 DNS 與 dialer；零值時用 netguard.SystemDeps()。
	// 驗收 harness 傳入 fakenet，Hub 的 OAuth endpoint 寫死在程式中，所以要靠它把正式的主機名稱導到假供應商。
	Net *netguard.Deps
	// Version 覆寫回報的版本（測試用）；空字串時用 Version()。
	Version string
}

// Server 是一個 Hub 實例：開著的儲存與 HTTP handler。
type Server struct {
	cfg     *hubconfig.Config
	store   hubstore.Store
	blobs   hubblob.Store
	handler http.Handler
	// HTTPClient 是 Hub 對外連線用的 client（經 netguard）；OAuth（hubauth）從這裡取用。
	HTTPClient *http.Client
}

// Open 依設定開啟儲存並建立 handler。資料目錄不存在時以 0700 建立。
func Open(ctx context.Context, o Options) (*Server, error) {
	if o.Config == nil {
		return nil, errors.New("hub.Open：沒有設定")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Version == "" {
		o.Version = Version()
	}
	netDeps := netguard.SystemDeps()
	if o.Net != nil {
		netDeps = *o.Net
	}
	cfg := o.Config
	st, err := openStore(ctx, cfg, o.Clock)
	if err != nil {
		return nil, err
	}
	blobs, err := openBlobs(cfg)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, store: st, blobs: blobs, HTTPClient: netguard.NewClient(netDeps)}
	auth, err := hubauth.New(hubauth.Deps{Config: cfg, Store: st, Clock: o.Clock, Log: o.Log, HTTP: s.HTTPClient})
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	s.handler = hubapi.New(hubapi.Deps{Config: cfg, Store: st, Blobs: blobs, Clock: o.Clock, Log: o.Log, Version: o.Version, Auth: auth})
	return s, nil
}

func openStore(ctx context.Context, cfg *hubconfig.Config, clk clock.Clock) (hubstore.Store, error) {
	switch cfg.StoreDriver {
	case hubconfig.StoreSQLite:
		return sqlite.Open(ctx, cfg.Data, clk)
	case hubconfig.StorePostgres:
		return postgres.Open(ctx, cfg.StoreDSN)
	}
	return nil, fmt.Errorf("不支援的 store.driver：%q", cfg.StoreDriver)
}

func openBlobs(cfg *hubconfig.Config) (hubblob.Store, error) {
	switch cfg.BlobsDriver {
	case hubconfig.BlobsFS:
		return hubblob.NewFS(filepath.Join(cfg.Data, BlobsDirName))
	case hubconfig.BlobsS3:
		return hubblob.NewS3()
	}
	return nil, fmt.Errorf("不支援的 blobs.driver：%q", cfg.BlobsDriver)
}

// Handler 回傳 Hub 的 HTTP handler（驗收 harness 把它掛到 fakenet 的 hub.example.org）。
func (s *Server) Handler() http.Handler { return s.handler }

// Store 回傳 Hub 的儲存（驗收的唯讀查詢與 doctor 使用）。
func (s *Server) Store() hubstore.Store { return s.store }

// Blobs 回傳 Hub 的 blob 儲存。
func (s *Server) Blobs() hubblob.Store { return s.blobs }

// Config 回傳生效的設定。
func (s *Server) Config() *hubconfig.Config { return s.cfg }

// Close 關閉儲存。
func (s *Server) Close() error { return s.store.Close() }

// shutdownTimeout 是收到停止訊號後等待進行中請求的上限。
const shutdownTimeout = 10 * time.Second

// Serve 在 ln 上提供 HTTP，直到 ctx 結束；結束時等待進行中的請求（最多 shutdownTimeout）。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
