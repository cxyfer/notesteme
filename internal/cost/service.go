package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/store"
)

// settings 的鍵。
const (
	settingBudget        = "cost.budget"
	settingPricesChecked = "cost.prices_checked_at"
	settingWarned        = "cost.budget_warned_month"
)

// 排程工作 budget.month（COST-R9，ADR-0021 第 1 點）：預算時區的每月 1 日 00:00 讓 budget_blocked 的步驟恢復。
const (
	MonthKind = "budget.month"
	MonthKey  = "monthly"
)

// ResumeToken 是 budget_blocked 步驟的恢復條件（resume_on）。
const ResumeToken = "budget"

// 成本紀錄的單位（COST-R1）。
const (
	UnitTokens = "tokens"
	UnitCredit = "credit"
)

// outcome（COST-R1）。
const (
	OutcomeOK           = "ok"
	OutcomeInvalid      = "invalid_output"
	OutcomeHTTPError    = "http_error"
	OutcomeNetworkError = "network_error"
	OutcomeDiscarded    = "discarded"
)

// 計價的來源（unit_price_json.source，COST-R2）。
const (
	SourceProvider = "provider"
	SourceOverride = "override"
	SourceBuiltin  = "builtin"
	SourceUnknown  = "unknown"
)

// staleAfterDays 是計價表多久沒確認就提醒（COST-R13：超過 90 天，第 91 天起）。
const staleAfterDays = 90

// Options 是只給測試的控制（ADR-0015，COST 待決問題 C8）。
type Options struct {
	// Estimates 取代這些步驟每個任務的估算金額（美元），例如 {"correct": 0.04}。
	Estimates map[string]float64
	// PricesAsOf 取代內建計價表的確認日期（YYYY-MM-DD）；空字串用 PricesAsOf。
	PricesAsOf string
}

// Service 是成本帳本、計價表與預算（COST）。方法都在呼叫端的交易中執行（r 是 Store.Tx 傳入的 Repo）：
// 預算檢查與預留同一個交易（ADR-0021 第 4 點），通知 outbox 也在同一個交易寫入（第 5 點）。
type Service struct {
	clock      clock.Clock
	outbox     *notify.Outbox
	estimates  map[string]Micro
	pricesAsOf string
}

// New 建立 Service；outbox 可以是 nil（不寫通知）。
func New(clk clock.Clock, outbox *notify.Outbox, o Options) *Service {
	s := &Service{clock: clk, outbox: outbox, pricesAsOf: PricesAsOf}
	if o.PricesAsOf != "" {
		s.pricesAsOf = o.PricesAsOf
	}
	if len(o.Estimates) > 0 {
		s.estimates = map[string]Micro{}
		for stage, usd := range o.Estimates {
			if m, ok := FromUSD(usd); ok {
				s.estimates[stage] = m
			}
		}
	}
	return s
}

// Now 回傳注入的時間。
func (s *Service) Now() time.Time { return s.clock.Now() }

// Budget 讀取預算設定；沒有設定時是 DefaultBudget。
func (s *Service) Budget(ctx context.Context, r *store.Repo) (Budget, error) {
	raw, ok, err := r.Setting(ctx, settingBudget)
	if err != nil || !ok {
		return DefaultBudget(), err
	}
	return unmarshalBudget(raw)
}

// SetBudget 存入預算設定，並把 budget.month 重新排在新時區的下一個月初（COST-R9、R11）。
func (s *Service) SetBudget(ctx context.Context, r *store.Repo, b Budget) error {
	raw, err := b.marshal()
	if err != nil {
		return err
	}
	if err := r.SetSetting(ctx, settingBudget, raw); err != nil {
		return err
	}
	return r.ScheduleJob(ctx, MonthKind, MonthKey, NextMonthStart(s.clock.Now(), b.Location()))
}

