package pipeline

import (
	"context"

	"github.com/cxyfer/notesteme/internal/validate"
)

// LayerKind 是被驗證的設定層的種類，決定要不要檢查完整性（決策 D8）。
type LayerKind int

const (
	// KindGlobal 是全域區段，以 {section: body} 傳入。
	KindGlobal LayerKind = iota
	// KindProfile 是處理方案的完整文件：檢查 PRF-R5 的完整性。
	KindProfile
	// KindOverride 是頻道、新增、重跑的覆寫：不檢查完整性。
	KindOverride
)

// ValidateDeps 是驗證時查詢外部資源用的依賴；nil 的欄位代表略過對應的檢查。
type ValidateDeps struct {
	// Providers 為 nil 時不檢查 provider 是否存在。
	Providers ProviderCatalog
	// Prompts 為 nil 時不檢查 prompt 是否存在。
	Prompts PromptResolver
}

// ValidateLayer 依固定順序檢查 doc，回傳全部錯誤（沒有錯誤時回傳 nil）。
// doc 可以是 JSON 或 YAML 解出來的 map，開頭先經過一次 JSON 轉換，數字一律以 float64 處理。
//
// TODO(WP-1)：實作 schema 檢查（M2 S1/S2 設計第 5.2 節）。契約階段先不檢查任何東西，永遠回傳 nil，
// 所以現有行為（BuildEffective 的語意檢查）完全不變。
func ValidateLayer(ctx context.Context, doc map[string]any, kind LayerKind, deps ValidateDeps) []validate.FieldError {
	_ = ctx
	_ = doc
	_ = kind
	_ = deps
	return nil
}
