package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// registerCosts 註冊 COST 的計價表與成本查詢路由（COST-R12～R15）。預算沿用 settings 的 budget 區段（COST-R4，C1）。
func (rt *routes) registerCosts(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "prices-list", Method: http.MethodGet, Path: "/api/prices",
		Summary: "列出計價表：內建與覆寫的單價、確認日期與是否過期（COST-R12、R13）",
	}, auth.ScopeRead, rt.listPrices)
	registerScoped(api, huma.Operation{
		OperationID: "prices-put", Method: http.MethodPut, Path: "/api/prices",
		Summary: "新增或取代一筆單價覆寫 {provider, model, unit_type, usd_per_unit, checked_at?}（COST-R12）",
	}, auth.ScopeAdmin, rt.putPrice)
	registerScoped(api, huma.Operation{
		OperationID: "prices-delete", Method: http.MethodDelete, Path: "/api/prices",
		Summary: "移除一筆單價覆寫 {provider, model, unit_type}；不存在時回 404（COST-R12）",
	}, auth.ScopeAdmin, rt.deletePrice)
	registerScoped(api, huma.Operation{
		OperationID: "prices-confirm", Method: http.MethodPost, Path: "/api/prices/confirm",
		Summary: "把內建計價表的確認日期設為今天（COST-R13）",
	}, auth.ScopeAdmin, rt.confirmPrices)
	registerScoped(api, huma.Operation{
		OperationID: "costs-summary", Method: http.MethodGet, Path: "/api/costs",
		Summary: "依日期、provider、模型、步驟、頻道或影音彙總花費，附本月預算與提醒（COST-R14）",
	}, auth.ScopeRead, rt.costSummary)
	registerScoped(api, huma.Operation{
		OperationID: "costs-entries", Method: http.MethodGet, Path: "/api/costs/entries",
		Summary: "列出成本帳本的紀錄，由新到舊（COST-R14）",
	}, auth.ScopeRead, rt.costEntries)
	registerScoped(api, huma.Operation{
		OperationID: "costs-export", Method: http.MethodGet, Path: "/api/costs/export.csv",
		Summary: "匯出成本帳本為 CSV（COST-R15，文字欄位防範 CSV injection）",
		Responses: map[string]*huma.Response{
			"200": {Description: "CSV", Content: map[string]*huma.MediaType{"text/csv": {Schema: &huma.Schema{Type: huma.TypeString}}}},
		},
	}, auth.ScopeRead, rt.costExport)
}

type priceListOutput struct{ Body cost.PriceList }

// priceBodyInput 的 body 收任意物件：欄位錯誤要用 COST-R12 的訊息與路徑，不交給 Huma 的 schema 驗證。
type priceBodyInput struct {
	Body *map[string]any
}

type priceOutput struct{ Body cost.PriceEntry }

type okOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

type costRangeInput struct {
	From string `query:"from" doc:"預算時區中的開始日期 YYYY-MM-DD（含），預設本月 1 日"`
	To   string `query:"to" doc:"預算時區中的結束日期 YYYY-MM-DD（含），預設今天"`
}

type costSummaryInput struct {
	From    string `query:"from" doc:"預算時區中的開始日期 YYYY-MM-DD（含），預設本月 1 日"`
	To      string `query:"to" doc:"預算時區中的結束日期 YYYY-MM-DD（含），預設今天"`
	GroupBy string `query:"group_by" doc:"day（預設）、provider、model、stage、channel 或 item"`
}

type costSummaryOutput struct{ Body cost.Summary }

type costEntriesInput struct {
	From   string `query:"from" doc:"預算時區中的開始日期 YYYY-MM-DD（含），預設本月 1 日"`
	To     string `query:"to" doc:"預算時區中的結束日期 YYYY-MM-DD（含），預設今天"`
	Limit  string `query:"limit" doc:"1～500，預設 100"`
	Cursor string `query:"cursor" doc:"上一頁回應的 next_cursor"`
}

type costEntriesOutput struct{ Body cost.Entries }

// costService 檢查 scope 並取得成本服務；沒有時回 501。
func (rt *routes) costService(ctx context.Context, scope string) (*cost.Service, error) {
	if err := requireScope(ctx, scope); err != nil {
		return nil, err
	}
	if rt.d.Cost == nil || rt.d.Store == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, defaultMessages[http.StatusNotImplemented].msg)
	}
	return rt.d.Cost, nil
}

// costErr 把 cost 的錯誤轉成 API 錯誤；其他錯誤交給 mapErr（只記錄，不洩漏細節）。
func (rt *routes) costErr(ctx context.Context, err error) error {
	var inv *cost.InvalidError
	if errors.As(err, &inv) {
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
		e.Errors = append([]validate.FieldError{}, inv.Fields...)
		return e
	}
	if q, ok := cost.IsQueryError(err); ok {
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, q.Message)
	}
	if errors.Is(err, cost.ErrNotFound) {
		return newError(http.StatusNotFound, CodeNotFound, "找不到這筆單價覆寫。")
	}
	return rt.mapErr(ctx, err)
}

func bodyOf(b *map[string]any) (map[string]any, error) {
	if b == nil || *b == nil {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供 JSON 物件格式的內容。")
	}
	return *b, nil
}

func (rt *routes) listPrices(ctx context.Context, _ *struct{}) (*priceListOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeRead)
	if err != nil {
		return nil, err
	}
	list, err := svc.Prices(ctx, rt.d.Store.Read())
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &priceListOutput{Body: list}, nil
}

