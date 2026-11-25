package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是媒體庫的儲存檢視（LIB-R26～R28）：列出（read）、建立與刪除（admin）。
// 檢視存的是媒體庫的網址參數（UI 的格式，見 ViewQuery），套用時 UI 直接還原網址；後端只驗證、不執行查詢。

// 儲存檢視的錯誤代碼（LIB-R27）。
const (
	CodeViewExists = "view_exists"
	CodeViewLimit  = "view_limit"
)

// 儲存檢視的上限（LIB-R27）。
const (
	MaxViewNameLen = 60
	MaxViews       = 100
	// maxViewStatus 是 status 的個數上限：每個步驟的每個狀態各一個（12 個步驟 × 8 種狀態），媒體庫能勾的都存得下。
	maxViewStatus = 96
	maxViewText   = 200
)

func (rt *routes) registerViews(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "views-list", Method: http.MethodGet, Path: "/api/views",
		Summary: "列出媒體庫的儲存檢視，依建立順序（LIB-R26）",
	}, auth.ScopeRead, rt.listViews)
	registerScoped(api, huma.Operation{
		OperationID: "views-create", Method: http.MethodPost, Path: "/api/views",
		Summary:       "把媒體庫目前的篩選與分組存成有名稱的檢視；內容不正確回 422，名稱重複回 409 view_exists，數量到上限回 409 view_limit（LIB-R27）",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createView)
	registerScoped(api, huma.Operation{
		OperationID: "views-delete", Method: http.MethodDelete, Path: "/api/views/{id}",
		Summary:       "刪除儲存檢視；不存在回 404（LIB-R28）",
		DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, rt.deleteView)
}

// ViewQuery 是檢視存下的媒體庫網址參數（LIB-R26），名稱與格式和媒體庫的網址相同（不是 GET /api/items 的參數）：
// 發布日是 YYYY-MM-DD，長度是分鐘，分組是 group。沒有出現的參數代表不篩選；group 省略代表預設的依頻道分組。
type ViewQuery struct {
	Q             string   `json:"q,omitempty" doc:"標題搜尋，最多 200 字元"`
	Platform      string   `json:"platform,omitempty" doc:"youtube、bilibili、podcast 或 local"`
	Channel       string   `json:"channel,omitempty" doc:"頻道 ID，最多 200 字元"`
	Status        []string `json:"status,omitempty" doc:"<步驟>:<狀態>，同 GET /api/items 的 status；最多 96 個"`
	ErrorClass    string   `json:"error_class,omitempty" doc:"retryable、wait、needs_action、budget_blocked 或 permanent"`
	Lang          string   `json:"lang,omitempty" doc:"原文語言，最多 200 字元"`
	PublishedFrom string   `json:"published_from,omitempty" doc:"發布日下限（含），YYYY-MM-DD"`
	PublishedTo   string   `json:"published_to,omitempty" doc:"發布日上限（含），YYYY-MM-DD"`
	DurMin        *int     `json:"dur_min,omitempty" doc:"長度下限（分鐘，含），不得小於 0"`
	DurMax        *int     `json:"dur_max,omitempty" doc:"長度上限（分鐘，含），不得小於 0"`
	Timing        string   `json:"timing,omitempty" doc:"source、merged、silence、interpolated 或 model"`
	Blocked       string   `json:"blocked,omitempty" doc:"停住的原因，例如 awaiting_user"`
	ReadState     string   `json:"read_state,omitempty" doc:"read 或 unread"`
	Group         string   `json:"group,omitempty" doc:"published、status 或 none（不分組）；省略時依頻道分組"`
}

// ViewBody 是一個儲存檢視。
type ViewBody struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Query     ViewQuery `json:"query"`
	CreatedAt time.Time `json:"created_at"`
}

type listViewsOutput struct {
	Body struct {
		Views []ViewBody `json:"views"`
	}
}

type createViewInput struct {
	Body struct {
		Name  string    `json:"name" doc:"名稱：去掉前後空白後 1～60 個字元，不含控制字元；不分大小寫不得重複"`
		Query ViewQuery `json:"query" doc:"至少一個篩選條件或分組"`
	}
}

type viewOutput struct {
	Body ViewBody
}

type viewIDInput struct {
	ID string `path:"id"`
}

func (rt *routes) listViews(ctx context.Context, _ *struct{}) (*listViewsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	out := &listViewsOutput{}
	out.Body.Views = []ViewBody{}
	if rt.d.Store == nil {
		return out, nil
	}
	list, err := rt.d.Store.Read().SavedViews(ctx)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	for _, v := range list {
		b, err := toViewBody(v)
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		out.Body.Views = append(out.Body.Views, b)
	}
	return out, nil
}

func (rt *routes) createView(ctx context.Context, in *createViewInput) (*viewOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Body.Name)
	query := normalizeViewQuery(in.Body.Query)
	fields := validateViewName(name)
	fields = append(fields, validateViewQuery(query)...)
	if len(fields) > 0 {
		return nil, firstFieldError(http.StatusUnprocessableEntity, CodeInvalidInput, fields)
	}
	if rt.d.Store == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	raw, err := json.Marshal(query)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	v, err := rt.d.Store.InsertSavedView(ctx, name, raw, MaxViews)
	switch {
	case errors.Is(err, store.ErrViewExists):
		return nil, apierr.Newf(http.StatusConflict, CodeViewExists, "view_exists", name)
	case errors.Is(err, store.ErrViewLimit):
		return nil, apierr.Newf(http.StatusConflict, CodeViewLimit, "view_limit", strconv.Itoa(MaxViews))
	case err != nil:
		return nil, rt.mapErr(ctx, err)
	}
	return &viewOutput{Body: ViewBody{ID: v.ID, Name: v.Name, Query: query, CreatedAt: v.CreatedAt}}, nil
}

