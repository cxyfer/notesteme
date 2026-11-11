package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

const artifactColumns = `id, item_id, kind, variant, lang, format, path, sha256, size, parents_json, provenance_json,
	COALESCE(superseded_by, ''), pinned, created_at`

func scanArtifact(sc scanner) (Artifact, error) {
	var (
		a                  Artifact
		parents, provenanc string
		pinned             int
		created            int64
	)
	err := sc.Scan(&a.ID, &a.ItemID, &a.Kind, &a.Variant, &a.Lang, &a.Format, &a.Path, &a.SHA256, &a.Size,
		&parents, &provenanc, &a.SupersededBy, &pinned, &created)
	if err != nil {
		return Artifact{}, err
	}
	if err := json.Unmarshal([]byte(parents), &a.Parents); err != nil {
		return Artifact{}, err
	}
	a.Provenance = json.RawMessage(provenanc)
	a.Pinned = pinned != 0
	a.CreatedAt = fromMs(created)
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
	if _, err := r.q.ExecContext(ctx,
		`UPDATE artifacts SET superseded_by = ?
		 WHERE item_id = ? AND kind = ? AND variant = ? AND lang = ? AND superseded_by IS NULL`,
		n.ID, n.ItemID, n.Kind, n.Variant, n.Lang); err != nil {
		return Artifact{}, err
	}
	if _, err := r.q.ExecContext(ctx,
		`INSERT INTO artifacts (id, item_id, kind, variant, lang, format, path, sha256, size, parents_json,
		                        provenance_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ItemID, n.Kind, n.Variant, n.Lang, n.Format, n.Path, n.SHA256, n.Size, string(parents),
		string(n.Provenance), r.nowMs()); err != nil {
		return Artifact{}, err
	}
	return r.Artifact(ctx, n.ID)
}

// Artifact 以 ID 查詢產出。
func (r *Repo) Artifact(ctx context.Context, id string) (Artifact, error) {
	a, err := scanArtifact(r.q.QueryRowContext(ctx, `SELECT `+artifactColumns+` FROM artifacts WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Artifact{}, ErrNotFound
	}
	return a, err
}

// CurrentArtifacts 列出影音現行（沒有被取代）的產出，依建立順序。kind 為空代表全部種類。
func (r *Repo) CurrentArtifacts(ctx context.Context, itemID, kind string) ([]Artifact, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+artifactColumns+` FROM artifacts
		 WHERE item_id = ? AND (? = '' OR kind = ?) AND superseded_by IS NULL ORDER BY created_at, rowid`,
		itemID, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArtifactVersions 回傳同一組 (item, kind, variant, lang) 的版本數，含被取代的。
func (r *Repo) ArtifactVersions(ctx context.Context, itemID, kind, variant, lang string) (n int, err error) {
	err = r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM artifacts WHERE item_id = ? AND kind = ? AND variant = ? AND (? = '' OR lang = ?)`,
		itemID, kind, variant, lang, lang).Scan(&n)
	return n, err
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
		if _, err := r.q.ExecContext(ctx,
			`INSERT INTO source_probes (id, item_id, run_id, source_id, lang, kind, available, est_cost, state,
			                            rejected_reason, probed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			NewID(), itemID, runID, p.SourceID, p.Lang, p.Kind, boolInt(p.Available), p.EstCost, p.State, p.Reason, now); err != nil {
			return err
		}
	}
	return nil
}

// LatestProbes 回傳影音最近一次 Probe 的結果，依記錄順序。
func (r *Repo) LatestProbes(ctx context.Context, itemID string) ([]Probe, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT id, item_id, run_id, source_id, lang, kind, available, est_cost, state, rejected_reason, probed_at
		 FROM source_probes
		 WHERE item_id = ? AND run_id = (SELECT run_id FROM source_probes WHERE item_id = ? ORDER BY rowid DESC LIMIT 1)
		 ORDER BY rowid`, itemID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Probe
	for rows.Next() {
		var (
			p         Probe
			available int
			at        int64
		)
		if err := rows.Scan(&p.ID, &p.ItemID, &p.RunID, &p.SourceID, &p.Lang, &p.Kind, &available, &p.EstCost,
			&p.State, &p.Reason, &at); err != nil {
			return nil, err
		}
		p.Available, p.ProbedAt = available != 0, fromMs(at)
		out = append(out, p)
	}
	return out, rows.Err()
}
