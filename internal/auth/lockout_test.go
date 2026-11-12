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

// 不同來源位址的失敗計數與鎖定互不影響（AUTH-R6）。
func TestLockoutIsPerAddress(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFailures; i++ {
		_, _ = svc.Login(ctx, "wrong", "192.0.2.1", "")
	}
	var locked *LockedError
	if _, err := svc.Login(ctx, "pw", "192.0.2.1", ""); !errors.As(err, &locked) {
		t.Fatalf("鎖定位址應被拒絕，err = %v", err)
	}
	if _, err := svc.Login(ctx, "pw", "192.0.2.2", ""); err != nil {
		t.Fatalf("其他位址不應受影響：%v", err)
	}
	// 其他位址的失敗也各自計數：4 次不鎖定。
	for i := 0; i < maxFailures-1; i++ {
		_, _ = svc.Login(ctx, "wrong", "192.0.2.3", "")
	}
	if _, err := svc.Login(ctx, "pw", "192.0.2.3", ""); err != nil {
		t.Fatalf("4 次失敗後仍可登入：%v", err)
	}
	clk.Advance(lockDuration + time.Second)
	if _, err := svc.Login(ctx, "pw", "192.0.2.1", ""); err != nil {
		t.Fatalf("鎖定到期後應可登入：%v", err)
	}
}

// 同一位址並行送出大量錯誤密碼，進入驗證的次數不超過上限；其他位址不受鎖定影響。
func TestConcurrentWrongPasswordsPerAddress(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	_ = svc.EnsurePassword(ctx, "pw")
	var verifications atomic.Int32
	real := svc.verify
	svc.verify = func(password, encoded string) (bool, error) {
		verifications.Add(1)
		time.Sleep(5 * time.Millisecond)
		return real(password, encoded)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.Login(ctx, "wrong", "192.0.2.1", "")
		}()
	}
	wg.Wait()
	if got := verifications.Load(); got > maxFailures {
		t.Fatalf("進入驗證 %d 次，上限 %d", got, maxFailures)
	}
	if _, err := svc.Login(ctx, "pw", "192.0.2.2", ""); err != nil {
		t.Fatalf("其他位址不應受影響：%v", err)
	}
}

func TestLockoutRecordLimit(t *testing.T) {
	clk := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	l := newLockouts(3)
	for i, a := range []string{"a", "b", "c", "d"} {
		l.fail(a, clk.Add(time.Duration(i)*time.Second))
	}
	if got := l.size(); got != 3 {
		t.Fatalf("紀錄數 = %d，上限 3", got)
	}
	// 最舊的 a 被淘汰，其餘保留。
	for _, a := range []string{"b", "c", "d"} {
		if _, ok := l.entries[a]; !ok {
			t.Errorf("%s 應該保留", a)
		}
	}
	if _, ok := l.entries["a"]; ok {
		t.Error("a 應該被淘汰")
	}
	// 再次失敗會更新「最近」順序：b 變成最新，下一個被淘汰的是 c。
	l.fail("b", clk.Add(10*time.Second))
	l.fail("e", clk.Add(11*time.Second))
	if _, ok := l.entries["c"]; ok {
		t.Error("c 應該被淘汰")
	}
	if _, ok := l.entries["b"]; !ok {
		t.Error("b 應該保留")
	}
}

func TestLockoutRecordsExpire(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	l := newLockouts(100)
	for i := 0; i < maxFailures; i++ {
		l.fail("locked", now)
	}
	l.fail("counting", now)
	if got := l.remaining("locked", now); got != lockDuration {
		t.Fatalf("remaining = %v", got)
	}
	// 鎖定到期後紀錄清除；只有失敗計數的紀錄保留到 failureTTL。
	later := now.Add(lockDuration + time.Second)
	if got := l.remaining("locked", later); got != 0 {
		t.Errorf("鎖定到期後 remaining = %v", got)
	}
	if _, ok := l.entries["locked"]; ok {
		t.Error("過期的鎖定紀錄應該清除")
	}
	if _, ok := l.entries["counting"]; !ok {
		t.Error("失敗計數尚未過期")
	}
	l.fail("other", now.Add(failureTTL+time.Second)) // 觸發 sweep
	if _, ok := l.entries["counting"]; ok {
		t.Error("過期的失敗計數應該清除")
	}
	if got := l.size(); got != 1 {
		t.Errorf("紀錄數 = %d，預期 1", got)
	}
}
