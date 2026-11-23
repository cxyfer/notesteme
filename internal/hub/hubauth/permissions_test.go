package hubauth

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

func intp(n int) *int { return &n }

func TestRuleHolds(t *testing.T) {
	linux := func(level *int) hubstore.ExternalIdentity {
		return hubstore.ExternalIdentity{Provider: "linuxdo", Subject: "1", Attrs: hubstore.IdentityAttrs{TrustLevel: level}}
	}
	gh := func(orgs ...string) hubstore.ExternalIdentity {
		return hubstore.ExternalIdentity{Provider: "github", Subject: "1", Attrs: hubstore.IdentityAttrs{Orgs: orgs}}
	}
	goog := func(email string, verified bool) hubstore.ExternalIdentity {
		return hubstore.ExternalIdentity{Provider: "google", Subject: "1", Email: email, EmailVerified: verified}
	}
	trust := hubconfig.Rule{Provider: hubconfig.LinuxDo, TrustLevelGTE: intp(2)}
	org := hubconfig.Rule{Provider: hubconfig.GitHub, OrgMember: []string{"notesteme-friends"}}
	domain := hubconfig.Rule{Provider: hubconfig.Google, EmailDomain: []string{"example.com"}}
	cases := []struct {
		name string
		rule hubconfig.Rule
		id   hubstore.ExternalIdentity
		want bool
	}{
		{"trust 剛好", trust, linux(intp(2)), true},
		{"trust 更高", trust, linux(intp(4)), true},
		{"trust 低一級", trust, linux(intp(1)), false},
		{"沒有 trust_level", trust, linux(nil), false},
		{"trust 規則對 github 身分", trust, gh(), false},
		{"組織成員", org, gh("other", "notesteme-friends"), true},
		{"組織名稱大小寫", org, gh("Notesteme-Friends"), true},
		{"不是成員", org, gh("notesteme"), false},
		{"沒有組織", org, gh(), false},
		{"已驗證的網域", domain, goog("carol@example.com", true), true},
		{"網域大小寫", domain, goog("carol@EXAMPLE.com", true), true},
		{"未驗證", domain, goog("carol@example.com", false), false},
		{"子網域", domain, goog("carol@mail.example.com", true), false},
		{"後綴相同", domain, goog("carol@evil-example.com", true), false},
		{"網域在前面", domain, goog("carol@example.com.evil.org", true), false},
		{"以最後一個 @ 為準", domain, goog("example.com@evil.org", true), false},
		{"沒有 @", domain, goog("example.com", true), false},
		{"@ 在結尾", domain, goog("carol@", true), false},
		{"網域規則對 github", domain, hubstore.ExternalIdentity{Provider: "github", Email: "a@example.com", EmailVerified: true}, false},
		{"空規則", hubconfig.Rule{Provider: hubconfig.GitHub}, gh("x"), false},
	}
	for _, c := range cases {
		if got := RuleHolds(c.rule, c.id); got != c.want {
			t.Errorf("%s：RuleHolds = %v，預期 %v", c.name, got, c.want)
		}
	}
}

func groupsOf(t *testing.T, e *env, userID string) []string {
	t.Helper()
	g, err := e.svc.Groups(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// noRules 拿掉自動規則（fakeIdP 的 GitHub 使用者在 friends 組織中）。
func noRules(cfg *hubconfig.Config) {
	g := cfg.Groups[hubconfig.GroupContributors]
	g.Rules = nil
	cfg.Groups[hubconfig.GroupContributors] = g
}

func withGroups(cfg *hubconfig.Config) {
	cfg.Groups[hubconfig.GroupContributors] = hubconfig.Group{Permissions: []string{"read", "download", "regenerate", "upload"},
		Rules: []hubconfig.Rule{{Provider: hubconfig.LinuxDo, TrustLevelGTE: intp(2)}, {Provider: hubconfig.GitHub, OrgMember: []string{"friends"}}}}
	cfg.Groups[hubconfig.GroupModerators] = hubconfig.Group{Permissions: []string{"read", "download", "moderate"}}
	cfg.Groups[hubconfig.GroupAdmins] = hubconfig.Group{Permissions: []string{"admin"}}
}

// 規則在登入時重新評估：成立就加入、不再成立就移除；其他供應商的規則與手動指派不受影響。
func TestApplySignInRules(t *testing.T) {
	e := newEnv(t, withGroups)
	ctx := context.Background()
	res, err := e.login(t, "linuxdo", "7") // fakeIdP 的 Linux.do trust_level 是 2
	if err != nil {
		t.Fatal(err)
	}
	uid := res.User.ID
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members", "contributors"}) {
		t.Fatalf("trust 2 登入後的群組 = %v", g)
	}
	low := hubstore.ExternalIdentity{Provider: "linuxdo", Subject: "7", Attrs: hubstore.IdentityAttrs{TrustLevel: intp(1)}}
	if err := e.svc.applySignIn(ctx, uid, low); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members"}) {
		t.Fatalf("trust 降到 1 後的群組 = %v", g)
	}
	// github 的規則給的成員資格，不會因為 linuxdo 登入而被移除。
	if err := e.svc.applySignIn(ctx, uid, hubstore.ExternalIdentity{Provider: "github", Subject: "9", Attrs: hubstore.IdentityAttrs{Orgs: []string{"friends"}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.applySignIn(ctx, uid, low); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members", "contributors"}) {
		t.Fatalf("github 規則仍成立時的群組 = %v", g)
	}
	// 手動指派：github 規則不再成立也保留。
	if _, err := e.store.SetManualGroup(ctx, uid, uid, hubconfig.GroupModerators, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.applySignIn(ctx, uid, hubstore.ExternalIdentity{Provider: "github", Subject: "9"}); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members", "moderators"}) {
		t.Fatalf("手動指派後的群組 = %v", g)
	}
	perms, _ := e.svc.AccountPermissions(ctx, uid)
	if strings.Join(perms, ",") != "read,download,moderate" {
		t.Errorf("權限 = %v", perms)
	}
}

// 設定檔的 admins：登入時加入；啟動時同步，設定檔移除的管理員立即失去權限；設定檔已經沒有的群組不計入。
func TestConfigAdmins(t *testing.T) {
	e := newEnv(t, withGroups, noRules, func(c *hubconfig.Config) {
		c.Admins = []hubconfig.Identity{{Provider: hubconfig.GitHub, Subject: "42"}}
	})
	ctx := context.Background()
	res, err := e.login(t, "github", "42")
	if err != nil {
		t.Fatal(err)
	}
	uid := res.User.ID
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members", "admins"}) {
		t.Fatalf("設定檔管理員登入後的群組 = %v", g)
	}
	perms, _ := e.svc.AccountPermissions(ctx, uid)
	if !slices.Equal(perms, hubconfig.Permissions) {
		t.Errorf("admin 的權限 = %v，預期全部", perms)
	}
	other, err := e.login(t, "linuxdo", "7")
	if err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, other.User.ID); !slices.Equal(g, []string{"members"}) {
		t.Fatalf("一般帳號的群組 = %v", g)
	}
	// 設定檔移除這位管理員後重新啟動（以新設定建立 Service 並同步）。
	e.cfg.Admins = nil
	if err := e.svc.SyncConfigAdmins(ctx); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members"}) {
		t.Fatalf("移除管理員後的群組 = %v", g)
	}
	// 再加回來：已經登入過的身分在啟動時就加入。
	e.cfg.Admins = []hubconfig.Identity{{Provider: hubconfig.GitHub, Subject: "42"}, {Provider: hubconfig.Google, Subject: "never-signed-in"}}
	if err := e.svc.SyncConfigAdmins(ctx); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, uid); !slices.Equal(g, []string{"members", "admins"}) {
		t.Fatalf("加回管理員後的群組 = %v", g)
	}
	// 設定檔沒有的群組（手動指派之後被刪掉）不列出、不計入權限。
	if _, err := e.store.SetManualGroup(ctx, uid, other.User.ID, "translators", true); err != nil {
		t.Fatal(err)
	}
	if g := groupsOf(t, e, other.User.ID); !slices.Equal(g, []string{"members"}) {
		t.Errorf("不存在的群組 = %v", g)
	}
}

