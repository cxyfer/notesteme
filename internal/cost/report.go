package cost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// InvalidError 是 422 invalid_request 的逐欄位錯誤（COST-R4、R12）。
type InvalidError struct {
	Fields []validate.FieldError
}

func (e *InvalidError) Error() string {
	if len(e.Fields) == 0 {
		return "內容不正確。"
	}
	return e.Fields[0].Message
}

// QueryError 是查詢參數的 422 invalid_request（COST-R14、R15）：只有頂層的訊息。
type QueryError struct{ Message string }

func (e *QueryError) Error() string { return e.Message }

// ---- 計價表 API（COST-R12、R13） ----

// 單價覆寫的上限（COST-R12）。
const (
	maxUnitUSD  = 10000
	maxNameRune = 200
)

// Units 是計價的單位（COST-R12）。
var Units = []string{UnitInputMTok, UnitOutputMTok, UnitAudioHour, UnitCredit}

// PriceKey 是一筆計價的 (provider, model, unit_type)。
type PriceKey struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	UnitType string `json:"unit_type"`
}

// PriceEntry 是 GET /api/prices 的一列。
type PriceEntry struct {
	PriceKey
	USDPerUnit float64 `json:"usd_per_unit"`
	Source     string  `json:"source"`
	CheckedAt  string  `json:"checked_at"`
	Stale      bool    `json:"stale"`
}

// PriceList 是 GET /api/prices 的回應。
type PriceList struct {
	Prices           []PriceEntry `json:"prices"`
	BuiltinCheckedAt string       `json:"builtin_checked_at"`
	BuiltinStale     bool         `json:"builtin_stale"`
}

// Notice 是頁面頂部的提醒（COST-R13）。
type Notice struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Actions []string `json:"actions"`
}

// ageDays 是 checked 到 now 的日數（UTC 的日期相減）。
func ageDays(checked, now time.Time) int {
	c := time.Date(checked.UTC().Year(), checked.UTC().Month(), checked.UTC().Day(), 0, 0, 0, 0, time.UTC)
	n := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return int(n.Sub(c).Hours() / 24)
}

// Prices 列出內建與覆寫的單價：同一組有覆寫時只列覆寫，依 provider、model、unit_type 排序（COST-R12）。
func (s *Service) Prices(ctx context.Context, r *store.Repo) (PriceList, error) {
	asOf, err := s.builtinCheckedAt(ctx, r)
	if err != nil {
		return PriceList{}, err
	}
	now := s.clock.Now()
	overrides, err := r.PriceOverrides(ctx)
	if err != nil {
		return PriceList{}, err
	}
	byKey := map[PriceKey]PriceEntry{}
	for _, p := range builtin {
		k := PriceKey{p.Provider, p.Model, p.Unit}
		byKey[k] = PriceEntry{PriceKey: k, USDPerUnit: p.USDPerUnit, Source: SourceBuiltin,
			CheckedAt: asOf.Format(time.DateOnly), Stale: ageDays(asOf, now) > staleAfterDays}
	}
	for _, o := range overrides {
		k := PriceKey{o.Provider, o.Model, o.UnitType}
		byKey[k] = PriceEntry{PriceKey: k, USDPerUnit: o.USDPerUnit, Source: SourceOverride,
			CheckedAt: o.CheckedAt.UTC().Format(time.DateOnly), Stale: ageDays(o.CheckedAt, now) > staleAfterDays}
	}
	out := PriceList{Prices: make([]PriceEntry, 0, len(byKey)), BuiltinCheckedAt: asOf.Format(time.DateOnly),
		BuiltinStale: ageDays(asOf, now) > staleAfterDays}
	for _, e := range byKey {
		out.Prices = append(out.Prices, e)
	}
	slices.SortFunc(out.Prices, func(a, b PriceEntry) int {
		return strings.Compare(a.Provider+"\x00"+a.Model+"\x00"+a.UnitType, b.Provider+"\x00"+b.Model+"\x00"+b.UnitType)
	})
	return out, nil
}

