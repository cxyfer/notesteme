package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
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
