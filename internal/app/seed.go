package app

import (
	"context"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// SeedTranscript 把字幕直接寫成產出，跳過產生它的步驟。只供驗收測試的 harness 使用（ADR-0015：
// harness 可以直接存取 Core）；它不對應任何 HTTP 端點或 CLI 指令，production 程式不會呼叫它。
func (s *Server) SeedTranscript(ctx context.Context, req pipeline.SeedRequest) error {
	return s.pipeline.SeedTranscript(ctx, req)
}

// Artifacts 回傳影音現行的產出。只供驗收測試的 harness 檢查「沒有建立某種產出」（例如 srt 不是產出，OUT-R1）。
func (s *Server) Artifacts(ctx context.Context, itemID string) ([]store.Artifact, error) {
	return s.store.Read().CurrentArtifacts(ctx, itemID, "")
}

// IngestAs 以指定的優先級（manual、subscription、backfill）新增影音。只供驗收測試的 harness 使用：
// API 不開放指定優先級，訂閱與回補來自內部的排程（PIPE-R9）。
func (s *Server) IngestAs(ctx context.Context, url, priority string) error {
	p, ok := queue.PriorityByName(priority)
	if !ok {
		return fmt.Errorf("不認識的優先級 %q，只有 manual、subscription、backfill", priority)
	}
	_, err := s.pipeline.Ingest(ctx, pipeline.IngestRequest{URL: url, Priority: p})
	return err
}

// SetImplVersion 覆寫步驟的 impl_version，模擬步驟的實作改版（PIPE-R6）。只供驗收測試的 harness 使用：
// 它不對應任何 HTTP 端點或 CLI 指令，production 程式不會呼叫它。
func (s *Server) SetImplVersion(stage string, v int) error {
	return s.pipeline.SetImplVersion(stage, v)
}

// SeedLangMixed 把影音的原文語言標記為 mixed。只供驗收測試的 harness 使用（同 SeedTranscript）。
func (s *Server) SeedLangMixed(ctx context.Context, itemID string) error {
	return s.pipeline.SeedLangMixed(ctx, itemID)
}

// SeedChapters 直接寫入影音現行的 chapters 產出（steps.md 4.13）。只供驗收測試的 harness 使用（同 SeedTranscript）。
func (s *Server) SeedChapters(ctx context.Context, itemID string, list []pipeline.SeedChapter) error {
	return s.pipeline.SeedChapters(ctx, itemID, list)
}

// SeedFrames 直接寫入影音現行的 frames 產出與圖片（steps.md 4.14）。只供驗收測試的 harness 使用（同 SeedTranscript）。
func (s *Server) SeedFrames(ctx context.Context, itemID string, list []pipeline.SeedFrame) error {
	return s.pipeline.SeedFrames(ctx, itemID, list)
}

// ItemNote 回傳影音現行的筆記（同 GET /api/items/:id/note）。只供驗收測試的 harness 讀取筆記而不改變「最近一次回應」（steps.md 4.15）。
func (s *Server) ItemNote(ctx context.Context, itemID string) (pipeline.NoteView, error) {
	return s.pipeline.ItemNote(ctx, itemID, "")
}

// SeedNote 直接寫入影音現行的 note 產出（steps.md 4.15）。只供驗收測試的 harness 使用（同 SeedTranscript）。
func (s *Server) SeedNote(ctx context.Context, itemID, markdown string) error {
	return s.pipeline.SeedNote(ctx, itemID, markdown)
}

// SeedSummary 直接寫入影音現行的 summary 產出，summary 步驟改成 done（steps.md 4.18）。只供驗收測試的 harness 使用。
func (s *Server) SeedSummary(ctx context.Context, itemID string, doc []byte) error {
	return s.pipeline.SeedSummary(ctx, itemID, doc)
}

// SeedItemCreatedAt 改寫影音的 created_at（SRCH-R7 的排序）。只供驗收測試的 harness 使用（同 SeedTranscript）。
func (s *Server) SeedItemCreatedAt(ctx context.Context, itemID string, at time.Time) error {
	return s.store.SeedItemCreatedAt(ctx, itemID, at)
}

// SearchIndexCheck 回報影音在全文搜尋索引中的列數，並執行 cues_fts 的 integrity-check（SRCH-R8）。
// 只供驗收測試的 harness 使用。
func (s *Server) SearchIndexCheck(ctx context.Context, itemID string) (int, error) {
	return s.store.SearchIndexCheck(ctx, itemID)
}

// SetInstanceID 設定實例 ID（bundle 的 created_by.instance_id，BND-R1）。只供驗收測試的 harness 使用。
func (s *Server) SetInstanceID(ctx context.Context, id string) error {
	return s.pipeline.SetInstanceID(ctx, id)
}
