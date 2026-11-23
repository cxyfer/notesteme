package steps

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// registerNotes 註冊「筆記與 Gemini」的步驟（steps.md 4.15，M3）。
//
// M3-S7 實作了 `Gemini accepts YouTube videos up to …`（只對 GEM 的場景放行，GEM G9）；M3-S4 實作了讀取筆記的三個句型。
// `the note of … is:`（VAULT 的閘門）由 M3-S5 實作，只對 VAULT 的場景放行（見 noteIs）。
func registerNotes(r *Registry, s *Scenario) {
	r.Step(`the note of {string} is:`, s.noteIs)
	r.Step(`the note of {string} contains {string}`, s.noteContains)
	r.Step(`the note of {string} does not contain {string}`, s.noteDoesNotContain)
	r.Step(`the note of {string} has the frontmatter:`, s.noteHasFrontmatter)
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

// noteIs 直接寫入影音現行的筆記（steps.md 4.15 的 seeding）。只對 VAULT 與 BND 的場景（有 @VAULT-、@BND- 開頭的 tag）放行；
// 其他領域（M4 的 BND 等）維持 pending，等各自的切片放寬（同 spbGate 的做法）。
func (s *Scenario) noteIs(ref string, doc *godog.DocString) error {
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@VAULT-") || strings.HasPrefix(t, "@BND-") }) {
		return fmt.Errorf("筆記的 seeding 只對 VAULT 的場景實作：%w", godog.ErrPending)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	return s.server.SeedNote(s.ctx, id, doc.Content)
}

// noteMarkdown 讀取影音現行的筆記（GET /api/items/:id/note 的 markdown，預設的筆記語言）；
// 經 harness 直接讀取，不改變「最近一次回應」。沒有筆記時回傳錯誤。
func (s *Scenario) noteMarkdown(ref string) (string, error) {
	id, err := s.itemID(ref)
	if err != nil {
		return "", err
	}
	v, err := s.server.ItemNote(s.ctx, id)
	if errors.Is(err, pipeline.ErrNoNote) {
		return "", fmt.Errorf("%s 沒有筆記", ref)
	}
	if err != nil {
		return "", err
	}
	return v.Markdown, nil
}

// noteContains 比對筆記的 Markdown 包含指定文字（逐字比對）。
func (s *Scenario) noteContains(ref, want string) error {
	md, err := s.noteMarkdown(ref)
	if err != nil {
		return err
	}
	if !strings.Contains(md, want) {
		return fmt.Errorf("%s 的筆記不含 %q：\n%s", ref, want, md)
	}
	return nil
}

// noteDoesNotContain 比對筆記的 Markdown 不包含指定文字；沒有筆記時失敗（不能證明「不含」）。
func (s *Scenario) noteDoesNotContain(ref, unwanted string) error {
	md, err := s.noteMarkdown(ref)
	if err != nil {
		return err
	}
	if strings.Contains(md, unwanted) {
		return fmt.Errorf("%s 的筆記含有 %q：\n%s", ref, unwanted, md)
	}
	return nil
}

// noteHasFrontmatter 以 YAML 解析筆記開頭的 frontmatter，逐列比對（值一律以字串比較，空白的 value 是空字串，欄位必須存在）。
func (s *Scenario) noteHasFrontmatter(ref string, table *godog.Table) error {
	md, err := s.noteMarkdown(ref)
	if err != nil {
		return err
	}
	front, ok := notes.ParseFrontmatter(md)
	if !ok {
		return fmt.Errorf("%s 的筆記沒有可以解析的 frontmatter：\n%s", ref, md)
	}
	if table == nil || len(table.Rows) < 2 || len(table.Rows[0].Cells) != 2 ||
		table.Rows[0].Cells[0].Value != "field" || table.Rows[0].Cells[1].Value != "value" {
		return errors.New("frontmatter 表格的表頭應是 field、value，至少一列")
	}
	for _, r := range table.Rows[1:] {
		field, want := r.Cells[0].Value, r.Cells[1].Value
		got, ok := front[field]
		if !ok {
			return fmt.Errorf("%s 的 frontmatter 沒有 %s：%v", ref, field, front)
		}
		if got != want {
			return fmt.Errorf("%s 的 frontmatter %s 是 %q，預期 %q", ref, field, got, want)
		}
	}
	return nil
}
