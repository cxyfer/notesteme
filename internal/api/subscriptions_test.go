package api

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subscription"
	"github.com/cxyfer/notesteme/internal/validate"
)

// fakeSubs 是訂閱 service 的替身：err 非 nil 時每個方法都回傳它，並記下收到的參數。
type fakeSubs struct {
	err      error
	created  subscription.CreateInput
	preview  subscription.PreviewInput
	url, id  string
	imported []byte
}

func (f *fakeSubs) Create(_ context.Context, in subscription.CreateInput) (subscription.View, error) {
	f.created = in
	return subscription.View{ID: "s1", Kind: "channel"}, f.err
}
func (f *fakeSubs) List(context.Context) ([]subscription.View, error) { return nil, f.err }
func (f *fakeSubs) Get(context.Context, string) (subscription.View, error) {
	return subscription.View{}, f.err
}
func (f *fakeSubs) Patch(context.Context, string, subscription.PatchInput) (subscription.View, error) {
	return subscription.View{}, f.err
}
func (f *fakeSubs) Delete(context.Context, string) error { return f.err }
func (f *fakeSubs) Entries(context.Context, string, string, int) ([]subscription.EntryView, error) {
	return nil, f.err
}
func (f *fakeSubs) CheckNow(context.Context, string) (time.Time, error) { return time.Time{}, f.err }
func (f *fakeSubs) PreviewNew(_ context.Context, u string, in subscription.PreviewInput) (subscription.Preview, error) {
	f.url, f.preview = u, in
	return subscription.Preview{WouldProcess: []subscription.PreviewRow{}, WouldSkip: []subscription.PreviewSkip{}}, f.err
}
func (f *fakeSubs) PreviewExisting(_ context.Context, id string, in subscription.PreviewInput) (subscription.Preview, error) {
	f.id, f.preview = id, in
	return subscription.Preview{WouldProcess: []subscription.PreviewRow{}, WouldSkip: []subscription.PreviewSkip{}}, f.err
}
func (f *fakeSubs) Import(_ context.Context, data []byte) (subscription.ImportResult, error) {
	f.imported = data
	return subscription.ImportResult{Created: 1, Duplicates: []string{}, Failed: []subscription.ImportFailure{}}, f.err
}

func newSubsRig(t *testing.T, f SubscriptionService) (*itemsRig, *auth.Service) {
	t.Helper()
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { d.Subscriptions = f; svc = d.Auth })
	return r, svc
}

