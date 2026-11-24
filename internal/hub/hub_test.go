package hub

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubblob"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// testMasterKey 是測試用的主金鑰（hub.Open 一律需要，HACC-R15）。
const testMasterKey = "test-master-key-0123456789abcdefghij"

func testConfig(t *testing.T, extra string) *hubconfig.Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "notesteme-hub.yaml")
	if err := os.WriteFile(p, []byte("public_url: https://hub.example.org\n"+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := hubconfig.Load(hubconfig.Sources{File: p, Env: map[string]string{hubconfig.EnvData: filepath.Join(dir, "data"), hubconfig.EnvMasterKey: testMasterKey}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestOpenSQLiteAndFS(t *testing.T) {
	cfg := testConfig(t, "")
	s, err := Open(context.Background(), Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.Store().CountUsers(context.Background()); err != nil || n != 0 {
		t.Errorf("CountUsers = %d %v", n, err)
	}
	if fi, err := os.Stat(filepath.Join(cfg.Data, BlobsDirName)); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("blob 目錄 = %v %v", fi, err)
	}
}

// s3 與 postgres 依設定開啟，不會默默改用 sqlite／fs（ADR-0030 第 2 點）。
func TestOpenOtherDrivers(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.BlobsDriver = hubconfig.BlobsS3
	cfg.S3 = hubconfig.S3Config{Endpoint: "http://127.0.0.1:1", Bucket: "b-1", AccessKeyID: "AKID", SecretAccessKey: "s3cr3t", Timeout: time.Second}
	s, err := Open(context.Background(), Options{Config: cfg})
	if err != nil {
		t.Fatalf("s3：%v", err)
	}
	if _, ok := s.Blobs().(*hubblob.S3); !ok {
		t.Errorf("s3：Blobs = %T", s.Blobs())
	}
	_ = s.Close()
	cfg = testConfig(t, "")
	cfg.StoreDriver, cfg.StoreDSN = hubconfig.StorePostgres, "postgres://hub:pw-s3cr3t@127.0.0.1:1/hub?connect_timeout=1&sslmode=disable"
	_, err = Open(context.Background(), Options{Config: cfg})
	if err == nil || errors.Is(err, hubstore.ErrNotImplemented) || strings.Contains(err.Error(), "pw-s3cr3t") {
		t.Errorf("postgres 連不上時：err = %v，預期連線錯誤且不含密碼", err)
	}
}

func TestServe(t *testing.T) {
	s, err := Open(context.Background(), Options{Config: testConfig(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	resp, err := http.Get("http://" + ln.Addr().String() + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/readyz = %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve 沒有在 ctx 結束後返回")
	}
}

// ADR-0030 第 1 點：Hub 不 import Client 的 pipeline、queue、worker，也不 import api、app（會間接帶進它們）。
func TestImportBoundary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("找不到 go 執行檔")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/cxyfer/notesteme/internal/hub/...", "github.com/cxyfer/notesteme/cmd/notesteme-hub").CombinedOutput()
	if err != nil {
		t.Fatalf("go list 失敗：%v\n%s", err, out)
	}
	forbidden := []string{"pipeline", "queue", "worker", "api", "app", "store", "toolexec", "tools", "source", "cli"}
	for _, line := range strings.Split(string(out), "\n") {
		for _, f := range forbidden {
			if line == "github.com/cxyfer/notesteme/internal/"+f || strings.HasPrefix(line, "github.com/cxyfer/notesteme/internal/"+f+"/") {
				t.Errorf("Hub 間接 import 了 %s", line)
			}
		}
	}
}

// 設定檔的 admins 在啟動時同步：從設定檔移除的管理員重新啟動後失去 admins，已經登入過的新管理員立即加入（HACC-R12、A12）。
func TestOpenSyncsConfigAdmins(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, "admins:\n  - { provider: github, subject: \"1\" }\n")
	s, err := Open(ctx, Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Store()
	old, _, _ := st.SignIn(ctx, hubstore.ExternalIdentity{Provider: "github", Subject: "1"}, "old")
	next, _, _ := st.SignIn(ctx, hubstore.ExternalIdentity{Provider: "github", Subject: "2"}, "next")
	if err := st.SetConfigGroup(ctx, old.ID, hubconfig.GroupAdmins, true); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	cfg.Admins = []hubconfig.Identity{{Provider: hubconfig.GitHub, Subject: "2"}}
	s, err = Open(ctx, Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, want := range map[string]int{old.ID: 0, next.ID: 1} {
		ms, err := s.Store().GroupMemberships(ctx, id)
		if err != nil || len(ms) != want {
			t.Errorf("%s 的成員資格 = %+v %v，預期 %d 筆", id, ms, err, want)
		}
	}
}

// 安全修補 A：HTTP server 有讀取與閒置的逾時（不設 WriteTimeout，MCP 與下載要能長時間回應）。
// ReadTimeout 只限制讀取請求：回應寫得比它久的請求（MCP 的串流、慢速下載）不會被取消（以 Go 1.26 實測鎖住）。
func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 || srv.WriteTimeout != 0 {
		t.Fatalf("server = %+v", srv)
	}
	srv.ReadHeaderTimeout, srv.ReadTimeout = 100*time.Millisecond, 100*time.Millisecond
	errc := make(chan error, 1)
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		for range 3 {
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte("x"))
		}
		errc <- r.Context().Err()
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body) // 讀到 handler 結束（提早關閉連線也會取消 context）
	_ = resp.Body.Close()
	if err := <-errc; err != nil {
		t.Fatalf("長時間的 GET 被取消：%v", err)
	}
}
