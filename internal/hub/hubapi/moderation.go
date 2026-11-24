package hubapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 版主與管理員的處理（M5-S5）：下架申請的列表與決定（HACC-R16）、檢舉的列表與決定、手動隱藏（HREP-R15）、
// 使用者列表、停用與恢復（HACC-R12、R17），以及上傳者的「我的分享」（HACC-R16）。
// 權限在 middleware 檢查，handler 再以 requirePermission 確認一次；決定以 store 中條件式的 UPDATE 判斷是否已處理（並發的第二個決定得到 409）。
// 移除版本後，沒有其他未移除的版本引用的 blob 在交易提交之後刪除。

const (
	codeReportDecided = "report_decided"
	codeVersionState  = "version_state"
	msgReportNotFound = "找不到這筆檢舉。"
	msgUserNotFound   = "找不到這個使用者。"
	maxDecisionNote   = 2000
	maxSuspendReason  = 500
	defaultAdminLimit = 100
	maxAdminLimit     = 500
)

// requirePermission 在 handler 內再確認一次權限（middleware 之外的第二層，任務要求雙重檢查）：
// 有效權限（PAT ∩ 帳號當下的權限）沒有 perm 時回與 middleware 相同的 403 forbidden。
func (rt *routes) requirePermission(ctx context.Context, perm string) (*hubauth.Principal, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := rt.d.Auth.HasPermission(ctx, p, perm)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	if !ok {
		return nil, hubauth.ErrForbidden(perm)
	}
	return p, nil
}

// ---- 下架申請 ----

// TakedownView 是版主看到的下架申請；contact_email 解不開時是 null。
type TakedownView struct {
	ID           string     `json:"id"`
	VersionID    string     `json:"version_id"`
	Reason       string     `json:"reason"`
	Statement    string     `json:"statement"`
	ContactEmail *string    `json:"contact_email"`
	State        string     `json:"state"`
	Note         string     `json:"note"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at"`
}

type adminListInput struct {
	State string `query:"state" doc:"只列這個狀態"`
	Limit string `query:"limit" doc:"最多幾筆（1～500，預設 100）"`
}

type takedownsOutput struct {
	Body struct {
		Takedowns []TakedownView `json:"takedowns"`
	}
}

type decisionInput struct {
	ID   string `path:"id" maxLength:"64" doc:"ID"`
	Body struct {
		Decision string `json:"decision" required:"false" doc:"下架申請：remove 或 reject；檢舉：dismiss 或 remove"`
		Note     string `json:"note,omitempty" required:"false" doc:"處理說明（最多 2,000 個字元）"`
	}
}

// parseAdminList 驗證列表的 state 與 limit（422 的 errors[].path 是 query.<參數>）。
func parseAdminList(in *adminListInput, states []string) (string, int, error) {
	var f fieldErrs
	f.enum("query.state", in.State, states)
	limit := defaultAdminLimit
	if in.Limit != "" {
		n, ok := parseLimit(in.Limit, maxAdminLimit)
		if !ok {
			f.add("query.limit", "必須是 1～500 的整數。")
		}
		limit = n
	}
	return in.State, limit, f.err()
}

// parseLimit 解析 1～max 的十進位整數（最多 3 位）。
func parseLimit(s string, maxN int) (int, bool) {
	if len(s) == 0 || len(s) > 3 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n >= 1 && n <= maxN
}

// checkDecision 驗證決定與說明。
func checkDecision(in *decisionInput, allowed []string) (string, error) {
	var f fieldErrs
	if !slices.Contains(allowed, in.Body.Decision) {
		f.add("decision", oneOf(allowed))
	}
	note := strings.TrimSpace(in.Body.Note)
	if !freeText(note, maxDecisionNote) {
		f.add("note", "最多 2,000 個字元，不能有控制字元。")
	}
	return note, f.err()
}

