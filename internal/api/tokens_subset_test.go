package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// AUTH-R8：token 不隱含繼承 scope。用 admin token 建立 token 時，新 token 的 scope 必須是呼叫者 scope 的子集，
// 否則只有 admin 的 token 可以替自己發出帶 ingest、agent 的 token。owner（session、本機）不受限制。
func TestTokenCannotMintScopesItLacks(t *testing.T) {
	r := newTokenRig(t)
	admin, err := r.auth.CreateToken(context.Background(), "admin-only", []string{auth.ScopeAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.do("POST", "/api/tokens", `{"name":"escalate","scopes":["admin","ingest"]}`, withBearer(admin.Token))
	if rec.Code != 403 {
		t.Fatalf("admin token 建立帶 ingest 的 token = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		RequiredScope string `json:"required_scope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RequiredScope != auth.ScopeIngest {
		t.Errorf("required_scope = %q：%s", body.RequiredScope, rec.Body)
	}
	if rec := r.do("POST", "/api/tokens", `{"name":"same","scopes":["admin"]}`, withBearer(admin.Token)); rec.Code != 201 {
		t.Fatalf("子集可以建立：%d %s", rec.Code, rec.Body)
	}
	wide, err := r.auth.CreateToken(context.Background(), "wide", []string{auth.ScopeAdmin, auth.ScopeRead, auth.ScopeIngest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := r.do("POST", "/api/tokens", `{"name":"narrow","scopes":["read","ingest"]}`, withBearer(wide.Token)); rec.Code != 201 {
		t.Fatalf("子集可以建立：%d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/tokens", `{"name":"owner","scopes":["read","agent","ingest","admin"]}`); rec.Code != 201 {
		t.Fatalf("owner 不受限制：%d %s", rec.Code, rec.Body)
	}
}
