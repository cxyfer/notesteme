package app

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// 契約階段：prompt 庫還沒實作，轉接層把 ErrNotImplemented 轉成 pipeline 的 501 類型錯誤；
// 並且 promptResolver 滿足 pipeline.PromptResolver。WP-3 實作時補上 not found、版本不存在的轉換測試。
func TestPromptResolverStubIsNotImplemented(t *testing.T) {
	var r pipeline.PromptResolver = promptResolver{prompts.New(prompts.Deps{})}
	_, err := r.ResolvePrompt(context.Background(), "correct-default")
	var ni *pipeline.NotImplementedError
	if !errors.As(err, &ni) {
		t.Errorf("ResolvePrompt = %v，預期 *pipeline.NotImplementedError", err)
	}
}
