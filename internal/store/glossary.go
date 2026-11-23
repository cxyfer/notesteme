package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// 詞彙表的範圍（glossaries.scope）。全域詞彙表只有一份，scope_id 固定是 GlobalGlossaryID。
const (
	GlossaryGlobal        = "global"
	GlossaryChannel       = "channel"
	GlobalGlossaryID      = "default"
	TermCandidatePending  = "pending"
	TermCandidateAccepted = "accepted"
	TermCandidateRejected = "rejected"
)

// Glossary 是一份詞彙表。UpdatedAt 為零值代表還沒有儲存過（terms 是空的）。
type Glossary struct {
	Scope     string
	ScopeID   string
	Terms     []string
	UpdatedAt time.Time
}

// Glossary 讀取詞彙表；還沒有儲存過時回傳空的詞彙表（不是錯誤）。
func (r *Repo) Glossary(ctx context.Context, scope, scopeID string) (Glossary, error) {
	row, err := r.q.GetGlossary(ctx, sqlcdb.GetGlossaryParams{Scope: scope, ScopeID: scopeID})
	if errors.Is(err, sql.ErrNoRows) {
		return Glossary{Scope: scope, ScopeID: scopeID, Terms: []string{}}, nil
	}
	if err != nil {
		return Glossary{}, err
	}
	g := Glossary{Scope: row.Scope, ScopeID: row.ScopeID, UpdatedAt: fromMs(row.UpdatedAt)}
	if err := json.Unmarshal([]byte(row.TermsJson), &g.Terms); err != nil {
		return Glossary{}, err
	}
	if g.Terms == nil {
		g.Terms = []string{}
	}
	return g, nil
}

// PutGlossary 整份取代詞彙表。
func (r *Repo) PutGlossary(ctx context.Context, scope, scopeID string, terms []string) error {
	if terms == nil {
		terms = []string{}
	}
	raw, err := json.Marshal(terms)
	if err != nil {
		return err
	}
	return r.q.PutGlossary(ctx, sqlcdb.PutGlossaryParams{Scope: scope, ScopeID: scopeID, TermsJson: string(raw), Now: r.nowMs()})
}

// TermCandidate 是 correct 提出的術語候選（COR-R6）。
type TermCandidate struct {
	ID        string
	ChannelID string
	ItemID    string // 影音被永久刪除之後是空字串
	Term      string
	State     string
	CreatedAt time.Time
	// 決定的內容（GLOS-R1）：pending 時都是零值；rejected 只有 DecidedAt。
	DecidedTerm  string
	DecidedScope string
	DecidedAt    time.Time
}

// TermDecision 是寫進候選的決定（GLOS-R1）。Term、Scope 在 rejected 時是空字串。
type TermDecision struct {
	State string
	Term  string
	Scope string
}

// toTermCandidate 把資料列轉成 TermCandidate。
func toTermCandidate(row sqlcdb.GlossaryCandidate) TermCandidate {
	c := TermCandidate{ID: row.ID, ChannelID: row.ChannelID, Term: row.Term, State: row.State, CreatedAt: fromMs(row.CreatedAt)}
	if row.ItemID != nil {
		c.ItemID = *row.ItemID
	}
	if row.DecidedTerm != nil {
		c.DecidedTerm = *row.DecidedTerm
	}
	if row.DecidedScope != nil {
		c.DecidedScope = *row.DecidedScope
	}
	if row.DecidedAt != nil {
		c.DecidedAt = fromMs(*row.DecidedAt)
	}
	return c
}

// AddTermCandidate 新增一個 pending 的術語候選；這個頻道已經有同一個詞（任何狀態）時不新增，回傳 false。
func (r *Repo) AddTermCandidate(ctx context.Context, channelID, itemID, term string) (bool, error) {
	var item *string
	if itemID != "" {
		item = &itemID
	}
	n, err := r.q.InsertTermCandidate(ctx, sqlcdb.InsertTermCandidateParams{
		ID: NewID(), ChannelID: channelID, ItemID: item, Term: term, Now: r.nowMs(),
	})
	return n > 0, err
}

// TermCandidates 依建立順序列出頻道的術語候選。
func (r *Repo) TermCandidates(ctx context.Context, channelID string) ([]TermCandidate, error) {
	rows, err := r.q.TermCandidatesByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]TermCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTermCandidate(row))
	}
	return out, nil
}

// CountTermCandidates 回傳頻道已有的術語候選數（任何狀態）。
func (r *Repo) CountTermCandidates(ctx context.Context, channelID string) (int, error) {
	n, err := r.q.CountTermCandidates(ctx, channelID)
	return int(n), err
}

