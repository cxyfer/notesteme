package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 GLOS 的術語審核：決定時可以改寫術語、選擇詞彙表的範圍（GLOS-R1～R3），
// 一次決定多個候選（GLOS-R4），以及跨頻道的審核清單（GLOS-R5）。
// 候選的收集與 M2 的單一決定沿用 COR C4（glossary.go、llm.go）。

// 決定的詞彙表範圍（GLOS-R1）。
const (
	TermScopeChannel = "channel"
	TermScopeGlobal  = "global"
	// MaxTermDecisions 是一次批次決定的項目上限（GLOS-R4）。
	MaxTermDecisions = 100
	// 審核清單的 state 篩選：除了候選的三種狀態，all 代表全部（GLOS-R5）。
	TermCandidatesAll = "all"
)

// ErrGlossaryFull 表示目標詞彙表已經有 MaxGlossaryTerms 個詞，不能再接受新的術語（GLOS-R2，API 回 409 glossary_full）。
var ErrGlossaryFull = fmt.Errorf("詞彙表已有 %d 個詞，請先刪除不用的詞再接受新的術語。", MaxGlossaryTerms)

// TermDecisionInput 是一個決定的請求內容（GLOS-R1）。Term、Scope 為 nil 代表省略：
// scope 預設 channel，term 預設候選原本的詞。
type TermDecisionInput struct {
	State string
	Term  *string
	Scope *string
}

// TermBatchDecision 是批次決定的一項（GLOS-R4）。
type TermBatchDecision struct {
	Candidate string
	TermDecisionInput
}

// TermBatchError 指出批次中出錯的項目（GLOS-R4）：Index 從 0 起算，Err 是那一項的錯誤
// （*InvalidError、ErrTermCandidateDecided、ErrGlossaryFull 或 store.ErrNotFound）。整批都沒有套用。
type TermBatchError struct {
	Index int
	Err   error
}

func (e *TermBatchError) Error() string { return fmt.Sprintf("decisions.%d：%v", e.Index, e.Err) }
func (e *TermBatchError) Unwrap() error { return e.Err }

// termInvalid 是指定路徑與訊息的 422；訊息不加路徑前綴（規格寫明了訊息）。
func termInvalid(path, msg string) *InvalidError {
	return &InvalidError{Msg: msg, Fields: []validate.FieldError{{Path: path, Message: msg}}}
}

// checkedDecision 是檢查過的決定；Term 為 nil 時用候選原本的詞。
type checkedDecision struct {
	state, scope string
	term         *string
}

// checkTermDecision 檢查並清理一個決定（GLOS-R1、R2），不讀資料庫。
// 術語來自使用者的輸入，會整份放進之後每個校正請求的固定前綴，所以清理規則與模型提出的候選相同（C4）。
func checkTermDecision(in TermDecisionInput) (checkedDecision, error) {
	switch in.State {
	case store.TermCandidateAccepted, store.TermCandidateRejected:
	default:
		return checkedDecision{}, invalidFields([]validate.FieldError{{Path: "state", Message: "只能是 accepted 或 rejected。"}})
	}
	if in.State == store.TermCandidateRejected {
		switch {
		case in.Term != nil:
			return checkedDecision{}, termInvalid("term", "拒絕時不能指定 term 或 scope。")
		case in.Scope != nil:
			return checkedDecision{}, termInvalid("scope", "拒絕時不能指定 term 或 scope。")
		}
		return checkedDecision{state: in.State}, nil
	}
	d := checkedDecision{state: in.State, scope: TermScopeChannel}
	if in.Scope != nil {
		if *in.Scope != TermScopeChannel && *in.Scope != TermScopeGlobal {
			return checkedDecision{}, invalidFields([]validate.FieldError{{Path: "scope", Message: "只能是 channel 或 global。"}})
		}
		d.scope = *in.Scope
	}
	if in.Term != nil {
		t := llm.CleanTerm(*in.Term)
		switch {
		case t == "":
			return checkedDecision{}, termInvalid("term", "術語不能是空白。")
		case utf8.RuneCountInString(t) > MaxGlossaryTermRunes:
			return checkedDecision{}, termInvalid("term", fmt.Sprintf("術語最多 %d 個字元。", MaxGlossaryTermRunes))
		}
		d.term = &t
	}
	return d, nil
}