// Notices 是 GET /api/costs（與之後的 /api/overview）的提醒：有任何 stale 的計價時一筆 prices_stale，天數取最久的一筆。
func (s *Service) Notices(ctx context.Context, r *store.Repo) ([]Notice, error) {
	asOf, err := s.builtinCheckedAt(ctx, r)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	oldest := ageDays(asOf, now)
	overrides, err := r.PriceOverrides(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range overrides {
		oldest = max(oldest, ageDays(o.CheckedAt, now))
	}
	notices := []Notice{}
	if oldest > staleAfterDays {
		notices = append(notices, Notice{
			Code: "prices_stale", Message: fmt.Sprintf("計價表已經 %d 天沒有確認，估價可能不準確。", oldest),
			Actions: []string{ActionConfirm, ActionAdjustPrice},
		})
	}
	return notices, nil
}

// ConfirmPrices 把內建計價表的確認日期設為今天（UTC，COST-R13）。
func (s *Service) ConfirmPrices(ctx context.Context, r *store.Repo) error {
	return r.SetSetting(ctx, settingPricesChecked, s.clock.Now().UTC().Format(time.DateOnly))
}

// validName 是 provider、model：1～200 個字元，不含控制字元。
func validName(v any) (string, bool) {
	str, ok := v.(string)
	if !ok || str == "" || !utf8.ValidString(str) || utf8.RuneCountInString(str) > maxNameRune {
		return "", false
	}
	for _, r := range str {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return "", false
		}
	}
	return str, true
}

const nameMessage = "名稱必須是 1 到 200 個字元。"

func parseKey(body map[string]any, errs *[]validate.FieldError) PriceKey {
	var k PriceKey
	var ok bool
	if k.Provider, ok = validName(body["provider"]); !ok {
		*errs = append(*errs, validate.FieldError{Path: "provider", Message: nameMessage})
	}
	if k.Model, ok = validName(body["model"]); !ok {
		*errs = append(*errs, validate.FieldError{Path: "model", Message: nameMessage})
	}
	if u, isStr := body["unit_type"].(string); !isStr || !slices.Contains(Units, u) {
		*errs = append(*errs, validate.FieldError{Path: "unit_type", Message: "單位只能是 input_mtok、output_mtok、audio_hour 或 credit。"})
	} else {
		k.UnitType = u
	}
	return k
}

