package cost

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestMoney(t *testing.T) {
	for _, c := range []struct {
		usd  float64
		want Micro
		ok   bool
	}{
		{0.1, 100000, true}, {0.0000014, 1, true}, {0.0000004, 0, true}, {0, 0, true}, {MaxUSD, Micro(MaxUSD * 1e6), true},
		{-0.000001, 0, false}, {math.NaN(), 0, false}, {math.Inf(1), 0, false}, {MaxUSD + 1, 0, false},
	} {
		got, ok := FromUSD(c.usd)
		if got != c.want || ok != c.ok {
			t.Errorf("FromUSD(%v) = %d, %v；預期 %d, %v", c.usd, got, ok, c.want, c.ok)
		}
	}
	a, _ := FromUSD(0.10)
	b, _ := FromUSD(0.20)
	if (a + b).USD() != 0.3 {
		t.Errorf("0.10 + 0.20 = %v，預期剛好 0.3", (a + b).USD())
	}
	for m, want := range map[Micro]string{0: "$0.00", 40000: "$0.04", 4999: "$0.00", 5000: "$0.01", 20000000: "$20.00", 1234567: "$1.23", -50000: "-$0.05"} {
		if got := m.Dollars(); got != want {
			t.Errorf("Dollars(%d) = %q，預期 %q", m, got, want)
		}
	}
	for m, want := range map[Micro]string{0: "0.00", 100000: "0.10", 12300: "0.0123", 1500000: "1.50", 1: "0.000001", 120000000: "120.00", -100000: "-0.10"} {
		if got := m.CSV(); got != want {
			t.Errorf("CSV(%d) = %q，預期 %q", m, got, want)
		}
	}
	// 10000 個輸入 token × $2／百萬 + 2000 個輸出 token × $10／百萬 = $0.04（場景的金額）。
	if got := llmMicro(10000, 2000, 2, 10); got != 40000 {
		t.Errorf("llmMicro = %d，預期 40000", got)
	}
	if got := llmMicro(-5, -5, 2, 10); got != 0 {
		t.Errorf("負數的 token 應當作 0，得到 %d", got)
	}
	if got := llmMicro(math.MaxInt, math.MaxInt, 10000, 10000); got != Micro(MaxUSD*1e6) {
		t.Errorf("溢位的金額應封頂，得到 %d", got)
	}
}

func TestParseBudget(t *testing.T) {
	b, errs := ParseBudget(map[string]any{"monthly_usd": 20.0, "timezone": "Asia/Taipei"})
	if len(errs) != 0 || b.Monthly == nil || *b.Monthly != 20000000 || b.PerItem != nil || b.WarnAt != 80 || b.Timezone != "Asia/Taipei" {
		t.Fatalf("ParseBudget = %+v %v", b, errs)
	}
	b, errs = ParseBudget(map[string]any{"monthly_usd": nil, "per_item_usd": 0.0, "warn_at_percent": 100.0})
	if len(errs) != 0 || b.Monthly != nil || b.PerItem == nil || *b.PerItem != 0 || b.WarnAt != 100 || !b.Limited() {
		t.Fatalf("null 與邊界值：%+v %v", b, errs)
	}
	for _, c := range []struct {
		body map[string]any
		path string
	}{
		{map[string]any{"monthly_usd": -1.0}, "monthly_usd"},
		{map[string]any{"monthly_usd": 100000.000001}, "monthly_usd"},
		{map[string]any{"monthly_usd": "20"}, "monthly_usd"},
		{map[string]any{"per_item_usd": 100001.0}, "per_item_usd"},
		{map[string]any{"warn_at_percent": 0.0}, "warn_at_percent"},
		{map[string]any{"warn_at_percent": 101.0}, "warn_at_percent"},
		{map[string]any{"warn_at_percent": 80.5}, "warn_at_percent"},
		{map[string]any{"timezone": "Mars/Olympus"}, "timezone"},
		{map[string]any{"timezone": "Local"}, "timezone"},
		{map[string]any{"timezone": ""}, "timezone"},
		{map[string]any{"timezone": "../../etc/passwd"}, "timezone"},
		{map[string]any{"timezone": 8.0}, "timezone"},
		{map[string]any{"currency": "TWD"}, "currency"},
	} {
		_, errs := ParseBudget(c.body)
		if len(errs) != 1 || errs[0].Path != c.path {
			t.Errorf("ParseBudget(%v) = %v，預期 %s 的錯誤", c.body, errs, c.path)
		}
	}
	_, errs = ParseBudget(map[string]any{"timezone": "Mars/Olympus"})
	if errs[0].Message != "不認識的時區 Mars/Olympus，請用 IANA 時區名稱，例如 Asia/Taipei。" {
		t.Errorf("時區訊息 = %q", errs[0].Message)
	}
	// 多個錯誤依欄位順序，不認識的欄位依名稱排在最後。
	_, errs = ParseBudget(map[string]any{"z": 1.0, "a": 1.0, "timezone": "x", "monthly_usd": -1.0})
	var paths []string
	for _, e := range errs {
		paths = append(paths, e.Path)
	}
	if strings.Join(paths, ",") != "monthly_usd,timezone,a,z" {
		t.Errorf("錯誤順序 = %v", paths)
	}
}

