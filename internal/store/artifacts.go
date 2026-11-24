package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

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
	// Timings 是字幕產出的 cue 中出現過的 timing origin（LIB-R7）；寫入時排序並去重，非字幕產出留空。
	Timings []string
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
		CorruptAt:    msToTime(row.CorruptAt),
	}
	if err := json.Unmarshal([]byte(row.ParentsJson), &a.Parents); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// normalizeTimings 排序並去重；nil 轉成空陣列，寫進資料庫的永遠是 JSON 陣列。
func normalizeTimings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	out = slices.Compact(out)
	if out == nil {
		return []string{}
	}
	return out
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
	timings, err := json.Marshal(normalizeTimings(n.Timings))
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
		TimingsJson:    string(timings),
		CreatedAt:      r.nowMs(),
	}); err != nil {
		return Artifact{}, err
	}
	if n.Kind == kindTranscript {
		// SRCH-R2：索引在寫入產出的同一個交易中更新。
		if err := r.reindexSearch(ctx, n.ItemID, n.Lang); err != nil {
			return Artifact{}, err
		}
	}
	return r.Artifact(ctx, n.ID)
}

// WithdrawArtifacts 撤下影音在指定語言的現行 (kind, variant) 產出：標記為被自己取代，保留歷史版本與檔案。
// 步驟不再要求某個語言時使用（例如 translate 的 language.targets 少了一個語言，或全部目標都與原文相同）：
// 產出的取代以語言為單位，不明確撤下的話，舊的 translated 產出會一直是現行版本。
// 指向自己而不是指向另一個語言的新版本：superseded_by 只在同一組 (item, kind, variant, lang) 內有意義，
// 指向別的語言的列被清除時 PurgeOldArtifacts 不會修正它（Codex P3）。langs 為空代表該 (kind, variant) 的所有語言。
func (r *Repo) WithdrawArtifacts(ctx context.Context, itemID, kind, variant string, langs []string) error {
	cur, err := r.CurrentArtifacts(ctx, itemID, kind)
	if err != nil {
		return err
	}
	for _, a := range cur {
		if a.Variant != variant || (len(langs) > 0 && !slices.Contains(langs, a.Lang)) {
			continue
		}
		if err := r.q.SupersedeCurrentArtifacts(ctx, sqlcdb.SupersedeCurrentArtifactsParams{
			SupersededBy: a.ID, ItemID: itemID, Kind: kind, Variant: variant, Lang: a.Lang,
		}); err != nil {
			return err
		}
		if kind == kindTranscript {
			if err := r.reindexSearch(ctx, itemID, a.Lang); err != nil {
				return err
			}
		}
	}
	return nil
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
	if err := r.q.ClearArtifactSuperseded(ctx, a.ID); err != nil {
		return err
	}
	if a.Kind == kindTranscript {
		return r.reindexSearch(ctx, a.ItemID, a.Lang)
	}
	return nil
}