func multipartImport(t *testing.T, field string, content []byte) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile(field, "subs.opml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(content)
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func (r *itemsRig) ownerMultipart(path, body, contentType string) *http.Response {
	r.t.Helper()
	rec := r.do(http.MethodPost, path, body, func(req *http.Request) {
		withCookies(r.sess, r.csrf, r.csrf)(req)
		req.Header.Set("Content-Type", contentType)
	})
	return rec.Result()
}

// 錯誤對應（SUB-R1、R6、R7、R12）：每種 service 錯誤對應到固定的狀態碼、code 與欄位。
func TestSubscriptionErrorMapping(t *testing.T) {
	f := &fakeSubs{}
	r, _ := newSubsRig(t, f)
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		check  func(t *testing.T, e Error)
	}{
		{"url", &subscription.URLError{Code: subscription.CodeNotACollection, Message: "這是單一影片，請直接新增，不需要訂閱。"}, 422, CodeNotACollection, nil},
		{"fields", &subscription.FieldsError{Fields: []validate.FieldError{{Path: "backfill.count", Message: "x"}}}, 422, CodeInvalidInput,
			func(t *testing.T, e Error) {
				if len(e.Errors) != 1 || e.Errors[0].Path != "backfill.count" {
					t.Errorf("errors = %+v", e.Errors)
				}
			}},
		{"duplicate", &subscription.DuplicateError{SubscriptionID: "old"}, 409, CodeDuplicateSubscription,
			func(t *testing.T, e Error) {
				if e.SubscriptionID != "old" || e.Message != "已經訂閱了這個來源。" {
					t.Errorf("409 = %+v", e)
				}
			}},
		{"not found", store.ErrNotFound, 404, CodeNotFound, nil},
		{"rate limited", &subscription.RateLimitedError{RetryAfter: 1500 * time.Millisecond}, 429, CodeRateLimited,
			func(t *testing.T, e Error) {
				if e.RetryAfterS == nil || *e.RetryAfterS != 2 || e.Message != "這個平台的請求太頻繁，請稍後再試。" {
					t.Errorf("429 = %+v", e)
				}
			}},
		{"rate limited 0", &subscription.RateLimitedError{}, 429, CodeRateLimited,
			func(t *testing.T, e Error) {
				if e.RetryAfterS == nil || *e.RetryAfterS != 1 {
					t.Errorf("retry_after_s 至少 1：%+v", e)
				}
			}},
		{"preview upstream", &subscription.PreviewError{Class: errclass.ClassPermanent, Message: "訂閱來源已不存在（HTTP 404）。請確認網址，或刪除這個訂閱。"}, 502, CodeUpstream, nil},
		{"too large", subscription.ImportTooLarge(), 413, CodeImportTooLarge, nil},
		{"too many", &subscription.ImportError{Code: subscription.CodeImportTooMany, Message: "m"}, 422, CodeImportTooMany, nil},
		{"format", &subscription.ImportError{Code: subscription.CodeImportFormat, Message: "m"}, 422, CodeImportFormat, nil},
		{"internal", errors.New("db is gone"), 500, CodeInternal,
			func(t *testing.T, e Error) {
				if strings.Contains(e.Message, "db is gone") {
					t.Errorf("內部錯誤不能外洩：%q", e.Message)
				}
			}},
	}
	opml, ct := multipartImport(t, "file", []byte("<opml/>"))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.err = c.err
			for _, req := range []struct{ method, path, body string }{
				{"POST", "/api/subscriptions", `{"url":"x"}`},
				{"POST", "/api/subscriptions/preview", `{"url":"x"}`},
				{"POST", "/api/subscriptions/s1/preview", ``},
				{"GET", "/api/subscriptions/s1", ``},
				{"POST", "/api/subscriptions/import", ``},
			} {
				var status int
				var body []byte
				if req.path == "/api/subscriptions/import" {
					resp := r.ownerMultipart(req.path, opml, ct)
					status = resp.StatusCode
					var buf bytes.Buffer
					_, _ = buf.ReadFrom(resp.Body)
					body = buf.Bytes()
				} else {
					rec := r.owner(req.method, req.path, req.body)
					status, body = rec.Code, rec.Body.Bytes()
				}
				// 預覽與匯入專屬的錯誤只會從那兩條路由出現；其他路由交給共用的 mapErr（500）。
				onlyPreview := c.code == CodeRateLimited || c.code == CodeUpstream || strings.HasPrefix(c.name, "too") || c.name == "format"
				want := c.status
				if onlyPreview && !strings.Contains(req.path, "preview") && !strings.Contains(req.path, "import") {
					want = 500
				}
				if status != want {
					t.Errorf("%s %s = %d %s，預期 %d", req.method, req.path, status, body, want)
					continue
				}
				e := decodeError(t, body)
				if want == c.status && e.Code != c.code {
					t.Errorf("%s %s code = %s，預期 %s", req.method, req.path, e.Code, c.code)
				}
				if want == c.status && c.check != nil {
					c.check(t, e)
				}
			}
		})
	}
}

