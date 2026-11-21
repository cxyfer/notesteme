package app

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// promptResolver 把 prompts.Service 轉接成 pipeline.PromptResolver：pipeline 不依賴 prompts 套件，
// 驗證處理方案（PRF-R4）與 Compose 解析版本（D11）時經由它查詢。
type promptResolver struct{ s *prompts.Service }

// ResolvePrompt 實作 pipeline.PromptResolver，把 prompts 的錯誤轉成 pipeline 的錯誤。
func (a promptResolver) ResolvePrompt(ctx context.Context, ref string) (pipeline.ResolvedPrompt, error) {
	r, err := a.s.Resolve(ctx, ref)
	if err != nil {
		var inv *prompts.InvalidError
		switch {
		case errors.Is(err, prompts.ErrNotFound):
			return pipeline.ResolvedPrompt{}, pipeline.ErrPromptNotFound
		case errors.Is(err, prompts.ErrVersionNotFound):
			return pipeline.ResolvedPrompt{}, pipeline.ErrPromptVersionNotFound
		case errors.As(err, &inv):
			return pipeline.ResolvedPrompt{}, &pipeline.InvalidError{Msg: inv.Msg, Fields: inv.Fields}
		}
		return pipeline.ResolvedPrompt{}, err
	}
	return pipeline.ResolvedPrompt{ID: r.ID, Kind: r.Kind, Version: r.Version, SHA256: r.SHA256}, nil
}
