package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// failingHTTP 讓每個對外請求都以 err 失敗，模擬 netguard 擋下請求。
type failingHTTP struct{ err error }

func (f failingHTTP) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// GRD 介面：被防護擋下的辨識請求回 422，code 與訊息對應規則。
func TestResolveMapsPolicyErrorsTo422(t *testing.T) {
	for name, tc := range map[string]struct {
		err           error
		code, message string
	}{
		"內部網路": {fmt.Errorf("%w: 192.168.1.1", netguard.ErrBlockedAddress), CodeBlockedAddress, "這個網址指向內部網路，已封鎖。"},
		"轉址太多": {netguard.ErrTooManyRedirects, CodeTooManyRedirects, "轉址超過 5 次。"},
		"回應太大": {&netguard.TooLargeError{Limit: netguard.LimitFeed}, CodeResponseTooLarge, "回應超過大小上限（10 MiB）。"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newItemsRig(t, func(d *pipeline.Deps) {
				d.HTTP = &http.Client{Transport: failingHTTP{err: tc.err}}
			})
			rec := r.owner("POST", "/api/resolve", `{"url":"https://feeds.example.com/show.xml"}`)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
			}
			if e := decode[Error](t, rec); e.Code != tc.code || e.Message != tc.message {
				t.Errorf("error = %+v，預期 %s／%s", e, tc.code, tc.message)
			}
		})
	}
}

// 不是防護造成的失敗仍是「無法辨識」，不要被誤判成防護。
func TestResolveOtherFetchFailuresStayUnrecognized(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.HTTP = &http.Client{Transport: failingHTTP{err: fmt.Errorf("connection refused")}}
	})
	rec := r.owner("POST", "/api/resolve", `{"url":"https://feeds.example.com/show.xml"}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Code != CodeUnrecognizedURL {
		t.Fatalf("status = %d, error = %+v", rec.Code, e)
	}
}

// 防護的錯誤用同一個函式對應，POST /api/items 的 mapErr 也一致。
func TestMapErrHandlesPolicyErrorsLikeResolve(t *testing.T) {
	rt := &routes{} // 防護的錯誤在寫 log 之前就對應完成，不需要 Deps
	err := rt.mapErr(context.Background(), &pipeline.PolicyError{Msg: "轉址超過 5 次。", Err: netguard.ErrTooManyRedirects})
	e := asError(err)
	if e.Status != http.StatusUnprocessableEntity || e.Code != CodeTooManyRedirects || e.Message != "轉址超過 5 次。" {
		t.Errorf("e = %+v", e)
	}
}

// SEC、OPS 的場景會打 GET /api/system 與 GET /api/providers/{id}；實作之前回 501，而不是 404。
func TestSystemAndProviderPlaceholdersAreNotImplemented(t *testing.T) {
	r := newItemsRig(t)
	for _, path := range []string{"/api/system", "/api/providers/whisperx"} {
		rec := r.owner("GET", path, "")
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("GET %s：status = %d，預期 501", path, rec.Code)
		}
		if e := decode[Error](t, rec); e.Code != CodeNotImplemented {
			t.Errorf("GET %s：code = %q", path, e.Code)
		}
	}
	// 未登入時仍然先被驗證層擋下。
	if rec := r.do("GET", "/api/system", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("未登入：status = %d", rec.Code)
	}
}
