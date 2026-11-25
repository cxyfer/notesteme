package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 GLOS 的術語審核：跨頻道的審核清單（GLOS-R5，read）與批次決定（GLOS-R4，admin）。
// 單一候選的決定仍是 glossaries.go 的 PATCH（C4 加上 GLOS-R1 的 term、scope）。

// CodeGlossaryFull 是目標詞彙表已滿、不能再接受術語（GLOS-R2）。
const CodeGlossaryFull = "glossary_full"

func (h *glossariesAPI) registerReview(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "term-candidates-list", Method: http.MethodGet, Path: "/api/term-candidates",
		Summary: "跨頻道的術語候選審核清單，依提出時間由舊到新",
	}, auth.ScopeRead, h.review)
	registerScoped(api, huma.Operation{
		OperationID: "term-candidates-decide", Method: http.MethodPost, Path: "/api/term-candidates/decide",
		Summary: "一次決定多個術語候選；整批是一個交易，任何一項失敗時整批都不套用",
	}, auth.ScopeAdmin, h.decideBatch)
}

// TermCandidateChannel 是審核清單中候選所屬的頻道。
type TermCandidateChannel struct {
	ID    string `json:"id"`
	Key   string `json:"key" doc:"<platform>:<external_id>"`
	Title string `json:"title"`
}

// TermCandidateItem 是第一次提出這個詞的影音。
type TermCandidateItem struct {
	ID    string `json:"id"`
	Ref   string `json:"ref" doc:"影音代號，例如 yt:<id>"`
	Title string `json:"title"`
}

// TermCandidateReview 是審核清單的一列（GLOS-R5）。
type TermCandidateReview struct {
	ID    string `json:"id"`
	Term  string `json:"term" doc:"模型提出的原詞"`
	State string `json:"state" enum:"pending,accepted,rejected"`
	// 決定的內容，同 TermCandidate（GLOS-R1）。
	DecidedTerm  *string              `json:"decided_term" doc:"接受時實際加進詞彙表的寫法；pending 與 rejected 是 null"`
	DecidedScope *string              `json:"decided_scope" doc:"channel 或 global；pending 與 rejected 是 null"`
	DecidedAt    *time.Time           `json:"decided_at" doc:"決定的時間；pending 是 null"`
	Channel      TermCandidateChannel `json:"channel"`
	// Item 在影音已永久刪除時是 null。
	Item      *TermCandidateItem `json:"item"`
	CreatedAt time.Time          `json:"created_at"`
}

type reviewInput struct {
	// state 不用 enum：規格指定 422 的 path 是 state（GLOS-R5），由 pipeline 檢查。
	State   string `query:"state" doc:"pending（預設）、accepted、rejected 或 all"`
	Channel string `query:"channel" doc:"頻道的 UUID 或 <platform>:<external_id>；省略時是全部頻道"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200" doc:"單頁筆數"`
	Cursor  string `query:"cursor" doc:"上一頁回傳的 next_cursor"`
}

type reviewOutput struct {
	Body struct {
		Candidates []TermCandidateReview `json:"candidates"`
		Total      int                   `json:"total" doc:"符合條件的總數，和目前在第幾頁無關"`
		NextCursor string                `json:"next_cursor,omitempty" doc:"下一頁的游標；最後一頁沒有這個欄位"`
	}
}

// reviewFingerprint 是審核清單篩選條件的指紋；加上前綴，影音列表的游標不能拿來這裡用。
func reviewFingerprint(state, channel string) string {
	sum := sha256.Sum256([]byte("term-candidates\x00" + state + "\x00" + channel))
	return hex.EncodeToString(sum[:8])
}

