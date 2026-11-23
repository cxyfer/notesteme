package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是 COR 的詞彙表與術語候選（COR-R4、R6）：讀取用 read，修改用 admin（AUTH 規格的 M2 路由 scope）。
// 術語候選的核可與拒絕是 PATCH（admin）；GLOS 的跨頻道審核清單與批次決定在 term_review.go。

// glossaryService 是詞彙表 handler 需要的 service。*pipeline.Pipeline 實作它。
type glossaryService interface {
	Glossary(ctx context.Context, scope, id string) (store.Glossary, error)
	PutGlossary(ctx context.Context, scope, id string, terms []string) (store.Glossary, error)
	TermCandidates(ctx context.Context, channelKey string) ([]store.TermCandidate, error)
	DecideTermCandidateWith(ctx context.Context, channelKey, candidateID string, in pipeline.TermDecisionInput) (store.TermCandidate, error)
	DecideTermCandidates(ctx context.Context, decisions []pipeline.TermBatchDecision) ([]store.TermCandidate, error)
	ListTermCandidates(ctx context.Context, q pipeline.TermCandidateQuery) (pipeline.TermCandidatePage, error)
}

var _ glossaryService = (*pipeline.Pipeline)(nil)

type glossariesAPI struct {
	rt  *routes
	svc glossaryService
}

func (rt *routes) registerGlossaries(api huma.API) {
	h := &glossariesAPI{rt: rt}
	if rt.d.Pipeline != nil {
		h.svc = rt.d.Pipeline
	}
	h.register(api)
}

func (h *glossariesAPI) register(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "glossaries-get", Method: http.MethodGet, Path: "/api/glossaries/{scope}/{id}",
		Summary: "讀取詞彙表；scope 是 global（id 固定 default）或 channel（id 是頻道 UUID 或 <platform>:<external_id>）；還沒有儲存過時 terms 是空陣列",
	}, auth.ScopeRead, h.get)
	registerScoped(api, huma.Operation{
		OperationID: "glossaries-put", Method: http.MethodPut, Path: "/api/glossaries/{scope}/{id}",
		Summary: "整份取代詞彙表；natural key 找不到頻道時建立它",
	}, auth.ScopeAdmin, h.put)
	registerScoped(api, huma.Operation{
		OperationID: "channels-term-candidates", Method: http.MethodGet, Path: "/api/channels/{id}/term-candidates",
		Summary: "頻道的術語候選（校正時模型提出、等待核可；不會自動加入詞彙表）",
	}, auth.ScopeRead, h.candidates)
	registerScoped(api, huma.Operation{
		OperationID: "channels-term-candidate-decide", Method: http.MethodPatch, Path: "/api/channels/{id}/term-candidates/{candidate}",
		Summary: "核可（accepted，詞加進頻道或全域詞彙表）或拒絕（rejected）一個 pending 的術語候選；接受時可以改寫 term、選擇 scope；已決定成另一個結果時回 409",
	}, auth.ScopeAdmin, h.decide)
	h.registerReview(api)
}

// Glossary 是一份詞彙表。
type Glossary struct {
	Scope string   `json:"scope" enum:"global,channel"`
	ID    string   `json:"id" doc:"global 是 default；channel 是頻道的 UUID"`
	Terms []string `json:"terms"`
	// UpdatedAt 是最後一次儲存的時間；還沒有儲存過時是 null。
	UpdatedAt *time.Time `json:"updated_at"`
}

type glossaryOutput struct{ Body Glossary }

func toGlossary(g store.Glossary) Glossary {
	out := Glossary{Scope: g.Scope, ID: g.ScopeID, Terms: g.Terms}
	if out.Terms == nil {
		out.Terms = []string{}
	}
	if !g.UpdatedAt.IsZero() {
		t := g.UpdatedAt
		out.UpdatedAt = &t
	}
	return out
}

type glossaryPath struct {
	Scope string `path:"scope" enum:"global,channel"`
	ID    string `path:"id" doc:"global 是 default；channel 是頻道的 UUID 或 <platform>:<external_id>"`
}

func (h *glossariesAPI) service(ctx context.Context) (glossaryService, error) {
	if h.svc == nil {
		return nil, h.rt.noPipeline(ctx)
	}
	return h.svc, nil
}

func (h *glossariesAPI) get(ctx context.Context, in *glossaryPath) (*glossaryOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	g, err := svc.Glossary(ctx, in.Scope, in.ID)
	if err != nil {
		return nil, h.mapErr(ctx, in.Scope, err)
	}
	return &glossaryOutput{Body: toGlossary(g)}, nil
}

type putGlossaryInput struct {
	Scope string `path:"scope" enum:"global,channel"`
	ID    string `path:"id" doc:"global 是 default；channel 是頻道的 UUID 或 <platform>:<external_id>"`
	Body  struct {
		Terms []string `json:"terms" doc:"整份取代；去掉前後空白，不可以有空字串或控制字元，每個詞最多 100 個字元、最多 500 個詞，大小寫相同的重複只留第一個"`
	}
}

