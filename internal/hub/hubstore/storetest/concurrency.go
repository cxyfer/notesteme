package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 並行的對抗案例：SQLite 的寫入天然序列化，Postgres 靠列鎖與重試達到同樣的結果（ADR-0030 第 2 點）。
// 每個案例讓多個 goroutine 同時呼叫，再檢查結果與「依序執行」的某一種順序一致。

// parallel 同時執行 n 次 fn，回傳所有錯誤。
func parallel(n int, fn func(i int) error) []error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		gate = make(chan struct{})
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			if err := fn(i); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	close(gate)
	wg.Wait()
	return errs
}

func runConcurrency(t *testing.T, open Open) {
	ctx := context.Background()

	t.Run("ConcurrentFirstSignInCreatesOneAccount", func(t *testing.T) {
		s, _ := fresh(t, open)
		const n = 8
		ids := make([]string, n)
		created := make([]bool, n)
		if errs := parallel(n, func(i int) error {
			u, c, err := s.SignIn(ctx, ident("github", "42", "alice"), "alice")
			ids[i], created[i] = u.ID, c
			return err
		}); len(errs) > 0 {
			t.Fatalf("並行登入：%v", errs)
		}
		nCreated := 0
		for i := range n {
			if ids[i] != ids[0] {
				t.Fatalf("並行的第一次登入得到不同的帳號：%v", ids)
			}
			if created[i] {
				nCreated++
			}
		}
		if nCreated != 1 {
			t.Fatalf("created 為 true 的次數 = %d，預期 1", nCreated)
		}
		if c, _ := s.CountUsers(ctx); c != 1 {
			t.Fatalf("CountUsers = %d，預期 1", c)
		}
	})

	t.Run("ConcurrentUnlinkKeepsOneIdentity", func(t *testing.T) {
		s, _ := fresh(t, open)
		u, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"google", "linuxdo"} {
			if _, err := s.LinkIdentity(ctx, u.ID, ident(p, "x-"+p, "alice")); err != nil {
				t.Fatal(err)
			}
		}
		providers := []string{"github", "google", "linuxdo"}
		errs := parallel(len(providers), func(i int) error { return s.UnlinkIdentity(ctx, u.ID, providers[i], "") })
		for _, err := range errs {
			if !errors.Is(err, hubstore.ErrLastIdentity) {
				t.Fatalf("並行移除：%v", err)
			}
		}
		ids, err := s.Identities(ctx, u.ID)
		if err != nil || len(ids) != 1 || len(errs) != 1 {
			t.Fatalf("並行移除後剩下 %d 個身分（%v）、%d 個 ErrLastIdentity，預期剩 1 個", len(ids), err, len(errs))
		}
	})

	t.Run("ConcurrentSameReporterOnlyOnce", func(t *testing.T) {
		s, _ := fresh(t, open)
		a, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		res, err := s.CreateShare(ctx, newShare(a.ID, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		bob, _, _ := s.SignIn(ctx, ident("github", "2", "bob"), "bob")
		const n = 6
		errs := parallel(n, func(int) error {
			_, _, err := s.CreateReport(ctx, hubstore.NewReport{VersionID: res.Versions[0].ID, ReporterID: bob.ID, Reason: "spam"})
			return err
		})
		if len(errs) != n-1 {
			t.Fatalf("並行的同一人檢舉：%d 個錯誤，預期 %d 個", len(errs), n-1)
		}
		for _, err := range errs {
			if !errors.Is(err, hubstore.ErrAlreadyReported) {
				t.Fatalf("並行的同一人檢舉 = %v，預期 ErrAlreadyReported", err)
			}
		}
		if rs, _ := s.Reports(ctx, hubstore.ReportOpen, 100); len(rs) != 1 {
			t.Fatalf("open 的檢舉 = %d 筆，預期 1", len(rs))
		}
	})

	t.Run("ConcurrentReportsReachAutoHide", func(t *testing.T) {
		s, clk := fresh(t, open)
		a, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		res, err := s.CreateShare(ctx, newShare(a.ID, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		const n = 5
		reporters := make([]string, n)
		for i := range reporters {
			u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(200+i), fmt.Sprint("r", i)), fmt.Sprint("r", i))
			if err != nil {
				t.Fatal(err)
			}
			reporters[i] = u.ID
		}
		clk.Advance(48 * time.Hour)
		hidden := make([]bool, n)
		if errs := parallel(n, func(i int) error {
			_, h, err := s.CreateReport(ctx, hubstore.NewReport{
				VersionID: res.Versions[0].ID, ReporterID: reporters[i], Reason: "spam", AutoHide: n, MinAccountAge: 24 * time.Hour,
			})
			hidden[i] = h
			return err
		}); len(errs) > 0 {
			t.Fatalf("並行檢舉：%v", errs)
		}
		nHidden := 0
		for _, h := range hidden {
			if h {
				nHidden++
			}
		}
		v, _ := s.Version(ctx, res.Versions[0].ID)
		if v.State != "hidden" || nHidden != 1 {
			t.Fatalf("%d 個並行檢舉達到門檻後 state = %s、hidden 回報 %d 次，預期 hidden 且回報 1 次", n, v.State, nHidden)
		}
	})

	t.Run("ConcurrentWithdrawsWithdrawTheVersion", func(t *testing.T) {
		s, _ := fresh(t, open)
		const n = 4
		var (
			shares []string
			users  []string
			vid    string
		)
		for i := range n {
			u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(300+i), fmt.Sprint("u", i)), fmt.Sprint("u", i))
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.CreateShare(ctx, newShare(u.ID, "bundle-"+fmt.Sprint(i), ver("source", shaN("c"), `{}`)))
			if err != nil {
				t.Fatal(err)
			}
			users, shares, vid = append(users, u.ID), append(shares, res.ShareID), res.Versions[0].ID
		}
		if errs := parallel(n, func(i int) error { return s.WithdrawShare(ctx, users[i], shares[i]) }); len(errs) > 0 {
			t.Fatalf("並行撤回：%v", errs)
		}
		if v, _ := s.Version(ctx, vid); v.State != "withdrawn" {
			t.Fatalf("所有分享都撤回後 state = %s，預期 withdrawn", v.State)
		}
	})

	t.Run("ConcurrentSharesOfSameVideoDedupe", func(t *testing.T) {
		s, _ := fresh(t, open)
		const n = 4
		users := make([]string, n)
		for i := range users {
			u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(400+i), fmt.Sprint("s", i)), fmt.Sprint("s", i))
			if err != nil {
				t.Fatal(err)
			}
			users[i] = u.ID
		}
		vids := make([]string, n)
		if errs := parallel(n, func(i int) error {
			res, err := s.CreateShare(ctx, newShare(users[i], "same", ver("source", shaN("d"), `{}`)))
			if err == nil {
				vids[i] = res.Versions[0].ID
			}
			return err
		}); len(errs) > 0 {
			t.Fatalf("並行分享：%v", errs)
		}
		for _, id := range vids {
			if id != vids[0] {
				t.Fatalf("同一份內容並行分享得到不同的版本：%v", vids)
			}
		}
		up, err := s.VersionUploaders(ctx, vids[0])
		if err != nil || len(up) != n {
			t.Fatalf("上傳者 = %d, %v，預期 %d", len(up), err, n)
		}
	})

	// 版本的鍵不唯一（安全修補 A）：影片已存在時，並行分享相同內容仍只建立一個版本（不能靠影片的唯一鍵重試序列化）。
	t.Run("ConcurrentSharesOfExistingVideoDedupe", func(t *testing.T) {
		s, _ := fresh(t, open)
		const n = 8
		users := make([]string, n)
		for i := range users {
			u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(450+i), fmt.Sprint("x", i)), fmt.Sprint("x", i))
			if err != nil {
				t.Fatal(err)
			}
			users[i] = u.ID
		}
		// 先讓 blob 存在：新 blob 的 INSERT 會在 Postgres 互相等待，那樣測不到版本的去重。
		for round := range 5 {
			if _, err := s.CreateShare(ctx, newShare(users[0], fmt.Sprint("seed", round), ver("source", shaN(fmt.Sprint(round)), `{}`))); err != nil {
				t.Fatal(err)
			}
		}
		for round := range 5 {
			sha := shaN(fmt.Sprint(round))
			vids := make([]string, n)
			if errs := parallel(n, func(i int) error {
				res, err := s.CreateShare(ctx, newShare(users[i], fmt.Sprint("r", round), ver("normalized", sha, `{}`)))
				if err == nil {
					vids[i] = res.Versions[0].ID
				}
				return err
			}); len(errs) > 0 {
				t.Fatalf("並行分享：%v", errs)
			}
			for _, id := range vids {
				if id != vids[0] {
					t.Fatalf("第 %d 輪：同一份內容並行分享得到不同的版本：%v", round, vids)
				}
			}
		}
	})

	t.Run("ConcurrentSuspendAndVotes", func(t *testing.T) {
		s, _ := fresh(t, open)
		a, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		res, err := s.CreateShare(ctx, newShare(a.ID, "b1", ver("source", shaN("e"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		vid := res.Versions[0].ID
		const n = 6
		voters := make([]string, n)
		for i := range voters {
			u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(500+i), fmt.Sprint("v", i)), fmt.Sprint("v", i))
			if err != nil {
				t.Fatal(err)
			}
			voters[i] = u.ID
		}
		if _, err := s.SetVote(ctx, vid, voters[0], 1); err != nil {
			t.Fatal(err)
		}
		// voters[0] 被停用的同時，其他人投票：最後的分數只算沒有被停用的 n-1 票。
		if errs := parallel(n, func(i int) error {
			if i == 0 {
				_, err := s.SuspendUser(ctx, "", voters[0], "spam")
				return err
			}
			_, err := s.SetVote(ctx, vid, voters[i], 1)
			return err
		}); len(errs) > 0 {
			t.Fatalf("並行停用與投票：%v", errs)
		}
		if v, _ := s.Version(ctx, vid); v.Score != n-1 {
			t.Fatalf("分數 = %d，預期 %d", v.Score, n-1)
		}
	})
}

// runParity 是 backend 之間容易不一致的細節：時間的精度與文字的排序（SQLite 以位元組比較）。
func runParity(t *testing.T, open Open) {
	ctx := context.Background()

	t.Run("TimesHaveMillisecondPrecision", func(t *testing.T) {
		s, clk := fresh(t, open)
		clk.Advance(1500*time.Microsecond + 7)
		want := clk.Now().Truncate(time.Millisecond)
		u, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil || !u.CreatedAt.Equal(want) || u.CreatedAt.Location() != time.UTC {
			t.Fatalf("CreatedAt = %v（%v），預期截到毫秒的 %v（UTC）", u.CreatedAt, err, want)
		}
		// 從資料庫讀回的時間也是 UTC（不受資料庫或程序的時區影響）。
		if got, err := s.User(ctx, u.ID); err != nil || !got.CreatedAt.Equal(want) || got.CreatedAt.Location() != time.UTC {
			t.Fatalf("讀回的 CreatedAt = %v（%v），預期 UTC 的 %v", got.CreatedAt, err, want)
		}
		exp := clk.Now().Add(time.Hour + 999*time.Microsecond)
		if err := s.CreateSession(ctx, hubstore.Session{ID: "s1", UserID: u.ID, TokenHash: "h", CreatedAt: clk.Now(),
			AuthenticatedAt: clk.Now(), ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
		se, err := s.SessionByHash(ctx, "h")
		if err != nil || !se.ExpiresAt.Equal(exp.Truncate(time.Millisecond)) {
			t.Fatalf("ExpiresAt = %v, %v，預期 %v", se.ExpiresAt, err, exp.Truncate(time.Millisecond))
		}
		res, err := s.CreateShare(ctx, newShare(u.ID, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		// 以列出的 CreatedAt 當 cursor：同一個版本不會再出現（cursor 的等號比較依賴兩邊相同的精度）。
		v := res.Versions[0]
		got, err := s.ListVersions(ctx, hubstore.VersionFilter{Limit: 10, Seen: hubstore.SeenBy{SeeAll: true},
			After: &hubstore.VersionCursor{CreatedAt: v.CreatedAt, ID: v.ID}})
		if err != nil || len(got) != 0 {
			t.Fatalf("cursor 之後的版本 = %d, %v，預期 0", len(got), err)
		}
	})

	t.Run("TextSortsByBytes", func(t *testing.T) {
		s, _ := fresh(t, open)
		u, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range []string{"beta", "alpha", "Alpha", "_x", "Beta"} {
			if _, err := s.SetManualGroup(ctx, "", u.ID, g, true); err != nil {
				t.Fatal(err)
			}
		}
		ms, err := s.GroupMemberships(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range ms {
			got = append(got, m.Group)
		}
		if fmt.Sprint(got) != "[Alpha Beta _x alpha beta]" {
			t.Fatalf("群組的順序 = %v，預期依位元組排序 [Alpha Beta _x alpha beta]", got)
		}
	})
}
