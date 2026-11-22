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
// 術語候選的核可與拒絕（審核 UI）是 M4。

// glossaryService 是詞彙表 handler 需要的 service。*pipeline.Pipeline 實作它。
type glossaryService interface {
	Glossary(ctx context.Context, scope, id string) (store.Glossary, error)
	PutGlossary(ctx context.Context, scope, id string, terms []string) (store.Glossary, error)
	TermCandidates(ctx context.Context, channelKey string) ([]store.TermCandidate, error)
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
		out.Body.Candidates = append(out.Body.Candidates, TermCandidate{ID: c.ID, Term: c.Term, State: c.State, ItemID: c.ItemID, CreatedAt: c.CreatedAt})
	}
	return out, nil
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