func TestMonthBoundaries(t *testing.T) {
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Skip("沒有時區資料：", err)
	}
	for _, c := range []struct {
		at    string
		loc   *time.Location
		month string
	}{
		{"2026-11-30T15:59:59.999Z", taipei, "2026-11"},
		{"2026-11-30T16:00:00Z", taipei, "2026-12"},
		{"2026-10-31T17:00:00Z", taipei, "2026-11"},
		{"2026-10-31T17:00:00Z", time.UTC, "2026-10"},
		{"2026-12-31T23:59:59Z", time.UTC, "2026-12"},
	} {
		at, _ := time.Parse(time.RFC3339Nano, c.at)
		if _, _, got := MonthOf(at, c.loc); got != c.month {
			t.Errorf("MonthOf(%s, %s) = %s，預期 %s", c.at, c.loc, got, c.month)
		}
	}
	at, _ := time.Parse(time.RFC3339, "2026-11-30T15:00:00Z")
	if got := NextMonthStart(at, taipei).UTC(); !got.Equal(time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("台北的下一個月初 = %s", got)
	}
	// 剛好在月初那一刻：下一次是再下一個月。
	if got := NextMonthStart(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.UTC); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("月初那一刻的下一個月初 = %s", got)
	}
}

// rig 是有真實 SQLite 的 Service。
type rig struct {
	t   *testing.T
	ctx context.Context
	clk *clock.Fake
	st  *store.Store
	svc *Service
	ev  []events.Event
	mu  sync.Mutex
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{t: t, ctx: ctx, clk: clk, st: st, svc: New(clk, nil, o)}
	st.SetEventSink(func(ev events.Event) {
		r.mu.Lock()
		r.ev = append(r.ev, ev)
		r.mu.Unlock()
	})
	return r
}

func (r *rig) item(ext string) string {
	r.t.Helper()
	return r.titled(ext, "T "+ext)
}

func (r *rig) titled(ext, title string) string {
	r.t.Helper()
	it, _, err := r.st.Write().InsertItemIfAbsent(r.ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "u"})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.Write().UpdateItemMeta(r.ctx, it.ID, store.ItemMeta{Title: title}); err != nil {
		r.t.Fatal(err)
	}
	return it.ID
}

