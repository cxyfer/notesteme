package steps

import (
	"fmt"
	"time"
)

// 這個檔案放事件持久化與清理的步驟（OPS-R10）；語意見 docs/specs/steps.md 第 4.12 節。

// opsEventExists 實作 `an event from {string} exists`；at 是 RFC 3339 時間。
// 事件直接寫進 events 表（harness.InsertEventAt）：場景要驗證的是清理，不是事件怎麼產生。
func (s *Scenario) opsEventExists(at string) error {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("事件時間 %q 不是 RFC 3339：%w", at, err)
	}
	// 資料庫要先 migrate 過；server 已明確停下時資料庫已經存在，不隱含啟動新的 server。
	if !s.stopped {
		if err := s.ensureServer(); err != nil {
			return err
		}
	}
	return s.World.InsertEventAt(t)
}

// opsNoEventOlderThan 實作 `no event older than {int} days remains`：以目前的 fake clock 往前算。
func (s *Scenario) opsNoEventOlderThan(days int) error {
	cutoff := s.World.Clock.Now().Add(-time.Duration(days) * 24 * time.Hour)
	n, err := s.World.EventsBefore(cutoff)
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("還有 %d 筆事件早於 %s（現在往前 %d 天）", n, cutoff.UTC().Format(time.RFC3339), days)
	}
	return nil
}

// opsEventCountIs 實作 `the number of events is {int}`。
// 一律以唯讀連線查 events 表：server 的 store 不對外提供，WAL 模式下執行中也能讀，
// 結果與經由 server 查詢相同，且不必區分 server 是否已停下。
func (s *Scenario) opsEventCountIs(want int) error {
	n, err := s.World.EventCount()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("事件筆數是 %d，預期 %d", n, want)
	}
	return nil
}
