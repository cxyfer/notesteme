package api

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/configio"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案是 config export／import 的 API（SEC-R10）：匯出用 read，匯入用 admin；
// scope 在讀取請求本體之前檢查。YAML 的解析與驗證都在 server 端（configio、pipeline），
// CLI 只負責讀檔與顯示，所以 `--server` 模式也一樣可用，server 不需要碰 client 的檔案路徑。

// CodeConfigInvalid 是匯入內容不正確的 422 錯誤碼；errors 逐一列出欄位路徑，整份都沒有套用。
const CodeConfigInvalid = "config_invalid"

// maxConfigImportBodyBytes 是匯入請求本體（JSON）的上限：YAML 的上限是 configio.MaxBytes（超過回 422），
// JSON 跳脫最多讓內容變大數倍，這裡放寬到 4 MiB，只是擋掉不合理的大小（huma 超過時回 413）。
const maxConfigImportBodyBytes = 4 << 20

// configService 是 handler 需要的 service。*pipeline.Pipeline 實作它；定義成介面是為了讓測試用假的實作。
type configService interface {
	ExportConfig(ctx context.Context) (*configio.Document, error)
	ImportConfig(ctx context.Context, data []byte) (pipeline.ConfigImportResult, error)
}

var _ configService = (*pipeline.Pipeline)(nil)

type configAPI struct {
	rt  *routes
	svc configService
}

func (rt *routes) registerConfigIO(api huma.API) {
	h := &configAPI{rt: rt}
	if rt.d.Pipeline != nil {
		h.svc = rt.d.Pipeline
	}
	h.register(api)
}

func (h *configAPI) register(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "config-export", Method: http.MethodGet, Path: "/api/config/export",
		Summary: "匯出 DB 管理的設定成 YAML（處理方案與預設處理方案；內建處理方案與所有 secrets 都不包含，SEC-R10）",
	}, auth.ScopeRead, h.export)
	registerScoped(api, huma.Operation{
		OperationID: "config-import", Method: http.MethodPost, Path: "/api/config/import",
		Summary:      "匯入 YAML：先驗證整份，有任何錯誤就整份不套用並回 422 與逐欄位的 errors；新增或取代同名處理方案，不刪除檔案沒有的（SEC-R10）",
		MaxBodyBytes: maxConfigImportBodyBytes,
	}, auth.ScopeAdmin, h.importConfig)
}

type configExportOutput struct {
	Body struct {
		Content        string   `json:"content" doc:"YAML 內容：鍵依字母排序、輸出穩定，可以直接放進版本控制，也可以原樣匯入"`
		Profiles       []string `json:"profiles" doc:"匯出的處理方案名稱（不含內建的 default），依名稱排序"`
		DefaultProfile string   `json:"default_profile,omitempty" doc:"明確設定過的預設處理方案；沒有設定過就沒有這個欄位"`
	}
}

func (h *configAPI) service(ctx context.Context) (configService, error) {
	if h.svc == nil {
		return nil, h.rt.noPipeline(ctx)
	}
	return h.svc, nil
}

func (h *configAPI) export(ctx context.Context, _ *struct{}) (*configExportOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := svc.ExportConfig(ctx)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	content, err := configio.Marshal(doc)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	out := &configExportOutput{}
	out.Body.Content = string(content)
	out.Body.Profiles = make([]string, 0, len(doc.Profiles))
	for name := range doc.Profiles {
		out.Body.Profiles = append(out.Body.Profiles, name)
	}
	sort.Strings(out.Body.Profiles)
	out.Body.DefaultProfile = doc.DefaultProfile
	return out, nil
}

type configImportInput struct {
	Body struct {
		Content string `json:"content" doc:"config export 輸出的 YAML 內容（上限 1 MiB）"`
	}
}

type configImportOutput struct {
	Body struct {
		Created        []string `json:"created" doc:"新建立的處理方案，依名稱排序"`
		Updated        []string `json:"updated" doc:"內容不同而建立了新版本的處理方案（PRF-R3），依名稱排序"`
		Unchanged      []string `json:"unchanged" doc:"內容與目前版本相同、沒有新增版本的處理方案，依名稱排序"`
		DefaultProfile string   `json:"default_profile,omitempty" doc:"檔案中的 default_profile；沒有這個欄位代表檔案沒有指定，預設處理方案沒有改動"`
		DefaultChanged bool     `json:"default_changed" doc:"預設處理方案是否因為這次匯入而不同"`
	}
}

func (h *configAPI) importConfig(ctx context.Context, in *configImportInput) (*configImportOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.ImportConfig(ctx, []byte(in.Body.Content))
	if err != nil {
		var inv *pipeline.InvalidError
		if errors.As(err, &inv) {
			e := newError(http.StatusUnprocessableEntity, CodeConfigInvalid, inv.Error())
			e.Errors = inv.Fields
			return nil, e
		}
		return nil, h.rt.mapErr(ctx, err)
	}
	h.rt.d.Log.InfoContext(ctx, "已匯入設定", "created", len(res.Created), "updated", len(res.Updated), "unchanged", len(res.Unchanged), "default_changed", res.DefaultChanged)
	out := &configImportOutput{}
	out.Body.Created, out.Body.Updated, out.Body.Unchanged = nonNil(res.Created), nonNil(res.Updated), nonNil(res.Unchanged)
	out.Body.DefaultProfile, out.Body.DefaultChanged = res.DefaultProfile, res.DefaultChanged
	return out, nil
}

// nonNil 讓 JSON 輸出 [] 而不是 null。
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