// ScheduleMonth 在 budget.month 還沒有排定時排在下一個月初（啟動時呼叫）；已經排定的不動，
// 停機時錯過的月初會在啟動後補跑一次（ADR-0021 第 1 點）。
func (s *Service) ScheduleMonth(ctx context.Context, r *store.Repo) error {
	b, err := s.Budget(ctx, r)
	if err != nil {
		return err
	}
	_, err = r.ScheduleJobIfAbsent(ctx, MonthKind, MonthKey, NextMonthStart(s.clock.Now(), b.Location()))
	return err
}

// NextMonthRun 是 budget.month 這次執行之後的下一次：now 之後的下一個月初。
func (s *Service) NextMonthRun(ctx context.Context, r *store.Repo, now time.Time) (time.Time, error) {
	b, err := s.Budget(ctx, r)
	if err != nil {
		return time.Time{}, err
	}
	return NextMonthStart(now, b.Location()), nil
}

// MonthTotals 是本月（預算時區）actual 與 reserved 的總和。
type MonthTotals struct {
	Month    string
	Actual   Micro
	Reserved Micro
}

// Total 是 actual 加 reserved。
func (t MonthTotals) Total() Micro { return t.Actual + t.Reserved }

// Month 回傳 now 所在月份的總和。
func (s *Service) Month(ctx context.Context, r *store.Repo, b Budget, now time.Time) (MonthTotals, error) {
	start, end, label := MonthOf(now, b.Location())
	a, res, err := r.SumCostMicrosBetween(ctx, start, end)
	if err != nil {
		return MonthTotals{}, err
	}
	return MonthTotals{Month: label, Actual: Micro(a), Reserved: Micro(res)}, nil
}

// BudgetView 是 GET /api/settings/budget 的回應（COST-R4）。nil 的金額寫成 null。
type BudgetView struct {
	MonthlyUSD   *float64 `json:"monthly_usd"`
	PerItemUSD   *float64 `json:"per_item_usd"`
	WarnAt       int      `json:"warn_at_percent"`
	Timezone     string   `json:"timezone"`
	Month        string   `json:"month"`
	SpentUSD     float64  `json:"spent_usd"`
	ReservedUSD  float64  `json:"reserved_usd"`
	RemainingUSD *float64 `json:"remaining_usd"`
}

func usdPtr(m *Micro) *float64 {
	if m == nil {
		return nil
	}
	v := m.USD()
	return &v
}

// View 組出 GET /api/settings/budget 的內容。remaining_usd 是月預算減去本月的 actual 與 reserved，最少 0。
func (s *Service) View(ctx context.Context, r *store.Repo) (BudgetView, error) {
	b, err := s.Budget(ctx, r)
	if err != nil {
		return BudgetView{}, err
	}
	m, err := s.Month(ctx, r, b, s.clock.Now())
	if err != nil {
		return BudgetView{}, err
	}
	v := BudgetView{
		MonthlyUSD: usdPtr(b.Monthly), PerItemUSD: usdPtr(b.PerItem), WarnAt: b.WarnAt, Timezone: b.Timezone,
		Month: m.Month, SpentUSD: m.Actual.USD(), ReservedUSD: m.Reserved.USD(),
	}
	if b.Monthly != nil {
		left := max(*b.Monthly-m.Total(), 0)
		v.RemainingUSD = usdPtr(&left)
	}
	return v, nil
}

// ---- 計價（COST-R2、R12、R13） ----

// Quote 是查到的單價與來源。
type Quote struct {
	USDPerUnit float64
	Source     string
	CheckedAt  time.Time
}

// builtinCheckedAt 是內建計價表的確認日期：PricesAsOf 與 POST /api/prices/confirm 兩者中較晚的一個（COST-R13）。
func (s *Service) builtinCheckedAt(ctx context.Context, r *store.Repo) (time.Time, error) {
	asOf, err := time.Parse(time.DateOnly, s.pricesAsOf)
	if err != nil {
		return time.Time{}, fmt.Errorf("cost: 內建計價表的日期不正確：%w", err)
	}
	raw, ok, err := r.Setting(ctx, settingPricesChecked)
	if err != nil {
		return time.Time{}, err
	}
	if ok {
		if c, err := time.Parse(time.DateOnly, raw); err == nil && c.After(asOf) {
			asOf = c
		}
	}
	return asOf, nil
}