// rows 由新到舊列出全部紀錄。
func (r *rig) rows() []store.CostListRow {
	r.t.Helper()
	rows, err := r.st.Read().CostRowsBetween(r.ctx, time.Unix(0, 0), r.clk.Now().AddDate(1, 0, 0), store.CostCursor{}, 1000)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

func (r *rig) row(id string) store.CostListRow {
	r.t.Helper()
	for _, row := range r.rows() {
		if row.ID == id {
			return row
		}
	}
	r.t.Fatalf("找不到成本紀錄 %s", id)
	return store.CostListRow{}
}

func (r *rig) budget(body map[string]any) {
	r.t.Helper()
	b, errs := ParseBudget(body)
	if len(errs) > 0 {
		r.t.Fatal(errs)
	}
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.SetBudget(r.ctx, tx, b) }); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) price(provider, model, unit string, usd float64) {
	r.t.Helper()
	err := r.st.Tx(r.ctx, func(tx *store.Repo) error {
		_, err := r.svc.PutPrice(r.ctx, tx, map[string]any{"provider": provider, "model": model, "unit_type": unit, "usd_per_unit": usd})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) seed(itemID string, usd float64, state string, at time.Time) {
	r.t.Helper()
	if _, err := r.st.Write().InsertCostRow(r.ctx, store.CostRow{ItemID: itemID, USD: usd, State: state, At: at}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) reserve(c Call) (Reservation, error) {
	var res Reservation
	err := r.st.Tx(r.ctx, func(tx *store.Repo) error {
		var err error
		res, err = r.svc.Reserve(r.ctx, tx, c)
		return err
	})
	return res, err
}

func (r *rig) settle(res Reservation, c Call, u Usage) {
	r.t.Helper()
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.Settle(r.ctx, tx, res, c, u) }); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) view() BudgetView {
	r.t.Helper()
	v, err := r.svc.View(r.ctx, r.st.Read())
	if err != nil {
		r.t.Fatal(err)
	}
	return v
}

func call(item, task string) Call {
	return Call{ItemID: item, TaskID: task, Stage: "correct", Provider: "anthropic", Model: "m", CustomBase: true, InputTokens: 10000, Output: 2000}
}

func blockedMessage(t *testing.T, err error) string {
	t.Helper()
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassBudgetBlocked || ce.ResumeOn != ResumeToken {
		t.Fatalf("預期 budget_blocked，得到 %v", err)
	}
	return ce.Message
}

func TestReserveBoundaries(t *testing.T) {
	// 估算 $0.04（覆寫的單價），本月已有 $0.03：月預算 $0.07 剛好相等不算超過；差 1 micro 就超過。
	for _, c := range []struct {
		monthly float64
		blocked bool
	}{{0.07, false}, {0.069999, true}, {0.070001, false}} {
		r := newRig(t, Options{})
		r.price("anthropic", "m", UnitInputMTok, 2)
		r.price("anthropic", "m", UnitOutputMTok, 10)
		r.seed("", 0.03, store.CostActual, r.clk.Now())
		r.budget(map[string]any{"monthly_usd": c.monthly})
		res, err := r.reserve(call("", "t1"))
		if c.blocked {
			if msg := blockedMessage(t, err); !strings.Contains(msg, "超過預算") {
				t.Errorf("訊息 = %q", msg)
			}
			if v := r.view(); v.ReservedUSD != 0 {
				t.Errorf("被擋下的任務不應預留，reserved = %v", v.ReservedUSD)
			}
			continue
		}
		if err != nil || res.Estimate != 40000 || res.Attempt != 1 || !res.Priced {
			t.Fatalf("monthly %v：%+v %v", c.monthly, res, err)
		}
		if v := r.view(); v.ReservedUSD != 0.04 || v.SpentUSD != 0.03 {
			t.Errorf("預留後 = %+v", v)
		}
	}
}

func TestReservePerItemAndMessages(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"correct": 0.04}})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	a, b := r.item("aaaaaaaaaaa"), r.item("bbbbbbbbbbb")
	// 單部上限不分月份：上個月的花費也算。
	r.seed(a, 0.03, store.CostActual, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	r.budget(map[string]any{"per_item_usd": 0.05})
	msg := blockedMessage(t, func() error { _, err := r.reserve(call(a, "t1")); return err }())
	if msg != "這部影音已花費 $0.03，這個步驟預估 $0.04，超過單部上限 $0.05。" {
		t.Errorf("單部上限的訊息 = %q", msg)
	}
	if _, err := r.reserve(call(b, "t2")); err != nil {
		t.Errorf("另一部影音不受影響：%v", err)
	}
	// 單部上限剛好相等不算超過；差 1 micro 就超過。
	r.budget(map[string]any{"per_item_usd": 0.07})
	if _, err := r.reserve(call(a, "t4")); err != nil {
		t.Errorf("剛好等於單部上限應該可以：%v", err)
	}
	r.budget(map[string]any{"per_item_usd": 0.109999})
	if _, err := r.reserve(call(a, "t5")); !IsBudgetBlocked(err) {
		t.Errorf("差 1 micro 應該擋下：%v", err)
	}
	// 兩者都超過時用月預算的訊息。
	r.budget(map[string]any{"per_item_usd": 0.05, "monthly_usd": 0.05})
	msg = blockedMessage(t, func() error { _, err := r.reserve(call(a, "t3")); return err }())
	if msg != "本月預算 $0.05 已用 $0.08（含預留），這個步驟預估 $0.04，超過預算。" {
		t.Errorf("月預算的訊息 = %q", msg)
	}
}

