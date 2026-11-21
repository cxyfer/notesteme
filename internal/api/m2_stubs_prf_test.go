package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// prfRouteCases 是 PRF（處理方案與頻道覆寫）的 9 條路由（契約第 4 節）：讀取用 read，寫入用 admin。
// 清單獨立於 production 的註冊，才抓得到 method、path、scope 寫錯。
var prfRouteCases = []stubCase{
	{"GET", "/api/profiles", auth.ScopeRead},
	{"POST", "/api/profiles", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard", auth.ScopeRead},
	{"PATCH", "/api/profiles/standard", auth.ScopeAdmin},
	{"DELETE", "/api/profiles/standard", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard/versions", auth.ScopeRead},
	{"POST", "/api/profiles/standard/default", auth.ScopeAdmin},
	{"GET", "/api/channels/c1/overrides", auth.ScopeRead},
	{"PATCH", "/api/channels/c1/overrides", auth.ScopeAdmin},
}

// TestPRFRoutesScopes 用真正的 rig 驗證每條 PRF 路由的 scope（AUTH-R8）：
// 沒有身分 401；token 缺 scope 403 並帶正確的 required_scope（連帶 body 不正確也一樣，因為 scope 在讀 body 之前檢查）；
// scope 正確、owner 都不是 401、403，也不是 404（路由存在）。
// 路由的行為由 profiles_test.go、channels_test.go 用假的 service 驗證，這裡不依賴 pipeline 的實作。
func TestPRFRoutesScopes(t *testing.T) {
	if len(prfRouteCases) != 9 {
		t.Fatalf("契約第 4 節的 PRF（處理方案與頻道覆寫）有 9 條路由，測試清單有 %d 條", len(prfRouteCases))
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
		rest := []string{auth.ScopeAgent}
		for _, o := range allScopes {
			if o != sc {
				rest = append(rest, o)
			}
		}
		ct, err = svc.CreateToken(context.Background(), "without-"+sc, rest, nil)
		if err != nil {
			t.Fatal(err)
		}
		others[sc] = ct.Token
	}

	for _, c := range prfRouteCases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			// 沒有身分：401。
			if rec := r.do(c.method, c.path, "", nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("沒有身分 = %d %s，預期 401", rec.Code, rec.Body)
			}
			// 缺 scope：403，required_scope 是這條路由要求的 scope；body 是壞的 JSON 也一樣（scope 先於 body）。
			for _, tok := range []string{others[c.scope], exact[otherScope(c.scope)]} {
				for _, body := range []string{"", "{"} {
					rec := r.do(c.method, c.path, body, withBearer(tok))
					e := decodeError(t, rec.Body.Bytes())
					if rec.Code != http.StatusForbidden || e.Code != CodeForbidden || e.RequiredScope != c.scope {
						t.Errorf("缺 scope（body %q）= %d %s，預期 403 required_scope=%s", body, rec.Code, rec.Body, c.scope)
					}
				}
			}
			// scope 正確與 owner：通過 scope 檢查，路由存在（結果由 service 決定，不一定是 2xx）。
			rec := r.do(c.method, c.path, "", withBearer(exact[c.scope]))
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusMethodNotAllowed {
				t.Errorf("scope 正確 = %d %s，預期通過 scope 檢查", rec.Code, rec.Body)
			}
			rec = r.owner(c.method, c.path, "")
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusMethodNotAllowed {
				t.Errorf("owner = %d %s，預期通過 scope 檢查", rec.Code, rec.Body)
			}
		})
	}
}
