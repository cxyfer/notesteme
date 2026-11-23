package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
)

// SponsorBlock 片段的產出（ADR-0020 第 4 點）：kind 與 variant 同名，CHAP 的 parents 以 variant 比對。
const (
	KindSponsorSegments  = "sponsor_segments"
	sponsorSegmentsFmt   = "notesteme-sponsor-segments+json"
	actionRequerySponsor = "重新查詢 SponsorBlock"
)

// SponsorBlockLookup 查詢 SponsorBlock（sponsorblock.Client）；nil 代表沒有設定，步驟以 permanent 失敗。
type SponsorBlockLookup interface {
	Lookup(ctx context.Context, videoID string, categories []string, durationMS int64) (sponsorblock.Result, error)
}

// sponsorSegmentsDoc 是 sponsor_segments 產出檔的內容。
type sponsorSegmentsDoc struct {
	Segments []sponsorblock.Segment `json:"segments"`
}

// runSponsorBlock 查詢 SponsorBlock 並存成 sponsor_segments 產出（SPB-R2～R6、R8）。
// 每次都重新查詢（不用快取）；結果與現行產出完全相同時沿用現行產出，不建立新版本。
// 查詢失敗不重試：步驟仍是 done、存空的產出，並附上提醒與「重新查詢 SponsorBlock」動作（SPB-R5、B3）。
func (p *Pipeline) runSponsorBlock(ctx context.Context, it store.Item, eff Effective) (result, error) {
	if it.Platform != source.PlatformYouTube || !sponsorblock.ValidVideoID(it.ExternalID) {
		// planChain 已經把非 YouTube 略過；這裡是最後一道防線，不送出任何請求。
		return result{}, errPermanent("sponsorblock 只適用 YouTube 影片")
	}
	if p.sponsor == nil {
		return result{}, errPermanent("沒有設定 SponsorBlock")
	}
	sb, err := eff.SponsorBlock()
	if err != nil {
		return result{}, errPermanent(err.Error())
	}
	var durMS int64
	if it.DurationS != nil && *it.DurationS > 0 {
		durMS = int64(*it.DurationS) * 1000
	}
	fetchedAt := p.clock.Now().UTC()
	res, err := p.sponsor.Lookup(ctx, it.ExternalID, sb.Categories, durMS)
	if err != nil {
		return result{}, err
	}

	body, err := json.Marshal(sponsorSegmentsDoc{Segments: nonNilSegments(res.Segments)})
	if err != nil {
		return result{}, err
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	meta := map[string]any{
		"base_url":   p.sponsorBase,
		"categories": strings.Join(sb.Categories, ","),
		"outcome":    res.Outcome,
		"fetched_at": fetchedAt.Format("2006-01-02T15:04:05Z"),
	}

	stage := &store.StageResult{Executor: ExecutorLocal, ImplVersion: p.implVersion(StageSponsorBlock)}
	done := store.StageState{Status: store.StatusDone}
	if res.Outcome == sponsorblock.OutcomeError {
		done.Message = fmt.Sprintf("無法取得 SponsorBlock 片段（%s），這次當作沒有片段。", res.Reason)
		done.Actions = []string{actionRequerySponsor}
	}

	// 結果與現行產出完全相同（內容、查詢結果、類別與 base_url）時不建立新版本（SPB-R8、B10）。
	if cur, ok, err := p.currentSponsorArtifact(ctx, it.ID); err != nil {
		return result{}, err
	} else if ok && cur.SHA256 == sha && sameSponsorLookup(cur.Provenance, meta) {
		stage.ArtifactID = cur.ID
		return result{task: taskDone, advance: true, stage: stage, state: done}, nil
	}

	prov, err := p.provenance(map[string]any{
		"stage": StageSponsorBlock, "impl_version": p.implVersion(StageSponsorBlock), "sponsorblock": meta,
	}, nil)
	if err != nil {
		return result{}, err
	}
	id := store.NewID()
	rel, fsha, size, err := writeArtifactFile(p.dataDir, it.ID, id, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return result{}, fmt.Errorf("寫入產出檔：%w", err)
	}
	art := &store.NewArtifact{
		ID: id, ItemID: it.ID, Kind: KindSponsorSegments, Variant: KindSponsorSegments,
		Format: sponsorSegmentsFmt, Path: rel, SHA256: fsha, Size: size, Provenance: prov,
	}
	return result{task: taskDone, advance: true, stage: stage, artifact: art, state: done}, nil
}

func nonNilSegments(s []sponsorblock.Segment) []sponsorblock.Segment {
	if s == nil {
		return []sponsorblock.Segment{}
	}
	return s
}

// currentSponsorArtifact 回傳影音現行的 sponsor_segments 產出。
func (p *Pipeline) currentSponsorArtifact(ctx context.Context, itemID string) (store.Artifact, bool, error) {
	arts, err := p.st.Read().CurrentArtifacts(ctx, itemID, KindSponsorSegments)
	if err != nil || len(arts) == 0 {
		return store.Artifact{}, false, err
	}
	return arts[len(arts)-1], true, nil
}

// sameSponsorLookup 比較現行產出的來源紀錄與這次查詢的 outcome、categories、base_url（不含 fetched_at）。
func sameSponsorLookup(prov json.RawMessage, meta map[string]any) bool {
	var doc struct {
		SponsorBlock map[string]any `json:"sponsorblock"`
	}
	if json.Unmarshal(prov, &doc) != nil || doc.SponsorBlock == nil {
		return false
	}
	for _, k := range []string{"outcome", "categories", "base_url"} {
		if doc.SponsorBlock[k] != meta[k] {
			return false
		}
	}
	return true
}

// SponsorSegments 回傳影音現行的 SponsorBlock 片段（SPB-R4）；步驟沒有執行過（沒有產出）時 ok 為 false。
// 產出檔讀不到或內容與 sha256 不符時回傳錯誤，由呼叫端決定是否略過。
func (p *Pipeline) SponsorSegments(ctx context.Context, itemID string) (segs []sponsorblock.Segment, ok bool, err error) {
	art, ok, err := p.currentSponsorArtifact(ctx, itemID)
	if err != nil || !ok {
		return nil, false, err
	}
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(art.Path)))
	if err != nil {
		return nil, false, err
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != art.SHA256 {
		return nil, false, fmt.Errorf("%s：%w", art.Path, ErrCorruptArtifact)
	}
	var doc sponsorSegmentsDoc
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&doc); err != nil {
		return nil, false, err
	}
	return nonNilSegments(doc.Segments), true, nil
}