func TestReserveUnpriced(t *testing.T) {
	r := newRig(t, Options{})
	// 沒有預算：照常預留 0，結算為 unknown。
	res, err := r.reserve(call("", "t1"))
	if err != nil || res.Priced || res.Estimate != 0 {
		t.Fatalf("沒有單價也沒有預算：%+v %v", res, err)
	}
	r.settle(res, call("", "t1"), Usage{Outcome: OutcomeOK, InputTokens: 10, OutputTokens: 5})
	if got := r.row(res.ID); got.State != store.CostUnknown || got.USD != 0 || !strings.Contains(got.UnitPriceJSON, `"source":"unknown"`) {
		t.Fatalf("unknown 的紀錄 = %+v", got)
	}
	// 有任一種預算時擋下（COST-R7，C2），即使估算被測試控制取代。
	r2 := newRig(t, Options{Estimates: map[string]float64{"correct": 0.01}})
	r2.budget(map[string]any{"per_item_usd": 5.0})
	c := call("", "t1")
	c.Provider, c.Model = "anthropic", "mystery-model"
	msg := blockedMessage(t, func() error { _, err := r2.reserve(c); return err }())
	if msg != "找不到 anthropic／mystery-model 的單價，無法檢查預算。" {
		t.Errorf("訊息 = %q", msg)
	}
	// 內建計價表只在沒有自訂 base_url 時適用（COST-R2）。
	builtin := Call{TaskID: "t9", Stage: "transcript", Provider: "groq", Model: "whisper-large-v3-turbo"}
	if q, ok, err := r.svc.Lookup(r.ctx, r.st.Read(), builtin.Provider, builtin.Model, UnitAudioHour, false); err != nil || !ok || q.Source != SourceBuiltin {
		t.Errorf("內建單價 = %+v %v %v", q, ok, err)
	}
	if _, ok, _ := r.svc.Lookup(r.ctx, r.st.Read(), builtin.Provider, builtin.Model, UnitAudioHour, true); ok {
		t.Error("自訂 base_url 不應套用內建單價")
	}
}

func TestSettleOutcomesAndDoubleSettle(t *testing.T) {
	r := newRig(t, Options{})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	c := call("", "task")
	neg, nan, huge, ok := -1.0, math.NaN(), 1e12, 0.0123
	cases := []struct {
		u      Usage
		usd    float64
		source string
	}{
		{Usage{Outcome: OutcomeInvalid, InputTokens: 10000, OutputTokens: 2000}, 0.04, SourceOverride},
		{Usage{Outcome: OutcomeHTTPError}, 0, SourceOverride},
		{Usage{Outcome: OutcomeNetworkError}, 0, SourceOverride},
		{Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000, ProviderUSD: &ok}, 0.0123, SourceProvider},
		// 不合理的回報（負數、NaN、超大）改用計價表。
		{Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000, ProviderUSD: &neg}, 0.04, SourceOverride},
		{Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000, ProviderUSD: &nan}, 0.04, SourceOverride},
		{Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000, ProviderUSD: &huge}, 0.04, SourceOverride},
	}
	for i, cs := range cases {
		res, err := r.reserve(c)
		if err != nil {
			t.Fatal(err)
		}
		if res.Attempt != i+1 {
			t.Errorf("第 %d 次的 attempt = %d", i+1, res.Attempt)
		}
		r.settle(res, c, cs.u)
		// 重複結算沒有作用。
		r.settle(res, c, Usage{Outcome: OutcomeOK, InputTokens: 1e6, OutputTokens: 1e6})
		got := r.row(res.ID)
		var up struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal([]byte(got.UnitPriceJSON), &up)
		if got.State != store.CostActual || math.Abs(got.USD-cs.usd) > 1e-12 || up.Source != cs.source || got.Outcome != cs.u.Outcome {
			t.Errorf("case %d：state=%s usd=%v source=%s outcome=%s", i, got.State, got.USD, up.Source, got.Outcome)
		}
	}
}