// Lookup 依 COST-R2 的 (2)～(3) 查單價：先覆寫，再內建計價表；provider 有自訂的 base_url 時不套用內建計價表。
func (s *Service) Lookup(ctx context.Context, r *store.Repo, provider, model, unit string, customBase bool) (Quote, bool, error) {
	o, ok, err := r.PriceOverride(ctx, provider, model, unit)
	if err != nil {
		return Quote{}, false, err
	}
	if ok {
		return Quote{USDPerUnit: o.USDPerUnit, Source: SourceOverride, CheckedAt: o.CheckedAt}, true, nil
	}
	if customBase {
		return Quote{}, false, nil
	}
	p, ok := Lookup(provider, model, unit)
	if !ok {
		return Quote{}, false, nil
	}
	at, err := s.builtinCheckedAt(ctx, r)
	if err != nil {
		return Quote{}, false, err
	}
	return Quote{USDPerUnit: p.USDPerUnit, Source: SourceBuiltin, CheckedAt: at}, true, nil
}

// llmPrices 查 LLM 的輸入與輸出單價；任一個找不到就算找不到（COST-R2）。
func (s *Service) llmPrices(ctx context.Context, r *store.Repo, provider, model string, customBase bool) (in, out Quote, ok bool, err error) {
	in, okIn, err := s.Lookup(ctx, r, provider, model, UnitInputMTok, customBase)
	if err != nil {
		return Quote{}, Quote{}, false, err
	}
	out, okOut, err := s.Lookup(ctx, r, provider, model, UnitOutputMTok, customBase)
	if err != nil {
		return Quote{}, Quote{}, false, err
	}
	return in, out, okIn && okOut, nil
}

// ---- 預留與結算（COST-R5～R10） ----

// Call 是一次付費呼叫的身分與估算的 token 數。BATCH 對每個請求各用一個 Call。
type Call struct {
	ItemID, ItemTitle   string
	StageRunID, TaskID  string
	Stage               string
	Provider, Model     string
	CustomBase          bool
	InputTokens, Output int
	// Batch 為 true 時是 Batch API 的請求（BATCH-R6）；目前只記在 unit_price_json。
	Batch bool
}

// Reservation 是 Reserve 寫入的預留。
type Reservation struct {
	ID       string
	Attempt  int
	Estimate Micro
	// Priced 為 false 時找不到單價（沒有預算時照常執行，結算為 unknown）。
	Priced bool
}

// Reserve 在呼叫端的交易中估算、檢查預算並寫入一筆 reserved 的紀錄（COST-R5、R6）。
// 超過預算、或有預算而找不到單價時不寫入，回傳 budget_blocked 的 *errclass.Error（COST-R7、R8）。
// 本月花費因此第一次達到提醒門檻時，在同一個交易寫入 budget.warning（COST-R10）。
func (s *Service) Reserve(ctx context.Context, r *store.Repo, c Call) (Reservation, error) {
	attempt := 1
	if c.TaskID != "" {
		n, err := r.CountTaskCostRows(ctx, c.TaskID)
		if err != nil {
			return Reservation{}, err
		}
		attempt = n + 1
	}
	in, out, priced, err := s.llmPrices(ctx, r, c.Provider, c.Model, c.CustomBase)
	if err != nil {
		return Reservation{}, err
	}
	var est Micro
	if v, ok := s.estimates[c.Stage]; ok {
		est = v
	} else if priced {
		est = llmMicro(c.InputTokens, c.Output, in.USDPerUnit, out.USDPerUnit)
	}
	b, err := s.Budget(ctx, r)
	if err != nil {
		return Reservation{}, err
	}
	if b.Limited() {
		if !priced {
			return Reservation{}, unpricedError(c.Provider, c.Model)
		}
		if err := s.check(ctx, r, b, c.ItemID, est); err != nil {
			return Reservation{}, err
		}
	}
	id, err := r.InsertCostRow(ctx, store.CostRow{
		ItemID: c.ItemID, ItemTitle: c.ItemTitle, StageRunID: c.StageRunID, TaskID: c.TaskID, Stage: c.Stage,
		Provider: c.Provider, Model: c.Model, UnitType: UnitTokens, USD: est.USD(), State: store.CostReserved,
		Attempt: attempt, At: s.clock.Now(),
	})
	if err != nil {
		return Reservation{}, err
	}
	if err := s.warn(ctx, r, b); err != nil {
		return Reservation{}, err
	}
	return Reservation{ID: id, Attempt: attempt, Estimate: est, Priced: priced}, nil
}

