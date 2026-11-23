package hubapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 投票與檢舉（M5-S5，HREP-R13～R15）。
//
// 順序：權限（middleware）→ 版本看得到（不存在與看不到 404，removed 410，同版本詳情）→ 只接受 visible
// （上傳者與版主看得到的 hidden 回 409 version_hidden）→ body 的欄位（422，errors[].path）→ 自己的版本（只限投票）→
// 帳號齡 → 重複檢舉 → 次數限制 → 寫入。分數在 store 的交易中以「沒有被停用的帳號的票」整筆重算。

const (
	codeOwnVersion      = "own_version"
	codeAccountTooNew   = "account_too_new"
	codeVersionHidden   = "version_hidden"
	codeUseTakedown     = "use_takedown"
	codeAlreadyReported = "already_reported"

	msgOwnVersion      = "不能對自己上傳的版本投票。"
	msgAccountTooNew   = "帳號建立未滿 24 小時，還不能投票或檢舉。"
	msgVersionHidden   = "這個版本目前被隱藏，不能投票或檢舉。"
	msgUseTakedown     = "版權與隱私問題請改用下架申請。"
	msgAlreadyReported = "你已經檢舉過這個版本，管理員處理前不能重複檢舉。"
	actionTakedown     = "提出下架申請"
)

// minVoterAge 是可以投票與檢舉的帳號齡（HREP-R13、R14）。
const minVoterAge = 24 * time.Hour

// maxReportNote 是檢舉說明的上限（字元）。
const maxReportNote = 1000

// reportReasons 是檢舉可以用的理由；takedownOnly 要改用下架申請。
var (
	reportReasons = []string{"wrong_language", "bad_quality", "spam", "offensive", "other"}
	takedownOnly  = []string{"copyright", "privacy"}
)

type voteInput struct {
	ID   string `path:"id" maxLength:"64" doc:"版本 ID"`
	Body struct {
		// 範圍不寫進 schema：由 handler 驗證，422 的 errors[].path 才會指到欄位。
		Value *int `json:"value" required:"false" doc:"1 或 -1"`
	}
}

// VoteView 是投票後的結果。
type VoteView struct {
	Value int `json:"value"`
	Score int `json:"score" doc:"沒有被停用的帳號的票數總和"`
}

type voteOutput struct{ Body VoteView }

type versionIDPath struct {
	ID string `path:"id" maxLength:"64" doc:"版本 ID"`
}

type reportInput struct {
	ID   string `path:"id" maxLength:"64" doc:"版本 ID"`
	Body struct {
		Reason string `json:"reason" required:"false" doc:"wrong_language、bad_quality、spam、offensive、other（版權與隱私問題改用下架申請）"`
		Note   string `json:"note,omitempty" required:"false" doc:"說明，最多 1,000 個字元"`
	}
}

// ReportCreatedView 是建立的檢舉。
type ReportCreatedView struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type reportOutput struct{ Body ReportCreatedView }

func registerCommunity(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	errs := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusGone,
		http.StatusUnprocessableEntity, http.StatusTooManyRequests}
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-vote-put", Method: http.MethodPut, Path: "/api/v1/versions/{id}/vote",
		Summary: "投票或改票（一人一票）", Errors: errs,
	}, perm(hubconfig.PermRead), "HREP-R13"), rt.putVote)
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-vote-delete", Method: http.MethodDelete, Path: "/api/v1/versions/{id}/vote", DefaultStatus: http.StatusNoContent,
		Summary: "收回投票", Errors: errs,
	}, perm(hubconfig.PermRead), "HREP-R13"), rt.deleteVote)
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-reports-create", Method: http.MethodPost, Path: "/api/v1/versions/{id}/reports", DefaultStatus: http.StatusCreated,
		Summary: "檢舉版本（版權與隱私問題改用下架申請）", Errors: errs,
	}, perm(hubconfig.PermRead), "HREP-R14、R15"), rt.createReport)
}

// communityTarget 回傳可以投票或檢舉的版本：看得到、不是 removed（410）、而且是 visible。
func (rt *routes) communityTarget(ctx context.Context, id string) (*hubauth.Principal, hubstore.Version, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, hubstore.Version{}, err
	}
	v, _, err := rt.seeVersion(ctx, id)
	if err != nil {
		return nil, v, err
	}
	if v.State != hubrepo.StateVisible {
		return nil, v, apierr.New(http.StatusConflict, codeVersionHidden, msgVersionHidden)
	}
	return p, v, nil
}

// checkAccountAge 是 HREP-R13、R14 的帳號齡限制。
func (rt *routes) checkAccountAge(p *hubauth.Principal) error {
	if rt.d.Clock.Now().Sub(p.User.CreatedAt) < minVoterAge {
		return apierr.New(http.StatusForbidden, codeAccountTooNew, msgAccountTooNew)
	}
	return nil
}

