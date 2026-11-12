package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 並行送出大量錯誤密碼時，鎖定生效前最多只能有 maxFailures 次進入 argon2 驗證（AUTH-R6）。
func TestConcurrentWrongPasswordsCannotBypassLockout(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	var verifications atomic.Int32
	real := svc.verify
	svc.verify = func(password, encoded string) (bool, error) {
		verifications.Add(1)
		// 讓驗證夠慢，使並行請求確實重疊。
		time.Sleep(20 * time.Millisecond)
		return real(password, encoded)
	}

	const n = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.Login(ctx, "wrong", "", "")
		}()
	}
	close(start)
	wg.Wait()

	if got := verifications.Load(); got > maxFailures {
		t.Fatalf("進入驗證 %d 次，上限 %d", got, maxFailures)
	}
	var locked *LockedError
	if _, err := svc.Login(ctx, "pw", "", ""); !errors.As(err, &locked) {
		t.Fatalf("之後應該被鎖定，err = %v", err)
	}
}
