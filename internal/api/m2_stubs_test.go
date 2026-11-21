package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m2StubCases 是 M2 S0 契約第 4 節的 29 條路由；這份清單獨立於 production 的 m2Stubs，
// 才抓得到 method、path、scope 寫錯。concrete 是帶入 path 參數後的實際路徑。
var m2StubCases = []struct{ method, path, scope string }{
	{"POST", "/api/items/i1/subtitles/import", auth.ScopeIngest},
	{"GET", "/api/ass-styles", auth.ScopeRead},
	{"POST", "/api/ass-styles", auth.ScopeAdmin},
	{"GET", "/api/ass-styles/s1", auth.ScopeRead},
	{"PATCH", "/api/ass-styles/s1", auth.ScopeAdmin},
	{"POST", "/api/ass-styles/preview", auth.ScopeRead},
	{"POST", "/api/ass-styles/import", auth.ScopeAdmin},
	{"GET", "/api/profiles", auth.ScopeRead},
	{"POST", "/api/profiles", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard", auth.ScopeRead},
	{"PATCH", "/api/profiles/standard", auth.ScopeAdmin},
	{"DELETE", "/api/profiles/standard", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard/versions", auth.ScopeRead},
	{"POST", "/api/profiles/standard/default", auth.ScopeAdmin},
	{"GET", "/api/channels/c1/overrides", auth.ScopeRead},
	{"PATCH", "/api/channels/c1/overrides", auth.ScopeAdmin},
	{"GET", "/api/prompts", auth.ScopeRead},
	{"POST", "/api/prompts", auth.ScopeAdmin},
	{"DELETE", "/api/prompts/p1", auth.ScopeAdmin},
	{"GET", "/api/prompts/p1/versions", auth.ScopeRead},
	{"POST", "/api/prompts/p1/versions", auth.ScopeAdmin},
	{"POST", "/api/prompts/p1/default", auth.ScopeAdmin},
	{"POST", "/api/prompts/playground", auth.ScopeIngest},
	{"POST", "/api/prompts/p1/compare", auth.ScopeIngest},
	{"GET", "/api/glossaries/channel/c1", auth.ScopeRead},
	{"PUT", "/api/glossaries/channel/c1", auth.ScopeAdmin},
	{"GET", "/api/channels/c1/term-candidates", auth.ScopeRead},
	{"POST", "/api/items/i1/switch-executor", auth.ScopeIngest},
	{"POST", "/api/tasks/t1/switch-executor", auth.ScopeIngest},
}

// TestM2StubRoutes 用同一份清單驗證每條 stub：
// 沒有身分 401；token 缺 scope 403 並帶正確的 required_scope；scope 正確 501 not_implemented；
// owner session（帶 CSRF）501。
func TestM2StubRoutes(t *testing.T) {
	if len(m2StubCases) != 29 {
		t.Fatalf("契約第 4 節有 29 條路由，測試清單有 %d 條", len(m2StubCases))
	}
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { svc = d.Auth })

	allScopes := []string{auth.ScopeRead, auth.ScopeIngest, auth.ScopeAdmin}
	exact := map[string]string{}  // 剛好只有這個 scope 的 token
	others := map[string]string{} // 有其他全部 scope、唯獨缺這個的 token
	for _, sc := range allScopes {
		ct, err := svc.CreateToken(context.Background(), "only-"+sc, []string{sc}, nil)
		if err != nil {
			t.Fatal(err)
		}
		exact[sc] = ct.Token
		var rest []string
		for _, o := range allScopes {
			if o != sc {
				rest = append(rest, o)
			}
		}
		rest = append(rest, auth.ScopeAgent)
		ct, err = svc.CreateToken(context.Background(), "without-"+sc, rest, nil)
		if err != nil {
			t.Fatal(err)
		}
		others[sc] = ct.Token
	}

	for _, c := range m2StubCases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			// 沒有身分：401。
			if rec := r.do(c.method, c.path, "", nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("沒有身分 = %d %s，預期 401", rec.Code, rec.Body)
			}
			// 缺 scope：403，required_scope 是這條路由要求的 scope。
			for _, tok := range []string{others[c.scope], exact[otherScope(c.scope)]} {
				rec := r.do(c.method, c.path, "", withBearer(tok))
				e := decodeError(t, rec.Body.Bytes())
				if rec.Code != http.StatusForbidden || e.Code != CodeForbidden || e.RequiredScope != c.scope {
					t.Errorf("缺 scope = %d %s，預期 403 required_scope=%s", rec.Code, rec.Body, c.scope)
				}
			}
			// scope 正確：501 not_implemented。
			rec := r.do(c.method, c.path, "", withBearer(exact[c.scope]))
			if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotImplemented || e.Code != CodeNotImplemented {
				t.Errorf("scope 正確 = %d %s，預期 501 not_implemented", rec.Code, rec.Body)
			}
			// owner session（帶 CSRF）：501。
			rec = r.owner(c.method, c.path, "")
			if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotImplemented || e.Code != CodeNotImplemented {
				t.Errorf("owner = %d %s，預期 501 not_implemented", rec.Code, rec.Body)
			}
		})
	}
}

