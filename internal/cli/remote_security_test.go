package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
)

// --server 收到轉址時不跟隨：bearer token 不會被送到另一個 server（即使只是不同的 port），
// 轉址回應當成錯誤，結束碼 1，訊息說明 server 回了轉址。
func TestRemoteRedirectIsNotFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			other, otherURL := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"items":[]}`)
			})
			_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, otherURL+"/api/items", status)
			})
			res, _, errOut := run(t, app.Env{}, "items", "ls", "--server", url, "--token", "secret")
			if res.Code != 1 || res.Mode != ModeClient {
				t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
			}
			if !strings.Contains(errOut, "轉址") {
				t.Errorf("stderr 應該說明 server 回了轉址：%s", errOut)
			}
			if n := len(other.requests); n != 0 {
				t.Errorf("轉址的目標收到 %d 個請求（%v），預期 0", n, other.auth)
			}
		})
	}
}

// 連線逾時 10 秒：Dial 收到的 ctx 有期限。
func TestRemoteDialHasTimeout(t *testing.T) {
	var got time.Duration
	env := app.Env{Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if d, ok := ctx.Deadline(); ok {
			got = time.Until(d)
		}
		return nil, errors.New("refused")
	}}
	res, _, _ := run(t, env, "items", "ls", "--server", "http://127.0.0.1:1", "--token", "t")
	if res.Code != 1 {
		t.Fatalf("結果 = %+v", res)
	}
	if got <= 9*time.Second || got > 10*time.Second {
		t.Errorf("Dial 的期限 = %v，預期 10 秒", got)
	}
}

// 逾時的設定：等待回應標頭 30 秒；整個 client 沒有總逾時，SSE 的長連線不會被截斷。
func TestRemoteTransportTimeouts(t *testing.T) {
	g := &globals{server: "http://127.0.0.1:1", token: "t"}
	be, err := openRemote(g, app.Env{})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := be.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", be.hc.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v，預期 30 秒", tr.ResponseHeaderTimeout)
	}
	if be.hc.Timeout != 0 {
		t.Errorf("client 不可以有總逾時（SSE 是長連線）：%v", be.hc.Timeout)
	}
}

// 標頭逾時只限制「等標頭」：標頭已經到了，body 慢慢送也不會被截斷（net/http 的計時器不能注入 clock，
// 所以這裡把期限縮到 100 毫秒，用真實時間驗證；上限只是防卡死）。
func TestRemoteHeaderTimeoutOnlyLimitsHeaders(t *testing.T) {
	old := remoteHeaderTimeout
	remoteHeaderTimeout = 100 * time.Millisecond
	t.Cleanup(func() { remoteHeaderTimeout = old })

	var hang atomic.Bool
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			select { // 永遠不送標頭
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond) // 比標頭逾時長
		io.WriteString(w, "late")
	})
	be, err := openRemote(&globals{server: srv, token: "t"}, app.Env{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := be.open(t.Context(), http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "late" {
		t.Fatalf("body = %q, %v", b, err)
	}

	hang.Store(true)
	start := time.Now()
	if _, err := be.open(t.Context(), http.MethodGet, "/x", nil); err == nil {
		t.Fatal("沒有標頭時應該逾時")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("逾時花了 %v", time.Since(start))
	}
}

// NOTESTEME_TOKEN 由 app.Env.Vars 讀取；--token 優先。
func TestRemoteTokenFromEnv(t *testing.T) {
	api, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"items":[]}`) })
	env := app.Env{Vars: map[string]string{"NOTESTEME_TOKEN": "from-env"}}
	if res, _, errOut := run(t, env, "items", "ls", "--server", srv); res.Code != 0 {
		t.Fatalf("env token：%+v，stderr：%s", res, errOut)
	}
	if res, _, errOut := run(t, env, "items", "ls", "--server", srv, "--token", "from-flag"); res.Code != 0 {
		t.Fatalf("--token：%+v，stderr：%s", res, errOut)
	}
	if len(api.auth) != 2 || api.auth[0] != "Bearer from-env" || api.auth[1] != "Bearer from-flag" {
		t.Errorf("Authorization = %v", api.auth)
	}
	// 兩者都沒有：參數錯誤，說明兩種做法。
	res, _, errOut := run(t, app.Env{Vars: map[string]string{}}, "items", "ls", "--server", srv)
	if res.Code != 2 || !strings.Contains(errOut, "--token") || !strings.Contains(errOut, "NOTESTEME_TOKEN") {
		t.Errorf("沒有 token：%+v，stderr：%s", res, errOut)
	}
}

// http:// 而且不是 loopback：token 以明文傳送，stderr 要警告；loopback 與 https:// 不警告。
func TestRemotePlainHTTPWarning(t *testing.T) {
	_, srv := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"items":[]}`) })
	srvAddr := strings.TrimPrefix(srv, "http://")
	toSrv := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srvAddr)
	}

	cases := []struct {
		server string
		warn   bool
	}{
		{"http://notes.example.test:8080", true},
		{"http://192.0.2.10:8080", true},
		{"http://127.0.0.1:8080", false},
		{"http://localhost:8080", false},
		{"http://[::1]:8080", false},
		{"https://notes.example.test", false},
	}
	for _, c := range cases {
		t.Run(c.server, func(t *testing.T) {
			env := app.Env{Dial: toSrv}
			_, _, errOut := run(t, env, "items", "ls", "--server", c.server, "--token", "t")
			got := strings.Contains(errOut, "明文")
			if got != c.warn {
				t.Errorf("警告 = %v，預期 %v（stderr：%s）", got, c.warn, errOut)
			}
		})
	}
	// 警告用的判斷不能把 URL 的使用者資訊當成主機。
	u, _ := url.Parse("http://127.0.0.1@evil.example.test/")
	if isLoopbackHost(u.Hostname()) {
		t.Error("userinfo 不應該被當成主機")
	}
}
