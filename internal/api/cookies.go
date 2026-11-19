package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/cookies"
)

// CookieStore 加密存放平台的 cookie（*cookies.Store 實作它）。
type CookieStore interface {
	Put(ctx context.Context, platform, content string) (count int, updatedAt time.Time, err error)
}

// Resumer 在使用者補上缺少的東西（例如 cookie）後，把等待它的 blocked 步驟恢復（*pipeline.Pipeline 實作它）。
// token 的格式見 errclass.Error.ResumeOn，例如 "cookie:youtube"；回傳恢復的步驟數。
type Resumer interface {
	ResumeBlocked(ctx context.Context, token string) (int, error)
}

// cookie 上傳被拒絕的錯誤代碼（422）；訊息一律不含 cookie 的值。
const (
	CodeCookieUnsupportedPlatform = "cookie_unsupported_platform"
	CodeCookieEmpty               = "cookie_empty"
	CodeCookieTooLarge            = "cookie_too_large"
	CodeCookieNoMatch             = "cookie_no_match"
	CodeCookieMissingSessdata     = "cookie_missing_sessdata"
	CodeCookieInvalidFormat       = "cookie_invalid_format"
)

// cookieErrors 把 cookies 套件的正規化錯誤對應到 422 的 code。
var cookieErrors = []struct {
	err  error
	code string
}{
	{cookies.ErrUnknownPlatform, CodeCookieUnsupportedPlatform},
	{cookies.ErrEmpty, CodeCookieEmpty},
	{cookies.ErrTooLarge, CodeCookieTooLarge},
	{cookies.ErrNoCookies, CodeCookieNoMatch},
	{cookies.ErrMissingSESSDATA, CodeCookieMissingSessdata},
	{cookies.ErrInvalidFormat, CodeCookieInvalidFormat},
}

// resumeTimeout 是 cookie 儲存後恢復 blocked 步驟的時間上限。
const resumeTimeout = 30 * time.Second

// maxCookieBodyBytes 是 POST /api/cookies 請求本體的上限：內容上限 64 KiB，
// 加上 JSON 跳脫（每個字元最多展開成 6 個位元組）的餘裕後仍在 512 KiB 之內。
const maxCookieBodyBytes = 512 << 10

// cookieBodyLimit 在進入 Huma 之前限制 POST /api/cookies 的請求本體：超過 maxCookieBodyBytes 時回 422 cookie_too_large。
// Huma 的 MaxBodyBytes 超過時只會回 413，而規格要求所有「cookie 太大」都是同一個 422 code。
// 本體最多讀到上限加 1 個位元組就停止，不會把超大的請求讀進記憶體。
func cookieBodyLimit(next http.Handler) http.Handler {
	tooLarge := func(w http.ResponseWriter) {
		writeError(w, newError(http.StatusUnprocessableEntity, CodeCookieTooLarge, cookies.ErrTooLarge.Error()))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/cookies" || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxCookieBodyBytes {
			tooLarge(w)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxCookieBodyBytes+1))
		if err != nil {
			writeError(w, newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg))
			return
		}
		if len(body) > maxCookieBodyBytes {
			tooLarge(w)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

type cookiesInput struct {
	Body struct {
		Platform string `json:"platform" doc:"youtube 或 bilibili"`
		Content  string `json:"content" doc:"Netscape cookie 檔，或瀏覽器標頭的 name=value; …（上限 64 KiB）"`
	}
}

type cookiesOutput struct {
	Body struct {
		Platform  string    `json:"platform"`
		Cookies   int       `json:"cookies" doc:"儲存的 cookie 數量；回應永遠不含 cookie 的值"`
		UpdatedAt time.Time `json:"updated_at"`
	}
}

func (rt *routes) registerCookies(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "cookies-put", Method: http.MethodPost, Path: "/api/cookies",
		Summary: "上傳平台 cookie（加密存放，每個平台一份），並恢復等待它的 blocked 步驟（SEC-R4、SRC-R8、PIPE-R4）",
		// 明確設定上限，不用 huma 的預設值；超過時由 cookieBodyLimit 先回 422（huma 自己只會回 413）。
		MaxBodyBytes: maxCookieBodyBytes,
	}, rt.putCookies)
}

// putCookies 只有 owner（session 或本機）可以使用：token 即使帶了 scope 也不行，
// 因為 cookie 等同登入憑證。session 的 CSRF 由 authMiddleware 檢查（AUTH-R5）。
func (rt *routes) putCookies(ctx context.Context, in *cookiesInput) (*cookiesOutput, error) {
	if p, ok := principalFrom(ctx); ok && p.Kind == "token" {
		return nil, newError(http.StatusForbidden, CodeForbidden, "只有 owner 可以上傳 cookie。")
	}
	if rt.d.Cookies == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	count, at, err := rt.d.Cookies.Put(ctx, in.Body.Platform, in.Body.Content)
	if err != nil {
		for _, m := range cookieErrors {
			if errors.Is(err, m.err) {
				return nil, newError(http.StatusUnprocessableEntity, m.code, m.err.Error())
			}
		}
		rt.d.Log.ErrorContext(ctx, "儲存 cookie 失敗", "platform", in.Body.Platform, "error", err)
		return nil, asError(err)
	}
	// 恢復失敗不影響 cookie 已經儲存的事實；使用者之後仍可手動重試。
	if rt.d.Resumer != nil {
		// cookie 已經存好：客戶端之後斷線（請求 ctx 取消）不該讓恢復做到一半就中斷，所以脫離請求的取消，另設逾時。
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeTimeout)
		defer cancel()
		n, err := rt.d.Resumer.ResumeBlocked(rctx, "cookie:"+in.Body.Platform)
		if err != nil {
			rt.d.Log.ErrorContext(ctx, "cookie 已儲存，但恢復 blocked 步驟失敗", "platform", in.Body.Platform, "error", err)
		} else if n > 0 {
			rt.d.Log.InfoContext(ctx, "cookie 已儲存，恢復 blocked 步驟", "platform", in.Body.Platform, "resumed", n)
		}
	}
	out := &cookiesOutput{}
	out.Body.Platform, out.Body.Cookies, out.Body.UpdatedAt = in.Body.Platform, count, at
	return out, nil
}