func (rt *routes) listTakedowns(ctx context.Context, in *adminListInput) (*takedownsOutput, error) {
	if _, err := rt.requirePermission(ctx, hubconfig.PermModerate); err != nil {
		return nil, err
	}
	state, limit, err := parseAdminList(in, []string{hubstore.TakedownOpen, hubstore.TakedownRemoved, hubstore.TakedownRejected})
	if err != nil {
		return nil, err
	}
	ts, err := rt.d.Store.Takedowns(ctx, state, limit)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &takedownsOutput{}
	o.Body.Takedowns = make([]TakedownView, 0, len(ts))
	for _, t := range ts {
		v := TakedownView{ID: t.ID, VersionID: t.VersionID, Reason: t.Reason, Statement: t.Statement, State: t.State,
			Note: t.Note, CreatedAt: t.CreatedAt, DecidedAt: t.DecidedAt}
		if email, err := rt.openContact(t); err == nil {
			v.ContactEmail = &email
		} else {
			rt.d.Log.WarnContext(ctx, "無法解開下架申請的聯絡 email", "takedown_id", t.ID)
		}
		o.Body.Takedowns = append(o.Body.Takedowns, v)
	}
	return o, nil
}

// deleteBlobs 刪掉移除版本後沒有其他未移除的版本引用的 blob（交易提交之後；失敗只寫 log）。
func (rt *routes) deleteBlobs(ctx context.Context, rm hubstore.Removal) {
	for _, sha := range rm.BlobsToDelete {
		if err := rt.d.Blobs.Delete(ctx, sha); err != nil {
			rt.d.Log.WarnContext(ctx, "刪除已移除版本的 blob 失敗", "sha256", sha, "error", err)
		}
	}
}

func (rt *routes) decideTakedown(ctx context.Context, in *decisionInput) (*takedownStateOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermModerate)
	if err != nil {
		return nil, err
	}
	note, err := checkDecision(in, []string{"remove", "reject"})
	if err != nil {
		return nil, err
	}
	var t hubstore.Takedown
	if in.Body.Decision == "reject" {
		t, err = rt.d.Store.RejectTakedown(ctx, p.User.ID, in.ID, note)
	} else {
		var rm hubstore.Removal
		t, rm, err = rt.d.Store.RemoveByTakedown(ctx, p.User.ID, in.ID, note)
		if err == nil {
			rt.deleteBlobs(ctx, rm)
		}
	}
	switch {
	case errors.Is(err, hubstore.ErrNotFound):
		return nil, apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgTakedownNotFound)
	case errors.Is(err, hubstore.ErrDecided):
		return nil, apierr.New(http.StatusConflict, codeTakedownDecided, "這個下架申請已經處理過了。")
	case errors.Is(err, hubstore.ErrVersionState):
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, "申請的版本不存在，只能駁回（reject）。")
	case err != nil:
		return nil, rt.apiError(ctx, err)
	}
	return &takedownStateOutput{Body: TakedownStateView{ID: t.ID, State: t.State}}, nil
}

// ---- 檢舉 ----

// ReporterView 是檢舉者（只有 moderate 看得到）。
type ReporterView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ReportView 是版主看到的檢舉。
type ReportView struct {
	ID        string       `json:"id"`
	VersionID string       `json:"version_id"`
	Reporter  ReporterView `json:"reporter"`
	Reason    string       `json:"reason"`
	Note      string       `json:"note"`
	State     string       `json:"state"`
	CreatedAt time.Time    `json:"created_at"`
	DecidedAt *time.Time   `json:"decided_at"`
}

type reportsOutput struct {
	Body struct {
		Reports []ReportView `json:"reports"`
	}
}

// ReportDecisionView 是處理檢舉的結果。
type ReportDecisionView struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	VersionState string `json:"version_state"`
}

type reportDecisionOutput struct{ Body ReportDecisionView }

func (rt *routes) listReports(ctx context.Context, in *adminListInput) (*reportsOutput, error) {
	if _, err := rt.requirePermission(ctx, hubconfig.PermModerate); err != nil {
		return nil, err
	}
	state, limit, err := parseAdminList(in, []string{hubstore.ReportOpen, hubstore.ReportDismissed, hubstore.ReportActioned})
	if err != nil {
		return nil, err
	}
	rs, err := rt.d.Store.Reports(ctx, state, limit)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &reportsOutput{}
	o.Body.Reports = make([]ReportView, 0, len(rs))
	names := map[string]string{}
	for _, r := range rs {
		name, ok := names[r.ReporterID]
		if !ok {
			if u, err := rt.d.Store.User(ctx, r.ReporterID); err == nil {
				name = u.DisplayName
			}
			names[r.ReporterID] = name
		}
		o.Body.Reports = append(o.Body.Reports, ReportView{ID: r.ID, VersionID: r.VersionID, Reporter: ReporterView{ID: r.ReporterID, DisplayName: name},
			Reason: r.Reason, Note: r.Note, State: r.State, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt})
	}
	return o, nil
}

