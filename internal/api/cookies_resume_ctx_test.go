package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// cancelingCookies 在儲存成功的同時取消請求的 ctx，模擬客戶端在 cookie 存好之後斷線。
type cancelingCookies struct{ cancel context.CancelFunc }

func (c cancelingCookies) Put(context.Context, string, string) (int, time.Time, error) {
	c.cancel()
	return 1, time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), nil
}

// ctxResumer 記錄 ResumeBlocked 收到的 ctx 狀態。
type ctxResumer struct {
	mu          sync.Mutex
	calls       int
	ctxErr      error
	hasDeadline bool
	remaining   time.Duration
}

func (c *ctxResumer) ResumeBlocked(ctx context.Context, _ string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.ctxErr = ctx.Err()
	var dl time.Time
	dl, c.hasDeadline = ctx.Deadline()
	c.remaining = time.Until(dl)
	return 1, ctx.Err()
}

// cookie 已經儲存之後，請求的 ctx 被取消（客戶端斷線）不能讓恢復中斷：
// 恢復用不受請求取消影響的 ctx，並有 30 秒的逾時。
func TestPostCookiesResumeSurvivesRequestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := &ctxResumer{}
	h := NewLocal(Deps{
		Log:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:   clock.NewFake(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)),
		Version: "test",
		Cookies: cancelingCookies{cancel: cancel},
		Resumer: res,
	})
	req := httptest.NewRequestWithContext(ctx, "POST", "/api/cookies", strings.NewReader(`{"platform":"youtube","content":"SID=x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if res.calls != 1 {
		t.Fatalf("ResumeBlocked 呼叫 %d 次", res.calls)
	}
	if res.ctxErr != nil {
		t.Errorf("恢復收到已取消的 ctx：%v", res.ctxErr)
	}
	if !res.hasDeadline || res.remaining <= 25*time.Second || res.remaining > 30*time.Second {
		t.Errorf("恢復的 ctx 應有 30 秒的逾時：hasDeadline=%v, remaining=%v", res.hasDeadline, res.remaining)
	}
}