func (rt *routes) putPrice(ctx context.Context, in *priceBodyInput) (*priceOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeAdmin)
	if err != nil {
		return nil, err
	}
	body, err := bodyOf(in.Body)
	if err != nil {
		return nil, err
	}
	var out cost.PriceEntry
	err = rt.d.Store.Tx(ctx, func(r *store.Repo) error {
		var err error
		out, err = svc.PutPrice(ctx, r, body)
		return err
	})
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &priceOutput{Body: out}, nil
}

func (rt *routes) deletePrice(ctx context.Context, in *priceBodyInput) (*okOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeAdmin)
	if err != nil {
		return nil, err
	}
	body, err := bodyOf(in.Body)
	if err != nil {
		return nil, err
	}
	if err := rt.d.Store.Tx(ctx, func(r *store.Repo) error { return svc.DeletePrice(ctx, r, body) }); err != nil {
		return nil, rt.costErr(ctx, err)
	}
	out := &okOutput{}
	out.Body.OK = true
	return out, nil
}

func (rt *routes) confirmPrices(ctx context.Context, _ *struct{}) (*priceListOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeAdmin)
	if err != nil {
		return nil, err
	}
	var list cost.PriceList
	err = rt.d.Store.Tx(ctx, func(r *store.Repo) error {
		if err := svc.ConfirmPrices(ctx, r); err != nil {
			return err
		}
		var err error
		list, err = svc.Prices(ctx, r)
		return err
	})
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &priceListOutput{Body: list}, nil
}

func (rt *routes) costSummary(ctx context.Context, in *costSummaryInput) (*costSummaryOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeRead)
	if err != nil {
		return nil, err
	}
	r := rt.d.Store.Read()
	rng, err := svc.ParseRange(ctx, r, in.From, in.To)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	sum, err := svc.Summarize(ctx, r, rng, in.GroupBy)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &costSummaryOutput{Body: sum}, nil
}

func (rt *routes) costEntries(ctx context.Context, in *costEntriesInput) (*costEntriesOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeRead)
	if err != nil {
		return nil, err
	}
	r := rt.d.Store.Read()
	rng, err := svc.ParseRange(ctx, r, in.From, in.To)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	limit, err := cost.ParseLimit(in.Limit)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	list, err := svc.ListEntries(ctx, r, rng, limit, in.Cursor)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &costEntriesOutput{Body: list}, nil
}

func (rt *routes) costExport(ctx context.Context, in *costRangeInput) (*huma.StreamResponse, error) {
	svc, err := rt.costService(ctx, auth.ScopeRead)
	if err != nil {
		return nil, err
	}
	r := rt.d.Store.Read()
	rng, err := svc.ParseRange(ctx, r, in.From, in.To)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	data, err := svc.Export(ctx, r, rng)
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	name := cost.ExportName(rng)
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", "text/csv; charset=utf-8")
		hc.SetHeader("Content-Disposition", `attachment; filename="`+name+`"`)
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetStatus(http.StatusOK)
		if _, err := hc.BodyWriter().Write(data); err != nil {
			rt.d.Log.WarnContext(hc.Context(), "輸出 CSV 時中斷", "error", err.Error())
		}
	}}, nil
}

// getBudget 是 GET /api/settings/budget（COST-R4）。
func (rt *routes) getBudget(ctx context.Context) (*settingsOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeAdmin)
	if err != nil {
		return nil, err
	}
	v, err := svc.View(ctx, rt.d.Store.Read())
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	return &settingsOutput{Body: budgetMap(v)}, nil
}

// putBudget 是 PUT /api/settings/budget：取代整個區段；成功後讓 budget_blocked 的步驟依新預算重新檢查（COST-R9）。
func (rt *routes) putBudget(ctx context.Context, body map[string]any) (*settingsOutput, error) {
	svc, err := rt.costService(ctx, auth.ScopeAdmin)
	if err != nil {
		return nil, err
	}
	b, errs := cost.ParseBudget(body)
	if len(errs) > 0 {
		return nil, rt.costErr(ctx, &cost.InvalidError{Fields: errs})
	}
	var v cost.BudgetView
	err = rt.d.Store.Tx(ctx, func(r *store.Repo) error {
		if err := svc.SetBudget(ctx, r, b); err != nil {
			return err
		}
		var err error
		v, err = svc.View(ctx, r)
		return err
	})
	if err != nil {
		return nil, rt.costErr(ctx, err)
	}
	if rt.d.Resumer != nil {
		if _, err := rt.d.Resumer.ResumeBlocked(ctx, cost.ResumeToken); err != nil {
			// 預算已經存下；恢復失敗時月初與下一次調整還會再試。
			rt.d.Log.WarnContext(ctx, "預算更新後恢復步驟失敗", "error", err.Error())
		}
	}
	return &settingsOutput{Body: budgetMap(v)}, nil
}

func budgetMap(v cost.BudgetView) map[string]any {
	return map[string]any{
		"monthly_usd": v.MonthlyUSD, "per_item_usd": v.PerItemUSD, "warn_at_percent": v.WarnAt, "timezone": v.Timezone,
		"month": v.Month, "spent_usd": v.SpentUSD, "reserved_usd": v.ReservedUSD, "remaining_usd": v.RemainingUSD,
	}
}
