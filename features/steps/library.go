package steps

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// registerLibrary 註冊「全文搜尋、bundle 與術語審核」的步驟（steps.md 4.20，M4）。
//
// SRCH 的句型由 S8 實作（searchLibraryHas 等），BND 的由 bundles.go 實作，GLOS 的由 glossary_review.go 實作；其餘的句型在 M4 規格展開時（2026-11-23）只註冊步驟樁：handler 一律回傳 godog.ErrPending，
// 由 M4 的實作切片換成真正的實作（假服務見 features/fixtures/README.md 的「M4 的假服務」）。
func registerLibrary(r *Registry, s *Scenario) {
	r.Step(`the search library has {string} titled {string} with the {string} transcript:`, func(ref, title, variant string, t *godog.Table) error {
		return s.searchLibraryHas(ref, title, variant, "en", t)
	})
	r.Step(`the search library has {string} titled {string} with the {string} transcript in {string}:`, s.searchLibraryHas)
	r.Step(`the search runs for {string}`, s.searchRunsFor)
	r.Step(`the search runs for the query:`, func(doc *godog.DocString) error {
		return s.searchRunsFor(strings.TrimSuffix(doc.Content, "\n"))
	})
	r.Step(`the search index has no rows for {string}`, func(ref string) error { return s.searchIndexRows(ref, false) })
	r.Step(`the search index has rows for {string}`, func(ref string) error { return s.searchIndexRows(ref, true) })
	r.Step(`the owner deletes the saved view {string}`, s.ownerDeletesSavedView)
	// BND 的句型由 bundles.go 實作。
	registerBundles(r, s)
	// GLOS 的句型由 glossary_review.go 實作。
	registerGlossaryReview(r, s)
}

// searchVariants 是 search library 句型接受的字幕變體（steps.md 4.20）。
var searchVariants = []string{"source", "normalized", "segmented", "corrected", "edited"}

// searchLibraryHas 建立假的 YouTube 影片（標題與原文語言）、做到 metadata，再寫入指定變體的現行字幕（SRCH，steps.md 4.20）。
// 同一個代號已經存在時只寫入字幕（標題必須相同）。新建的影音 created_at 設為 fake clock 的現在加上序號毫秒，
// 讓同一個場景中後建立的影音嚴格較新（SRCH-R7）。
func (s *Scenario) searchLibraryHas(ref, title, variant, lang string, table *godog.Table) error {
	if !slices.Contains(searchVariants, variant) {
		return fmt.Errorf("變體 %q 不正確，應是 %v 其中之一", variant, searchVariants)
	}
	if !strings.HasPrefix(ref, "yt:") {
		return fmt.Errorf("search library 只接受 yt: 的代號，收到 %q", ref)
	}
	key, err := s.videoKey(ref)
	if err != nil {
		return err
	}
	v, existed := s.World.Tools.Videos[key]
	if existed {
		if v.Title != title {
			return fmt.Errorf("%s 已經存在，標題是 %q 而不是 %q", ref, v.Title, title)
		}
	} else if err := s.editVideo(ref, func(v *faketool.Video) error {
		v.Title, v.Language = title, lang
		return nil
	}); err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	if !existed {
		id, err := s.ensureItemForSeed(ref)
		if err != nil {
			return err
		}
		s.searchSeq++
		at := s.World.Clock.Now().Add(time.Duration(s.searchSeq) * time.Millisecond)
		if err := s.server.SeedItemCreatedAt(s.ctx, id, at); err != nil {
			return err
		}
	}
	return s.seedTranscript(variant, ref, lang, table)
}

// searchRunsFor 用 owner 的 session 送出全文搜尋（steps.md 4.20）。
func (s *Scenario) searchRunsFor(q string) error {
	return s.ownerSends("GET /api/search?mode=fulltext&q=" + url.QueryEscape(q))
}

// searchIndexRows 檢查索引中這部影音的列（SRCH-R8），並確認 cues_fts 與外部內容表一致。
func (s *Scenario) searchIndexRows(ref string, want bool) error {
	id, ok := s.World.Refs[ref]
	if !ok || id == "" {
		return fmt.Errorf("代號 %q 從未對應到影音 ID", ref)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	n, err := s.server.SearchIndexCheck(s.ctx, id)
	if err != nil {
		return err
	}
	switch {
	case want && n == 0:
		return fmt.Errorf("索引中沒有 %s 的列", ref)
	case !want && n > 0:
		return fmt.Errorf("索引中仍有 %s 的 %d 列", ref, n)
	}
	return nil
}

// ownerDeletesSavedView 用 owner 的 session 讀 GET /api/views，依名稱找到檢視後送出 DELETE /api/views/<id>（LIB-R28）。
// 檢視的 id 由產品產生，場景無法寫死，所以用名稱指定。最近一次回應是 DELETE 的回應；找不到這個名稱時步驟失敗。
func (s *Scenario) ownerDeletesSavedView(name string) error {
	if err := s.ownerSends("GET /api/views"); err != nil {
		return err
	}
	if s.last.status != 200 {
		return fmt.Errorf("GET /api/views 回應 %d：%s", s.last.status, snippet(s.last.body))
	}
	var list struct {
		Views []struct{ ID, Name string }
	}
	if err := json.Unmarshal(s.last.body, &list); err != nil {
		return fmt.Errorf("GET /api/views 的回應不是 JSON：%w", err)
	}
	for _, v := range list.Views {
		if v.Name == name {
			return s.ownerSends("DELETE /api/views/" + url.PathEscape(v.ID))
		}
	}
	return fmt.Errorf("找不到名稱是 %q 的檢視", name)
}
