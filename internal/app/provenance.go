package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ArtifactProvenanceRecord 是產出的來源紀錄（ADR-0005）。
type ArtifactProvenanceRecord struct {
	// Provenance 是 provenance_json 的原文。
	Provenance json.RawMessage
	// ParentVariants 是上游產出（parents_json）的變體名稱，依 parents 的順序。
	ParentVariants []string
}

// ArtifactProvenance 回傳影音現行產出的來源紀錄與上游產出的變體名稱。
// name 是產出的種類（kind）或變體（variant），例如 "source"；符合的現行產出必須剛好一個，
// 沒有或有多個（例如不同語言）都回傳錯誤並說明，不猜哪一個。
// 只供驗收測試的 harness 使用（ADR-0015：harness 可以直接存取 Core），
// 沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (s *Server) ArtifactProvenance(ctx context.Context, itemID, name string) (ArtifactProvenanceRecord, error) {
	r := s.store.Read()
	cur, err := r.CurrentArtifacts(ctx, itemID, "")
	if err != nil {
		return ArtifactProvenanceRecord{}, err
	}
	var match []string // 描述符合的產出，供錯誤訊息使用
	var found int
	for i, a := range cur {
		if !strings.EqualFold(a.Kind, name) && !strings.EqualFold(a.Variant, name) {
			continue
		}
		found = i
		match = append(match, fmt.Sprintf("%s/%s/%q", a.Kind, a.Variant, a.Lang))
	}
	switch len(match) {
	case 0:
		return ArtifactProvenanceRecord{}, fmt.Errorf("影音沒有 %q 產出", name)
	case 1:
	default:
		return ArtifactProvenanceRecord{}, fmt.Errorf("%q 符合多個現行產出（kind/variant/lang）：%s；來源紀錄要以單一產出讀取", name, strings.Join(match, "、"))
	}
	a := cur[found]
	rec := ArtifactProvenanceRecord{Provenance: a.Provenance, ParentVariants: []string{}}
	for _, pid := range a.Parents {
		p, err := r.Artifact(ctx, pid)
		if err != nil {
			return ArtifactProvenanceRecord{}, fmt.Errorf("讀取上游產出 %s：%w", pid, err)
		}
		rec.ParentVariants = append(rec.ParentVariants, p.Variant)
	}
	return rec, nil
}