func TestSetGroupGuards(t *testing.T) {
	e := newEnv(t, withGroups)
	res, err := e.login(t, "github", "1")
	if err != nil {
		t.Fatal(err)
	}
	p := &Principal{User: res.User}
	for group, want := range map[string]string{"admins": CodeAdminByConfig, "members": "invalid_request", "nope": "not_found"} {
		if c := code(e.svc.SetGroup(context.Background(), p, res.User.ID, group, true)); c != want {
			t.Errorf("SetGroup(%s) = %s，預期 %s", group, c, want)
		}
	}
	if c := code(e.svc.SetGroup(context.Background(), p, "missing", "moderators", true)); c != "not_found" {
		t.Errorf("不存在的使用者 = %s", c)
	}
}

func TestPATFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		tok, err := newPATToken()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidPATFormat(tok) || seen[tok] {
			t.Fatalf("token %q", tok)
		}
		seen[tok] = true
	}
	for _, bad := range []string{"", "nsh_", "nsh_" + strings.Repeat("a", 39), "nsh_" + strings.Repeat("a", 41),
		"nsh_" + strings.Repeat("a", 39) + "-", "NSH_" + strings.Repeat("a", 40), "nst_" + strings.Repeat("a", 40),
		"nsh_" + strings.Repeat("a", 39) + "é"} {
		if ValidPATFormat(bad) {
			t.Errorf("%q 不應該通過格式檢查", bad)
		}
	}
}

func FuzzValidPATFormat(f *testing.F) {
	f.Add("nsh_" + strings.Repeat("a", 40))
	f.Add("nsh_\x00")
	f.Fuzz(func(t *testing.T, s string) {
		if ValidPATFormat(s) && (len(s) != 44 || !strings.HasPrefix(s, PATPrefix)) {
			t.Fatalf("%q 通過格式檢查", s)
		}
	})
}

// 有效權限 = PAT ∩ 帳號：帳號的 admin 不會讓只有 read 的 PAT 變成 admin，PAT 的 admin 也不能超出帳號。
func TestEffectivePermissions(t *testing.T) {
	e := newEnv(t, withGroups, noRules)
	ctx := context.Background()
	res, err := e.login(t, "github", "1")
	if err != nil {
		t.Fatal(err)
	}
	u := res.User
	eff := func(perms ...string) string {
		got, err := e.svc.EffectivePermissions(ctx, &Principal{User: u, PAT: &hubstore.PAT{Permissions: perms}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, ",")
	}
	if g := eff("admin"); g != "read,download" {
		t.Errorf("一般帳號的 admin PAT = %s", g)
	}
	if g := eff("read", "upload"); g != "read" {
		t.Errorf("read+upload PAT = %s", g)
	}
	if g := eff(); g != "" {
		t.Errorf("沒有權限的 PAT = %s", g)
	}
	if err := e.store.SetConfigGroup(ctx, u.ID, hubconfig.GroupAdmins, true); err != nil {
		t.Fatal(err)
	}
	if g := eff("read"); g != "read" {
		t.Errorf("admin 帳號的 read PAT = %s", g)
	}
	if g := eff("admin"); g != strings.Join(hubconfig.Permissions, ",") {
		t.Errorf("admin 帳號的 admin PAT = %s", g)
	}
}