// 預覽的參數照原樣交給 service；body 可以省略。
func TestSubscriptionPreviewInput(t *testing.T) {
	f := &fakeSubs{}
	r, _ := newSubsRig(t, f)
	rec := r.owner("POST", "/api/subscriptions/preview", `{"url":"https://www.youtube.com/channel/UCfixture000000000000000","backfill":{"mode":"recent","count":5},"filters":{"title_exclude":"x","max_duration_s":60}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"would_process":[]`) || !strings.Contains(rec.Body.String(), `"would_skip":[]`) {
		t.Fatalf("預覽 = %d %s", rec.Code, rec.Body)
	}
	if f.url != "https://www.youtube.com/channel/UCfixture000000000000000" || f.preview.Backfill.Mode != "recent" || *f.preview.Backfill.Count != 5 ||
		*f.preview.Filters.TitleExclude != "x" || *f.preview.Filters.MaxDurationS != 60 {
		t.Errorf("參數 = %q %+v", f.url, f.preview)
	}
	if rec := r.owner("POST", "/api/subscriptions/preview", ``); rec.Code != 200 || f.url != "" {
		t.Errorf("省略 body：%d %q", rec.Code, f.url)
	}
	if rec := r.owner("POST", "/api/subscriptions/abc/preview", ``); rec.Code != 200 || f.id != "abc" || f.preview.Backfill != nil || f.preview.Filters != nil {
		t.Errorf("既有訂閱：%d %q %+v", rec.Code, f.id, f.preview)
	}
	if rec := r.owner("POST", "/api/subscriptions/abc/preview", `{"backfill":{"mode":"all"}}`); rec.Code != 200 || f.preview.Backfill.Mode != "all" {
		t.Errorf("覆蓋：%d %+v", rec.Code, f.preview)
	}
	// 建立時 REST 不能指定標題（SUB-R1）：多出來的 title 不會傳給 service。
	if rec := r.owner("POST", "/api/subscriptions", `{"url":"x","backfill":{"mode":"none"}}`); rec.Code != 201 || f.created.URL != "x" || f.created.Backfill.Mode != "none" {
		t.Errorf("建立：%d %+v", rec.Code, f.created)
	}
}