// Exceeds 是預算檢查的結果（LIB-R14 的 dry_run 也用它）。
type Exceeds struct {
	Monthly bool
	PerItem bool
}

// Check 不寫入任何東西，只回報再加上 est 會不會超過月預算或這部影音的單部上限（相等不算超過，COST-R6）。
// itemID 為空字串時不檢查單部上限。
func (s *Service) Check(ctx context.Context, r *store.Repo, itemID string, est Micro) (Exceeds, error) {
	b, err := s.Budget(ctx, r)
	if err != nil {
		return Exceeds{}, err
	}
	var e Exceeds
	if b.Monthly != nil {
		m, err := s.Month(ctx, r, b, s.clock.Now())
		if err != nil {
			return Exceeds{}, err
		}
		e.Monthly = m.Total()+est > *b.Monthly
	}
	if b.PerItem != nil && itemID != "" {
		total, err := r.SumItemCostMicros(ctx, itemID)
		if err != nil {
			return Exceeds{}, err
		}
		e.PerItem = Micro(total)+est > *b.PerItem
	}
	return e, nil
}

// check 依 COST-R6、R8 判斷，超過時回傳 budget_blocked（兩者都超過時用月預算的訊息）。
func (s *Service) check(ctx context.Context, r *store.Repo, b Budget, itemID string, est Micro) error {
	if b.Monthly != nil {
		m, err := s.Month(ctx, r, b, s.clock.Now())
		if err != nil {
			return err
		}
		if m.Total()+est > *b.Monthly {
			return blocked(fmt.Sprintf("本月預算 %s 已用 %s（含預留），這個步驟預估 %s，超過預算。",
				b.Monthly.Dollars(), m.Total().Dollars(), est.Dollars()), ActionAdjustBudget, ActionViewCosts)
		}
	}
	if b.PerItem != nil && itemID != "" {
		total, err := r.SumItemCostMicros(ctx, itemID)
		if err != nil {
			return err
		}
		if Micro(total)+est > *b.PerItem {
			return blocked(fmt.Sprintf("這部影音已花費 %s，這個步驟預估 %s，超過單部上限 %s。",
				Micro(total).Dollars(), est.Dollars(), b.PerItem.Dollars()), ActionAdjustBudget, ActionViewCosts)
		}
	}
	return nil
}

// 動作（COST-R7、R8、R13）。
const (
	ActionSetPrice     = "設定單價"
	ActionAdjustBudget = "調整預算"
	ActionViewCosts    = "查看成本"
	ActionConfirm      = "確認計價表"
	ActionAdjustPrice  = "調整單價"
)

func blocked(msg string, actions ...string) error {
	return &errclass.Error{
		Class: errclass.ClassBudgetBlocked, Source: "cost", Detail: "budget", Message: msg, Actions: actions, ResumeOn: ResumeToken,
	}
}

func unpricedError(provider, model string) error {
	return blocked(fmt.Sprintf("找不到 %s／%s 的單價，無法檢查預算。", cleanValue(provider), cleanValue(model)), ActionSetPrice, ActionAdjustBudget)
}