// otherScope 回傳另一個和 scope 不同的 scope，用來造出「只有別的 scope」的 token。
func otherScope(scope string) string {
	if scope == auth.ScopeRead {
		return auth.ScopeAdmin
	}
	return auth.ScopeRead
}

// production 的 m2Stubs 要和測試清單一一對應（method、path 樣板、scope），避免漏註冊或多註冊。
func TestM2StubsMatchContract(t *testing.T) {
	used := make([]int, len(m2Stubs))
	for _, c := range m2StubCases {
		n := 0
		for i, s := range m2Stubs {
			if s.Method == c.method && pathMatches(s.Path, c.path) {
				used[i]++
				n++
				if s.Scope != c.scope {
					t.Errorf("%s %s 的 scope = %s，契約是 %s", c.method, c.path, s.Scope, c.scope)
				}
			}
		}
		if n != 1 {
			t.Errorf("%s %s 在 m2Stubs 中對應到 %d 條", c.method, c.path, n)
		}
	}
	for i, s := range m2Stubs {
		if used[i] != 1 {
			t.Errorf("m2Stubs 的 %s %s 沒有對應到契約的路由", s.Method, s.Path)
		}
		if s.ID == "" || s.Summary == "" {
			t.Errorf("m2Stubs 項目不完整：%+v", s)
		}
	}
}

// pathMatches 判斷實際路徑是否符合帶 {param} 的樣板。
func pathMatches(tmpl, path string) bool {
	ts, ps := strings.Split(tmpl, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if strings.HasPrefix(ts[i], "{") {
			continue
		}
		if ts[i] != ps[i] {
			return false
		}
	}
	return true
}

// rerun 帶 profile 或 chunks：501 not_implemented（不是 422），而且沒有新增 stage run。
func TestRerunProfileAndChunksNotImplemented(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123xxxxx"}`)).Items[0].ID
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := r.owner("GET", "/api/items/"+id, "").Body.String()
	for _, body := range []string{
		`{"from":"normalize","profile":"mixed"}`,
		`{"from":"normalize","chunks":[2,3]}`,
		`{"from":"normalize","profile":"mixed","chunks":[1]}`,
	} {
		rec := r.owner("POST", "/api/items/"+id+"/rerun", body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotImplemented || e.Code != CodeNotImplemented {
			t.Errorf("%s = %d %s，預期 501 not_implemented", body, rec.Code, rec.Body)
		}
	}
	if after := r.owner("GET", "/api/items/"+id, "").Body.String(); after != before {
		t.Errorf("501 之後影音不應有變動：\n前 %s\n後 %s", before, after)
	}
	// 沒帶 profile、chunks 的重跑照常。
	if rec := r.owner("POST", "/api/items/"+id+"/rerun", `{"from":"normalize"}`); rec.Code != http.StatusAccepted {
		t.Errorf("一般重跑 = %d %s", rec.Code, rec.Body)
	}
}