func TestReleaseAndOrphans(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"correct": 0.5}})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	res, err := r.reserve(call("", "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.Release(r.ctx, tx, res) }); err != nil {
		t.Fatal(err)
	}
	if v := r.view(); v.ReservedUSD != 0 {
		t.Errorf("釋放後 reserved = %v", v.ReservedUSD)
	}
	// 已結算的紀錄不會被釋放刪掉。
	res2, _ := r.reserve(call("", "t2"))
	r.settle(res2, call("", "t2"), Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000})
	_ = r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.Release(r.ctx, tx, res2) })
	if v := r.view(); v.SpentUSD != 0.04 {
		t.Errorf("已結算的紀錄被刪掉了：%+v", v)
	}
	// 程序中斷留下的預留（沒有執行中的任務）在重啟時刪除。
	r.seed("", 0.5, store.CostReserved, r.clk.Now())
	r.seed("", 0.7, store.CostActual, r.clk.Now())
	n, err := r.st.Write().DeleteOrphanReservations(r.ctx)
	if err != nil || n != 1 {
		t.Errorf("DeleteOrphanReservations = %d %v", n, err)
	}
	if v := r.view(); v.ReservedUSD != 0 || v.SpentUSD != 0.74 {
		t.Errorf("清理只刪預留：%+v", v)
	}
}

func TestConcurrentReservationsNeverOverspend(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"correct": 0.04}})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	r.budget(map[string]any{"monthly_usd": 0.10})
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN, blockedN := 0, 0
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.reserve(call("", "t"+string(rune('a'+i))))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okN++
			case IsBudgetBlocked(err):
				blockedN++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if okN != 2 || blockedN != 10 {
		t.Errorf("預留 %d、擋下 %d，預期 2 與 10", okN, blockedN)
	}
	if v := r.view(); v.ReservedUSD != 0.08 || *v.RemainingUSD != 0.02 {
		t.Errorf("併發預留後 = %+v", v)
	}
}

func TestWarningOncePerMonth(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"correct": 0.04}})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	r.budget(map[string]any{"monthly_usd": 0.10, "warn_at_percent": 80.0})
	r.seed("", 0.039999, store.CostActual, r.clk.Now())
	// 0.039999 + 0.04 = 0.079999 < 0.08：還沒到門檻。
	if _, err := r.reserve(call("", "t1")); err != nil {
		t.Fatal(err)
	}
	if n := countWarnings(r); n != 0 {
		t.Fatalf("還沒到門檻就提醒了：%d", n)
	}
	r.seed("", 0.000001, store.CostActual, r.clk.Now())
	// 剛好 0.08：達到門檻。
	if _, err := r.reserve(call("", "t2")); err == nil {
		t.Fatal("0.12 應該超過預算")
	}
	_ = r.st.Tx(r.ctx, func(tx *store.Repo) error { b, _ := r.svc.Budget(r.ctx, tx); return r.svc.warn(r.ctx, tx, b) })
	if n := countWarnings(r); n != 1 {
		t.Fatalf("達到門檻應提醒一次，得到 %d", n)
	}
	r.mu.Lock()
	ev := r.ev[len(r.ev)-1]
	r.mu.Unlock()
	if ev.Month != "2026-11" || *ev.Percent != 80 || *ev.MonthlyUSD != 0.1 || *ev.SpentUSD != 0.04 || *ev.ReservedUSD != 0.04 {
		t.Errorf("事件 = %+v", ev)
	}
	// 調整預算不會重設；同一個月不再提醒。
	r.budget(map[string]any{"monthly_usd": 0.10, "warn_at_percent": 1.0})
	_ = r.st.Tx(r.ctx, func(tx *store.Repo) error { b, _ := r.svc.Budget(r.ctx, tx); return r.svc.warn(r.ctx, tx, b) })
	if n := countWarnings(r); n != 1 {
		t.Errorf("同一個月提醒了 %d 次", n)
	}
	// 月預算為 0 時不提醒（沒有比例可言）。
	r.clk.Set(time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC))
	r.budget(map[string]any{"monthly_usd": 0.0})
	_ = r.st.Tx(r.ctx, func(tx *store.Repo) error { b, _ := r.svc.Budget(r.ctx, tx); return r.svc.warn(r.ctx, tx, b) })
	if n := countWarnings(r); n != 1 {
		t.Errorf("月預算 0 時提醒了：%d", n)
	}
}

