// Package hubapi 是 Hub 的 HTTP 層：net/http + Huma v2（OpenAPI 3.1，ADR-0032 第 1 點）。
//
// 路由前綴 /api/v1/（OpenAPI 公開在 GET /api/v1/openapi.json）；/auth/<provider>/start|callback 是瀏覽器的 OAuth
// 流程；/mcp 是 Hub MCP（HMCP）；/healthz、/readyz 同 Client。錯誤格式與 Client 相同（internal/apierr）。
//
// M5-S0 只有骨架：/healthz、/readyz 與 OpenAPI 已實作，其他路由都是 501 not_implemented 的 stub（stubs.go），
// 每條 stub 記錄它需要的存取層級（Access），身分驗證與權限的 middleware 由 S1、S2 依這張表加入。
package hubapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubblob"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// Deps 是 handler 需要的依賴。
type Deps struct {
	Config  *hubconfig.Config
	Store   hubstore.Store
	Blobs   hubblob.Store
	Clock   clock.Clock
	Log     *slog.Logger
	Version string
}

// PublicDocPaths 是 Huma 在 cfg.OpenAPIPath 底下提供的 OpenAPI 文件，一律不需要登入（ADR-0032 第 1 點）。
// 它們不是 Huma operation，不在 Stubs 表中：S2 的權限 middleware 要放行這些路徑（HACC A9）。
var PublicDocPaths = []string{"/api/v1/openapi.json", "/api/v1/openapi.yaml", "/api/v1/openapi-3.0.json", "/api/v1/openapi-3.0.yaml"}

// OpenAPISnapshotVersion 是匯出 OpenAPI 快照時的版本號：固定的值讓快照不隨 binary 版本改變。
const OpenAPISnapshotVersion = "snapshot"

// readyTimeout 是 /readyz 檢查的上限。
const readyTimeout = 5 * time.Second

// CodeNotReady 是 /readyz 失敗時的代碼（同 Client）。
const CodeNotReady = "not_ready"

// New 建立 Hub 的 HTTP handler。
func New(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	mux := http.NewServeMux()
	newHumaAPI(mux, d)
	registerRawStubs(mux)
	return recoverMiddleware(d.Log, mux)
}

// newHumaAPI 建立 Huma API 並註冊所有 Huma 路由；OpenAPISpec 共用同一段。
func newHumaAPI(mux *http.ServeMux, d Deps) huma.API {
	cfg := huma.DefaultConfig("Notesteme Hub", d.Version)
	cfg.Info.Description = "Notesteme Hub 的 API（/api/v1）。Client 契約見 ADR-0032 第 1 點。"
	cfg.OpenAPIPath = "/api/v1/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	rt := &routes{d: d}
	huma.Register(api, huma.Operation{
		OperationID: "healthz", Method: http.MethodGet, Path: "/healthz",
		Summary: "存活檢查（免驗證）",
	}, rt.healthz)
	huma.Register(api, huma.Operation{
		OperationID: "readyz", Method: http.MethodGet, Path: "/readyz",
		Summary: "就緒檢查：確認資料庫可寫（免驗證）",
	}, rt.readyz)
	registerStubs(api)
	return api
}

// OpenAPISpec 回傳與 GET /api/v1/openapi.json 相同內容的 OpenAPI 3.1 文件（縮排的 JSON，結尾換行）；不需要資料庫。
func OpenAPISpec() ([]byte, error) {
	api := newHumaAPI(http.NewServeMux(), Deps{Version: OpenAPISnapshotVersion})
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

type routes struct{ d Deps }

type statusOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}

func okStatus() *statusOutput {
	o := &statusOutput{}
	o.Body.Status = "ok"
	return o
}

func (rt *routes) healthz(context.Context, *struct{}) (*statusOutput, error) { return okStatus(), nil }

// readyz 檢查資料庫可寫；失敗時回 503 並列出項目（同 Client 的 OPS-R5，不含內部路徑）。
func (rt *routes) readyz(ctx context.Context, _ *struct{}) (*statusOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	var failed []string
	if rt.d.Store == nil || rt.d.Store.CheckWritable(ctx) != nil {
		rt.d.Log.ErrorContext(ctx, "readyz：Hub 資料庫不可寫")
		failed = append(failed, "database")
	}
	if len(failed) > 0 {
		e := apierr.New(http.StatusServiceUnavailable, CodeNotReady, "服務尚未就緒："+strings.Join(failed, "、")+" 無法使用。")
		e.Failed = failed
		return nil, e
	}
	return okStatus(), nil
}

// recoverMiddleware 把 panic 轉成 500（不洩漏細節）。
func recoverMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.ErrorContext(r.Context(), "Hub handler panic", "panic", v, "path", r.URL.Path)
				apierr.Write(w, apierr.As(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
