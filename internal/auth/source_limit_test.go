package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// AUTH-R6：IPv6 的鎖定以 /64 為單位。一般的網路至少分配一個 /64，同一個 /64 換位址不能多得 5 次嘗試；
// 其他 /64 與 IPv4 照舊各自計算。
func TestLockoutGroupsIPv6By64(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	evaluated := 0
	for host := 1; host <= 4; host++ {
		for i := 0; i < maxFailures; i++ {
			_, err := svc.Login(ctx, "wrong", fmt.Sprintf("2001:db8:1:2::%x", host), "")
			var locked *LockedError
			if !errors.As(err, &locked) {
				evaluated++
			}
		}
	}
	if evaluated != maxFailures {
		t.Fatalf("同一個 /64 驗證了 %d 次密碼，上限是 %d", evaluated, maxFailures)
	}
	var locked *LockedError
	if _, err := svc.Login(ctx, "pw", "2001:db8:1:2:ffff::9", ""); !errors.As(err, &locked) {
		t.Fatalf("同一個 /64 的其他位址也要鎖定：%v", err)
	}
	if _, err := svc.Login(ctx, "pw", "2001:db8:1:3::1", ""); err != nil {
		t.Fatalf("其他 /64 不受影響：%v", err)
	}
	if _, err := svc.Login(ctx, "pw", "192.0.2.1", ""); err != nil {
		t.Fatalf("IPv4 不受影響：%v", err)
	}
}

func TestSourceKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1":            "192.0.2.1",
		"::ffff:192.0.2.1":     "192.0.2.1",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:DB8:1:2::1":      "2001:db8:1:2::/64",
		"fe80::1%eth0":         "fe80::/64",
		"not-an-ip":            "not-an-ip",
		"":                     "",
	} {
		if got := sourceKey(in); got != want {
			t.Errorf("sourceKey(%q) = %q，預期 %q", in, got, want)
		}
	}
}

// AUTH-R17：免驗證的 start 每個來源最多 MaxOIDCStartsPerSource 次（OIDCLoginTTL 內），
// 同一個來源不能佔滿全域的未完成上限，讓 owner 在接下來 10 分鐘拿到 429。
func TestOIDCStartLimitedPerSource(t *testing.T) {
	svc, _, clk, _ := newOIDCService(t)
	ctx := context.Background()
	for i := 0; i < MaxOIDCStartsPerSource; i++ {
		if _, err := svc.StartOIDC(ctx, "", fmt.Sprintf("2001:db8:66::%x", i+1)); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := svc.StartOIDC(ctx, "", "2001:db8:66::ffff"); !errors.Is(err, ErrOIDCBusy) {
		t.Fatalf("同一個 /64 超過上限：%v", err)
	}
	if _, err := svc.StartOIDC(ctx, "", "198.51.100.7"); err != nil {
		t.Fatalf("owner 不受影響：%v", err)
	}
	clk.Advance(OIDCLoginTTL)
	if _, err := svc.StartOIDC(ctx, "", "2001:db8:66::1"); err != nil {
		t.Fatalf("過了 %s 之後可以再開始：%v", OIDCLoginTTL, err)
	}
}

// startLimiter 的來源筆數有上限：大量不同來源只會淘汰最久沒有活動的紀錄，不會無限成長。
func TestStartLimiterRecordLimit(t *testing.T) {
	l := newStartLimiter(2, 1, time.Minute)
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	if !l.allow("a", now) || !l.allow("b", now.Add(time.Second)) || l.allow("b", now.Add(2*time.Second)) {
		t.Fatal("每個來源 1 次")
	}
	if !l.allow("c", now.Add(3*time.Second)) || len(l.entries) != 2 {
		t.Fatalf("超過筆數時淘汰最舊的：%v", l.entries)
	}
	if _, ok := l.entries["a"]; ok {
		t.Fatal("應淘汰最久沒有活動的 a")
	}
	if !l.allow("b", now.Add(time.Minute+time.Second)) {
		t.Fatal("時間窗過了之後可以再開始")
	}
}
