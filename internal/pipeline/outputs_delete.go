package pipeline

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是刪除影音時輸出檔的處理（DEL-R3、R9）：預設保留；指定 delete_outputs 時只刪除
// 「目前內容的 sha256 與 outputs 紀錄相同」的檔案，其餘保留並列出原因。

// 保留輸出檔的原因（KeptOutput.Reason）。
const (
	// KeptModified 是內容和紀錄不同（使用者改過），或不是一般檔案（symlink、目錄）。
	KeptModified = "modified"
	// KeptOutsideRoots 是紀錄的路徑現在不在允許的目錄內（設定改過）：不碰。
	KeptOutsideRoots = "outside_roots"
	// KeptUnreadable 是檢查或刪除時失敗（權限等）：保留。
	KeptUnreadable = "unreadable"
)

// outputStatus 是一筆輸出紀錄對應的檔案現況。
type outputStatus struct {
	Path string
	// State 是 intact（沒被改過）、modified 或 missing；Reason 非空代表無法判斷而保留（State 此時是 modified）。
	State  output.FileState
	Reason string
}

// keepReason 回傳保留的原因；沒被改過或已經不存在時回傳空字串。
func (s outputStatus) keepReason() string {
	if s.Reason != "" {
		return s.Reason
	}
	if s.State == output.StateModified {
		return KeptModified
	}
	return ""
}

// outputStatuses 檢查影音的每一筆輸出紀錄；remove 為 true 時順便刪除沒被改過的檔案。
// 檔案的資料夾先過路徑防護（serve 只有 inbox 與 media_roots），再用 os.Root 限制在該資料夾內操作、不跟隨 symlink。
func (p *Pipeline) outputStatuses(ctx context.Context, recs []store.Output, remove bool) []outputStatus {
	out := make([]outputStatus, 0, len(recs))
	for _, rec := range recs {
		st := outputStatus{Path: rec.Path}
		dir, err := p.files.Resolve(filepath.Dir(rec.Path))
		switch {
		case errors.Is(err, pathguard.ErrOutsideRoots):
			st.State, st.Reason = output.StateModified, KeptOutsideRoots
		case err != nil:
			p.log.Warn("解析輸出檔的資料夾失敗", "path", rec.Path, "error", err)
			st.State, st.Reason = output.StateModified, KeptUnreadable
		default:
			name := filepath.Base(rec.Path)
			if remove {
				st.State, err = output.Remove(dir, name, rec.SHA256)
			} else {
				st.State, err = output.Inspect(dir, name, rec.SHA256)
			}
			if err != nil {
				p.log.Warn("檢查或刪除輸出檔失敗", "path", rec.Path, "error", err)
				st.State, st.Reason = output.StateModified, KeptUnreadable
			}
		}
		out = append(out, st)
	}
	return out
}

// deleteOutputs 刪除影音沒被改過的輸出檔（DEL-R9），回傳保留下來的檔案。紀錄本身不動：
// 移到垃圾桶時保留，還原後重新發布會更新；永久刪除時隨影音 cascade。已經不存在的檔案略過。
func (p *Pipeline) deleteOutputs(ctx context.Context, itemID string) ([]KeptOutput, error) {
	recs, err := p.st.Read().Outputs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	kept := []KeptOutput{}
	for _, st := range p.outputStatuses(ctx, recs, true) {
		if reason := st.keepReason(); reason != "" {
			kept = append(kept, KeptOutput{Path: st.Path, Reason: reason})
		}
	}
	return kept, nil
}

// previewOutputs 回傳刪除預覽的輸出檔清單：會被刪除的（modified 為 false）與會保留的（modified 為 true）；
// 已經不存在的檔案不列。唯讀。
func (p *Pipeline) previewOutputs(ctx context.Context, itemID string) ([]PreviewOutput, error) {
	recs, err := p.st.Read().Outputs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	list := []PreviewOutput{}
	for _, st := range p.outputStatuses(ctx, recs, false) {
		if st.State == output.StateMissing {
			continue
		}
		list = append(list, PreviewOutput{Path: st.Path, Modified: st.keepReason() != "", Reason: st.keepReason()})
	}
	return list, nil
}

// republishMissingOutputs 在還原之後把被刪除的輸出檔寫回（DEL-R9）：有輸出紀錄的檔案已經不存在時，
// 從 publish 重跑；publish 依目前的設定重新渲染，不呼叫 LLM。失敗只記 log，不影響還原。
func (p *Pipeline) republishMissingOutputs(ctx context.Context, itemID string) {
	recs, err := p.st.Read().Outputs(ctx, itemID)
	if err != nil || len(recs) == 0 {
		return
	}
	missing := false
	for _, st := range p.outputStatuses(ctx, recs, false) {
		if st.State == output.StateMissing {
			missing = true
			break
		}
	}
	if !missing {
		return
	}
	if _, err := p.Rerun(ctx, itemID, RerunRequest{From: StagePublish}); err != nil {
		p.log.Warn("還原後重新發布輸出檔失敗", "item", itemID, "error", err)
	}
}
