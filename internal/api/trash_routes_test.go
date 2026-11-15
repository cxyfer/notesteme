package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// DEL-B2 的路由已註冊（不是 404），實作前一律回 501 not_implemented；不帶 body 的 DELETE 也能通過驗證。
func TestDeleteRoutesAnswer501(t *testing.T) {
	r := newItemsRig(t)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/items/some-id/deletion-preview", ""},
		{"DELETE", "/api/trash/some-id", ""},
		{"DELETE", "/api/trash", ""},
	} {
		rec := r.owner(c.method, c.path, c.body)
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusNotImplemented || e.Code != CodeNotImplemented {
			t.Errorf("%s %s = %d %s，預期 501 not_implemented", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// 沒有登入或缺 CSRF 的請求在路由之前就被擋下。
func TestDeleteRoutesNeedOwnerSession(t *testing.T) {
	r := newItemsRig(t)
	if rec := r.do("GET", "/api/trash", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有登入：%d，預期 401", rec.Code)
	}
	noCSRF := withCookies(r.sess, r.csrf, "")
	if rec := r.do("DELETE", "/api/trash", "", noCSRF); rec.Code != http.StatusForbidden {
		t.Errorf("缺 CSRF：%d，預期 403", rec.Code)
	}
}

// token 不能呼叫（DEL-R12）；owner 與本機通過。
func TestRequireOwnerRejectsTokens(t *testing.T) {
	ctx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: "token"})
	err := requireOwner(ctx, "刪除影音")
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != CodeForbidden {
		t.Errorf("token：%v，預期 403 forbidden", err)
	}
	for _, kind := range []string{"session", PrincipalLocal} {
		ctx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: kind})
		if err := requireOwner(ctx, "刪除影音"); err != nil {
			t.Errorf("%s：%v，預期通過", kind, err)
		}
	}
}

// 垃圾桶中的影音：讀取回 404（mapErr），內容帶訊息、動作與影音 ID；新增回 409 時用同一個 helper。
func TestInTrashErrorShape(t *testing.T) {
	rt := &routes{d: Deps{}}
	err := rt.mapErr(context.Background(), fmt.Errorf("包一層：%w", &pipeline.InTrashError{ItemID: "item-1"}))
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("mapErr = %v，預期 404", err)
	}
	raw, _ := json.Marshal(e)
	want := `{"code":"in_trash","message":"這部影音在垃圾桶中。","actions":["還原"],"item_id":"item-1"}`
	if string(raw) != want {
		t.Errorf("內容 = %s，預期 %s", raw, want)
	}
	if c := inTrashError(http.StatusConflict, "x"); c.Status != http.StatusConflict || c.Code != CodeInTrash {
		t.Errorf("409：%+v", c)
	}
}
