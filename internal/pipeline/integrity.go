package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// NFR-REL-2：讀取或重用產出檔時比對 sha256，不符（或檔案不見）就標記損毀並重跑產生它的步驟。
//
// 比對的時機：
//   - 快取命中（DoneStageByCacheKey）要重用某份產出時：重新計算整個檔案的 sha256（只在重用判斷時，不是每個請求）。
//     損毀的產出標記之後，SQL 也不再把它當成快取命中，這個步驟照常重新產生。
//   - 下游步驟或 API 讀取字幕產出時（readTranscript 本來就讀進整個檔案並驗證 sha256）：不符時標記，
//     並從產生它的步驟重新處理（Rerun），之後的步驟跟著重跑，LLM 的 chunk 快取照常重用（不重複付費）。

// CorruptArtifactError 是讀到損毀的產出：Rerun 為 true 代表已經從 Stage 重新處理（這次發現的人觸發的），
// false 代表已經有人觸發過，或這份產出不是由步驟產生（匯入、編輯、從 bundle 或 Hub 匯入），不能自動重跑。
type CorruptArtifactError struct {
	ArtifactID string
	Stage      string
	Rerun      bool
}

func (e *CorruptArtifactError) Error() string {
	if e.Stage != "" {
		return fmt.Sprintf("產出檔已損毀，已重新處理產生它的步驟（%s），完成後再試一次。", e.Stage)
	}
	return "產出檔已損毀，而且不是由處理步驟產生的，無法自動重新產生；請重新匯入或重新處理。"
}

func (e *CorruptArtifactError) Unwrap() error { return ErrCorruptArtifact }

// verifyArtifactFile 確認產出檔存在、大小與 sha256 和記錄相符；不符或不見時回傳包著 ErrCorruptArtifact 的錯誤。
// 大小不同時不必讀完檔案；其他讀取錯誤原樣回傳（不視為損毀）。
func verifyArtifactFile(dataDir string, a store.Artifact) error {
	f, err := os.Open(filepath.Join(dataDir, filepath.FromSlash(a.Path)))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s 不見了：%w", a.Path, ErrCorruptArtifact)
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil {
		return err
	} else if st.Size() != a.Size {
		return fmt.Errorf("%s 的大小 %d 與記錄的 %d 不符：%w", a.Path, st.Size(), a.Size, ErrCorruptArtifact)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("%s：%w", a.Path, ErrCorruptArtifact)
	}
	return nil
}

// cacheHit 是 DoneStageByCacheKey 加上檔案檢查：命中的產出檔損毀或不見時標記損毀，當成沒有命中（這個步驟會重新產生）。
// 沒有產出的命中（ArtifactID 為空）照原樣回傳。
func (p *Pipeline) cacheHit(ctx context.Context, r *store.Repo, itemID, stage, key string) (store.StageRun, bool, error) {
	hit, err := r.DoneStageByCacheKey(ctx, itemID, stage, key)
	if errors.Is(err, store.ErrNotFound) {
		return store.StageRun{}, false, nil
	}
	if err != nil {
		return store.StageRun{}, false, err
	}
	if hit.ArtifactID == "" {
		return hit, true, nil
	}
	a, err := r.Artifact(ctx, hit.ArtifactID)
	if errors.Is(err, store.ErrNotFound) {
		return store.StageRun{}, false, nil // 已被清除（PIPE-R7）：沒有東西可以重用
	}
	if err != nil {
		return store.StageRun{}, false, err
	}
	switch err := verifyArtifactFile(p.dataDir, a); {
	case errors.Is(err, ErrCorruptArtifact):
		p.log.Warn("快取命中的產出檔已損毀，重新產生", "item", itemID, "stage", stage, "artifact", a.ID, "error", err)
		if err := p.st.Tx(ctx, func(tx *store.Repo) error { _, err := tx.MarkArtifactCorrupt(ctx, a.ID); return err }); err != nil {
			return store.StageRun{}, false, err
		}
		return store.StageRun{}, false, nil
	case err != nil:
		return store.StageRun{}, false, err
	}
	return hit, true, nil
}

// producerStage 是產生這份產出的步驟（來源紀錄的 stage）；匯入、編輯（stage 不是 pipeline 的步驟）
// 與從 bundle、Hub 播種的（seeded）產出沒有可以重跑的步驟，回傳空字串。
func producerStage(a store.Artifact) string {
	var prov struct {
		Stage  string `json:"stage"`
		Seeded bool   `json:"seeded"`
	}
	if json.Unmarshal(a.Provenance, &prov) != nil || prov.Seeded || !ValidStage(prov.Stage) {
		return ""
	}
	return prov.Stage
}

// handleCorrupt 處理讀到的損毀產出：標記損毀；這次才標記、而且它仍是現行版本、由步驟產生時，
// 從那個步驟重新處理影音（沿用影音的 until；設定依影音目前的處理方案與覆寫，和使用者手動重跑相同）。
// 回傳 *CorruptArtifactError 給呼叫端（API 回 409 artifact_corrupt；步驟以 permanent 結束，重跑後它已被取代、結果會被丟棄）。
// 重跑失敗時只記 log：產出已標記，之後手動重跑不會再命中這份快取。
func (p *Pipeline) handleCorrupt(ctx context.Context, a store.Artifact) error {
	ce := &CorruptArtifactError{ArtifactID: a.ID}
	var marked bool
	if err := p.st.Tx(ctx, func(r *store.Repo) error {
		var err error
		marked, err = r.MarkArtifactCorrupt(ctx, a.ID)
		return err
	}); err != nil {
		return err
	}
	stage := producerStage(a)
	if stage == "" {
		p.log.Error("產出檔已損毀，無法自動重新產生", "item", a.ItemID, "artifact", a.ID, "path", a.Path)
		return ce
	}
	ce.Stage = stage
	if !marked || a.SupersededBy != "" {
		return ce
	}
	it, err := p.st.Read().ItemByID(ctx, a.ItemID)
	if err != nil {
		return err
	}
	p.log.Warn("產出檔已損毀，從產生它的步驟重新處理", "item", a.ItemID, "artifact", a.ID, "stage", stage)
	if _, err := p.Rerun(ctx, a.ItemID, RerunRequest{From: stage, Until: it.UntilStage}); err != nil {
		p.log.Error("重新處理損毀的產出失敗", "item", a.ItemID, "stage", stage, "error", err)
		return ce
	}
	ce.Rerun = true
	return ce
}

// readTranscriptArtifact 讀取字幕產出並驗證 sha256；損毀時交給 handleCorrupt（標記並重跑產生它的步驟）。
func (p *Pipeline) readTranscriptArtifact(ctx context.Context, a store.Artifact) (subtitle.Transcript, error) {
	tr, err := readTranscript(p.dataDir, a.Path, a.SHA256)
	if errors.Is(err, ErrCorruptArtifact) || errors.Is(err, os.ErrNotExist) {
		return tr, p.handleCorrupt(ctx, a)
	}
	return tr, err
}