// TermCandidate 讀取頻道的一個術語候選；找不到回傳 ErrNotFound。
func (r *Repo) TermCandidate(ctx context.Context, channelID, id string) (TermCandidate, error) {
	row, err := r.q.GetTermCandidate(ctx, sqlcdb.GetTermCandidateParams{ID: id, ChannelID: channelID})
	if errors.Is(err, sql.ErrNoRows) {
		return TermCandidate{}, ErrNotFound
	}
	if err != nil {
		return TermCandidate{}, err
	}
	return toTermCandidate(row), nil
}

// TermCandidateByID 依 id 讀取術語候選（不限頻道，GLOS-R4 的批次決定）；找不到回傳 ErrNotFound。
func (r *Repo) TermCandidateByID(ctx context.Context, id string) (TermCandidate, error) {
	row, err := r.q.GetTermCandidateByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return TermCandidate{}, ErrNotFound
	}
	if err != nil {
		return TermCandidate{}, err
	}
	return toTermCandidate(row), nil
}

// AddTermCandidateAt 同 AddTermCandidate，但 created_at 由呼叫端指定（測試的 seeding）。
func (r *Repo) AddTermCandidateAt(ctx context.Context, channelID, itemID, term string, at time.Time) (bool, error) {
	var item *string
	if itemID != "" {
		item = &itemID
	}
	n, err := r.q.InsertTermCandidateAt(ctx, sqlcdb.InsertTermCandidateAtParams{
		ID: NewID(), ChannelID: channelID, ItemID: item, Term: term, CreatedAt: at.UnixMilli(),
	})
	return n > 0, err
}

// DecideTermCandidate 把 pending 的術語候選改成 d.State，並記錄決定的內容與時間（GLOS-R1）；
// 候選已不是 pending 時不改動，回傳 false。
func (r *Repo) DecideTermCandidate(ctx context.Context, channelID, id string, d TermDecision) (bool, error) {
	now := r.nowMs()
	n, err := r.q.DecideTermCandidate(ctx, sqlcdb.DecideTermCandidateParams{
		State: d.State, DecidedTerm: nonEmpty(d.Term), DecidedScope: nonEmpty(d.Scope), Now: &now, ID: id, ChannelID: channelID,
	})
	return n > 0, err
}

// nonEmpty 把空字串換成 NULL。
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// TermCandidateReview 是審核清單的一列（GLOS-R5）：候選加上頻道與第一次提出它的影音。
type TermCandidateReview struct {
	TermCandidate
	ChannelPlatform, ChannelExternalID, ChannelTitle string
	// HasItem 為 false 代表影音已永久刪除。
	HasItem                    bool
	ItemPlatform, ItemExternal string
	ItemPart                   int
	ItemTitle                  string
}

// TermCandidateFilter 是審核清單的條件。State 是 pending、accepted、rejected 或 all；ChannelID 空字串代表全部頻道；
// AfterMs 小於 0 代表第一頁，否則從 (AfterMs, AfterID) 之後開始。
type TermCandidateFilter struct {
	State     string
	ChannelID string
	AfterMs   int64
	AfterID   string
	Limit     int
}

// ReviewTermCandidates 依 created_at、id 由舊到新列出術語候選（GLOS-R5）。
func (r *Repo) ReviewTermCandidates(ctx context.Context, f TermCandidateFilter) ([]TermCandidateReview, error) {
	rows, err := r.q.ReviewTermCandidates(ctx, sqlcdb.ReviewTermCandidatesParams{
		State: f.State, ChannelID: f.ChannelID, AfterMs: f.AfterMs, AfterID: f.AfterID, Lim: int64(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]TermCandidateReview, 0, len(rows))
	for _, row := range rows {
		v := TermCandidateReview{
			TermCandidate: toTermCandidate(sqlcdb.GlossaryCandidate{
				ID: row.ID, ChannelID: row.ChannelID, ItemID: row.ItemID, Term: row.Term, State: row.State, CreatedAt: row.CreatedAt,
				DecidedTerm: row.DecidedTerm, DecidedScope: row.DecidedScope, DecidedAt: row.DecidedAt,
			}),
			ChannelPlatform: row.ChannelPlatform, ChannelExternalID: row.ChannelExternalID, ChannelTitle: row.ChannelTitle,
		}
		if row.ItemPlatform != nil {
			v.HasItem = true
			v.ItemPlatform = *row.ItemPlatform
			v.ItemExternal = deref(row.ItemExternalID)
			v.ItemTitle = deref(row.ItemTitle)
			if row.ItemPart != nil {
				v.ItemPart = int(*row.ItemPart)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// CountReviewTermCandidates 回傳符合條件（不含游標）的術語候選數。
func (r *Repo) CountReviewTermCandidates(ctx context.Context, state, channelID string) (int, error) {
	n, err := r.q.CountReviewTermCandidates(ctx, sqlcdb.CountReviewTermCandidatesParams{State: state, ChannelID: channelID})
	return int(n), err
}
