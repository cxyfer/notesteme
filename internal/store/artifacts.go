package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// NewArtifact 是新增產出所需的欄位。ID 由呼叫端先產生，因為產出檔的檔名就是它。
type NewArtifact struct {
	ID      string
	ItemID  string
	Kind    string
	Variant string
	Lang    string
	Format  string
	// Path 相對於資料目錄。
	Path    string
	SHA256  string
	Size    int64
	Parents []string
	// Provenance 是來源紀錄（ADR-0005），必須是非空的 JSON 物件；沒有來源紀錄的產出視為錯誤。
	Provenance json.RawMessage
}

// ErrNoProvenance 表示產出沒有來源紀錄（ADR-0005）。
var ErrNoProvenance = errors.New("store: 產出沒有來源紀錄")

func validateProvenance(p json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(p, &obj); err != nil || len(obj) == 0 {
		return ErrNoProvenance
	}
	return nil
}

func artifactFromRow(row sqlcdb.Artifact) (Artifact, error) {
	a := Artifact{
		ID:           row.ID,
		ItemID:       row.ItemID,
		Kind:         row.Kind,
		Variant:      row.Variant,
		Lang:         row.Lang,
		Format:       row.Format,
		Path:         row.Path,
		SHA256:       row.Sha256,
		Size:         row.Size,
		Provenance:   json.RawMessage(row.ProvenanceJson),
		SupersededBy: deref(row.SupersededBy),
		Pinned:       row.Pinned != 0,
		CreatedAt:    fromMs(row.CreatedAt),
	}
	if err := json.Unmarshal([]byte(row.ParentsJson), &a.Parents); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// InsertArtifact 新增產出。同一組 (item, kind, variant, lang) 原有的現行版本標記為被這一版取代（PIPE-R7）。
func (r *Repo) InsertArtifact(ctx context.Context, n NewArtifact) (Artifact, error) {
	if err := validateProvenance(n.Provenance); err != nil {
		return Artifact{}, fmt.Errorf("產出 %s/%s：%w", n.Kind, n.Variant, err)
	}
	if n.ID == "" {
		n.ID = NewID()
	}
	parents, err := json.Marshal(nonNil(n.Parents))
	if err != nil {
		return Artifact{}, err
	}
	if err := r.q.SupersedeCurrentArtifacts(ctx, sqlcdb.SupersedeCurrentArtifactsParams{
		SupersededBy: n.ID, ItemID: n.ItemID, Kind: n.Kind, Variant: n.Variant, Lang: n.Lang,
	}); err != nil {
		return Artifact{}, err
	}
	if err := r.q.InsertArtifact(ctx, sqlcdb.InsertArtifactParams{
		ID:             n.ID,
		ItemID:         n.ItemID,
		Kind:           n.Kind,
		Variant:        n.Variant,
		Lang:           n.Lang,
		Format:         n.Format,
		Path:           n.Path,
		Sha256:         n.SHA256,
		Size:           n.Size,
		ParentsJson:    string(parents),
		ProvenanceJson: string(n.Provenance),
		CreatedAt:      r.nowMs(),
	}); err != nil {
		return Artifact{}, err
	}
	return r.Artifact(ctx, n.ID)
}

// RestoreArtifact 讓一個被取代的產出重新成為現行版本：原本的現行版本改為被它取代，
// 它自己的 superseded_by 清除。已經是現行版本時什麼都不做。
// 快取命中重用舊產出時使用（PIPE-R5）：設定改回舊值，現行版本也要跟著回到那一版。
func (r *Repo) RestoreArtifact(ctx context.Context, id string) error {
	a, err := r.Artifact(ctx, id)
	if err != nil {
		return err
	}
	if a.SupersededBy == "" {
		return nil
	}
	if err := r.q.SupersedeOtherCurrentArtifacts(ctx, sqlcdb.SupersedeOtherCurrentArtifactsParams{
		KeepID: a.ID, ItemID: a.ItemID, Kind: a.Kind, Variant: a.Variant, Lang: a.Lang,
	}); err != nil {
		return err
	}
	return r.q.ClearArtifactSuperseded(ctx, a.ID)
}

// Artifact 以 ID 查詢產出。
func (r *Repo) Artifact(ctx context.Context, id string) (Artifact, error) {
	row, err := r.q.ArtifactByID(ctx, id)
	if err != nil {
		return Artifact{}, notFound(err)
	}
	return artifactFromRow(row)
}

// CurrentArtifacts 列出影音現行（沒有被取代）的產出，依建立順序。kind 為空代表全部種類。
func (r *Repo) CurrentArtifacts(ctx context.Context, itemID, kind string) ([]Artifact, error) {
	rows, err := r.q.CurrentArtifacts(ctx, sqlcdb.CurrentArtifactsParams{ItemID: itemID, Kind: kind})
	if err != nil {
		return nil, err
	}
	var out []Artifact
	for _, row := range rows {
		a, err := artifactFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ArtifactVersions 回傳同一組 (item, kind, variant, lang) 的版本數，含被取代的。
func (r *Repo) ArtifactVersions(ctx context.Context, itemID, kind, variant, lang string) (n int, err error) {
	c, err := r.q.CountArtifactVersions(ctx, sqlcdb.CountArtifactVersionsParams{
		ItemID: itemID, Kind: kind, Variant: variant, Lang: lang,
	})
	return int(c), err
}

// NewProbe 是一次 Probe 中某個來源的結果。
type NewProbe struct {
	SourceID  string
	Lang      string
	Kind      string
	Available bool
	EstCost   *float64
	State     string
	Reason    string
}

// InsertProbes 記錄一次 Probe 的全部結果（SRC-R17）。
func (r *Repo) InsertProbes(ctx context.Context, itemID string, probes []NewProbe) error {
	runID, now := NewID(), r.nowMs()
	for _, p := range probes {
		if err := r.q.InsertProbe(ctx, sqlcdb.InsertProbeParams{
			ID:             NewID(),
			ItemID:         itemID,
			RunID:          runID,
			SourceID:       p.SourceID,
			Lang:           p.Lang,
			Kind:           p.Kind,
			Available:      boolInt(p.Available),
			EstCost:        p.EstCost,
			State:          p.State,
			RejectedReason: p.Reason,
			ProbedAt:       now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// LatestProbes 回傳影音最近一次 Probe 的結果，依記錄順序。
func (r *Repo) LatestProbes(ctx context.Context, itemID string) ([]Probe, error) {
	rows, err := r.q.LatestProbes(ctx, itemID)
	if err != nil {
		return nil, err
	}
	var out []Probe
	for _, row := range rows {
		out = append(out, Probe{
			ID:        row.ID,
			ItemID:    row.ItemID,
			RunID:     row.RunID,
			SourceID:  row.SourceID,
			Lang:      row.Lang,
			Kind:      row.Kind,
			Available: row.Available != 0,
			EstCost:   row.EstCost,
			State:     row.State,
			Reason:    row.RejectedReason,
			ProbedAt:  fromMs(row.ProbedAt),
		})
	}
	return out, nil
}
