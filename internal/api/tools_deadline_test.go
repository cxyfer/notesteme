package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// deadlineListener 記錄最近一條連線最後一次 SetReadDeadline 設的值。
type deadlineListener struct {
	net.Listener
	mu   sync.Mutex
	last *deadlineConn
}

type deadlineConn struct {
	net.Conn
	mu       sync.Mutex
	deadline time.Time
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlineConn) readDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func (l *deadlineListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	dc := &deadlineConn{Conn: c}
	l.mu.Lock()
	l.last = dc
	l.mu.Unlock()
	return dc, nil
}

// deadlineProbeTools 在請求到達管理者時，記下連線上的讀取期限。
type deadlineProbeTools struct {
	fakeTools
	ln       *deadlineListener
	deadline time.Time
}

func (p *deadlineProbeTools) UpdateYtDlp(ctx context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error) {
	p.ln.mu.Lock()
	conn := p.ln.last
	p.ln.mu.Unlock()
	p.deadline = conn.readDeadline()
	return p.fakeTools.UpdateYtDlp(ctx, req)
}

// 更新要跑好幾分鐘。huma 讀 body 時預設對連線設 5 秒的讀取期限，期限過了 net/http 會以為 client 斷線而取消請求的
// context，更新就被中斷：請求到達管理者時，連線上不能留著讀取期限。
func TestUpdateYtDlpClearsTheConnectionReadDeadline(t *testing.T) {
	ln := &deadlineListener{}
	pt := &deadlineProbeTools{fakeTools: fakeTools{res: &ytdlp.Result{Version: "2026.11.10"}}, ln: ln}
	d := opsDeps()
	d.Tools = pt
	srv := httptest.NewUnstartedServer(NewLocal(d))
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Listener = inner
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	for _, body := range []string{"", `{"pin":"2026.11.10"}`} {
		pt.deadline = time.Now() // 先設成非零值，確認真的被覆寫
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		req, err := http.NewRequest("POST", srv.URL+"/api/tools/yt-dlp/update", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("body %q：status = %d", body, resp.StatusCode)
		}
		if !pt.deadline.IsZero() {
			t.Errorf("body %q：到達管理者時連線還有讀取期限 %v", body, pt.deadline)
		}
	}
}
