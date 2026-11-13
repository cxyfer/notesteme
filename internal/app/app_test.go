package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, IO{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errOut,
	}, Env{Vars: map[string]string{}})
	return code, out.String(), errOut.String()
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := run("--version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if want := "notesteme dev\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunNotImplemented(t *testing.T) {
	code, stdout, stderr := run("frobnicate")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "not implemented") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "not implemented")
	}
}

func TestRunNoArgs(t *testing.T) {
	code, _, stderr := run()
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stderr == "" {
		t.Error("stderr is empty, want usage")
	}
}

func TestRunServeUnknownArgument(t *testing.T) {
	if code, _, stderr := run("serve", "--bogus"); code != 2 || !strings.Contains(stderr, "--bogus") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// testEnv 回傳指向暫存資料目錄的 Env，listener 綁在 127.0.0.1:0。
func testEnv(t *testing.T) Env {
	t.Helper()
	root := shortTempDir(t)
	// /readyz 要求 ffmpeg 與 yt-dlp 可執行：放兩個空的可執行檔到 PATH。
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "yt-dlp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Env{
		Vars: map[string]string{
			"PATH":                 bin,
			"NOTESTEME_CONFIG":     filepath.Join(root, "none.yaml"),
			"NOTESTEME_DATA":       filepath.Join(root, "data"),
			"NOTESTEME_PASSWORD":   "pw",
			"NOTESTEME_MASTER_KEY": "mk",
		},
		Clock:  clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)),
		Listen: testListen,
		Test:   &TestControls{LowCostHashing: true},
	}
}