// applyTermDecision 在交易 r 中套用一個決定（GLOS-R1～R3），回傳更新後的候選。
//   - 已經是同一個 state：什麼都不做，回傳第一次的決定（冪等，C4）。
//   - 已經決定成另一個結果：ErrTermCandidateDecided。
//   - 接受：詞加進目標詞彙表（大小寫相同的詞已經在時不重複加入）；詞彙表已滿時 ErrGlossaryFull，候選維持 pending。
func (p *Pipeline) applyTermDecision(ctx context.Context, r *store.Repo, cand store.TermCandidate, d checkedDecision) (store.TermCandidate, error) {
	switch {
	case cand.State == d.state:
		return cand, nil
	case cand.State != store.TermCandidatePending:
		return store.TermCandidate{}, ErrTermCandidateDecided
	}
	rec := store.TermDecision{State: d.state}
	if d.state == store.TermCandidateAccepted {
		term := cand.Term
		if d.term != nil {
			term = *d.term
		}
		scope, scopeID := store.GlossaryChannel, cand.ChannelID
		if d.scope == TermScopeGlobal {
			scope, scopeID = store.GlossaryGlobal, store.GlobalGlossaryID
		}
		if err := addGlossaryTerm(ctx, r, scope, scopeID, term); err != nil {
			return store.TermCandidate{}, err
		}
		rec.Term, rec.Scope = term, d.scope
	}
	ok, err := r.DecideTermCandidate(ctx, cand.ChannelID, cand.ID, rec)
	if err != nil {
		return store.TermCandidate{}, err
	}
	if !ok {
		// 交易中先讀過是 pending；這裡只會在別的寫入搶先時發生。
		return store.TermCandidate{}, ErrTermCandidateDecided
	}
	p.log.LogAttrs(ctx, slog.LevelInfo, "術語候選已決定",
		slog.String("candidate", cand.ID), slog.String("channel", cand.ChannelID),
		slog.String("state", rec.State), slog.String("scope", rec.Scope))
	return r.TermCandidateByID(ctx, cand.ID)
}

// addGlossaryTerm 把詞加到詞彙表最後；大小寫相同的詞已經在時不改動，詞彙表已有 MaxGlossaryTerms 個詞時回傳 ErrGlossaryFull。
func addGlossaryTerm(ctx context.Context, r *store.Repo, scope, scopeID, term string) error {
	g, err := r.Glossary(ctx, scope, scopeID)
	if err != nil {
		return err
	}
	key := strings.ToLower(term)
	if slices.ContainsFunc(g.Terms, func(t string) bool { return strings.ToLower(t) == key }) {
		return nil
	}
	if len(g.Terms) >= MaxGlossaryTerms {
		return ErrGlossaryFull
	}
	return r.PutGlossary(ctx, scope, scopeID, append(slices.Clone(g.Terms), term))
}

// DecideTermCandidateWith 決定頻道的一個術語候選（C4 的 PATCH 加上 GLOS-R1 的 term、scope）。
// 寫入詞彙表與更新候選在同一個交易中完成（GLOS-R3）。
func (p *Pipeline) DecideTermCandidateWith(ctx context.Context, channelKey, candidateID string, in TermDecisionInput) (store.TermCandidate, error) {
	d, err := checkTermDecision(in)
	if err != nil {
		return store.TermCandidate{}, err
	}
	var out store.TermCandidate
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		channelID, err := p.resolveChannelID(ctx, r, channelKey, false)
		if err != nil {
			return err
		}
		if channelID == "" {
			return store.ErrNotFound
		}
		cand, err := r.TermCandidate(ctx, channelID, candidateID)
		if err != nil {
			return err
		}
		out, err = p.applyTermDecision(ctx, r, cand, d)
		return err
	})
	return out, err
}

