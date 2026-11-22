package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// prmRouteCases 是 PRM（prompt 庫，不含 playground 與 compare）的 6 條路由，依「能順利執行」的順序排列：
// 後面的路由用到前面建立的 prompt。scope 是契約第 4 節的要求，want 是 scope 正確時的成功狀態。
// playground 與 compare 已經實作，見 prompts_playground_test.go。
var prmRouteCases = []struct {
	method, path, body, scope string
	want                      int
}{
	{"POST", "/api/prompts", `{"id":"scope-test","kind":"note","body":"x"}`, auth.ScopeAdmin, 201},
	{"POST", "/api/prompts/scope-test/versions", `{"body":"y"}`, auth.ScopeAdmin, 201},
	{"POST", "/api/prompts/scope-test/default", `{"version":2}`, auth.ScopeAdmin, 200},
	{"GET", "/api/prompts", "", auth.ScopeRead, 200},
	{"GET", "/api/prompts/correct-default/versions", "", auth.ScopeRead, 200},
	{"DELETE", "/api/prompts/scope-test", "", auth.ScopeAdmin, 204},
}

// 6 條 PRM 路由都是真正的實作：沒有身分 401；缺 scope 403 並帶正確的 required_scope，而且沒有副作用；
// scope 正確時不是 501，而是預期的成功狀態。
func TestPRMRoutesScopes(t *testing.T) {
	if len(prmRouteCases) != 6 {
		t.Fatalf("契約第 4 節的 PRM（prompt 庫，不含 playground 與 compare）有 6 條路由，測試清單有 %d 條", len(prmRouteCases))
	}
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) {
		svc = d.Auth
		d.Prompts = prompts.New(prompts.Deps{Store: d.Store, Clock: d.Clock})
	})

	token := func(name string, scopes ...string) string {
		t.Helper()
		ct, err := svc.CreateToken(context.Background(), name, scopes, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ct.Token
	}
	// 每個 scope 各一個「只有它」與「缺它」的 token。
	only, without := map[string]string{}, map[string]string{}
	for _, sc := range []string{auth.ScopeRead, auth.ScopeIngest, auth.ScopeAdmin} {
		only[sc] = token("only-"+sc, sc)
		var rest []string
		for _, o := range []string{auth.ScopeRead, auth.ScopeIngest, auth.ScopeAdmin, auth.ScopeAgent} {
			if o != sc {
				rest = append(rest, o)
			}
		}
		without[sc] = token("without-"+sc, rest...)
	}

	// 先確認每條路由缺 scope 時被擋下。這一段全部跑完之後，資料庫裡還不該有 scope-test。
	for _, c := range prmRouteCases {
		if rec := r.do(c.method, c.path, c.body, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 沒有身分 = %d %s，預期 401", c.method, c.path, rec.Code, rec.Body)
		}
		for _, tok := range []string{without[c.scope], only[otherScope(c.scope)]} {
			rec := r.do(c.method, c.path, c.body, withBearer(tok))
			e := decodeError(t, rec.Body.Bytes())
			if rec.Code != http.StatusForbidden || e.Code != CodeForbidden || e.RequiredScope != c.scope {
				t.Errorf("%s %s 缺 scope = %d %s，預期 403 required_scope=%s", c.method, c.path, rec.Code, rec.Body, c.scope)
			}
		}
	}
	if rec := r.do("GET", "/api/prompts", "", withBearer(only[auth.ScopeRead])); rec.Code != 200 || strings.Contains(rec.Body.String(), "scope-test") {
		t.Fatalf("缺 scope 的請求不該有副作用：%d %s", rec.Code, rec.Body)
	}

	// 用只有這個 scope 的 token 依序呼叫（寫入用 admin，讀取用 read）：不是 501，而且是預期的成功狀態。
	for _, c := range prmRouteCases {
		rec := r.do(c.method, c.path, c.body, withBearer(only[c.scope]))
		if rec.Code == http.StatusNotImplemented {
			t.Errorf("%s %s scope 正確卻是 501：%s", c.method, c.path, rec.Body)
		}
		if rec.Code != c.want {
			t.Errorf("%s %s scope 正確 = %d %s，預期 %d", c.method, c.path, rec.Code, rec.Body, c.want)
		}
	}
}