// shortTempDir 建立很短的暫存目錄：t.TempDir 含測試名稱，可能超過 Unix socket 的路徑長度上限。
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// testListen 把 TCP 綁在 127.0.0.1:0，"unix" 照原樣 listen（與 harness.World.Listen 相同）。
func testListen(network, addr string) (net.Listener, error) {
	if network == "unix" {
		return net.Listen(network, addr)
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func get(t *testing.T, addr, path string) int {
	t.Helper()
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestStartServerGuards(t *testing.T) {
	t.Run("沒有 master key", func(t *testing.T) {
		env := testEnv(t)
		delete(env.Vars, "NOTESTEME_MASTER_KEY")
		_, err := StartServer(context.Background(), env)
		if !errors.Is(err, ErrNoMasterKey) || !strings.Contains(err.Error(), "尚未設定 NOTESTEME_MASTER_KEY。") {
			t.Fatalf("err = %v", err)
		}
		if _, statErr := os.Stat(env.Vars["NOTESTEME_DATA"]); !os.IsNotExist(statErr) {
			t.Error("master key 檢查失敗時不應該建立資料目錄")
		}
	})
	t.Run("沒有密碼", func(t *testing.T) {
		env := testEnv(t)
		delete(env.Vars, "NOTESTEME_PASSWORD")
		_, err := StartServer(context.Background(), env)
		if err == nil || !strings.Contains(err.Error(), "尚未設定登入密碼。請設定 NOTESTEME_PASSWORD 後重新啟動。") {
			t.Fatalf("err = %v", err)
		}
		// 失敗後要釋放鎖，讓之後可以重新啟動。
		env.Vars["NOTESTEME_PASSWORD"] = "pw"
		srv, err := StartServer(context.Background(), env)
		if err != nil {
			t.Fatalf("retry after failed start: %v", err)
		}
		srv.Kill()
	})
	t.Run("master key 不正確", func(t *testing.T) {
		env := testEnv(t)
		srv, err := StartServer(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		env.Vars["NOTESTEME_MASTER_KEY"] = "other"
		_, err = StartServer(context.Background(), env)
		if err == nil || !strings.Contains(err.Error(), "master key 不正確，無法解開資料金鑰。") ||
			!strings.Contains(err.Error(), "notesteme secrets reset") {
			t.Fatalf("err = %v", err)
		}
		// 失敗後釋放鎖，用正確的 key 可以再啟動。
		env.Vars["NOTESTEME_MASTER_KEY"] = "mk"
		srv, err = StartServer(context.Background(), env)
		if err != nil {
			t.Fatalf("retry with correct key: %v", err)
		}
		srv.Kill()
	})
	t.Run("兩者都沒有時回報 master key", func(t *testing.T) {
		env := testEnv(t)
		delete(env.Vars, "NOTESTEME_PASSWORD")
		delete(env.Vars, "NOTESTEME_MASTER_KEY")
		if _, err := StartServer(context.Background(), env); !errors.Is(err, ErrNoMasterKey) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("DB 已有密碼時不需要環境變數", func(t *testing.T) {
		env := testEnv(t)
		srv, err := StartServer(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		delete(env.Vars, "NOTESTEME_PASSWORD")
		srv, err = StartServer(context.Background(), env)
		if err != nil {
			t.Fatalf("restart without NOTESTEME_PASSWORD: %v", err)
		}
		srv.Kill()
	})
}

func TestServerLifecycle(t *testing.T) {
	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.MkdirAll(filepath.Join(data, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(data, "tmp", "stale")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := StartServer(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("啟動時應該清空 /data/tmp")
	}
	if code := get(t, srv.Addr(), "/readyz"); code != 200 {
		t.Errorf("readyz = %d", code)
	}
	info, err := store.ReadLockInfo(data)
	if err != nil || info == nil || info.Holder != "serve" || info.Listen != srv.Addr() || info.PID != os.Getpid() {
		t.Fatalf("lock info = %+v, %v", info, err)
	}
	if _, err := StartServer(context.Background(), env); !errors.Is(err, store.ErrLocked) {
		t.Errorf("second start err = %v, want ErrLocked", err)
	}

	// Kill：不清鎖檔內容，但釋放 flock，可以立刻重新啟動。
	srv.Kill()
	if info, _ := store.ReadLockInfo(data); info == nil {
		t.Error("Kill 不應該清空鎖檔內容")
	}
	srv, err = StartServer(context.Background(), env)
	if err != nil {
		t.Fatalf("restart after Kill: %v", err)
	}

	// 正常關閉：清空鎖檔內容。
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, _ := store.ReadLockInfo(data); info != nil {
		t.Errorf("鎖檔內容應該被清空，got %+v", info)
	}
	select {
	case <-srv.Done():
	default:
		t.Error("Done 應該已關閉")
	}
	// 重複呼叫不出錯。
	srv.Kill()
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

// lineWriter 把每次寫入的內容送到 channel，讓測試以訊號（而不是輪詢）等待 serve 就緒。
type lineWriter struct{ ch chan string }

func (w lineWriter) Write(p []byte) (int, error) {
	w.ch <- string(p)
	return len(p), nil
}

func TestRunServeShutsDownOnCancel(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan string, 4)
	var errOut bytes.Buffer
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- Run(ctx, []string{"serve"}, IO{Stdout: lineWriter{lines}, Stderr: &errOut}, env)
	}()

	// serve 在 listener 就緒後輸出一行 "notesteme listening on <addr>"。
	var addr string
	select {
	case line := <-lines:
		addr = strings.TrimSpace(strings.TrimPrefix(line, "notesteme listening on "))
	case code := <-codeCh:
		t.Fatalf("serve exited early: %d %s", code, errOut.String())
	}
	if code := get(t, addr, "/healthz"); code != 200 {
		t.Errorf("healthz = %d", code)
	}
	cancel()
	if code := <-codeCh; code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if info, _ := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"]); info != nil {
		t.Errorf("正常關閉後鎖檔應該是空的，got %+v", info)
	}
}

func TestRunServeStartFailure(t *testing.T) {
	env := testEnv(t)
	delete(env.Vars, "NOTESTEME_MASTER_KEY")
	var errOut bytes.Buffer
	code := Run(context.Background(), []string{"serve"}, IO{Stdout: &bytes.Buffer{}, Stderr: &errOut}, env)
	if code != 1 || !strings.Contains(errOut.String(), "尚未設定 NOTESTEME_MASTER_KEY。") {
		t.Errorf("exit = %d, stderr = %q", code, errOut.String())
	}
}
