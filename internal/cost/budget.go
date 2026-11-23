package cost

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/validate"
)

// 預算的上下限（COST-R4）。
const (
	maxBudgetUSD    = 100000
	defaultWarnAt   = 80
	defaultTimezone = "UTC"
)

// Budget 是預算設定（COST-R4）。Monthly、PerItem 為 nil 代表不限。
type Budget struct {
	Monthly  *Micro
	PerItem  *Micro
	WarnAt   int
	Timezone string
}

// DefaultBudget 是沒有設定時的預算：不限、提醒門檻 80%、UTC。
func DefaultBudget() Budget { return Budget{WarnAt: defaultWarnAt, Timezone: defaultTimezone} }

// Limited 回報是否設定了任一種預算（COST-R7 只在有預算時擋下沒有單價的模型）。
func (b Budget) Limited() bool { return b.Monthly != nil || b.PerItem != nil }

// Location 回傳預算時區；存下的值不正確時退回 UTC（寫入時已驗證，不應發生）。
func (b Budget) Location() *time.Location {
	if loc, err := loadZone(b.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

// storedBudget 是 settings 中 cost.budget 的 JSON：金額存整數 micro-USD，讀回來不會有浮點誤差。
type storedBudget struct {
	Monthly  *int64 `json:"monthly_micro"`
	PerItem  *int64 `json:"per_item_micro"`
	WarnAt   int    `json:"warn_at_percent"`
	Timezone string `json:"timezone"`
}

func (b Budget) marshal() (string, error) {
	s := storedBudget{WarnAt: b.WarnAt, Timezone: b.Timezone}
	if b.Monthly != nil {
		v := int64(*b.Monthly)
		s.Monthly = &v
	}
	if b.PerItem != nil {
		v := int64(*b.PerItem)
		s.PerItem = &v
	}
	raw, err := json.Marshal(s)
	return string(raw), err
}

func unmarshalBudget(raw string) (Budget, error) {
	var s storedBudget
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Budget{}, fmt.Errorf("cost: 預算設定不正確：%w", err)
	}
	b := DefaultBudget()
	if s.Monthly != nil {
		v := Micro(*s.Monthly)
		b.Monthly = &v
	}
	if s.PerItem != nil {
		v := Micro(*s.PerItem)
		b.PerItem = &v
	}
	if s.WarnAt >= 1 && s.WarnAt <= 100 {
		b.WarnAt = s.WarnAt
	}
	if s.Timezone != "" {
		b.Timezone = s.Timezone
	}
	return b, nil
}

// budgetFields 是 PUT 接受的欄位，依錯誤的回報順序。
var budgetFields = []string{"monthly_usd", "per_item_usd", "warn_at_percent", "timezone"}

// ParseBudget 驗證 PUT /api/settings/budget 的 body（COST-R4）：取代整個區段，沒給的欄位回到預設值。
// 錯誤依欄位的固定順序，不認識的欄位依名稱排在最後；路徑是欄位名稱本身（沒有區段前綴）。
func ParseBudget(body map[string]any) (Budget, []validate.FieldError) {
	b := DefaultBudget()
	var errs []validate.FieldError
	amount := func(field, msg string) *Micro {
		v, ok := body[field]
		if !ok || v == nil {
			return nil
		}
		f, isNum := v.(float64)
		if !isNum || f < 0 || f > maxBudgetUSD {
			errs = append(errs, validate.FieldError{Path: field, Message: msg})
			return nil
		}
		m, _ := FromUSD(f)
		return &m
	}
	b.Monthly = amount("monthly_usd", "月預算必須介於 0 到 100000 美元之間。")
	b.PerItem = amount("per_item_usd", "單部上限必須介於 0 到 100000 美元之間。")
	if v, ok := body["warn_at_percent"]; ok && v != nil {
		f, isNum := v.(float64)
		if !isNum || f != math.Trunc(f) || f < 1 || f > 100 {
			errs = append(errs, validate.FieldError{Path: "warn_at_percent", Message: "提醒門檻必須是 1 到 100 的整數。"})
		} else {
			b.WarnAt = int(f)
		}
	}
	if v, ok := body["timezone"]; ok && v != nil {
		tz, isStr := v.(string)
		if !isStr {
			errs = append(errs, validate.FieldError{Path: "timezone", Message: "不認識的時區，請用 IANA 時區名稱，例如 Asia/Taipei。"})
		} else if _, err := loadZone(tz); err != nil {
			errs = append(errs, validate.FieldError{Path: "timezone", Message: "不認識的時區 " + cleanValue(tz) + "，請用 IANA 時區名稱，例如 Asia/Taipei。"})
		} else {
			b.Timezone = tz
		}
	}
	var unknown []string
	for k := range body {
		if !slices.Contains(budgetFields, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	for _, k := range unknown {
		errs = append(errs, validate.FieldError{Path: k, Message: "不認識的欄位。"})
	}
	return b, errs
}

// loadZone 只接受 IANA 時區名稱：time.LoadLocation 也接受的空字串與 Local 會依主機設定而變，不接受。
func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" || len(name) > 64 || strings.ContainsAny(name, "\x00\\") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("cost: 不認識的時區 %q", name)
	}
	return time.LoadLocation(name)
}

// cleanValue 截短並去掉控制字元，讓使用者的值可以放進錯誤訊息。
func cleanValue(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 64 {
			b.WriteString("…")
			break
		}
	}
	return b.String()
}

// MonthOf 回傳 t 在 loc 中所屬的日曆月：開始（含）、結束（不含）與 YYYY-MM（COST-R11）。
func MonthOf(t time.Time, loc *time.Location) (start, end time.Time, label string) {
	lt := t.In(loc)
	start = time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, loc)
	end = start.AddDate(0, 1, 0)
	return start, end, start.Format("2006-01")
}

// NextMonthStart 回傳 t 之後（不含 t 所在的這一刻）下一個月份的開始，也就是 budget.month 的下一次執行時間（COST-R9）。
func NextMonthStart(t time.Time, loc *time.Location) time.Time {
	_, end, _ := MonthOf(t, loc)
	return end
}