func unknownFields(body map[string]any, allowed []string, errs *[]validate.FieldError) {
	var unknown []string
	for k := range body {
		if !slices.Contains(allowed, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	for _, k := range unknown {
		*errs = append(*errs, validate.FieldError{Path: k, Message: "不認識的欄位。"})
	}
}

// PutPrice 新增或取代一筆覆寫（PUT /api/prices，COST-R12）。checked_at 預設為現在，不能晚於今天（UTC）。
func (s *Service) PutPrice(ctx context.Context, r *store.Repo, body map[string]any) (PriceEntry, error) {
	var errs []validate.FieldError
	k := parseKey(body, &errs)
	price, isNum := body["usd_per_unit"].(float64)
	if !isNum || math.IsNaN(price) || price < 0 || price > maxUnitUSD {
		errs = append(errs, validate.FieldError{Path: "usd_per_unit", Message: "單價必須介於 0 到 10000 美元之間。"})
	}
	now := s.clock.Now()
	checked := now
	if v, ok := body["checked_at"]; ok && v != nil {
		str, _ := v.(string)
		d, err := time.Parse(time.DateOnly, str)
		switch {
		case err != nil:
			errs = append(errs, validate.FieldError{Path: "checked_at", Message: "日期必須是 YYYY-MM-DD。"})
		case d.After(now):
			errs = append(errs, validate.FieldError{Path: "checked_at", Message: "確認日期不能晚於今天。"})
		default:
			checked = d
		}
	}
	unknownFields(body, []string{"provider", "model", "unit_type", "usd_per_unit", "checked_at"}, &errs)
	if len(errs) > 0 {
		return PriceEntry{}, &InvalidError{Fields: errs}
	}
	if err := r.UpsertPriceOverride(ctx, store.PriceOverride{
		Provider: k.Provider, Model: k.Model, UnitType: k.UnitType, USDPerUnit: price, CheckedAt: checked,
	}); err != nil {
		return PriceEntry{}, err
	}
	return PriceEntry{PriceKey: k, USDPerUnit: price, Source: SourceOverride, CheckedAt: checked.UTC().Format(time.DateOnly),
		Stale: ageDays(checked, now) > staleAfterDays}, nil
}

// DeletePrice 移除一筆覆寫（DELETE /api/prices）；不存在時回傳 ErrNotFound。
func (s *Service) DeletePrice(ctx context.Context, r *store.Repo, body map[string]any) error {
	var errs []validate.FieldError
	k := parseKey(body, &errs)
	unknownFields(body, []string{"provider", "model", "unit_type"}, &errs)
	if len(errs) > 0 {
		return &InvalidError{Fields: errs}
	}
	ok, err := r.DeletePriceOverride(ctx, k.Provider, k.Model, k.UnitType)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// ---- 成本查詢（COST-R14、R15） ----

// maxRangeDays 是查詢範圍的上限；maxExportRows 是一次匯出的上限。
const (
	maxRangeDays  = 366
	maxExportRows = 100000
)

// GroupBys 是 GET /api/costs 的分組方式。
var GroupBys = []string{"day", "provider", "model", "stage", "channel", "item"}

// Range 是預算時區中的日期範圍（含兩端）。
type Range struct {
	From, To   string
	Loc        *time.Location
	Start, End time.Time // [Start, End)
}

// ParseRange 解析 from、to（預算時區的 YYYY-MM-DD，含兩端）；預設為本月 1 日到今天。
func (s *Service) ParseRange(ctx context.Context, r *store.Repo, from, to string) (Range, error) {
	b, err := s.Budget(ctx, r)
	if err != nil {
		return Range{}, err
	}
	loc := b.Location()
	now := s.clock.Now().In(loc)
	day := func(v string, def time.Time) (time.Time, bool) {
		if v == "" {
			return def, true
		}
		t, err := time.ParseInLocation(time.DateOnly, v, loc)
		return t, err == nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	f, ok := day(from, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc))
	if !ok {
		return Range{}, &QueryError{Message: "日期必須是 YYYY-MM-DD。"}
	}
	t, ok := day(to, today)
	if !ok {
		return Range{}, &QueryError{Message: "日期必須是 YYYY-MM-DD。"}
	}
	if t.Before(f) {
		return Range{}, &QueryError{Message: "結束日期不能早於開始日期。"}
	}
	// 以日曆日計算（日光節約時間的一天不是 24 小時）。
	if days := daysBetween(f, t) + 1; days > maxRangeDays {
		return Range{}, &QueryError{Message: "查詢範圍最多 366 天。"}
	}
	return Range{From: f.Format(time.DateOnly), To: t.Format(time.DateOnly), Loc: loc, Start: f, End: t.AddDate(0, 0, 1)}, nil
}

func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// Group 是 GET /api/costs 的一組。
type Group struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	USD         float64 `json:"usd"`
	ReservedUSD float64 `json:"reserved_usd"`
	Calls       int     `json:"calls"`
}

// MonthBudget 是 GET /api/costs 的 budget 欄位。
type MonthBudget struct {
	Month        string   `json:"month"`
	MonthlyUSD   *float64 `json:"monthly_usd"`
	SpentUSD     float64  `json:"spent_usd"`
	ReservedUSD  float64  `json:"reserved_usd"`
	ProjectedUSD float64  `json:"projected_usd"`
}

// Summary 是 GET /api/costs 的回應（COST-R14）。
type Summary struct {
	From         string      `json:"from"`
	To           string      `json:"to"`
	Timezone     string      `json:"timezone"`
	TotalUSD     float64     `json:"total_usd"`
	ReservedUSD  float64     `json:"reserved_usd"`
	UnknownCalls int         `json:"unknown_calls"`
	Groups       []Group     `json:"groups"`
	Budget       MonthBudget `json:"budget"`
	Notices      []Notice    `json:"notices"`
}

// allRows 讀出範圍內所有紀錄（由新到舊）；超過 limit 筆時回傳 limit+1 筆，呼叫端判斷。
func allRows(ctx context.Context, r *store.Repo, rng Range, limit int) ([]store.CostListRow, error) {
	var out []store.CostListRow
	cur := store.CostCursor{}
	const page = 5000
	for len(out) <= limit {
		rows, err := r.CostRowsBetween(ctx, rng.Start, rng.End, cur, page)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < page {
			break
		}
		last := rows[len(rows)-1]
		cur = store.CostCursor{At: last.At, ID: last.ID}
	}
	return out, nil
}

// Summarize 依 groupBy 彙總範圍內的花費（COST-R14）。groupBy 空字串是 day。
func (s *Service) Summarize(ctx context.Context, r *store.Repo, rng Range, groupBy string) (Summary, error) {
	if groupBy == "" {
		groupBy = "day"
	}
	if !slices.Contains(GroupBys, groupBy) {
		return Summary{}, &QueryError{Message: "group_by 只能是 day、provider、model、stage、channel 或 item。"}
	}
	b, err := s.Budget(ctx, r)
	if err != nil {
		return Summary{}, err
	}
	rows, err := allRows(ctx, r, rng, math.MaxInt32)
	if err != nil {
		return Summary{}, err
	}
	type acc struct {
		key, label       string
		actual, reserved Micro
		calls            int
	}
	groups := map[string]*acc{}
	var total, reserved Micro
	unknown := 0
	for _, e := range rows {
		var key, label string
		switch groupBy {
		case "day":
			key = e.At.In(rng.Loc).Format(time.DateOnly)
			label = key
		case "provider":
			key, label = e.Provider, e.Provider
		case "model":
			key, label = e.Model, e.Model
		case "stage":
			key, label = e.Stage, e.Stage
		case "channel":
			key, label = e.Channel, e.Channel
		case "item":
			key, label = e.ItemID, e.ItemTitle
			if key == "" {
				// 已永久刪除的影音以標題為準（DEL-R7）。
				key = "deleted:" + e.ItemTitle
			}
		}
		g := groups[key]
		if g == nil {
			g = &acc{key: key, label: label}
			groups[key] = g
		}
		m, _ := FromUSD(e.USD)
		g.calls++
		switch e.State {
		case store.CostActual:
			g.actual += m
			total += m
		case store.CostReserved:
			g.reserved += m
			reserved += m
		case store.CostUnknown:
			unknown++
		}
	}
	out := Summary{From: rng.From, To: rng.To, Timezone: b.Timezone, TotalUSD: total.USD(), ReservedUSD: reserved.USD(),
		UnknownCalls: unknown, Groups: make([]Group, 0, len(groups))}
	list := make([]*acc, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	slices.SortFunc(list, func(a, c *acc) int {
		if groupBy != "day" && a.actual != c.actual {
			if a.actual > c.actual {
				return -1
			}
			return 1
		}
		return strings.Compare(a.key, c.key)
	})
	for _, g := range list {
		out.Groups = append(out.Groups, Group{Key: g.key, Label: g.label, USD: g.actual.USD(), ReservedUSD: g.reserved.USD(), Calls: g.calls})
	}
	now := s.clock.Now()
	m, err := s.Month(ctx, r, b, now)
	if err != nil {
		return Summary{}, err
	}
	start, end, _ := MonthOf(now, b.Location())
	elapsed := daysBetween(start, now.In(b.Location())) + 1
	inMonth := daysBetween(start, end)
	projected := Micro(math.Round(float64(m.Actual) / float64(elapsed) * float64(inMonth)))
	out.Budget = MonthBudget{Month: m.Month, MonthlyUSD: usdPtr(b.Monthly), SpentUSD: m.Actual.USD(), ReservedUSD: m.Reserved.USD(), ProjectedUSD: projected.USD()}
	if out.Notices, err = s.Notices(ctx, r); err != nil {
		return Summary{}, err
	}
	return out, nil
}

// Entry 是 GET /api/costs/entries 的一筆（COST-R14）。
type Entry struct {
	At        string          `json:"at"`
	ItemID    string          `json:"item_id"`
	Ref       string          `json:"ref"`
	ItemTitle string          `json:"item_title"`
	Channel   string          `json:"channel"`
	Stage     string          `json:"stage"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Units     float64         `json:"units"`
	UnitType  string          `json:"unit_type"`
	USD       float64         `json:"usd"`
	State     string          `json:"state"`
	Attempt   int             `json:"attempt"`
	Outcome   string          `json:"outcome"`
	UnitPrice json.RawMessage `json:"unit_price"`
}

// Entries 是一頁明細。
type Entries struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// ParseLimit 解析 limit（1～500，預設 100）。
func ParseLimit(v string) (int, error) {
	if v == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 500 {
		return 0, &QueryError{Message: "limit 必須是 1 到 500 的整數。"}
	}
	return n, nil
}

func encodeCursor(e store.CostListRow) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(e.At.UnixMilli(), 10) + "." + e.ID))
}

func decodeCursor(v string) (store.CostCursor, error) {
	if v == "" {
		return store.CostCursor{}, nil
	}
	bad := &QueryError{Message: "cursor 不正確。"}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return store.CostCursor{}, bad
	}
	ms, id, ok := strings.Cut(string(raw), ".")
	n, err := strconv.ParseInt(ms, 10, 64)
	if !ok || err != nil || id == "" || n < 0 {
		return store.CostCursor{}, bad
	}
	return store.CostCursor{At: time.UnixMilli(n), ID: id}, nil
}

func entryOf(e store.CostListRow, loc *time.Location) Entry {
	up := json.RawMessage(e.UnitPriceJSON)
	if !json.Valid(up) {
		up = json.RawMessage("{}")
	}
	m, _ := FromUSD(e.USD)
	return Entry{
		At: e.At.In(loc).Format(time.RFC3339), ItemID: e.ItemID, Ref: e.Ref, ItemTitle: e.ItemTitle, Channel: e.Channel,
		Stage: e.Stage, Provider: e.Provider, Model: e.Model, Units: e.Units, UnitType: e.UnitType, USD: m.USD(),
		State: e.State, Attempt: e.Attempt, Outcome: e.Outcome, UnitPrice: up,
	}
}

// ListEntries 列出一頁明細，由新到舊。
func (s *Service) ListEntries(ctx context.Context, r *store.Repo, rng Range, limit int, cursor string) (Entries, error) {
	cur, err := decodeCursor(cursor)
	if err != nil {
		return Entries{}, err
	}
	rows, err := r.CostRowsBetween(ctx, rng.Start, rng.End, cur, limit+1)
	if err != nil {
		return Entries{}, err
	}
	out := Entries{Entries: make([]Entry, 0, min(len(rows), limit))}
	for i, e := range rows {
		if i == limit {
			out.NextCursor = encodeCursor(rows[i-1])
			break
		}
		out.Entries = append(out.Entries, entryOf(e, rng.Loc))
	}
	return out, nil
}

// ExportName 是 CSV 的檔名（COST-R15）。
func ExportName(rng Range) string { return "notesteme-costs-" + rng.From + "-" + rng.To + ".csv" }

// csvHeader 是 CSV 的欄位（COST-R15）。
var csvHeader = []string{"at", "item_id", "item_title", "channel", "stage", "provider", "model", "units", "unit_type", "usd", "state", "attempt", "outcome"}

// csvText 防範 CSV injection：以 =、+、-、@、tab 或 CR 開頭的文字前面加一個 '（COST-R15）。
func csvText(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// ErrTooManyRows 是匯出超過上限。
var ErrTooManyRows = &QueryError{Message: "一次最多匯出 100000 筆，請縮小日期範圍。"}

// Export 匯出範圍內的紀錄為 CSV（由舊到新，RFC 4180，CRLF）。
func (s *Service) Export(ctx context.Context, r *store.Repo, rng Range) ([]byte, error) {
	n, err := r.CountCostRowsBetween(ctx, rng.Start, rng.End)
	if err != nil {
		return nil, err
	}
	if n > maxExportRows {
		return nil, ErrTooManyRows
	}
	rows, err := allRows(ctx, r, rng, maxExportRows)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxExportRows {
		return nil, ErrTooManyRows
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		e := rows[i]
		m, _ := FromUSD(e.USD)
		rec := []string{
			e.At.In(rng.Loc).Format(time.RFC3339), csvText(e.ItemID), csvText(e.ItemTitle), csvText(e.Channel), csvText(e.Stage),
			csvText(e.Provider), csvText(e.Model), strconv.FormatFloat(e.Units, 'f', -1, 64), csvText(e.UnitType), m.CSV(),
			csvText(e.State), strconv.Itoa(e.Attempt), csvText(e.Outcome),
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// IsQueryError 回報 err 是不是查詢參數的錯誤。
func IsQueryError(err error) (*QueryError, bool) {
	var q *QueryError
	ok := errors.As(err, &q)
	return q, ok
}
