package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/vault"
)

// VAULT-R1：GET /api/vaults 只列出 id、adapter、base，不回傳路徑、remote 與作者。
func TestListVaults(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.Vaults = map[string]vault.Def{
			"zeta": {ID: "zeta", Adapter: vault.AdapterGit, Remote: "https://secret.example/v.git", Author: "A <a@b>", Base: "VN"},
			"main": {ID: "main", Adapter: vault.AdapterFS, Path: "/srv/private-vault", Base: "Video Notes"},
		}
	})
	rec := r.owner(http.MethodGet, "/api/vaults", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("狀態 %d：%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `{"id":"main","adapter":"fs","base":"Video Notes"}`) || strings.Index(body, `"main"`) > strings.Index(body, `"zeta"`) {
		t.Errorf("回應 = %s", body)
	}
	for _, leak := range []string{"/srv/private-vault", "secret.example", "a@b"} {
		if strings.Contains(body, leak) {
			t.Errorf("回應洩漏了 %q：%s", leak, body)
		}
	}
	if rec := r.do(http.MethodGet, "/api/vaults", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分：%d", rec.Code)
	}
}

// VAULT-R10：GET /api/hooks 只列出 id，不回傳 argv、timeout 與 env_allowlist；需要 admin。
func TestListHooks(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.Hooks = map[string]vault.Hook{
			"zz-sync": {ID: "zz-sync", Argv: []string{"/usr/bin/sync-tool", "--token=s3cr3t"}, EnvAllowlist: []string{"SYNC_HOME"}},
			"notify":  {ID: "notify", Argv: []string{"notify-send", "{{title}}"}},
		}
	})
	rec := r.owner(http.MethodGet, "/api/hooks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("狀態 %d：%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"hooks":[{"id":"notify"},{"id":"zz-sync"}]`) {
		t.Errorf("回應 = %s", body)
	}
	for _, leak := range []string{"sync-tool", "s3cr3t", "SYNC_HOME", "notify-send"} {
		if strings.Contains(body, leak) {
			t.Errorf("回應洩漏了 %q：%s", leak, body)
		}
	}
	if rec := r.do(http.MethodGet, "/api/hooks", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分：%d", rec.Code)
	}
}