func (rt *routes) decideReport(ctx context.Context, in *decisionInput) (*reportDecisionOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermModerate)
	if err != nil {
		return nil, err
	}
	if _, err := checkDecision(in, []string{"dismiss", "remove"}); err != nil {
		return nil, err
	}
	var r hubstore.Report
	if in.Body.Decision == "dismiss" {
		r, err = rt.d.Store.DismissReport(ctx, p.User.ID, in.ID)
	} else {
		var rm hubstore.Removal
		r, rm, err = rt.d.Store.RemoveByReport(ctx, p.User.ID, in.ID)
		if err == nil {
			rt.deleteBlobs(ctx, rm)
		}
	}
	switch {
	case errors.Is(err, hubstore.ErrNotFound):
		return nil, apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgReportNotFound)
	case errors.Is(err, hubstore.ErrDecided):
		return nil, apierr.New(http.StatusConflict, codeReportDecided, "這筆檢舉已經處理過了。")
	case err != nil:
		return nil, rt.apiError(ctx, err)
	}
	v, err := rt.d.Store.Version(ctx, r.VersionID)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return &reportDecisionOutput{Body: ReportDecisionView{ID: r.ID, State: r.State, VersionState: v.State}}, nil
}

// VersionStateView 是隱藏後的版本狀態。
type VersionStateView struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type versionStateOutput struct{ Body VersionStateView }

func (rt *routes) hideVersion(ctx context.Context, in *versionIDPath) (*versionStateOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermModerate)
	if err != nil {
		return nil, err
	}
	_, err = rt.d.Store.HideVersion(ctx, p.User.ID, in.ID)
	switch {
	case errors.Is(err, hubstore.ErrNotFound):
		return nil, versionNotFound()
	case errors.Is(err, hubstore.ErrVersionState):
		return nil, apierr.New(http.StatusConflict, codeVersionState, "這個版本已經移除或撤回，不能隱藏。")
	case err != nil:
		return nil, rt.apiError(ctx, err)
	}
	return &versionStateOutput{Body: VersionStateView{ID: in.ID, State: "hidden"}}, nil
}

// ---- 使用者與停用 ----

// AdminUserView 是管理員看到的帳號。
type AdminUserView struct {
	ID              string                 `json:"id"`
	DisplayName     string                 `json:"display_name"`
	CreatedAt       time.Time              `json:"created_at"`
	Groups          []string               `json:"groups"`
	Identities      []hubauth.IdentityView `json:"identities"`
	SuspendedAt     *time.Time             `json:"suspended_at"`
	SuspendedReason string                 `json:"suspended_reason"`
}

type adminUsersInput struct {
	Limit string `query:"limit" doc:"最多幾筆（1～500，預設 100）"`
}

type adminUsersOutput struct {
	Body struct {
		Users []AdminUserView `json:"users"`
	}
}

func (rt *routes) listUsers(ctx context.Context, in *adminUsersInput) (*adminUsersOutput, error) {
	if _, err := rt.requirePermission(ctx, hubconfig.PermAdmin); err != nil {
		return nil, err
	}
	_, limit, err := parseAdminList(&adminListInput{Limit: in.Limit}, nil)
	if err != nil {
		return nil, err
	}
	us, err := rt.d.Store.Users(ctx, limit)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &adminUsersOutput{}
	o.Body.Users = make([]AdminUserView, 0, len(us))
	for _, u := range us {
		groups, err := rt.d.Auth.Groups(ctx, u.ID)
		if err != nil {
			return nil, rt.apiError(ctx, err)
		}
		ids, err := rt.d.Store.Identities(ctx, u.ID)
		if err != nil {
			return nil, rt.apiError(ctx, err)
		}
		v := AdminUserView{ID: u.ID, DisplayName: u.DisplayName, CreatedAt: u.CreatedAt, Groups: groups,
			Identities: make([]hubauth.IdentityView, 0, len(ids)), SuspendedAt: u.SuspendedAt, SuspendedReason: u.SuspendedReason}
		for _, id := range ids {
			v.Identities = append(v.Identities, hubauth.IdentityView{Provider: id.Provider, Login: id.Login})
		}
		o.Body.Users = append(o.Body.Users, v)
	}
	return o, nil
}

