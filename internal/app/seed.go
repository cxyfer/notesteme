package app

import (
	"context"
	"fmt"

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
