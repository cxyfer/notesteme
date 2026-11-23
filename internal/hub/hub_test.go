package hub

import (
	"context"
	"errors"
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

func testConfig(t *testing.T, extra string) *hubconfig.Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "notesteme-hub.yaml")
	if err := os.WriteFile(p, []byte("public_url: https://hub.example.org\n"+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := hubconfig.Load(hubconfig.Sources{File: p, Env: map[string]string{hubconfig.EnvData: filepath.Join(dir, "data")}})
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

// postgres 與 s3 還沒有實作：Open 明確失敗，不會默默改用 sqlite／fs。
func TestOpenUnimplementedDrivers(t *testing.T) {
	cfg := testConfig(t, "blobs: { driver: s3 }\n")
	if _, err := Open(context.Background(), Options{Config: cfg}); !errors.Is(err, hubblob.ErrNotImplemented) {
		t.Errorf("s3：err = %v", err)
	}
	cfg = testConfig(t, "")
	cfg.StoreDriver, cfg.StoreDSN = hubconfig.StorePostgres, "postgres://x"
	if _, err := Open(context.Background(), Options{Config: cfg}); !errors.Is(err, hubstore.ErrNotImplemented) {
		t.Errorf("postgres：err = %v", err)
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
