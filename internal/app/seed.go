package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// SeedTranscript 把字幕直接寫成產出，跳過產生它的步驟。只供驗收測試的 harness 使用（ADR-0015：
// harness 可以直接存取 Core）；它不對應任何 HTTP 端點或 CLI 指令，production 程式不會呼叫它。
func (s *Server) SeedTranscript(ctx context.Context, req pipeline.SeedRequest) error {
	return s.pipeline.SeedTranscript(ctx, req)
}