// PurgeOldArtifacts 清除同一組 (item, kind, variant, lang) 中比最近 keep 版更舊的版本（PIPE-R7）。
// 下列版本永遠保留：現行版本（沒有被取代的）、被引用的版本（pinned），以及被其他產出列為 parent 的版本，
// 否則來源紀錄會指向不存在的產出。
//
// 被清除的列從資料庫刪除；指向它們的步驟紀錄改為沒有產出（外鍵），原本被它們取代的版本改為被現行版本取代，
// 所以 superseded_by 不會指向不存在的列。回傳被清除的產出，呼叫端應該在 commit 之後刪除它們的檔案。
// 必須在交易中呼叫，與新增版本的 InsertArtifact 一起成功或回復。
func (r *Repo) PurgeOldArtifacts(ctx context.Context, itemID, kind, variant, lang string, keep int) ([]Artifact, error) {
	rows, err := r.q.ArtifactGroup(ctx, sqlcdb.ArtifactGroupParams{ItemID: itemID, Kind: kind, Variant: variant, Lang: lang})
	if err != nil || len(rows) <= keep {
		return nil, err
	}
	refs, err := r.q.ArtifactParentRefs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	parents := map[string]bool{}
	for _, ref := range refs {
		var ids []string
		if err := json.Unmarshal([]byte(ref.ParentsJson), &ids); err != nil {
			return nil, fmt.Errorf("產出 %s 的 parents：%w", ref.ID, err)
		}
		for _, id := range ids {
			parents[id] = true
		}
	}

	var purged []Artifact
	current := ""
	for i, row := range rows {
		if row.SupersededBy == nil {
			current = row.ID
		}
		if i < keep || row.SupersededBy == nil || row.Pinned != 0 || parents[row.ID] {
			continue
		}
		a, err := artifactFromRow(row)
		if err != nil {
			return nil, err
		}
		if err := r.q.DetachStageRunsFromArtifact(ctx, &row.ID); err != nil {
			return nil, err
		}
		if err := r.q.DeleteArtifact(ctx, row.ID); err != nil {
			return nil, err
		}
		purged = append(purged, a)
	}
	if len(purged) > 0 && current != "" {
		if err := r.q.RepointDanglingSuperseded(ctx, sqlcdb.RepointDanglingSupersededParams{
			CurrentID: current, ItemID: itemID, Kind: kind, Variant: variant, Lang: lang,
		}); err != nil {
			return nil, err
		}
	}
	return purged, nil
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

// Probe 的觸發來源（source_probes.probe_trigger，SRC-R19）。
const (
	ProbeTriggerStage   = "stage"
	ProbeTriggerReprobe = "reprobe"
)

// InsertProbes 記錄 transcript 步驟一次 Probe 的全部結果（SRC-R17）。
func (r *Repo) InsertProbes(ctx context.Context, itemID string, probes []NewProbe) error {
	return r.InsertProbesFor(ctx, itemID, ProbeTriggerStage, probes)
}

// InsertProbesFor 同 InsertProbes，另外指定觸發來源；重新 Probe 的結果（reprobe）不會出現在 LatestProbes（SRC-R19）。
func (r *Repo) InsertProbesFor(ctx context.Context, itemID, trigger string, probes []NewProbe) error {
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
			ProbeTrigger:   trigger,
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

// PinArtifact 把產出標記為 pinned（被 vault 輸出引用，ADR-0020 第 4 點）：清除舊版本時保留。
func (r *Repo) PinArtifact(ctx context.Context, id string) error {
	return r.q.PinArtifact(ctx, id)
}

// ItemArtifactSHAs 回傳影音所有產出（含被取代的版本）的內容 sha256（BND-R9 略過相同內容的產出）。
func (r *Repo) ItemArtifactSHAs(ctx context.Context, itemID string) (map[string]bool, error) {
	rows, err := r.q.ItemArtifactSHAs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, s := range rows {
		out[s] = true
	}
	return out, nil
}

// BundleImport 是影音最近一次從 bundle 匯入的來源（BND-R8 的 imported_from）。
type BundleImport struct {
	InstanceID   string
	AppVersion   string
	BundleSHA256 string
	// ImportedAt 是 RFC 3339 的匯入時間；解析失敗時是零值。
	ImportedAt time.Time
}

// LatestBundleImport 回傳影音（任何版本的產出）最近一次從 bundle 匯入的來源；沒有時 ok 為 false。
// 只認 bundle 的形狀（有 bundle_sha256），字幕檔匯入的 imported_from 不算。
func (r *Repo) LatestBundleImport(ctx context.Context, itemID string) (BundleImport, bool, error) {
	row, err := r.q.LatestBundleImport(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return BundleImport{}, false, nil
	}
	if err != nil {
		return BundleImport{}, false, err
	}
	b := BundleImport{InstanceID: row.InstanceID, AppVersion: row.AppVersion, BundleSHA256: row.BundleSha256}
	if t, err := time.Parse(time.RFC3339, row.ImportedAt); err == nil {
		b.ImportedAt = t.UTC()
	}
	return b, true, nil
}

// MarkArtifactCorrupt 把產出標記為損毀（NFR-REL-2）。回傳 true 代表這次才標記（之前沒有標記過）：
// 只有它要重跑產生這份產出的步驟，並行讀到同一個壞檔的其他呼叫端不重複重跑。
func (r *Repo) MarkArtifactCorrupt(ctx context.Context, id string) (bool, error) {
	n, err := r.q.MarkArtifactCorrupt(ctx, sqlcdb.MarkArtifactCorruptParams{Now: r.nowMs(), ID: id})
	return n == 1, err
}
