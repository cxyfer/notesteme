package app

import (
	"context"
	"encoding/json"
)

// StageConfig 回傳影音某個步驟最新一筆 stage run 綁定的快照：config 是生效設定，layers 是組成它的各層（PRF-R2）。
// 只供驗收測試的 harness 使用（ADR-0015：harness 可以直接存取 Core），沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (s *Server) StageConfig(ctx context.Context, itemID, stage string) (config, layers json.RawMessage, err error) {
	return s.pipeline.StageConfig(ctx, itemID, stage)
}
