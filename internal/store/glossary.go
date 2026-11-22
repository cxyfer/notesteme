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
		c := TermCandidate{ID: row.ID, ChannelID: row.ChannelID, Term: row.Term, State: row.State, CreatedAt: fromMs(row.CreatedAt)}
		if row.ItemID != nil {
			c.ItemID = *row.ItemID
		}
		out = append(out, c)
	}
	return out, nil
}

// CountTermCandidates 回傳頻道已有的術語候選數（任何狀態）。
func (r *Repo) CountTermCandidates(ctx context.Context, channelID string) (int, error) {
	n, err := r.q.CountTermCandidates(ctx, channelID)
	return int(n), err
}
