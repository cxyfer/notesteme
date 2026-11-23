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
