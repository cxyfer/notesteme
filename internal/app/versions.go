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
	g, ok, err := s.artifactGroup(ctx, itemID, name)
	if err != nil || !ok {
		return 0, err
	}
	return s.store.Read().ArtifactVersions(ctx, itemID, g.kind, g.variant, g.lang)
}

// ArtifactVersionID 回傳影音某種產出第 n 個版本的 ID（1 是保留中最舊的版本，含被取代的版本）；
// name 的規則同 ArtifactVersions。給 steps.md 的 `the version … of the … artifact of … is known as …` 使用
// （LIB-R25 的場景用 {artifact:<名稱>} 指稱版本）；只供驗收測試的 harness 使用。
func (s *Server) ArtifactVersionID(ctx context.Context, itemID, name string, n int) (string, error) {
	g, ok, err := s.artifactGroup(ctx, itemID, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("沒有 %q 產出", name)
	}
	vs, err := s.store.Read().ArtifactGroupVersions(ctx, itemID, g.kind, g.variant, g.lang)
	if err != nil {
		return "", err
	}
	if n < 1 || n > len(vs) {
		return "", fmt.Errorf("%q 產出有 %d 個版本，沒有第 %d 個", name, len(vs), n)
	}
	return vs[len(vs)-n].ID, nil // vs 由新到舊
}

type artifactGroupKey struct{ kind, variant, lang string }

// artifactGroup 找出符合 name（kind 或 variant）的唯一一組產出；沒有時 ok 是 false，多組時回傳錯誤。
func (s *Server) artifactGroup(ctx context.Context, itemID, name string) (artifactGroupKey, bool, error) {
	cur, err := s.store.Read().CurrentArtifacts(ctx, itemID, "")
	if err != nil {
		return artifactGroupKey{}, false, err
	}
	var groups []artifactGroupKey
	seen := map[artifactGroupKey]bool{}
	for _, a := range cur {
		g := artifactGroupKey{a.Kind, a.Variant, a.Lang}
		if seen[g] || (!strings.EqualFold(a.Kind, name) && !strings.EqualFold(a.Variant, name)) {
			continue
		}
		seen[g] = true
		groups = append(groups, g)
	}
	switch len(groups) {
	case 0:
		return artifactGroupKey{}, false, nil
	case 1:
		return groups[0], true, nil
	}
	desc := make([]string, len(groups))
	for i, g := range groups {
		desc[i] = fmt.Sprintf("%s/%s/%q", g.kind, g.variant, g.lang)
	}
	return artifactGroupKey{}, false, fmt.Errorf("%q 符合多組產出（kind/variant/lang）：%s；版本數要以單一組計算，不加總", name, strings.Join(desc, "、"))
}
