package subscription

import (
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/validate"
)

// 回補範圍的模式（SUB-R3）。
const (
	BackfillNone   = "none"
	BackfillRecent = "recent"
	BackfillAll    = "all"
)

// 直播的處理方式（SUB-R8）。
const (
	LiveWait = "wait"
	LiveSkip = "skip"
)

// 數值的範圍（SUB-R3、R4、R9）。
const (
	MaxRecent      = 200
	MinSchedule    = time.Hour
	MaxSchedule    = 168 * time.Hour
	DefaultSched   = 6 * time.Hour
	maxTitleRegexp = 200
)

// Backfill 是回補範圍；Count 只在 recent 時有值。
type Backfill struct {
	Mode  string `json:"mode"`
	Count *int   `json:"count,omitempty"`
}

// Filters 是過濾條件（SUB-R4）；存進資料庫與回應都是這個形狀。
type Filters struct {
	MinDurationS  *int   `json:"min_duration_s,omitempty"`
	MaxDurationS  *int   `json:"max_duration_s,omitempty"`
	ExcludeShorts bool   `json:"exclude_shorts"`
	Live          string `json:"live"`
	TitleInclude  string `json:"title_include,omitempty"`
	TitleExclude  string `json:"title_exclude,omitempty"`
}

// DefaultFilters 是預設的過濾條件：排除 Shorts、直播等到有錄影（SUB-R4）。
func DefaultFilters() Filters { return Filters{ExcludeShorts: true, Live: LiveWait} }

// FiltersInput 是請求中的過濾條件：沒有給的欄位用預設值。
type FiltersInput struct {
	MinDurationS  *int    `json:"min_duration_s,omitempty"`
	MaxDurationS  *int    `json:"max_duration_s,omitempty"`
	ExcludeShorts *bool   `json:"exclude_shorts,omitempty"`
	Live          *string `json:"live,omitempty"`
	TitleInclude  *string `json:"title_include,omitempty"`
	TitleExclude  *string `json:"title_exclude,omitempty"`
}

// apply 把輸入蓋在 base 上（PATCH 以既有的設定為 base）。
func (in *FiltersInput) apply(base Filters) Filters {
	if in == nil {
		return base
	}
	f := base
	if in.MinDurationS != nil {
		f.MinDurationS = in.MinDurationS
	}
	if in.MaxDurationS != nil {
		f.MaxDurationS = in.MaxDurationS
	}
	if in.ExcludeShorts != nil {
		f.ExcludeShorts = *in.ExcludeShorts
	}
	if in.Live != nil {
		f.Live = *in.Live
	}
	if in.TitleInclude != nil {
		f.TitleInclude = *in.TitleInclude
	}
	if in.TitleExclude != nil {
		f.TitleExclude = *in.TitleExclude
	}
	return f
}

// validateBackfill 檢查回補範圍（SUB-R3）。
func validateBackfill(b Backfill) []validate.FieldError {
	switch b.Mode {
	case BackfillNone, BackfillAll:
		return nil
	case BackfillRecent:
		if b.Count == nil || *b.Count < 1 || *b.Count > MaxRecent {
			return []validate.FieldError{{Path: "backfill.count", Message: "最近幾部必須是 1 到 200 之間的整數。"}}
		}
		return nil
	}
	return []validate.FieldError{{Path: "backfill.mode", Message: "回補範圍必須是 none、recent 或 all。"}}
}

// normalizeBackfill 去掉 recent 以外的 count。
func normalizeBackfill(b Backfill) Backfill {
	if b.Mode != BackfillRecent {
		b.Count = nil
	}
	return b
}

// validateFilters 檢查過濾條件（SUB-R4、R8），錯誤依欄位的固定順序。
func validateFilters(f Filters) []validate.FieldError {
	var errs []validate.FieldError
	for _, d := range []struct {
		path string
		v    *int
	}{{"filters.min_duration_s", f.MinDurationS}, {"filters.max_duration_s", f.MaxDurationS}} {
		if d.v != nil && *d.v < 0 {
			errs = append(errs, validate.FieldError{Path: d.path, Message: "片長必須是 0 以上的整數（秒）。"})
		}
	}
	if len(errs) == 0 && f.MinDurationS != nil && f.MaxDurationS != nil && *f.MinDurationS >= *f.MaxDurationS {
		errs = append(errs, validate.FieldError{Path: "filters.max_duration_s", Message: "最長片長必須大於最短片長。"})
	}
	if f.Live != LiveWait && f.Live != LiveSkip {
		errs = append(errs, validate.FieldError{Path: "filters.live", Message: "直播的處理方式必須是 wait 或 skip。"})
	}
	for _, r := range []struct{ path, expr string }{{"filters.title_include", f.TitleInclude}, {"filters.title_exclude", f.TitleExclude}} {
		if msg := checkTitleRegexp(r.expr); msg != "" {
			errs = append(errs, validate.FieldError{Path: r.path, Message: msg})
		}
	}
	return errs
}

// checkTitleRegexp 回傳標題 regex 的錯誤訊息；空字串代表沒有設定或合法。RE2 沒有回溯（lookaround 不支援）。
func checkTitleRegexp(expr string) string {
	switch {
	case expr == "":
		return ""
	case utf8.RuneCountInString(expr) > maxTitleRegexp:
		return "標題的正規表示式最長 200 個字元。"
	}
	if _, err := regexp.Compile(expr); err != nil {
		return "標題的正規表示式無效。"
	}
	return ""
}

// parseSchedule 解析檢查頻率（SUB-R9）；nil 代表預設。
func parseSchedule(s *string) (time.Duration, []validate.FieldError) {
	if s == nil {
		return DefaultSched, nil
	}
	d, err := time.ParseDuration(*s)
	if err != nil || d < MinSchedule || d > MaxSchedule {
		return 0, []validate.FieldError{{Path: "schedule", Message: "檢查頻率必須介於 1 小時到 7 天之間。"}}
	}
	return d, nil
}

// FormatSchedule 以最短的 Go duration 寫法輸出（6h、90m、1h30m）。
func FormatSchedule(d time.Duration) string {
	s := d.String()
	// time.Duration.String 會輸出 6h0m0s；去掉多餘的 0m、0s。
	for _, suf := range []string{"0s", "0m"} {
		if len(s) > len(suf) && s[len(s)-len(suf):] == suf && (s[len(s)-len(suf)-1] < '0' || s[len(s)-len(suf)-1] > '9') {
			s = s[:len(s)-len(suf)]
		}
	}
	return s
}

func decodeBackfill(raw json.RawMessage) Backfill {
	b := Backfill{Mode: BackfillNone}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &b)
	}
	if b.Mode == "" {
		b.Mode = BackfillNone
	}
	return b
}

func decodeFilters(raw json.RawMessage) Filters {
	f := DefaultFilters()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &f)
	}
	if f.Live == "" {
		f.Live = LiveWait
	}
	return f
}
