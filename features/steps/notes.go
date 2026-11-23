package steps

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerNotes 註冊「筆記與 Gemini」的步驟（steps.md 4.15，M3）。
//
// M3 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M3 的實作切片換成真正的實作（假服務見 features/internal/fakegemini）。
// M3-S7 實作了 `Gemini accepts YouTube videos up to …`（只對 GEM 的場景放行，GEM G9）；筆記的四個句型仍是樁（NOTE 的切片）。
func registerNotes(r *Registry, s *Scenario) {
	r.Step(`the note of {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the note of {string} contains {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the note of {string} does not contain {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the note of {string} has the frontmatter:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`Gemini accepts YouTube videos up to {string}`, s.geminiAcceptsUpTo)
}

// geminiAcceptsUpTo 設定假 Gemini 可以讀取的片長上限（steps.md 4.15）。只對 GEM 的場景（有 @GEM- 開頭的 tag）放行，
// 其他領域的場景維持 pending（同 spbGate 的做法）。
func (s *Scenario) geminiAcceptsUpTo(limit string) error {
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@GEM-") }) {
		return fmt.Errorf("Gemini 的句型只對 GEM 的場景實作：%w", godog.ErrPending)
	}
	d, err := time.ParseDuration(limit)
	if err != nil || d <= 0 {
		return fmt.Errorf("片長上限 %q 不是正的時間長度（例如 45m）", limit)
	}
	s.World.Gemini.SetMaxDuration(d)
	return nil
}
