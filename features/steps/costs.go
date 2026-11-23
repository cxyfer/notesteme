package steps

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/app"
)

// registerCosts 註冊「成本、預算與 Batch API」的步驟（steps.md 4.19，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁；M4-S4 實作 COST 的句型。COST 的句型也是 LIB-08、09（LIB-bulk-overview）
// 與 BATCH 的閘門，所以只對 COST 的場景放行（見 costGate），其他領域維持 pending、停在同一個閘門。
// BATCH 自己的句型仍是步驟樁，由 BATCH 切片實作。
func registerCosts(r *Registry, s *Scenario) {
	r.Step(`the price of {string} for {string} is {string} USD per {string}`, s.priceIs)
	r.Step(`the budget is:`, s.budgetIs)
	r.Step(`the built-in prices were checked on {string}`, s.builtinPricesCheckedOn)
	r.Step(`the cost estimate of every {string} task is {string} USD`, s.costEstimateIs)
	r.Step(`the cost ledger has an entry of {string} USD for {string} at {string}`, s.costLedgerHasEntryAt)
	r.Step(`the cost ledger has a reservation of {string} USD for {string}`, s.costLedgerHasReservation)
	r.Step(`the LLM usage reported by {string} is {int} input and {int} output tokens`, s.llmUsageIs)
	r.Step(`the LLM cost reported by {string} is {string} USD`, s.llmCostIs)
	r.Step(`the cost ledger entries of {string} are:`, s.costLedgerEntriesAre)
	r.Step(`the cost export has a row with {string} set to {string}`, s.costExportHasRow)
	r.Step(`the batch API of {string} completes after {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the batch API of {string} never completes`, func(string) error { return godog.ErrPending })
	r.Step(`the batch API of {string} fails the requests for chunk {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the batch API of {string} points its results to {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the batch count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the batch request count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the cancelled batch count of {string} is {int}`, func(string, int) error { return godog.ErrPending })
}

// costGate 讓 COST 的句型只對 COST 的場景（有 @COST- 開頭的 tag）放行（M4-S4）。
// LIB-bulk-overview（LIB-08、09）的閘門是 `the cost estimate of every …`，BATCH 也用 COST 的價格與 usage 句型；
// 它們的切片實作之前維持 pending，回傳裸的 godog.ErrPending，junit 的訊息與切片之前相同（同 notifyGate）。
func (s *Scenario) costGate() error {
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@COST-") }) {
		return godog.ErrPending
	}
	return nil
}

// costSeeds 是 COST 的 Given 累積的計價覆寫與預算（第一個送出請求的步驟才寫入，見 flushCosts）。
type costSeeds struct {
	prices []map[string]any
	budget map[string]any
}

func (s *Scenario) priceIs(provider, model, amount, unit string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	usd, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return fmt.Errorf("單價 %q 不是數字", amount)
	}
	p := map[string]any{"provider": provider, "model": model, "unit_type": unit, "usd_per_unit": usd}
	// 同一組 (provider, model, unit_type) 寫兩次時，後面的取代前面的。
	s.costs.prices = slices.DeleteFunc(s.costs.prices, func(o map[string]any) bool {
		return o["provider"] == provider && o["model"] == model && o["unit_type"] == unit
	})
	s.costs.prices = append(s.costs.prices, p)
	return nil
}

// budgetIs 累積預算：表格的 value 是 JSON 的寫法，不是合法 JSON 時當作字串（例如 Asia/Taipei）。
func (s *Scenario) budgetIs(t *godog.Table) error {
	if err := s.costGate(); err != nil {
		return err
	}
	rows, err := tableMaps(t)
	if err != nil {
		return err
	}
	body := map[string]any{}
	for _, row := range rows {
		field, raw := row["field"], strings.TrimSpace(row["value"])
		if field == "" {
			return errors.New("預算表格需要 field 與 value 欄位")
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			v = raw
		}
		body[field] = v
	}
	s.costs.budget = body
	return nil
}

// flushCosts 寫入累積的計價覆寫與預算（steps.md 4.19：全域設定、處理方案之後，佇列之前）；必須回 200，501 時 pending。
func (s *Scenario) flushCosts() error {
	prices, budget := s.costs.prices, s.costs.budget
	s.costs = costSeeds{}
	for _, p := range prices {
		if _, err := s.configWrite(http.MethodPut, "/api/prices", p, http.StatusOK, "設定單價"); err != nil {
			return err
		}
	}
	if budget != nil {
		if _, err := s.configWrite(http.MethodPut, "/api/settings/budget", budget, http.StatusOK, "設定預算"); err != nil {
			return err
		}
	}
	return nil
}

// requireBeforeStart 是「必須在 server 啟動之前」的測試控制：server 已啟動時步驟失敗，不默默失效。
func (s *Scenario) requireBeforeStart(what string) error {
	if s.started {
		return fmt.Errorf("%s 必須在 server 啟動之前設定", what)
	}
	return nil
}

