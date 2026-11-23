package digest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	// 容器映像可能沒有 zoneinfo：時區資料編進執行檔（DIG-R8 的 IANA 時區）。
	_ "time/tzdata"
)

// 週期（DIG-R8）。
const (
	PeriodDaily  = "daily"
	PeriodWeekly = "weekly"
)

// weekdays 是 weekday 的值（mon～sun）。
var weekdays = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// loadZone 載入 IANA 時區；空字串與 Local（依主機設定而變）不接受。
func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("不認得的時區")
	}
	return time.LoadLocation(name)
}

// parseClock 解析 HH:MM（00:00～23:59）。
func parseClock(s string) (h, m int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// schedule 是一個彙整的排程：在 loc 的 at（每週為 weekday 的 at）執行（DIG-R8）。
type schedule struct {
	loc     *time.Location
	hour    int
	minute  int
	weekly  bool
	weekday time.Weekday
}

func newSchedule(period, at, weekday, tz string) (schedule, error) {
	loc, err := loadZone(tz)
	if err != nil {
		return schedule{}, err
	}
	h, m, ok := parseClock(at)
	if !ok {
		return schedule{}, fmt.Errorf("at 不正確")
	}
	s := schedule{loc: loc, hour: h, minute: m, weekly: period == PeriodWeekly}
	if s.weekly {
		wd, ok := weekdays[weekday]
		if !ok {
			return schedule{}, fmt.Errorf("weekday 不正確")
		}
		s.weekday = wd
	}
	return s, nil
}

// period 是一個週期的長度（第一次執行時範圍的起點，DIG-R9）。
func (s schedule) period() time.Duration {
	if s.weekly {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// slotOn 回傳當地日期 (y, mo, d) 的 at：當地時間不存在（夏令時間跳過）時是之後第一個有效的時間，
// 重複的時間（夏令時間結束）取第一次（DIG-R8）。
func (s schedule) slotOn(y int, mo time.Month, d int) time.Time {
	naive := time.Date(y, mo, d, s.hour, s.minute, 0, 0, time.UTC)
	var best time.Time
	// 依附近的兩個 UTC 位移各算一次，留下當地時間確實是 at 的最早一個。
	for _, probe := range []time.Time{naive.Add(-36 * time.Hour), naive.Add(36 * time.Hour)} {
		_, off := probe.In(s.loc).Zone()
		t := naive.Add(-time.Duration(off) * time.Second)
		lt := t.In(s.loc)
		if lt.Year() == y && lt.Month() == mo && lt.Day() == d && lt.Hour() == s.hour && lt.Minute() == s.minute {
			if best.IsZero() || t.Before(best) {
				best = t
			}
		}
	}
	if !best.IsZero() {
		return best
	}
	// 跳過的時間：用轉換前的位移算出的時刻已經落在轉換之後，它所在時區區段的開頭就是第一個有效的時間。
	_, off := naive.Add(-36 * time.Hour).In(s.loc).Zone()
	t := naive.Add(-time.Duration(off) * time.Second)
	start, _ := t.In(s.loc).ZoneBounds()
	if start.IsZero() || start.After(t) {
		return t
	}
	return start
}

// each 從 from 所在的當地日期起，往前（step = -1）或往後（step = 1）的 9 天中，依序把符合星期的 at 交給 fn，fn 回傳 true 時停止。
func (s schedule) each(from time.Time, step int, fn func(time.Time) bool) {
	lt := from.In(s.loc)
	for k := 0; k < 9; k++ {
		day := time.Date(lt.Year(), lt.Month(), lt.Day()+step*k, 12, 0, 0, 0, time.UTC)
		if s.weekly && day.Weekday() != s.weekday {
			continue
		}
		if fn(s.slotOn(day.Year(), day.Month(), day.Day())) {
			return
		}
	}
}

// next 回傳嚴格晚於 t 的下一個 at（DIG-R9：下一次執行對齊當地時間，不是「執行時間＋間隔」）。
func (s schedule) next(t time.Time) time.Time {
	var out time.Time
	s.each(t, 1, func(slot time.Time) bool {
		if slot.After(t) {
			out = slot
			return true
		}
		return false
	})
	if out.IsZero() {
		out = t.Add(s.period()) // 不會發生：9 天內一定有符合的日期
	}
	return out
}

// prev 回傳不晚於 t 的最近一個 at（停機後補跑一次時，範圍的終點涵蓋整段錯過的期間，DIG-R9）。
func (s schedule) prev(t time.Time) time.Time {
	var out time.Time
	s.each(t, -1, func(slot time.Time) bool {
		if !slot.After(t) {
			out = slot
			return true
		}
		return false
	})
	if out.IsZero() {
		out = t
	}
	return out
}

// localDate 是 t 在彙整時區的日期（YYYY-MM-DD）。
func (s schedule) localDate(t time.Time) string { return t.In(s.loc).Format("2006-01-02") }

// title 是文字管道的標題（DIG-R10）：每日是範圍終點的當地日期；每週是起點到終點前一天的當地日期。
func (s schedule) title(start, end time.Time, n int) string {
	if s.weekly {
		le := end.In(s.loc)
		last := time.Date(le.Year(), le.Month(), le.Day()-1, 12, 0, 0, 0, time.UTC).Format("2006-01-02")
		return fmt.Sprintf("Notesteme 每週彙整：%s～%s（%d 部）", s.localDate(start), last, n)
	}
	return fmt.Sprintf("Notesteme 每日彙整：%s（%d 部）", s.localDate(end), n)
}