func (h *glossariesAPI) put(ctx context.Context, in *putGlossaryInput) (*glossaryOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	g, err := svc.PutGlossary(ctx, in.Scope, in.ID, in.Body.Terms)
	if err != nil {
		return nil, h.mapErr(ctx, in.Scope, err)
	}
	return &glossaryOutput{Body: toGlossary(g)}, nil
}

// TermCandidate 是 correct 提出的術語候選。
type TermCandidate struct {
	ID        string    `json:"id"`
	Term      string    `json:"term"`
	State     string    `json:"state" enum:"pending,accepted,rejected"`
	ItemID    string    `json:"item_id" doc:"提出這個詞的影音；影音被永久刪除之後是空字串"`
	CreatedAt time.Time `json:"created_at"`
	// 決定的內容（GLOS-R1）：pending 時都是 null；rejected 只有 decided_at。
	DecidedTerm  *string    `json:"decided_term" doc:"接受時實際加進詞彙表的寫法；pending 與 rejected 是 null"`
	DecidedScope *string    `json:"decided_scope" doc:"channel 或 global；pending 與 rejected 是 null"`
	DecidedAt    *time.Time `json:"decided_at" doc:"決定的時間；pending 是 null"`
}

// decided 回傳候選決定的三個欄位（GLOS-R1）；零值換成 null。
func decided(c store.TermCandidate) (term, scope *string, at *time.Time) {
	if c.DecidedTerm != "" {
		v := c.DecidedTerm
		term = &v
	}
	if c.DecidedScope != "" {
		v := c.DecidedScope
		scope = &v
	}
	if !c.DecidedAt.IsZero() {
		v := c.DecidedAt
		at = &v
	}
	return term, scope, at
}

func toTermCandidate(c store.TermCandidate) TermCandidate {
	out := TermCandidate{ID: c.ID, Term: c.Term, State: c.State, ItemID: c.ItemID, CreatedAt: c.CreatedAt}
	out.DecidedTerm, out.DecidedScope, out.DecidedAt = decided(c)
	return out
}

type termCandidatesOutput struct {
	Body struct {
		Candidates []TermCandidate `json:"candidates"`
	}
}

type channelTermsInput struct {
	ID string `path:"id" doc:"頻道的 UUID，或 <platform>:<external_id>"`
}

func (h *glossariesAPI) candidates(ctx context.Context, in *channelTermsInput) (*termCandidatesOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.TermCandidates(ctx, in.ID)
	if err != nil {
		return nil, h.mapErr(ctx, store.GlossaryChannel, err)
	}
	out := &termCandidatesOutput{}
	out.Body.Candidates = make([]TermCandidate, 0, len(list))
	for _, c := range list {
		out.Body.Candidates = append(out.Body.Candidates, toTermCandidate(c))
	}
	return out, nil
}

type decideCandidateInput struct {
	ID        string `path:"id" doc:"頻道的 UUID，或 <platform>:<external_id>"`
	Candidate string `path:"candidate" doc:"術語候選的 id"`
	Body      struct {
		State string `json:"state" enum:"accepted,rejected"`
		// term、scope 不用 enum／長度限制：規格指定了錯誤的 path 與訊息（GLOS-R1、R2），由 pipeline 檢查。
		Term  *string `json:"term,omitempty" doc:"接受時實際加進詞彙表的寫法（省略時是候選原本的詞）；控制字元換成空白、合併連續空白，最多 100 個字元；拒絕時不能帶"`
		Scope *string `json:"scope,omitempty" doc:"channel（預設）或 global；拒絕時不能帶"`
	}
}

type termCandidateOutput struct{ Body TermCandidate }

func (h *glossariesAPI) decide(ctx context.Context, in *decideCandidateInput) (*termCandidateOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.DecideTermCandidateWith(ctx, in.ID, in.Candidate, pipeline.TermDecisionInput{State: in.Body.State, Term: in.Body.Term, Scope: in.Body.Scope})
	if err != nil {
		if e := termDecisionErr(err, ""); e != nil {
			return nil, e
		}
		return nil, h.mapErr(ctx, store.GlossaryChannel, err)
	}
	return &termCandidateOutput{Body: toTermCandidate(c)}, nil
}

// mapErr 同 routes.mapErr；找不到時，頻道詞彙表說找不到頻道，全域詞彙表說找不到詞彙表。
func (h *glossariesAPI) mapErr(ctx context.Context, scope string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound) && scope == store.GlossaryChannel:
		return newError(http.StatusNotFound, CodeNotFound, "找不到這個頻道。")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, pipeline.ErrGlossaryScope):
		return newError(http.StatusNotFound, CodeNotFound, "找不到這個詞彙表。")
	}
	return h.rt.mapErr(ctx, err)
}