func (rt *routes) deleteView(ctx context.Context, in *viewIDInput) (*emptyOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Store == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這個檢視。")
	}
	err := rt.d.Store.Write().DeleteSavedView(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這個檢視。")
	}
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &emptyOutput{}, nil
}

func toViewBody(v store.SavedView) (ViewBody, error) {
	b := ViewBody{ID: v.ID, Name: v.Name, CreatedAt: v.CreatedAt}
	if err := json.Unmarshal(v.Query, &b.Query); err != nil {
		return ViewBody{}, fmt.Errorf("檢視 %s 的查詢內容損壞：%w", v.ID, err)
	}
	return b, nil
}

// normalizeViewQuery 去掉字串的前後空白、去掉重複的 status（保留第一次出現的順序）；空字串視為沒有這個參數。
func normalizeViewQuery(q ViewQuery) ViewQuery {
	for _, p := range []*string{&q.Q, &q.Platform, &q.Channel, &q.ErrorClass, &q.Lang, &q.PublishedFrom, &q.PublishedTo,
		&q.Timing, &q.Blocked, &q.ReadState, &q.Group} {
		*p = strings.TrimSpace(*p)
	}
	var status []string
	for _, s := range q.Status {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(status, s) {
			status = append(status, s)
		}
	}
	q.Status = status
	return q
}

func validateViewName(name string) []validate.FieldError {
	n := len([]rune(name))
	switch {
	case n == 0:
		return []validate.FieldError{{Path: "name", Message: "名稱不能是空的。"}}
	case n > MaxViewNameLen:
		return []validate.FieldError{{Path: "name", Message: fmt.Sprintf("名稱最多 %d 個字元。", MaxViewNameLen)}}
	case strings.ContainsFunc(name, unicode.IsControl):
		return []validate.FieldError{{Path: "name", Message: "名稱不能含控制字元（例如換行、tab）。"}}
	}
	return nil
}

// validateViewQuery 逐一驗證參數，規則與媒體庫和 GET /api/items 相同：能對應到 GET /api/items 的參數，
// 用 pipeline.ItemQuery.Validate 驗證（每次只帶一個參數，錯誤才能指到欄位）。
func validateViewQuery(q ViewQuery) []validate.FieldError {
	var fs []validate.FieldError
	add := func(path, msg string) { fs = append(fs, validate.FieldError{Path: "query." + path, Message: msg}) }
	check := func(path string, iq pipeline.ItemQuery) {
		if err := iq.Validate(); err != nil {
			add(path, err.Error())
		}
	}
	// 每個欄位都是 omitempty：序列化成 {} 就是沒有任何參數。
	if raw, err := json.Marshal(q); err == nil && string(raw) == "{}" {
		return []validate.FieldError{{Path: "query", Message: "檢視至少要有一個篩選條件或分組。"}}
	}
	if n := len([]rune(q.Q)); n > pipeline.MaxQueryLen {
		add("q", fmt.Sprintf("q 最多 %d 個字元。", pipeline.MaxQueryLen))
	}
	check("platform", pipeline.ItemQuery{Platform: q.Platform})
	for _, p := range []struct{ path, v string }{{"channel", q.Channel}, {"lang", q.Lang}} {
		if len([]rune(p.v)) > maxViewText {
			add(p.path, fmt.Sprintf("%s 最多 %d 個字元。", p.path, maxViewText))
		}
	}
	if len(q.Status) > maxViewStatus {
		add("status", fmt.Sprintf("status 最多 %d 個。", maxViewStatus))
	} else if len(q.Status) > 0 {
		if status, err := parseStatus(q.Status); err != nil {
			var e *Error
			if errors.As(err, &e) {
				add("status", e.Message)
			}
		} else {
			check("status", pipeline.ItemQuery{Status: status})
		}
	}
	if q.ErrorClass != "" && !slices.Contains(viewErrorClasses, q.ErrorClass) {
		add("error_class", "error_class 必須是 retryable、wait、needs_action、budget_blocked 或 permanent。")
	}
	for _, p := range []struct{ path, v string }{{"published_from", q.PublishedFrom}, {"published_to", q.PublishedTo}} {
		if _, err := time.Parse(time.DateOnly, p.v); p.v != "" && err != nil {
			add(p.path, p.path+" 必須是存在的日期，格式 YYYY-MM-DD。")
		}
	}
	for _, p := range []struct {
		path string
		v    *int
	}{{"dur_min", q.DurMin}, {"dur_max", q.DurMax}} {
		if p.v != nil && *p.v < 0 {
			add(p.path, p.path+" 不得小於 0（分鐘）。")
		}
	}
	check("timing", pipeline.ItemQuery{Timing: q.Timing})
	check("blocked", pipeline.ItemQuery{Blocked: q.Blocked})
	check("read_state", pipeline.ItemQuery{ReadState: q.ReadState})
	switch q.Group {
	case "", "published", "status", "none":
	default:
		add("group", "group 必須是 published、status 或 none。")
	}
	return fs
}

// viewErrorClasses 是媒體庫可以篩選的錯誤類別（errclass）。
var viewErrorClasses = []string{
	string(errclass.ClassRetryable), string(errclass.ClassWait), string(errclass.ClassNeedsAction),
	string(errclass.ClassBudgetBlocked), string(errclass.ClassPermanent),
}
