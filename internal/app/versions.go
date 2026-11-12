package app

import (
	"context"
	"strings"
)

// ArtifactVersions 回傳影音某種產出的版本數，含被取代的版本。name 是產出的種類（kind）或變體（variant），
// 例如 "source"；同一種產出有多個語言時加總。只供驗收測試的 harness 使用（ADR-0015：harness 可以直接存取 Core），
// 沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (s *Server) ArtifactVersions(ctx context.Context, itemID, name string) (int, error) {
	r := s.store.Read()
	cur, err := r.CurrentArtifacts(ctx, itemID, "")
	if err != nil {
		return 0, err
	}
	type group struct{ kind, variant, lang string }
	seen := map[group]bool{}
	total := 0
	for _, a := range cur {
		g := group{a.Kind, a.Variant, a.Lang}
		if seen[g] || (!strings.EqualFold(a.Kind, name) && !strings.EqualFold(a.Variant, name)) {
			continue
		}
		seen[g] = true
		n, err := r.ArtifactVersions(ctx, itemID, a.Kind, a.Variant, a.Lang)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