// IsBudgetBlocked 回報 err 是不是 Reserve 的 budget_blocked。
func IsBudgetBlocked(err error) bool {
	c, ok := errclass.ClassOf(err)
	return ok && c == errclass.ClassBudgetBlocked
}

// Usage 是一次呼叫的實際結果。
type Usage struct {
	Outcome      string
	InputTokens  int
	OutputTokens int
	// ProviderUSD 是 provider 在回應中回報的金額（OpenAI 相容的 usage.cost，COST-R2 的 (1)）；nil 代表沒有回報。
	ProviderUSD *float64
}

// unitPrice 是 unit_price_json（COST-R1）。
type unitPrice struct {
	Source       string   `json:"source"`
	CheckedAt    string   `json:"checked_at,omitempty"`
	InputMTok    *float64 `json:"input_mtok,omitempty"`
	OutputMTok   *float64 `json:"output_mtok,omitempty"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	Batch        bool     `json:"batch"`
}

// Settle 把 Reserve 的預留改成實際的結果（COST-R1、R2、R5）。已經結算過、或已被啟動時的清理刪掉的預留不再改動，
// 所以重複結算沒有作用。實際金額可以比預留高或低；改成實際金額後同樣判斷提醒門檻（COST-R10）。
func (s *Service) Settle(ctx context.Context, r *store.Repo, res Reservation, c Call, u Usage) error {
	set, err := s.settlement(ctx, r, c, u)
	if err != nil {
		return err
	}
	ok, err := r.SettleCostRow(ctx, res.ID, set)
	if err != nil || !ok {
		return err
	}
	b, err := s.Budget(ctx, r)
	if err != nil {
		return err
	}
	return s.warn(ctx, r, b)
}

func (s *Service) settlement(ctx context.Context, r *store.Repo, c Call, u Usage) (store.CostSettlement, error) {
	in, out, priced, err := s.llmPrices(ctx, r, c.Provider, c.Model, c.CustomBase)
	if err != nil {
		return store.CostSettlement{}, err
	}
	up := unitPrice{InputTokens: max(u.InputTokens, 0), OutputTokens: max(u.OutputTokens, 0), Batch: c.Batch}
	if priced {
		inP, outP := in.USDPerUnit, out.USDPerUnit
		up.InputMTok, up.OutputMTok = &inP, &outP
		up.Source, up.CheckedAt = in.Source, in.CheckedAt.UTC().Format(time.DateOnly)
		if in.CheckedAt.After(out.CheckedAt) {
			up.CheckedAt = out.CheckedAt.UTC().Format(time.DateOnly)
		}
	}
	set := store.CostSettlement{
		State: store.CostActual, Units: float64(up.InputTokens + up.OutputTokens), UnitType: UnitTokens, Outcome: u.Outcome,
	}
	reported, hasReported := Micro(0), false
	if u.ProviderUSD != nil {
		// 負數、NaN 或大得不合理的回報當作沒有回報，改用計價表（COST 待決問題）。
		reported, hasReported = FromUSD(*u.ProviderUSD)
	}
	switch {
	case hasReported:
		up.Source, up.CheckedAt = SourceProvider, s.clock.Now().UTC().Format(time.DateOnly)
		set.USD = reported.USD()
	case u.Outcome == OutcomeHTTPError || u.Outcome == OutcomeNetworkError:
		// 錯誤的呼叫沒有費用（provider 回報了金額時除外）。
		set.USD = 0
		if !priced {
			up.Source = SourceUnknown
		}
	case priced:
		set.USD = llmMicro(up.InputTokens, up.OutputTokens, in.USDPerUnit, out.USDPerUnit).USD()
	default:
		up.Source = SourceUnknown
		set.State, set.USD = store.CostUnknown, 0
	}
	raw, err := json.Marshal(up)
	if err != nil {
		return store.CostSettlement{}, err
	}
	set.UnitPriceJSON = string(raw)
	return set, nil
}

// Release 刪除 Reserve 的預留（任務失敗、取消或放回佇列，COST-R5）；已經結算的不刪。
func (s *Service) Release(ctx context.Context, r *store.Repo, res Reservation) error {
	_, err := r.DeleteCostReservation(ctx, res.ID)
	return err
}

// RecordAgent 記一筆 Agent 提交的結果（COST-R3）：state unknown、usd 0，不預留、不計入預算。
// Agent 回報的 token 數記在 unit_price_json。
func (s *Service) RecordAgent(ctx context.Context, r *store.Repo, c Call, inputTokens, outputTokens int) error {
	attempt := 1
	if c.TaskID != "" {
		n, err := r.CountTaskCostRows(ctx, c.TaskID)
		if err != nil {
			return err
		}
		attempt = n + 1
	}
	raw, err := json.Marshal(unitPrice{Source: SourceUnknown, InputTokens: max(inputTokens, 0), OutputTokens: max(outputTokens, 0)})
	if err != nil {
		return err
	}
	_, err = r.InsertCostRow(ctx, store.CostRow{
		ItemID: c.ItemID, ItemTitle: c.ItemTitle, StageRunID: c.StageRunID, TaskID: c.TaskID, Stage: c.Stage,
		Provider: c.Provider, Model: c.Model, Units: float64(max(inputTokens, 0) + max(outputTokens, 0)), UnitType: UnitTokens,
		State: store.CostUnknown, Attempt: attempt, Outcome: OutcomeOK, UnitPriceJSON: string(raw), At: s.clock.Now(),
	})
	return err
}

// warn 在本月 actual + reserved 第一次達到 monthly × warn_at_percent / 100 時寫入 budget.warning 與通知 outbox（COST-R10）。
// 同一個月只寫一次（settings 記下已提醒的月份，調整預算不會重設，C5）；沒有月預算或月預算為 0 時不提醒。
func (s *Service) warn(ctx context.Context, r *store.Repo, b Budget) error {
	if b.Monthly == nil || *b.Monthly <= 0 {
		return nil
	}
	m, err := s.Month(ctx, r, b, s.clock.Now())
	if err != nil {
		return err
	}
	total := m.Total()
	// 整數比較：total / monthly >= warn / 100。金額上限 1e15 micro，乘以 100 不會溢位。
	if int64(total)*100 < int64(*b.Monthly)*int64(b.WarnAt) {
		return nil
	}
	warned, _, err := r.Setting(ctx, settingWarned)
	if err != nil {
		return err
	}
	if warned == m.Month {
		return nil
	}
	if err := r.SetSetting(ctx, settingWarned, m.Month); err != nil {
		return err
	}
	pct64 := int64(total) * 100 / int64(*b.Monthly)
	pct := int(min(pct64, math.MaxInt32))
	spent, reserved, monthly := m.Actual.USD(), m.Reserved.USD(), b.Monthly.USD()
	if err := r.Emit(ctx, events.Event{
		Type: events.TypeBudgetWarning, Month: m.Month, Percent: &pct, SpentUSD: &spent, ReservedUSD: &reserved, MonthlyUSD: &monthly,
	}); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{
		"month": m.Month, "percent": pct, "spent_usd": spent, "reserved_usd": reserved, "monthly_usd": monthly,
	})
	if err != nil {
		return err
	}
	_, err = s.outbox.Emit(ctx, r, notify.Event{
		Name: notify.EventBudgetWarning, DedupeKey: notify.DedupeBudgetWarning(m.Month), Budget: detail,
		Summary: fmt.Sprintf("本月已用 %s（含預留），達到預算 %s 的 %d%%。", total.Dollars(), b.Monthly.Dollars(), pct),
	})
	return err
}

// ErrNotFound 是要刪除的單價覆寫不存在（COST-R12 的 404）。
var ErrNotFound = errors.New("cost: 找不到這筆單價覆寫")