// limitByUsage 依一段時間內的次數限流：已達 limit 時回 429（retry_after_s 是最早的一筆滿一個 window 的剩餘時間）。
func (rt *routes) limitByUsage(u hubstore.Usage, limit int, window time.Duration) error {
	if limit <= 0 || u.Count < limit {
		return nil
	}
	return errRateLimited(max(u.Oldest.Add(window).Sub(rt.d.Clock.Now()), time.Second))
}

// castVote 投票（value 為 0 時收回）：自己的版本、帳號齡、每小時的次數，之後寫入並回傳新的分數。
func (rt *routes) castVote(ctx context.Context, p *hubauth.Principal, v hubstore.Version, value int) (int, error) {
	if value != 0 {
		ups, err := rt.d.Store.VersionUploaders(ctx, v.ID)
		if err != nil {
			return 0, rt.apiError(ctx, err)
		}
		if slices.ContainsFunc(ups, func(u hubstore.Uploader) bool { return u.UserID == p.User.ID }) {
			return 0, apierr.New(http.StatusForbidden, codeOwnVersion, msgOwnVersion)
		}
	}
	if err := rt.checkAccountAge(p); err != nil {
		return 0, err
	}
	u, err := rt.d.Store.VoteUsage(ctx, p.User.ID, rt.d.Clock.Now().Add(-time.Hour))
	if err != nil {
		return 0, rt.apiError(ctx, err)
	}
	if err := rt.limitByUsage(u, rt.rateLimits().VotesPerHour, time.Hour); err != nil {
		return 0, err
	}
	score, err := rt.d.Store.SetVote(ctx, v.ID, p.User.ID, value)
	if err != nil {
		return 0, rt.apiError(ctx, err)
	}
	return score, nil
}

func (rt *routes) putVote(ctx context.Context, in *voteInput) (*voteOutput, error) {
	// 先確認版本，再驗證 body：看不到的版本不因 body 的內容而回不同的錯誤。
	p, v, err := rt.communityTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Body.Value == nil || (*in.Body.Value != 1 && *in.Body.Value != -1) {
		var f fieldErrs
		f.add("value", "必須是 1 或 -1。")
		return nil, f.err()
	}
	score, err := rt.castVote(ctx, p, v, *in.Body.Value)
	if err != nil {
		return nil, err
	}
	return &voteOutput{Body: VoteView{Value: *in.Body.Value, Score: score}}, nil
}

func (rt *routes) deleteVote(ctx context.Context, in *versionIDPath) (*struct{}, error) {
	p, v, err := rt.communityTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if _, err := rt.castVote(ctx, p, v, 0); err != nil {
		return nil, err
	}
	return nil, nil
}

// freeText 驗證使用者輸入的說明文字：合法的 UTF-8、最多 maxRunes 個字元，除了換行與 tab 不能有控制字元。
func freeText(s string, maxRunes int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' })
}

func (rt *routes) createReport(ctx context.Context, in *reportInput) (*reportOutput, error) {
	p, v, err := rt.communityTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if slices.Contains(takedownOnly, in.Body.Reason) {
		e := apierr.New(http.StatusUnprocessableEntity, codeUseTakedown, msgUseTakedown)
		e.Actions = []string{actionTakedown}
		return nil, e
	}
	var f fieldErrs
	if !slices.Contains(reportReasons, in.Body.Reason) {
		f.add("reason", oneOf(reportReasons))
	}
	if !freeText(in.Body.Note, maxReportNote) {
		f.add("note", "最多 1,000 個字元，不能有控制字元。")
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	if err := rt.checkAccountAge(p); err != nil {
		return nil, err
	}
	u, err := rt.d.Store.ReportUsage(ctx, p.User.ID, rt.d.Clock.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	if err := rt.limitByUsage(u, rt.rateLimits().ReportsPerDay, 24*time.Hour); err != nil {
		return nil, err
	}
	mo := rt.d.Config.Moderation
	r, hidden, err := rt.d.Store.CreateReport(ctx, hubstore.NewReport{
		VersionID: v.ID, ReporterID: p.User.ID, Reason: in.Body.Reason, Note: strings.TrimSpace(in.Body.Note),
		AutoHide: mo.AutoHideReports, MinAccountAge: mo.AutoHideMinAccountAge,
	})
	if errors.Is(err, hubstore.ErrAlreadyReported) {
		return nil, apierr.New(http.StatusConflict, codeAlreadyReported, msgAlreadyReported)
	}
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	if hidden {
		rt.d.Log.InfoContext(ctx, "版本因檢舉自動隱藏", "version_id", v.ID)
	}
	return &reportOutput{Body: ReportCreatedView{ID: r.ID, State: r.State}}, nil
}