func (h *glossariesAPI) review(ctx context.Context, in *reviewInput) (*reviewOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	state, err := pipeline.CheckTermCandidateState(in.State)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	fp := reviewFingerprint(state, in.Channel)
	q := pipeline.TermCandidateQuery{State: state, Channel: in.Channel, Limit: in.Limit}
	if in.Cursor != "" {
		if q.After, err = decodeItemCursor(in.Cursor, h.rt.d.CursorKey, fp, false); err != nil {
			return nil, errBadCursor()
		}
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	page, err := svc.ListTermCandidates(ctx, q)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	out := &reviewOutput{}
	out.Body.Candidates = make([]TermCandidateReview, 0, len(page.Candidates))
	for _, c := range page.Candidates {
		v := TermCandidateReview{
			ID: c.ID, Term: c.Term, State: c.State, CreatedAt: c.CreatedAt,
			Channel: TermCandidateChannel{ID: c.ChannelID, Key: c.ChannelPlatform + ":" + c.ChannelExternalID, Title: c.ChannelTitle},
		}
		v.DecidedTerm, v.DecidedScope, v.DecidedAt = decided(c.TermCandidate)
		if c.HasItem {
			v.Item = &TermCandidateItem{ID: c.ItemID, Ref: source.Ref(c.ItemPlatform, c.ItemExternal, c.ItemPart), Title: c.ItemTitle}
		}
		out.Body.Candidates = append(out.Body.Candidates, v)
	}
	out.Body.Total = page.Total
	if page.Next != nil {
		out.Body.NextCursor = encodeItemCursor(*page.Next, h.rt.d.CursorKey, fp, false)
	}
	return out, nil
}

// termDecisionBody 是批次決定的一項。state、term、scope 不用 schema 的限制：錯誤要指到 decisions.<n>.<欄位>（GLOS-R4）。
type termDecisionBody struct {
	Candidate string  `json:"candidate" doc:"術語候選的 id"`
	State     string  `json:"state" doc:"accepted 或 rejected"`
	Term      *string `json:"term,omitempty" doc:"同 PATCH 的 term"`
	Scope     *string `json:"scope,omitempty" doc:"同 PATCH 的 scope"`
}

type decideBatchInput struct {
	Body struct {
		Decisions []termDecisionBody `json:"decisions" doc:"最多 100 項，同一個 candidate 只能出現一次"`
	}
}

type decideBatchOutput struct {
	Body struct {
		Candidates []TermCandidate `json:"candidates" doc:"順序與 decisions 相同"`
	}
}

func (h *glossariesAPI) decideBatch(ctx context.Context, in *decideBatchInput) (*decideBatchOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	ds := make([]pipeline.TermBatchDecision, 0, len(in.Body.Decisions))
	for _, d := range in.Body.Decisions {
		ds = append(ds, pipeline.TermBatchDecision{Candidate: d.Candidate,
			TermDecisionInput: pipeline.TermDecisionInput{State: d.State, Term: d.Term, Scope: d.Scope}})
	}
	list, err := svc.DecideTermCandidates(ctx, ds)
	if err != nil {
		var be *pipeline.TermBatchError
		if errors.As(err, &be) {
			prefix := "decisions." + strconv.Itoa(be.Index)
			if e := termDecisionErr(be.Err, prefix); e != nil {
				return nil, e
			}
		}
		return nil, h.rt.mapErr(ctx, err)
	}
	out := &decideBatchOutput{}
	out.Body.Candidates = make([]TermCandidate, 0, len(list))
	for _, c := range list {
		out.Body.Candidates = append(out.Body.Candidates, toTermCandidate(c))
	}
	return out, nil
}

// termDecisionErr 把一個決定的錯誤對應成 HTTP 錯誤；prefix 不是空字串時（批次），errors[].path 指到那一項：
// 欄位錯誤是 <prefix>.<欄位>，404 是 <prefix>.candidate，409 是 <prefix>。不是決定的錯誤時回傳 nil。
func termDecisionErr(err error, prefix string) *apierr.Error {
	var inv *pipeline.InvalidError
	var e *apierr.Error
	switch {
	case errors.As(err, &inv):
		e = fieldsError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Msg, inv.Fields)
		e.Errors = nil // 批次時路徑要加上前綴
		for _, f := range inv.Fields {
			if prefix != "" {
				f.Path = prefix + "." + f.Path
			}
			e.Errors = append(e.Errors, f)
		}
		return e
	case errors.Is(err, store.ErrNotFound):
		e = newError(http.StatusNotFound, CodeNotFound, "找不到這個術語候選。")
		if prefix != "" {
			e.Errors = []validate.FieldError{{Path: prefix + ".candidate", Message: e.Message}}
		}
	case errors.Is(err, pipeline.ErrTermCandidateDecided):
		e = newError(http.StatusConflict, CodeCandidateDecided, "這個術語候選已經決定過了。")
	case errors.Is(err, pipeline.ErrGlossaryFull):
		e = apierr.Newf(http.StatusConflict, CodeGlossaryFull, "glossary_full", strconv.Itoa(pipeline.MaxGlossaryTerms))
	default:
		return nil
	}
	if prefix != "" && e.Errors == nil {
		e.Errors = []validate.FieldError{{Path: prefix, Message: e.Message}}
	}
	return e
}
