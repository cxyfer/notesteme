package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// failingListener 的 Accept 一律失敗，讓 http.Server.Serve 自己結束。
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, errors.New("accept 失敗") }

// server 自己失敗時，runServe 也要走 Shutdown（釋放資料目錄的鎖）再回傳 1。
func TestRunServeShutsDownWhenServeFails(t *testing.T) {
	env := testEnv(t)
	env.Listen = func(network, addr string) (net.Listener, error) {
		if network == "unix" {
			return net.Listen(network, addr)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		return failingListener{ln}, err
	}
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"serve"}, IO{Stdout: &out, Stderr: &errOut}, env)
	if code != 1 || !strings.Contains(errOut.String(), "accept 失敗") {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}

	// Shutdown 會釋放資料目錄的 flock；沒有的話這裡會得到 ErrLocked。
	lock, err := store.LockDir(env.Vars["NOTESTEME_DATA"])
	if err != nil {
		t.Fatalf("server 失敗後資料目錄的鎖應該已釋放：%v", err)
	}
	_ = lock.Close()
}