// DecideTermCandidates 一次決定多個術語候選（GLOS-R4）：整批是一個交易，任何一項失敗時整批都不套用，
// 錯誤是 *TermBatchError（指出第一個出錯的項目）；整批層級的錯誤（項目數、重複的候選）是 *InvalidError。
// 回傳的候選順序與 decisions 相同。
func (p *Pipeline) DecideTermCandidates(ctx context.Context, decisions []TermBatchDecision) ([]store.TermCandidate, error) {
	switch {
	case len(decisions) == 0:
		return nil, invalidFields([]validate.FieldError{{Path: "decisions", Message: "至少要有一項。"}})
	case len(decisions) > MaxTermDecisions:
		return nil, invalidFields([]validate.FieldError{{Path: "decisions", Message: fmt.Sprintf("最多 %d 項。", MaxTermDecisions)}})
	}
	seen := make(map[string]bool, len(decisions))
	for i, d := range decisions {
		if seen[d.Candidate] {
			return nil, invalidFields([]validate.FieldError{{Path: fmt.Sprintf("decisions.%d.candidate", i), Message: "同一個候選只能出現一次。"}})
		}
		seen[d.Candidate] = true
	}
	out := make([]store.TermCandidate, len(decisions))
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		// 依序檢查並套用，回報的就是第一個出錯的項目（G4）。
		for i, item := range decisions {
			d, err := checkTermDecision(item.TermDecisionInput)
			if err != nil {
				return &TermBatchError{Index: i, Err: err}
			}
			cand, err := r.TermCandidateByID(ctx, item.Candidate)
			if err != nil {
				return &TermBatchError{Index: i, Err: err}
			}
			if out[i], err = p.applyTermDecision(ctx, r, cand, d); err != nil {
				return &TermBatchError{Index: i, Err: err}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TermCandidateQuery 是審核清單的條件（GLOS-R5）。State 空字串代表 pending；Channel 是頻道的 UUID 或
// <platform>:<external_id>，空字串代表全部頻道；After 是游標的位置（nil 代表第一頁）。
type TermCandidateQuery struct {
	State   string
	Channel string
	Limit   int
	After   *ItemCursor
}

// TermCandidatePage 是審核清單的一頁。Next 為 nil 代表最後一頁。
type TermCandidatePage struct {
	Candidates []store.TermCandidateReview
	Total      int
	Next       *ItemCursor
}

// CheckTermCandidateState 檢查審核清單的 state 篩選，回傳正規化的值（空字串是 pending）。
func CheckTermCandidateState(state string) (string, error) {
	switch state {
	case "":
		return store.TermCandidatePending, nil
	case store.TermCandidatePending, store.TermCandidateAccepted, store.TermCandidateRejected, TermCandidatesAll:
		return state, nil
	}
	return "", invalidFields([]validate.FieldError{{Path: "state", Message: "只能是 pending、accepted、rejected 或 all。"}})
}

// ListTermCandidates 列出所有頻道（或一個頻道）的術語候選，依 created_at、id 由舊到新（GLOS-R5、G5）。
// 頻道找不到時是空的清單（同 C4 的 GET /api/channels/:id/term-candidates）。
func (p *Pipeline) ListTermCandidates(ctx context.Context, q TermCandidateQuery) (TermCandidatePage, error) {
	state, err := CheckTermCandidateState(q.State)
	if err != nil {
		return TermCandidatePage{}, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	r := p.st.Read()
	page := TermCandidatePage{Candidates: []store.TermCandidateReview{}}
	channelID := ""
	if q.Channel != "" {
		channelID, err = p.resolveChannelID(ctx, r, q.Channel, false)
		if errors.Is(err, store.ErrNotFound) || (err == nil && channelID == "") {
			return page, nil
		}
		if err != nil {
			return TermCandidatePage{}, err
		}
	}
	f := store.TermCandidateFilter{State: state, ChannelID: channelID, AfterMs: -1, Limit: q.Limit + 1}
	if q.After != nil {
		f.AfterMs, f.AfterID = q.After.CreatedAtMs, q.After.ID
	}
	rows, err := r.ReviewTermCandidates(ctx, f)
	if err != nil {
		return TermCandidatePage{}, err
	}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		last := rows[len(rows)-1]
		page.Next = &ItemCursor{CreatedAtMs: last.CreatedAt.UnixMilli(), ID: last.ID}
	}
	page.Candidates = rows
	if page.Total, err = r.CountReviewTermCandidates(ctx, state, channelID); err != nil {
		return TermCandidatePage{}, err
	}
	return page, nil
}

// SeedTermCandidate 在影音所屬的頻道寫入一個 pending 的術語候選，created_at 是 at（GLOS 的驗收測試 seeding）。
// 清理與 UNIQUE 同 correct 的收集（C4）：詞被清理掉、或頻道已經有這個詞時回傳錯誤。只供驗收測試的 harness 使用。
func (p *Pipeline) SeedTermCandidate(ctx context.Context, itemID, term string, at time.Time) error {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	if it.ChannelID == "" {
		return fmt.Errorf("影音 %s 沒有頻道，不能寫入術語候選", itemID)
	}
	clean := llm.SanitizeTermCandidates([]string{term}, 1)
	if len(clean) == 0 {
		return fmt.Errorf("術語候選 %q 清理之後是空的或太長", term)
	}
	added, err := p.st.Write().AddTermCandidateAt(ctx, it.ChannelID, it.ID, clean[0], at)
	if err != nil {
		return err
	}
	if !added {
		return fmt.Errorf("頻道已經有術語候選 %q", clean[0])
	}
	return nil
}