type suspendInput struct {
	ID   string `path:"id" maxLength:"64" doc:"使用者 ID"`
	Body struct {
		Reason string `json:"reason" required:"false" doc:"停用的原因（1～500 個字元）"`
	}
}

type userIDPath struct {
	ID string `path:"id" maxLength:"64" doc:"使用者 ID"`
}

// SuspensionView 是停用或恢復後的狀態。
type SuspensionView struct {
	ID        string `json:"id"`
	Suspended bool   `json:"suspended"`
}

type suspensionOutput struct{ Body SuspensionView }

func (rt *routes) suspendUser(ctx context.Context, in *suspendInput) (*suspensionOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermAdmin)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(in.Body.Reason)
	var f fieldErrs
	if reason == "" || !freeText(reason, maxSuspendReason) {
		f.add("reason", "請以 1～500 個字元說明停用的原因。")
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	if in.ID == p.User.ID {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, "不能停用自己的帳號。")
	}
	// 持有 admin 的帳號只能由 CLI（notesteme-hub admin suspend）停用：管理員由設定檔指定，API 不能互相停用（安全修補 A）。
	perms, err := rt.d.Auth.AccountPermissions(ctx, in.ID)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	if slices.Contains(perms, hubconfig.PermAdmin) {
		return nil, apierr.New(http.StatusForbidden, hubauth.CodeAdminByConfig, "管理員只能用 notesteme-hub admin suspend 停用。")
	}
	if _, err := rt.d.Store.SuspendUser(ctx, p.User.ID, in.ID, reason); err != nil {
		if errors.Is(err, hubstore.ErrNotFound) {
			return nil, apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgUserNotFound)
		}
		return nil, rt.apiError(ctx, err)
	}
	return &suspensionOutput{Body: SuspensionView{ID: in.ID, Suspended: true}}, nil
}

func (rt *routes) unsuspendUser(ctx context.Context, in *userIDPath) (*suspensionOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermAdmin)
	if err != nil {
		return nil, err
	}
	if _, err := rt.d.Store.UnsuspendUser(ctx, p.User.ID, in.ID); err != nil {
		if errors.Is(err, hubstore.ErrNotFound) {
			return nil, apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgUserNotFound)
		}
		return nil, rt.apiError(ctx, err)
	}
	return &suspensionOutput{Body: SuspensionView{ID: in.ID, Suspended: false}}, nil
}

// ---- 我的分享 ----

// RemovalView 是版本被移除的原因。
type RemovalView struct {
	Source string `json:"source" doc:"takedown（下架申請）或 report（檢舉）"`
	Reason string `json:"reason" doc:"申請或檢舉的理由"`
}

// MyVersionView 是我的分享中的一個版本：列表的欄位加上可見度、狀態與移除的原因。
type MyVersionView struct {
	VersionItem
	Visibility string       `json:"visibility"`
	State      string       `json:"state"`
	Removal    *RemovalView `json:"removal,omitempty"`
}

// MyShareView 是我的一次分享。
type MyShareView struct {
	ID          string          `json:"id"`
	Video       VideoRef        `json:"video"`
	CreatedAt   time.Time       `json:"created_at"`
	WithdrawnAt *time.Time      `json:"withdrawn_at"`
	Versions    []MyVersionView `json:"versions"`
}

type mySharesOutput struct {
	Body struct {
		Shares []MyShareView `json:"shares"`
	}
}