func (s *Scenario) builtinPricesCheckedOn(date string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return fmt.Errorf("日期 %q 不是 YYYY-MM-DD", date)
	}
	if err := s.requireBeforeStart("內建計價表的確認日期"); err != nil {
		return err
	}
	s.World.PricesAsOf = date
	return nil
}

func (s *Scenario) costEstimateIs(stage, amount string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	if err := s.requireBeforeStart("估算的金額"); err != nil {
		return err
	}
	if s.World.CostEstimates == nil {
		s.World.CostEstimates = map[string]float64{}
	}
	s.World.CostEstimates[stage] = usd
	return nil
}

func (s *Scenario) costLedgerHasEntryAt(amount, ref, at string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339", at)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	return s.server.SeedCostAt(s.ctx, id, usd, "actual", t)
}

func (s *Scenario) costLedgerHasReservation(amount, ref string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	return s.server.SeedCost(s.ctx, id, usd, "reserved")
}

func (s *Scenario) llmUsageIs(provider string, in, out int) error {
	if err := s.costGate(); err != nil {
		return err
	}
	if in < 0 || out < 0 {
		return errors.New("token 數不能是負數")
	}
	return s.World.LLM.SetUsage(provider, in, out)
}

func (s *Scenario) llmCostIs(provider, amount string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	return s.World.LLM.SetCost(provider, usd)
}

// costLedgerEntriesAre 依 at、rowid 排序後與表格依序比對，筆數必須相同；只比對表格中出現的欄位（steps.md 4.19）。
func (s *Scenario) costLedgerEntriesAre(ref string, t *godog.Table) error {
	if err := s.costGate(); err != nil {
		return err
	}
	want, err := tableMaps(t)
	if err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	rows, err := s.server.CostRowsOf(s.ctx, id)
	if err != nil {
		return err
	}
	if len(rows) != len(want) {
		return fmt.Errorf("%s 的成本紀錄有 %d 筆，預期 %d 筆：%s", ref, len(rows), len(want), describeCostRows(rows))
	}
	for i, w := range want {
		got := rows[i]
		var up struct {
			Source string `json:"source"`
			Batch  bool   `json:"batch"`
		}
		_ = json.Unmarshal([]byte(got.UnitPriceJSON), &up)
		for _, col := range t.Rows[0].Cells {
			field, exp := col.Value, w[col.Value]
			var actual string
			switch field {
			case "usd":
				v, err := strconv.ParseFloat(exp, 64)
				if err != nil {
					return fmt.Errorf("第 %d 筆的 usd %q 不是數字", i+1, exp)
				}
				if math.Abs(v-got.USD) > 1e-9 {
					return fmt.Errorf("第 %d 筆的 usd 是 %v，預期 %v：%s", i+1, got.USD, v, describeCostRows(rows))
				}
				continue
			case "stage":
				actual = got.Stage
			case "provider":
				actual = got.Provider
			case "model":
				actual = got.Model
			case "attempt":
				actual = strconv.Itoa(got.Attempt)
			case "outcome":
				actual = got.Outcome
			case "state":
				actual = got.State
			case "unit_type":
				actual = got.UnitType
			case "price_source":
				actual = up.Source
			case "batch":
				actual = strconv.FormatBool(up.Batch)
			default:
				return fmt.Errorf("成本紀錄沒有欄位 %q", field)
			}
			if actual != exp {
				return fmt.Errorf("第 %d 筆的 %s 是 %q，預期 %q：%s", i+1, field, actual, exp, describeCostRows(rows))
			}
		}
	}
	return nil
}

func describeCostRows(rows []app.CostRow) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("；")
		}
		fmt.Fprintf(&b, "%s/%s/%s attempt=%d outcome=%s state=%s usd=%v %s", r.Stage, r.Provider, r.Model, r.Attempt, r.Outcome, r.State, r.USD, r.UnitPriceJSON)
	}
	return b.String()
}

// costExportHasRow 把最近一次回應的 body 依 RFC 4180 解析成 CSV（第一列是欄位名稱），至少一列的這個欄位與指定值完全相等。
func (s *Scenario) costExportHasRow(field, value string) error {
	if err := s.costGate(); err != nil {
		return err
	}
	if s.last == nil {
		return errors.New("還沒有送出任何請求")
	}
	recs, err := csv.NewReader(bytes.NewReader(s.last.body)).ReadAll()
	if err != nil || len(recs) == 0 {
		return fmt.Errorf("回應不是 CSV：%s", snippet(s.last.body))
	}
	col := slices.Index(recs[0], field)
	if col < 0 {
		return fmt.Errorf("CSV 沒有欄位 %q（欄位：%s）", field, strings.Join(recs[0], ","))
	}
	var seen []string
	for _, rec := range recs[1:] {
		if col < len(rec) && rec[col] == value {
			return nil
		}
		if col < len(rec) {
			seen = append(seen, rec[col])
		}
	}
	return fmt.Errorf("CSV 沒有 %s 為 %q 的列（實際：%q）", field, value, seen)
}
