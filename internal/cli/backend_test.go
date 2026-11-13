package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/store"
)

// dialFunc 是唯一的撥號實作：沒有設定 Env.Dial 時用 net.Dialer；有設定時原樣轉給它。
func TestDialFunc(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := dialFunc(app.Env{}, 0)(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	boom := errors.New("boom")
	var gotNet, gotAddr string
	env := app.Env{Dial: func(_ context.Context, network, addr string) (net.Conn, error) {
		gotNet, gotAddr = network, addr
		return nil, boom
	}}
	if _, err := dialFunc(env, 0)(t.Context(), "tcp", "example.test:80"); !errors.Is(err, boom) || gotNet != "tcp" || gotAddr != "example.test:80" {
		t.Errorf("err = %v, network = %q, addr = %q", err, gotNet, gotAddr)
	}
}

// withoutKey 回傳沒有 master key 的環境複本。
func withoutKey(env app.Env) app.Env {
	vars := map[string]string{}
	for k, v := range env.Vars {
		if k != "NOTESTEME_MASTER_KEY" {
			vars[k] = v
		}
	}
	env.Vars = vars
	return env
}

// client 模式不需要 master key（只有持有資料目錄的程序才需要）：serve 執行中，
// CLI 的環境變數沒有 master key，仍然走本機 socket，而且不改動資料目錄的權限。
func TestClientModeNeedsNoMasterKey(t *testing.T) {
	env, data := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	// serve 把資料目錄設成 0700；CLI 只是探測鎖，不可以再動它。
	if err := os.Chmod(data, 0o750); err != nil {
		t.Fatal(err)
	}

	res, out, errOut := run(t, withoutKey(env), "items", "ls", "--json")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(out, `"items"`) {
		t.Errorf("輸出 = %q", out)
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("client 模式不應該改動資料目錄的權限：%v %v", fi, err)
	}
}

// runAdvancing 在背景執行 CLI，並以 fake clock 一步一步推進，直到它結束；回傳結果、stderr 與推進的總時間。
// 真實時間只當防卡死上限。
func runAdvancing(t *testing.T, env app.Env, step time.Duration, args ...string) (Result, string, time.Duration) {
	t.Helper()
	clk := fakeClockOf(t, env)
	start := clk.Now()
	type out struct {
		res    Result
		errOut string
	}
	done := make(chan out, 1)
	go func() {
		res, _, e := run(t, env, args...)
		done <- out{res, e}
	}()
	for guard := time.Now().Add(10 * time.Second); ; {
		select {
		case o := <-done:
			return o.res, o.errOut, clk.Now().Sub(start)
		default:
		}
		if time.Now().After(guard) {
			t.Fatal("CLI 沒有在預期時間內結束")
		}
		if clk.PendingTimers() > 0 {
			clk.Advance(step)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
}

// waitTimer 等到 CLI 建立了重試用的計時器。
func waitTimer(t *testing.T, env app.Env) {
	t.Helper()
	clk := fakeClockOf(t, env)
	for guard := time.Now().Add(10 * time.Second); clk.PendingTimers() == 0; {
		if time.Now().After(guard) {
			t.Fatal("CLI 沒有建立重試用的計時器")
		}
		time.Sleep(time.Millisecond)
	}
}

// 持有者是另一個 CLI：重試約 2 秒（fake clock）之後回報「另一個 notesteme 指令使用中」，結束碼 1。
func TestLockedByAnotherCLIRetriesThenExits1(t *testing.T) {
	env, _ := testEnv(t)
	sa, err := app.OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	res, errOut, waited := runAdvancing(t, env, lockRetryStep, "items", "ls")
	if res.Code != 1 || res.Mode != "" || !strings.Contains(errOut, "正被另一個 notesteme 指令使用中") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
	if waited < lockRetryWindow || waited > lockRetryWindow+lockRetryStep {
		t.Errorf("重試了 %v，預期約 %v", waited, lockRetryWindow)
	}
}

// 另一個 CLI 在重試期間結束：這個 CLI 取得資料目錄，以獨立模式完成。
func TestLockedByAnotherCLISucceedsAfterItExits(t *testing.T) {
	env, _ := testEnv(t)
	sa, err := app.OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	done := make(chan Result, 1)
	go func() {
		res, _, _ := run(t, env, "items", "ls", "--json")
		done <- res
	}()
	waitTimer(t, env)
	if err := sa.Close(); err != nil {
		t.Fatal(err)
	}
	clk := fakeClockOf(t, env)
	for guard := time.Now().Add(10 * time.Second); ; {
		select {
		case res := <-done:
			if res.Code != 0 || res.Mode != ModeStandalone {
				t.Errorf("結果 = %+v", res)
			}
			return
		default:
		}
		if time.Now().After(guard) {
			t.Fatal("對方結束後 CLI 沒有完成")
		}
		if clk.PendingTimers() > 0 {
			clk.Advance(lockRetryStep)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
}

// 鎖檔是空的（serve 啟動中，還沒寫入連線資訊）：重試期間用完後回報「serve 正在啟動」，結束碼 1。
func TestLockWithoutInfoRetriesThenExits1(t *testing.T) {
	env, data := testEnv(t)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := store.LockDir(data)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	res, errOut, waited := runAdvancing(t, env, lockRetryStep, "items", "ls")
	if res.Code != 1 || res.Mode != "" || !strings.Contains(errOut, "serve 正在啟動") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
	if waited < lockRetryWindow {
		t.Errorf("只重試了 %v", waited)
	}
}

// serve 在 CLI 重試期間寫好連線資訊：CLI 改用資訊內容處理（這裡是沒有 socket：立刻回報，不再等待）。
func TestLockInfoAppearsDuringRetry(t *testing.T) {
	env, data := testEnv(t)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := store.LockDir(data)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	done := make(chan Result, 1)
	var errOut string
	go func() {
		res, _, e := run(t, env, "items", "ls")
		errOut = e
		done <- res
	}()
	waitTimer(t, env)
	if err := l.WriteInfo(store.LockInfo{Holder: "serve", PID: 1, Listen: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	clk := fakeClockOf(t, env)
	clk.Advance(lockRetryStep)
	select {
	case res := <-done:
		if res.Code != 1 || !strings.Contains(errOut, "沒有提供本機 socket") {
			t.Errorf("結果 = %+v，stderr：%s", res, errOut)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("寫入連線資訊後 CLI 沒有結束")
	}
	if got := clk.Now().Sub(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)); got != lockRetryStep {
		t.Errorf("推進了 %v，預期只等一次", got)
	}
}

// serve 完成啟動但路徑太長而沒有 socket（Listen 有值、Socket 為空）：立刻回報改用 --server，結束碼 1。
func TestServeWithoutSocketExits1(t *testing.T) {
	env, data := testEnv(t)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := store.LockDir(data)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.WriteInfo(store.LockInfo{Holder: "serve", PID: 1, Listen: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}

	res, _, errOut := run(t, env, "items", "ls") // 不推進 clock：不可以重試
	if res.Code != 1 || res.Mode != "" || !strings.Contains(errOut, "沒有提供本機 socket（資料目錄路徑太長），請改用 --server") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
}

// 鎖檔裡的 Socket 與 CLI 自己算出的路徑不同時，以 CLI 的為準。
func TestClientUsesOwnSocketPath(t *testing.T) {
	env, data := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	info, err := store.ReadLockInfo(data)
	if err != nil || info == nil {
		t.Fatalf("lock info = %+v, %v", info, err)
	}
	info.Socket = filepath.Join(data, "elsewhere", "other.sock")
	raw, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(data, store.LockFileName), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	res, out, errOut := run(t, env, "items", "ls", "--json")
	if res.Code != 0 || res.Mode != ModeClient || !strings.Contains(out, `"items"`) {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
}