func countWarnings(r *rig) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.ev {
		if e.Type == events.TypeBudgetWarning {
			n++
		}
	}
	return n
}

func TestPricesAPI(t *testing.T) {
	r := newRig(t, Options{PricesAsOf: "2026-07-01"})
	put := func(body map[string]any) error {
		return r.st.Tx(r.ctx, func(tx *store.Repo) error { _, err := r.svc.PutPrice(r.ctx, tx, body); return err })
	}
	for _, c := range []struct {
		body map[string]any
		path string
	}{
		{map[string]any{"provider": "a", "model": "m", "unit_type": "per_request", "usd_per_unit": 1.0}, "unit_type"},
		{map[string]any{"provider": "a", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": -0.5}, "usd_per_unit"},
		{map[string]any{"provider": "a", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 10000.01}, "usd_per_unit"},
		{map[string]any{"provider": "", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 1.0}, "provider"},
		{map[string]any{"provider": "a\nb", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 1.0}, "provider"},
		{map[string]any{"provider": "a", "model": strings.Repeat("字", 201), "unit_type": UnitInputMTok, "usd_per_unit": 1.0}, "model"},
		{map[string]any{"provider": "a", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 1.0, "checked_at": "2026-12-01"}, "checked_at"},
		{map[string]any{"provider": "a", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 1.0, "checked_at": "11/01"}, "checked_at"},
		{map[string]any{"provider": "a", "model": "m", "unit_type": UnitInputMTok, "usd_per_unit": 1.0, "x": 1}, "x"},
	} {
		var inv *InvalidError
		if err := put(c.body); !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != c.path {
			t.Errorf("PutPrice(%v) = %v，預期 %s 的錯誤", c.body, err, c.path)
		}
	}
	if err := put(map[string]any{"provider": "groq", "model": "whisper-large-v3", "unit_type": UnitAudioHour, "usd_per_unit": 0.2, "checked_at": "2026-11-01"}); err != nil {
		t.Fatal(err)
	}
	list, err := r.svc.Prices(r.ctx, r.st.Read())
	if err != nil {
		t.Fatal(err)
	}
	if list.BuiltinCheckedAt != "2026-07-01" || !list.BuiltinStale || len(list.Prices) != 2 {
		t.Fatalf("Prices = %+v", list)
	}
	// 同一組有覆寫時只列覆寫；依 provider、model 排序。
	if p := list.Prices[0]; p.Model != "whisper-large-v3" || p.Source != SourceOverride || p.Stale || p.CheckedAt != "2026-11-01" {
		t.Errorf("覆寫 = %+v", p)
	}
	notices, _ := r.svc.Notices(r.ctx, r.st.Read())
	if len(notices) != 1 || notices[0].Message != "計價表已經 145 天沒有確認，估價可能不準確。" {
		t.Errorf("notices = %+v", notices)
	}
	// 第 90 天還不算過期，第 91 天起才是。
	r.clk.Set(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	if l, _ := r.svc.Prices(r.ctx, r.st.Read()); l.BuiltinStale {
		t.Error("第 90 天不應過期")
	}
	r.clk.Set(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if l, _ := r.svc.Prices(r.ctx, r.st.Read()); !l.BuiltinStale {
		t.Error("第 91 天應過期")
	}
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.ConfirmPrices(r.ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.svc.Prices(r.ctx, r.st.Read()); l.BuiltinStale || l.BuiltinCheckedAt != "2026-09-30" {
		t.Errorf("確認後 = %+v", l)
	}
	del := func(body map[string]any) error {
		return r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.DeletePrice(r.ctx, tx, body) })
	}
	key := map[string]any{"provider": "groq", "model": "whisper-large-v3", "unit_type": UnitAudioHour}
	if err := del(key); err != nil {
		t.Fatal(err)
	}
	if err := del(key); !errors.Is(err, ErrNotFound) {
		t.Errorf("第二次刪除 = %v，預期 ErrNotFound", err)
	}
}

func TestRangesAndSummary(t *testing.T) {
	r := newRig(t, Options{})
	a := r.item("aaaaaaaaaaa")
	r.seed(a, 0.10, store.CostActual, time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC))
	r.seed(a, 0.20, store.CostActual, time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC))
	r.seed(a, 0.40, store.CostActual, time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC))
	r.seed("", 0.05, store.CostReserved, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	r.seed("", 0, store.CostUnknown, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	for _, c := range []struct{ from, to, msg string }{
		{"2026/11/01", "", "日期必須是 YYYY-MM-DD。"},
		{"", "2026-13-01", "日期必須是 YYYY-MM-DD。"},
		{"2026-11-10", "2026-11-01", "結束日期不能早於開始日期。"},
		{"2025-01-01", "2026-11-23", "查詢範圍最多 366 天。"},
		{"2025-11-22", "2026-11-23", "查詢範圍最多 366 天。"},
	} {
		_, err := r.svc.ParseRange(r.ctx, r.st.Read(), c.from, c.to)
		if q, ok := IsQueryError(err); !ok || q.Message != c.msg {
			t.Errorf("ParseRange(%q, %q) = %v，預期 %q", c.from, c.to, err, c.msg)
		}
	}
	if _, err := r.svc.ParseRange(r.ctx, r.st.Read(), "2025-11-23", "2026-11-23"); err != nil {
		t.Errorf("剛好 366 天應該可以：%v", err)
	}
	rng, err := r.svc.ParseRange(r.ctx, r.st.Read(), "2026-11-01", "2026-11-30")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := r.svc.Summarize(r.ctx, r.st.Read(), rng, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalUSD != 0.3 || sum.ReservedUSD != 0.05 || sum.UnknownCalls != 1 || len(sum.Groups) != 3 || sum.Groups[0].Key != "2026-11-02" {
		t.Errorf("Summary = %+v", sum)
	}
	// 本月 actual $0.30，過了 23 天（含今天），11 月 30 天：$0.30 / 23 × 30 ≈ $0.391304。
	if sum.Budget.ProjectedUSD != 0.391304 {
		t.Errorf("projected = %v", sum.Budget.ProjectedUSD)
	}
	if _, err := r.svc.Summarize(r.ctx, r.st.Read(), rng, "weekday"); err == nil {
		t.Error("不認識的 group_by 應該失敗")
	}
	byItem, _ := r.svc.Summarize(r.ctx, r.st.Read(), rng, "item")
	if byItem.Groups[0].Key != a || byItem.Groups[0].Label != "T aaaaaaaaaaa" || byItem.Groups[0].USD != 0.3 {
		t.Errorf("依影音 = %+v", byItem.Groups)
	}
	// 分頁：每頁 2 筆，由新到舊，接起來是全部 4 筆（範圍外的不算）。
	var all []Entry
	cursor := ""
	for range 5 {
		page, err := r.svc.ListEntries(r.ctx, r.st.Read(), rng, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Entries...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if len(all) != 4 || all[0].State != store.CostUnknown || all[3].USD != 0.1 {
		t.Errorf("分頁 = %+v", all)
	}
	for _, bad := range []string{"!!", "bm90LWEtY3Vyc29y", "LTEuYQ"} {
		if _, err := r.svc.ListEntries(r.ctx, r.st.Read(), rng, 2, bad); err == nil {
			t.Errorf("cursor %q 應該失敗", bad)
		}
	}
	for _, bad := range []string{"0", "501", "x"} {
		if _, err := ParseLimit(bad); err == nil {
			t.Errorf("limit %q 應該失敗", bad)
		}
	}
}

func TestExportCSVInjection(t *testing.T) {
	r := newRig(t, Options{})
	titles := []string{"=SUM(1+2)", "+cmd", "-2+3", "@SUM(A1)", "\tTab", "\rCR", "Plain, \"quoted\"\nline", ""}
	for i, title := range titles {
		id := r.titled("csv"+string(rune('a'+i))+"xxxxxxx", title)
		if _, err := r.st.Write().InsertCostRow(r.ctx, store.CostRow{ItemID: id, USD: 0.1, State: store.CostActual, Provider: "=evil", Model: "@m", Stage: "-s", At: r.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	rng, _ := r.svc.ParseRange(r.ctx, r.st.Read(), "2026-11-01", "2026-11-30")
	data, err := r.svc.Export(r.ctx, r.st.Read(), rng)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\r\n") {
		t.Error("CSV 應該用 CRLF")
	}
	// UseCRLF 的 encoding/csv 會丟掉欄位中單獨的 CR；前綴的 ' 仍在，試算表不會把它當公式。
	if !strings.Contains(string(data), "\"'CR\"") {
		t.Errorf("CR 開頭的標題沒有加上 '：%q", data)
	}
	recs, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(recs[0], ",") != strings.Join(csvHeader, ",") || len(recs) != len(titles)+1 {
		t.Fatalf("CSV = %q", recs)
	}
	want := map[string]bool{"'=SUM(1+2)": true, "'+cmd": true, "'-2+3": true, "'@SUM(A1)": true, "'\tTab": true, "'CR": true, "Plain, \"quoted\"\nline": true, "": true}
	for _, rec := range recs[1:] {
		if !want[rec[2]] {
			t.Errorf("item_title %q 沒有被正確處理", rec[2])
		}
		if rec[5] != "'=evil" || rec[6] != "'@m" || rec[4] != "'-s" || rec[9] != "0.10" {
			t.Errorf("其他文字欄位或金額 = %q", rec)
		}
	}
	if ExportName(rng) != "notesteme-costs-2026-11-01-2026-11-30.csv" {
		t.Errorf("檔名 = %s", ExportName(rng))
	}
}

func TestScheduleMonthFollowsTimezone(t *testing.T) {
	r := newRig(t, Options{})
	schedule := func() {
		t.Helper()
		if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.ScheduleMonth(r.ctx, tx) }); err != nil {
			t.Fatal(err)
		}
	}
	// 沒有存過預算時不排定。
	schedule()
	if _, err := r.st.Read().ScheduledJob(r.ctx, MonthKind, MonthKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("沒有預算時不應排定：%v", err)
	}
	r.budget(map[string]any{})
	job, err := r.st.Read().ScheduledJob(r.ctx, MonthKind, MonthKey)
	if err != nil || !job.NextRunAt.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC 的月初 = %+v %v", job, err)
	}
	r.budget(map[string]any{"timezone": "Asia/Taipei"})
	job, _ = r.st.Read().ScheduledJob(r.ctx, MonthKind, MonthKey)
	if !job.NextRunAt.Equal(time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("台北的月初 = %s", job.NextRunAt)
	}
	// 已經排定的不動（停機時錯過的月初在啟動後補跑一次）。
	r.clk.Set(time.Date(2027, 1, 5, 0, 0, 0, 0, time.UTC))
	schedule()
	job, _ = r.st.Read().ScheduledJob(r.ctx, MonthKind, MonthKey)
	if !job.NextRunAt.Equal(time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("已排定的工作被改了：%s", job.NextRunAt)
	}
	next, _ := r.svc.NextMonthRun(r.ctx, r.st.Read(), r.clk.Now())
	if !next.Equal(time.Date(2027, 1, 31, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("下一次 = %s", next)
	}
}

func TestRecordAgent(t *testing.T) {
	r := newRig(t, Options{})
	r.budget(map[string]any{"monthly_usd": 0.0})
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error { return r.svc.RecordAgent(r.ctx, tx, call("", "t1"), 7, 3) }); err != nil {
		t.Fatal(err)
	}
	rows := r.rows()
	if len(rows) != 1 || rows[0].State != store.CostUnknown || rows[0].USD != 0 || rows[0].Attempt != 1 {
		t.Fatalf("Agent 的紀錄 = %+v", rows)
	}
	if v := r.view(); v.SpentUSD != 0 || v.ReservedUSD != 0 {
		t.Errorf("Agent 的紀錄不應計入預算：%+v", v)
	}
}
