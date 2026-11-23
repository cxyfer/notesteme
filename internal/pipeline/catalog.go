package pipeline

import (
	"context"
	"slices"
)

// ProviderCatalog 回答「這個 LLM provider 存在嗎」（PRF-R4 引用不存在的 provider，決策 D10）。
// S3 之後由設定檔 llm.providers 登記的 provider 加上內建的組成；S1 只依賴這個介面。
type ProviderCatalog interface {
	HasLLMProvider(id string) bool
}

// builtinProviderIDs 是內建的 LLM provider（LLM 待決問題 L6；gemini 是 M3 的 GEM-R1）。
var builtinProviderIDs = []string{"anthropic", "openai", "gemini"}

// BuiltinProviders 是只含內建 provider 的目錄：anthropic、openai、gemini。
type BuiltinProviders struct{}

// HasLLMProvider 實作 ProviderCatalog。
func (BuiltinProviders) HasLLMProvider(id string) bool {
	return slices.Contains(builtinProviderIDs, id)
}

// PromptResolver 解析 prompt 引用（"<id>"、"<id>@default"、"<id>@<N>"，決策 D11）。
// 實作在 internal/app（轉接 prompts.Service），pipeline 不依賴 prompts 套件。
type PromptResolver interface {
	// ResolvePrompt 回傳引用實際指到的版本。錯誤：ErrPromptNotFound、ErrPromptVersionNotFound、
	// 引用格式錯誤時是 *InvalidError。
	ResolvePrompt(ctx context.Context, ref string) (ResolvedPrompt, error)
}

// ResolvedPrompt 是解析後的 prompt：Kind 用來檢查與步驟相符（segment、correct、translate），
// Version、SHA256 寫進快照該步驟的 prompt_version、prompt_sha256。
type ResolvedPrompt struct {
	ID      string
	Kind    string
	Version int
	SHA256  string
	// Body 是版本的內文；執行 LLM 步驟時用來組 prompt（快照只記版本與 sha256）。
	Body string
}