func (rt *routes) myShares(ctx context.Context, in *adminUsersInput) (*mySharesOutput, error) {
	p, err := rt.requirePermission(ctx, hubconfig.PermUpload)
	if err != nil {
		return nil, err
	}
	_, limit, err := parseAdminList(&adminListInput{Limit: in.Limit}, nil)
	if err != nil {
		return nil, err
	}
	ss, err := rt.d.Store.MyShares(ctx, p.User.ID, limit)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	viewer, err := rt.viewer(ctx, p)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &mySharesOutput{}
	o.Body.Shares = make([]MyShareView, 0, len(ss))
	for _, s := range ss {
		v := MyShareView{ID: s.ID, Video: videoRef(s.Video), CreatedAt: s.CreatedAt, WithdrawnAt: s.WithdrawnAt, Versions: []MyVersionView{}}
		for _, x := range s.Versions {
			ups, err := rt.d.Store.VersionUploaders(ctx, x.ID)
			if err != nil {
				return nil, rt.apiError(ctx, err)
			}
			// 撤回的分享：版本之後可能被別人以相同內容重新分享並恢復（安全修補 A），看不到它時只顯示自己分享過的識別，
			// 狀態是 withdrawn，不透露別人的可見度、上傳者與模型。
			if s.WithdrawnAt != nil {
				ids := make([]string, 0, len(ups))
				for _, u := range ups {
					ids = append(ids, u.UserID)
				}
				if !hubrepo.CanSee(hubrepo.Subject{Visibility: x.Visibility, State: x.State, Uploaders: ids}, viewer, rt.d.Config.SiteVisibility) {
					v.Versions = append(v.Versions, MyVersionView{VersionItem: VersionItem{
						ID: x.ID, Video: videoRef(s.Video), Kind: x.Kind, Variant: x.Variant, Lang: x.Lang, SHA256: x.SHA256,
						CreatedAt: x.CreatedAt, Uploaders: []UploaderView{},
					}, State: hubrepo.StateWithdrawn})
					continue
				}
			}
			mv := MyVersionView{VersionItem: versionItem(x.Version, s.Video, ups), Visibility: x.Visibility, State: x.State}
			if x.State == "removed" && x.RemovalSource != "" {
				mv.Removal = &RemovalView{Source: x.RemovalSource, Reason: x.RemovalReason}
			}
			v.Versions = append(v.Versions, mv)
		}
		o.Body.Shares = append(o.Body.Shares, v)
	}
	return o, nil
}

// registerModeration 註冊 M5-S5 的管理路由（Access 同 HACC 規格「介面」的權限矩陣）。
func registerModeration(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	authErrs := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}
	mod, admin := perm(hubconfig.PermModerate), perm(hubconfig.PermAdmin)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-shares", Method: http.MethodGet, Path: "/api/v1/me/shares",
		Summary: "我的分享與版本狀態（含被移除的原因）", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermUpload), "HACC-R16"), rt.myShares)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-takedowns-list", Method: http.MethodGet, Path: "/api/v1/admin/takedowns",
		Summary: "列出下架申請（含聯絡 email）", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, mod, "HACC-R16"), rt.listTakedowns)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-takedowns-decide", Method: http.MethodPost, Path: "/api/v1/admin/takedowns/{id}/decision",
		Summary: "決定下架申請（remove 或 reject）", Errors: append(authErrs, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity),
	}, mod, "HACC-R16"), rt.decideTakedown)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-reports-list", Method: http.MethodGet, Path: "/api/v1/admin/reports",
		Summary: "列出檢舉（含檢舉者）", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, mod, "HREP-R14"), rt.listReports)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-reports-decide", Method: http.MethodPost, Path: "/api/v1/admin/reports/{id}/decision",
		Summary: "處理檢舉（dismiss 或 remove）", Errors: append(authErrs, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity),
	}, mod, "HREP-R15"), rt.decideReport)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-versions-hide", Method: http.MethodPost, Path: "/api/v1/admin/versions/{id}/hide",
		Summary: "隱藏版本", Errors: append(authErrs, http.StatusNotFound, http.StatusConflict),
	}, mod, "HREP-R15"), rt.hideVersion)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-users-list", Method: http.MethodGet, Path: "/api/v1/admin/users",
		Summary: "列出使用者", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, admin, "HACC-R12"), rt.listUsers)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-users-suspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/suspend",
		Summary: "停用帳號（session 與 Hub token 回 403，票不計入分數）", Errors: append(authErrs, http.StatusNotFound, http.StatusUnprocessableEntity),
	}, admin, "HACC-R17"), rt.suspendUser)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-users-unsuspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/unsuspend",
		Summary: "恢復帳號（停用前的 session 與 Hub token 失效）", Errors: append(authErrs, http.StatusNotFound),
	}, admin, "HACC-R17"), rt.unsuspendUser)
}
