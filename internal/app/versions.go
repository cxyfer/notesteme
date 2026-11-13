package app

import (
	"context"
	"fmt"
	"strings"
)

// ArtifactVersions 回傳影音某種產出的版本數，含被取代的版本（PIPE-R7、steps.md）。
// name 是產出的種類（kind）或變體（variant），例如 "source"。
//
// 版本數以同一組 (kind, variant, lang) 計算。場景沒有指定語言，所以符合 name 的產出必須只有一組；
// 有多組時回傳錯誤並列出各組，不跨語言或跨組加總（加總會掩蓋「其中一組版本數不對」）。
// 沒有任何符合的產出時回傳 0。只供驗收測試的 harness 使用（ADR-0015：harness 可以直接存取 Core），
// 沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (s *Server) ArtifactVersions(ctx context.Context, itemID, name string) (int, error) {
	r := s.store.Read()
	cur, err := r.CurrentArtifacts(ctx, itemID, "")
	if err != nil {
		return 0, err
	}
	type group struct{ kind, variant, lang string }
	var groups []group
	seen := map[group]bool{}
	for _, a := range cur {
		g := group{a.Kind, a.Variant, a.Lang}
		if seen[g] || (!strings.EqualFold(a.Kind, name) && !strings.EqualFold(a.Variant, name)) {
			continue
		}
		seen[g] = true
		groups = append(groups, g)
	}
	switch len(groups) {
	case 0:
		return 0, nil
	case 1:
		g := groups[0]
		return r.ArtifactVersions(ctx, itemID, g.kind, g.variant, g.lang)
	}
	desc := make([]string, len(groups))
	for i, g := range groups {
		desc[i] = fmt.Sprintf("%s/%s/%q", g.kind, g.variant, g.lang)
	}
	return 0, fmt.Errorf("%q 符合多組產出（kind/variant/lang）：%s；版本數要以單一組計算，不加總", name, strings.Join(desc, "、"))
}