// 匯入：檔案內容照原樣交給 service；大小在讀取本體之前與之後都檢查，回 413 too_large；
// 沒有 admin 時不讀本體，回 403；缺少 file 欄位是 422。
func TestSubscriptionImportRoute(t *testing.T) {
	f := &fakeSubs{}
	r, svc := newSubsRig(t, f)
	body, ct := multipartImport(t, "file", []byte("Channel Id,Channel Url,Channel Title\n"))
	resp := r.ownerMultipart("/api/subscriptions/import", body, ct)
	if resp.StatusCode != 200 || string(f.imported) != "Channel Id,Channel Url,Channel Title\n" {
		t.Fatalf("匯入 = %d %q", resp.StatusCode, f.imported)
	}

	tooLarge := func(name string, resp *http.Response) {
		t.Helper()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		e := decodeError(t, buf.Bytes())
		if resp.StatusCode != 413 || e.Code != CodeImportTooLarge || e.Message != "匯入檔超過大小上限（1 MiB）。" {
			t.Errorf("%s = %d %s", name, resp.StatusCode, buf.String())
		}
	}
	// 檔案剛好超過 1 MiB、本體在 multipart 的餘裕內：service 收到的資料超過上限（service 判斷），這裡由 service 替身回 413。
	f.imported = nil
	big, ct := multipartImport(t, "file", bytes.Repeat([]byte("a"), subscription.MaxImportBytes+1))
	f.err = subscription.ImportTooLarge()
	tooLarge("檔案略大", r.ownerMultipart("/api/subscriptions/import", big, ct))
	if len(f.imported) != subscription.MaxImportBytes+1 {
		t.Errorf("handler 最多讀上限加 1：%d", len(f.imported))
	}
	f.err = nil
	// 本體超過上限加餘裕：middleware 直接回 413，service 不會被呼叫。
	f.imported = nil
	huge, ct := multipartImport(t, "file", bytes.Repeat([]byte("a"), subscription.MaxImportBytes+importFormOverhead+1))
	tooLarge("本體過大", r.ownerMultipart("/api/subscriptions/import", huge, ct))
	if f.imported != nil {
		t.Error("本體過大時不該呼叫 service")
	}
	// Content-Length 宣告過大（實際本體沒有那麼大）：不讀本體，直接 413。
	rec := r.do("POST", "/api/subscriptions/import", "x", func(req *http.Request) {
		withCookies(r.sess, r.csrf, r.csrf)(req)
		req.Header.Set("Content-Type", ct)
		req.ContentLength = 50 << 20
	})
	tooLarge("Content-Length", rec.Result())

	// 缺少 file 欄位。
	other, ct := multipartImport(t, "other", []byte("x"))
	resp = r.ownerMultipart("/api/subscriptions/import", other, ct)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if e := decodeError(t, buf.Bytes()); resp.StatusCode != 422 || len(e.Errors) != 1 || e.Errors[0].Path != "file" {
		t.Errorf("缺 file = %d %s", resp.StatusCode, buf.String())
	}

	// read、ingest 的 token：403（即使本體過大，也先檢查 scope）。
	tok, err := svc.CreateToken(context.Background(), "ri", []string{auth.ScopeRead, auth.ScopeIngest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec = r.do("POST", "/api/subscriptions/import", string(huge), func(req *http.Request) {
		withBearer(tok.Token)(req)
		req.Header.Set("Content-Type", ct)
	})
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 403 || e.RequiredScope != auth.ScopeAdmin {
		t.Errorf("read,ingest = %d %s", rec.Code, rec.Body)
	}
	// 其他路徑、方法不受 middleware 影響。
	if rec := r.owner("GET", "/api/subscriptions/import", ""); rec.Code == 413 {
		t.Errorf("GET 不該被限制：%d", rec.Code)
	}
}

// 沒有啟用訂閱（Deps.Subscriptions 為 nil）：預覽與匯入回 501。
func TestSubscriptionPreviewDisabled(t *testing.T) {
	r := newItemsRigAPI(t, nil)
	for _, p := range []string{"/api/subscriptions/preview", "/api/subscriptions/s1/preview"} {
		if rec := r.owner("POST", p, ""); rec.Code != 501 {
			t.Errorf("%s = %d %s", p, rec.Code, rec.Body)
		}
	}
	body, ct := multipartImport(t, "file", []byte("x"))
	if resp := r.ownerMultipart("/api/subscriptions/import", body, ct); resp.StatusCode != 501 {
		t.Errorf("import = %d", resp.StatusCode)
	}
	if rec := r.owner("GET", "/api/subscriptions", ""); rec.Code != 501 {
		t.Errorf("list = %d", rec.Code)
	}
}

// MCP 的 list_subscriptions、create_subscription（SUB-R13）：參數照原樣轉成 API 的 body，錯誤的 code 與訊息與 API 相同；
// create_subscription 需要 admin，read 的 token 看不到它。
func TestSubscriptionMCPTools(t *testing.T) {
	f := &fakeSubs{}
	m := newMCPRig(t, func(d *Deps) { d.Subscriptions = f })
	ctx := context.Background()
	cs := m.connect(ctx, m.token(auth.ScopeRead, auth.ScopeAdmin))
	res := callTool(t, cs, "create_subscription", map[string]any{
		"url": "https://www.youtube.com/channel/UCfixture000000000000000", "schedule": "2h",
		"backfill": map[string]any{"mode": "recent", "count": 3}, "filters": map[string]any{"live": "skip"},
	})
	if res.IsError || !strings.Contains(resultText(res), `"id":"s1"`) {
		t.Fatalf("create = %s", resultText(res))
	}
	if f.created.URL != "https://www.youtube.com/channel/UCfixture000000000000000" || *f.created.Schedule != "2h" ||
		f.created.Backfill.Mode != "recent" || *f.created.Backfill.Count != 3 || *f.created.Filters.Live != "skip" {
		t.Errorf("參數 = %+v", f.created)
	}
	f.err = &subscription.URLError{Code: subscription.CodeNotACollection, Message: "這是單一影片，請直接新增，不需要訂閱。"}
	res = callTool(t, cs, "create_subscription", map[string]any{"url": "https://www.youtube.com/watch?v=abc123xxxxx"})
	if !res.IsError || !strings.Contains(resultText(res), "這是單一影片，請直接新增，不需要訂閱。") || !strings.Contains(resultText(res), CodeNotACollection) {
		t.Errorf("錯誤 = %s", resultText(res))
	}
	res = callTool(t, cs, "create_subscription", map[string]any{"url": strings.Repeat("a", 3000)})
	if !res.IsError {
		t.Errorf("過長的網址要在 tool 擋下：%s", resultText(res))
	}
	f.err = nil
	res = callTool(t, cs, "list_subscriptions", map[string]any{})
	if res.IsError || !strings.Contains(resultText(res), `"subscriptions"`) {
		t.Errorf("list = %s", resultText(res))
	}

	reader := m.connect(ctx, m.token(auth.ScopeRead, auth.ScopeIngest))
	res = callTool(t, reader, "create_subscription", map[string]any{"url": "https://www.youtube.com/channel/UCfixture000000000000000"})
	if !res.IsError || !strings.Contains(resultText(res), "admin") {
		t.Errorf("缺 admin = %s", resultText(res))
	}
	tools, err := reader.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "create_subscription" {
			t.Error("read 的 token 不該看到 create_subscription")
		}
	}
}
