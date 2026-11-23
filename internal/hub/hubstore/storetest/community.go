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

// 社群與管理的共用測試（M5-S5，HREP-R13～R15、HACC-R15～R17）。

func runCommunity(t *testing.T, open Open) {
	ctx := context.Background()
	// setup 建立上傳者 alice 與一個 visible 的版本，回傳版本。
	setup := func(t *testing.T, s hubstore.Store) (string, hubstore.Version) {
		a, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.CreateShare(ctx, newShare(a.ID, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		return a.ID, res.Versions[0].Version
	}
	member := func(t *testing.T, s hubstore.Store, n int) string {
		u, _, err := s.SignIn(ctx, ident("github", fmt.Sprint(100+n), fmt.Sprint("m", n)), fmt.Sprint("m", n))
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	score := func(t *testing.T, s hubstore.Store, id string) int {
		v, err := s.Version(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.Score
	}

	t.Run("VotesAreOnePerPersonAndSkipSuspended", func(t *testing.T) {
		s, clk := fresh(t, open)
		_, v := setup(t, s)
		bob, carol := member(t, s, 1), member(t, s, 2)
		for _, step := range []struct {
			who   string
			value int
			want  int
		}{{bob, 1, 1}, {bob, 1, 1}, {carol, -1, 0}, {carol, 1, 2}, {bob, 0, 1}, {bob, -1, 0}} {
			got, err := s.SetVote(ctx, v.ID, step.who, step.value)
			if err != nil || got != step.want {
				t.Fatalf("SetVote(%d) = %d, %v，預期 %d", step.value, got, err, step.want)
			}
		}
		u, err := s.VoteUsage(ctx, bob, clk.Now().Add(-time.Hour))
		if err != nil || u.Count != 4 {
			t.Fatalf("VoteUsage = %+v, %v，預期 4 次（含收回）", u, err)
		}
		changed, err := s.SuspendUser(ctx, "", carol, "spam")
		if err != nil || !changed || score(t, s, v.ID) != -1 {
			t.Fatalf("停用 carol 後的分數 = %d, %v, %v，預期 -1（只剩 bob 的 -1）", score(t, s, v.ID), changed, err)
		}
		if changed, err := s.SuspendUser(ctx, "", carol, "again"); err != nil || changed {
			t.Fatalf("重複停用 = %v, %v", changed, err)
		}
		u2, _ := s.User(ctx, carol)
		if !u2.Suspended() || u2.SuspendedReason != "spam" {
			t.Fatalf("停用的帳號 = %+v", u2)
		}
		// 停用期間的票仍然保留，但不計入。
		if got, err := s.SetVote(ctx, v.ID, bob, 1); err != nil || got != 1 {
			t.Fatalf("停用者的票不計入 = %d, %v", got, err)
		}
		if changed, err := s.UnsuspendUser(ctx, "", carol); err != nil || !changed || score(t, s, v.ID) != 2 {
			t.Fatalf("恢復後的分數 = %d, %v, %v，預期 2", score(t, s, v.ID), changed, err)
		}
		if _, err := s.SuspendUser(ctx, "", "nope", "x"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("停用不存在的帳號 = %v", err)
		}
		if _, err := s.UnsuspendUser(ctx, "", "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("恢復不存在的帳號 = %v", err)
		}
		if changed, err := s.UnsuspendUser(ctx, "", carol); err != nil || changed {
			t.Fatalf("恢復沒有停用的帳號 = %v, %v", changed, err)
		}
	})

	t.Run("ConcurrentVotesAddUp", func(t *testing.T) {
		s, _ := fresh(t, open)
		_, v := setup(t, s)
		const n = 12
		voters := make([]string, n)
		for i := range voters {
			voters[i] = member(t, s, i)
		}
		var wg sync.WaitGroup
		errs := make(chan error, n*2)
		for _, id := range voters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.SetVote(ctx, v.ID, id, -1); err != nil {
					errs <- err
				}
				if _, err := s.SetVote(ctx, v.ID, id, 1); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if got := score(t, s, v.ID); got != n {
			t.Fatalf("並發投票後的分數 = %d，預期 %d", got, n)
		}
	})

	t.Run("UnsuspendEndsOldSessionsAndTokens", func(t *testing.T) {
		s, clk := fresh(t, open)
		bob := member(t, s, 1)
		if err := s.CreateSession(ctx, hubstore.Session{ID: "s1", UserID: bob, TokenHash: "h1", CreatedAt: clk.Now(),
			AuthenticatedAt: clk.Now(), ExpiresAt: clk.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreatePAT(ctx, hubstore.PAT{ID: "p1", UserID: bob, Name: "n", TokenHash: "ph", Permissions: []string{"read"},
			CreatedAt: clk.Now(), ExpiresAt: clk.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SuspendUser(ctx, "", bob, "x"); err != nil {
			t.Fatal(err)
		}
		// 停用時保留 session 與 token（middleware 回 403 account_suspended）。
		if _, err := s.SessionByHash(ctx, "h1"); err != nil {
			t.Fatalf("停用後 session 應保留：%v", err)
		}
		if _, err := s.UnsuspendUser(ctx, "", bob); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SessionByHash(ctx, "h1"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("恢復後舊的 session 應失效：%v", err)
		}
		p, err := s.PATByHash(ctx, "ph")
		if err != nil || p.ValidAt(clk.Now()) {
			t.Fatalf("恢復後舊的 token 應撤銷：%+v, %v", p, err)
		}
	})

	t.Run("ReportsDedupeAndAutoHideWithEstablishedAccounts", func(t *testing.T) {
		s, clk := fresh(t, open)
		_, v := setup(t, s)
		old1, old2 := member(t, s, 1), member(t, s, 2)
		clk.Advance(8 * 24 * time.Hour)
		young := member(t, s, 3)
		in := func(who string) hubstore.NewReport {
			return hubstore.NewReport{VersionID: v.ID, ReporterID: who, Reason: "spam", AutoHide: 2, MinAccountAge: 7 * 24 * time.Hour}
		}
		r, hidden, err := s.CreateReport(ctx, in(young))
		if err != nil || hidden || r.State != hubstore.ReportOpen {
			t.Fatalf("第一筆檢舉 = %+v, %v, %v", r, hidden, err)
		}
		if _, _, err := s.CreateReport(ctx, in(young)); !errors.Is(err, hubstore.ErrAlreadyReported) {
			t.Fatalf("重複檢舉 = %v", err)
		}
		if _, hidden, err := s.CreateReport(ctx, in(old1)); err != nil || hidden {
			t.Fatalf("一個帳號齡足夠的檢舉不隱藏：%v, %v", hidden, err)
		}
		// 停用的檢舉者不計入。
		if _, err := s.SuspendUser(ctx, "", old1, "x"); err != nil {
			t.Fatal(err)
		}
		if _, hidden, err := s.CreateReport(ctx, in(old2)); err != nil || hidden {
			t.Fatalf("停用者的檢舉不計入：%v, %v", hidden, err)
		}
		if _, err := s.UnsuspendUser(ctx, "", old1); err != nil {
			t.Fatal(err)
		}
		old3 := member(t, s, 4)
		clk.Advance(8 * 24 * time.Hour)
		last, hidden, err := s.CreateReport(ctx, in(old3))
		if err != nil || !hidden {
			t.Fatalf("達到門檻時要隱藏：%v, %v", hidden, err)
		}
		if got, _ := s.Version(ctx, v.ID); got.State != "hidden" {
			t.Fatalf("state = %q", got.State)
		}
		u, err := s.ReportUsage(ctx, young, clk.Now().Add(-30*24*time.Hour))
		if err != nil || u.Count != 1 {
			t.Fatalf("ReportUsage = %+v, %v", u, err)
		}
		list, err := s.Reports(ctx, hubstore.ReportOpen, 10)
		if err != nil || len(list) != 4 || list[0].ID != last.ID {
			t.Fatalf("Reports = %+v, %v", list, err)
		}
		d, err := s.DismissReport(ctx, old3, last.ID)
		if err != nil || d.State != hubstore.ReportDismissed {
			t.Fatalf("DismissReport = %+v, %v", d, err)
		}
		if got, _ := s.Version(ctx, v.ID); got.State != "visible" {
			t.Fatalf("駁回後 state = %q，預期 visible", got.State)
		}
		if open, _ := s.Reports(ctx, hubstore.ReportOpen, 10); len(open) != 0 {
			t.Fatalf("駁回後仍有未處理的檢舉：%+v", open)
		}
		if _, err := s.DismissReport(ctx, old3, last.ID); !errors.Is(err, hubstore.ErrDecided) {
			t.Fatalf("重複處理 = %v", err)
		}
		if _, err := s.DismissReport(ctx, old3, "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("處理不存在的檢舉 = %v", err)
		}
		// 結案之後可以再檢舉。
		if _, _, err := s.CreateReport(ctx, in(young)); err != nil {
			t.Fatalf("結案後再檢舉 = %v", err)
		}
		if r, err := s.Report(ctx, last.ID); err != nil || r.DecidedAt == nil {
			t.Fatalf("Report = %+v, %v", r, err)
		}
		if _, err := s.Report(ctx, "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("Report(nope) = %v", err)
		}
	})

	t.Run("RemovalKeepsSharedBlobsAndBlocksReupload", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, v := setup(t, s)
		// 另一部影片有相同內容的版本：blob 仍被引用，不能刪。
		other := newShare(alice, "b2", ver("source", shaN("a"), `{}`))
		other.Video.ExternalID = "def"
		if _, err := s.CreateShare(ctx, other); err != nil {
			t.Fatal(err)
		}
		bob := member(t, s, 1)
		rep, _, err := s.CreateReport(ctx, hubstore.NewReport{VersionID: v.ID, ReporterID: bob, Reason: "offensive"})
		if err != nil {
			t.Fatal(err)
		}
		got, rm, err := s.RemoveByReport(ctx, bob, rep.ID)
		if err != nil || got.State != hubstore.ReportActioned || len(rm.BlobsToDelete) != 0 {
			t.Fatalf("RemoveByReport = %+v, %+v, %v", got, rm, err)
		}
		if _, err := s.CreateShare(ctx, newShare(alice, "b3", ver("source", shaN("a"), `{}`))); !errors.Is(err, hubstore.ErrRemovedContent) {
			t.Fatalf("重新上傳已移除的內容 = %v", err)
		}
		mine, err := s.MyShares(ctx, alice, 10)
		if err != nil || len(mine) != 2 {
			t.Fatalf("MyShares = %+v, %v", mine, err)
		}
		last := mine[len(mine)-1].Versions[0]
		if last.State != "removed" || last.RemovalSource != hubstore.RemovalReport || last.RemovalReason != "offensive" {
			t.Fatalf("我的分享中的版本 = %+v", last)
		}
		if _, err := s.HideVersion(ctx, bob, v.ID); !errors.Is(err, hubstore.ErrVersionState) {
			t.Fatalf("隱藏已移除的版本 = %v", err)
		}
	})

	t.Run("TakedownsDecideOnceAndRemoveUnsharedBlobs", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, v := setup(t, s)
		mod := member(t, s, 1)
		td := func() hubstore.Takedown {
			x, err := s.CreateTakedown(ctx, hubstore.Takedown{VersionID: v.ID, Reason: "copyright", Statement: "statement",
				DEKNonce: []byte{1}, DEKSealed: []byte{2}, ContactNonce: []byte{3}, ContactSealed: []byte{4}})
			if err != nil || x.State != hubstore.TakedownOpen || x.ID == "" {
				t.Fatalf("CreateTakedown = %+v, %v", x, err)
			}
			return x
		}
		a, b := td(), td()
		if got, err := s.Takedown(ctx, a.ID); err != nil || string(got.ContactSealed) != "\x04" {
			t.Fatalf("Takedown = %+v, %v", got, err)
		}
		rej, err := s.RejectTakedown(ctx, mod, a.ID, "no")
		if err != nil || rej.State != hubstore.TakedownRejected || rej.Note != "no" {
			t.Fatalf("RejectTakedown = %+v, %v", rej, err)
		}
		if _, _, err := s.RemoveByTakedown(ctx, mod, a.ID, "x"); !errors.Is(err, hubstore.ErrDecided) {
			t.Fatalf("重複決定 = %v", err)
		}
		if got, _ := s.Version(ctx, v.ID); got.State != "visible" {
			t.Fatalf("駁回後 state = %q", got.State)
		}
		got, rm, err := s.RemoveByTakedown(ctx, mod, b.ID, "ok")
		if err != nil || got.State != hubstore.TakedownRemoved || len(rm.BlobsToDelete) != 1 || rm.BlobsToDelete[0] != shaN("a") {
			t.Fatalf("RemoveByTakedown = %+v, %+v, %v", got, rm, err)
		}
		if got, _ := s.Version(ctx, v.ID); got.State != "removed" {
			t.Fatalf("移除後 state = %q", got.State)
		}
		if _, err := s.CreateShare(ctx, newShare(alice, "b9", ver("source", shaN("a"), `{}`))); !errors.Is(err, hubstore.ErrRemovedContent) {
			t.Fatalf("重新上傳已下架的內容 = %v", err)
		}
		if _, _, err := s.RemoveByTakedown(ctx, mod, "nope", ""); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("不存在的申請 = %v", err)
		}
		if _, err := s.RejectTakedown(ctx, mod, "nope", ""); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("駁回不存在的申請 = %v", err)
		}
		// 申請的版本不存在：不能移除，申請維持 open。
		ghost, err := s.CreateTakedown(ctx, hubstore.Takedown{VersionID: "missing", Reason: "other", Statement: "s",
			DEKNonce: []byte{1}, DEKSealed: []byte{2}, ContactNonce: []byte{3}, ContactSealed: []byte{4}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.RemoveByTakedown(ctx, mod, ghost.ID, ""); !errors.Is(err, hubstore.ErrVersionState) {
			t.Fatalf("移除不存在的版本 = %v", err)
		}
		if g, _ := s.Takedown(ctx, ghost.ID); g.State != hubstore.TakedownOpen {
			t.Fatalf("失敗的決定改了申請：%+v", g)
		}
		list, err := s.Takedowns(ctx, "", 10)
		if err != nil || len(list) != 3 || list[0].ID != ghost.ID {
			t.Fatalf("Takedowns = %d, %v", len(list), err)
		}
		if open, _ := s.Takedowns(ctx, hubstore.TakedownOpen, 10); len(open) != 1 {
			t.Fatalf("open 的申請 = %d", len(open))
		}
		entries, err := s.AuditEntries(ctx, 10)
		if err != nil || len(entries) != 2 || entries[0].Action != hubstore.AuditTakedown || entries[0].Actor != "m1" {
			t.Fatalf("稽核紀錄 = %+v, %v", entries, err)
		}
	})

	t.Run("HideAndAdminActionsAreAudited", func(t *testing.T) {
		s, _ := fresh(t, open)
		_, v := setup(t, s)
		root, bob := member(t, s, 1), member(t, s, 2)
		if changed, err := s.HideVersion(ctx, root, v.ID); err != nil || !changed {
			t.Fatalf("HideVersion = %v, %v", changed, err)
		}
		if changed, err := s.HideVersion(ctx, root, v.ID); err != nil || changed {
			t.Fatalf("重複隱藏 = %v, %v", changed, err)
		}
		if _, err := s.HideVersion(ctx, root, "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("隱藏不存在的版本 = %v", err)
		}
		if _, err := s.SuspendUser(ctx, root, bob, "spam"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UnsuspendUser(ctx, root, bob); err != nil {
			t.Fatal(err)
		}
		if changed, err := s.SetCLIGroup(ctx, bob, "admins", true); err != nil || !changed {
			t.Fatalf("SetCLIGroup = %v, %v", changed, err)
		}
		if changed, err := s.SetCLIGroup(ctx, bob, "admins", true); err != nil || changed {
			t.Fatalf("重複 SetCLIGroup = %v, %v", changed, err)
		}
		// 啟動時依設定檔的同步不會清掉 cli 的成員資格。
		if err := s.ReplaceConfigGroup(ctx, "admins", nil); err != nil {
			t.Fatal(err)
		}
		ms, _ := s.GroupMemberships(ctx, bob)
		if len(ms) != 1 || ms[0].Source != hubstore.SourceCLI {
			t.Fatalf("成員資格 = %+v", ms)
		}
		if changed, err := s.SetCLIGroup(ctx, bob, "admins", false); err != nil || !changed {
			t.Fatalf("移除 cli 成員資格 = %v, %v", changed, err)
		}
		if _, err := s.SetCLIGroup(ctx, "nope", "admins", true); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("不存在的帳號 = %v", err)
		}
		entries, err := s.AuditEntries(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Actor+" "+e.Action)
		}
		want := fmt.Sprint([]string{hubstore.CLIActor + " group.remove", hubstore.CLIActor + " group.add", "m1 user.unsuspend", "m1 user.suspend", "m1 version.hide"})
		if fmt.Sprint(got) != want {
			t.Fatalf("稽核紀錄 = %v，預期 %v", got, want)
		}
		users, err := s.Users(ctx, 10)
		if err != nil || len(users) != 3 {
			t.Fatalf("Users = %d, %v", len(users), err)
		}
		if one, _ := s.Users(ctx, 1); len(one) != 1 {
			t.Fatalf("Users(limit 1) = %d", len(one))
		}
	})
}
