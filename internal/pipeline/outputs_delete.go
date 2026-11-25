package pipeline

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/vault"
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
	// KeptVaultMissing 是紀錄的 vault 已經不在設定檔中：不知道它的根目錄，不碰。
	KeptVaultMissing = "vault_not_configured"
	// git vault 的刪除（VAULT-R12）沒有完成時的原因，分類同 VAULT-R9。同步（pull）就失敗時 clone 中的檔案
	// 都沒有動；推送失敗時刪除已在本機 commit，下一次發布到同一個 vault 時一併推送（同 VAULT-R9 的重試）。
	// KeptGitUnreachable 是連線失敗、逾時、遠端 5xx。
	KeptGitUnreachable = "git_unreachable"
	// KeptGitRejected 是遠端有本機沒有的提交（non-fast-forward、無法快轉）。
	KeptGitRejected = "git_rejected"
	// KeptGitAuth 是認證失敗。
	KeptGitAuth = "git_auth"
	// KeptGitFailed 是其他失敗（找不到 git、clone 無法使用、commit 失敗等）。
	KeptGitFailed = "git_failed"
)

// gitDeleteTimeout 是一個 git vault 刪除的整體期限（含等待同一個 vault 的發布）。刪除在 DELETE 請求中同步執行，
// 不受請求 ctx 取消影響（已經 commit 移到垃圾桶）。
const gitDeleteTimeout = 3 * time.Minute

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
		if rec.Target == output.TargetVault {
			out = append(out, p.vaultOutputStatus(ctx, rec, remove))
			continue
		}
		st := outputStatus{Path: rec.Path}
		dir, err := p.files.Resolve(filepath.Dir(rec.Path))
		switch {
		case errors.Is(err, pathguard.ErrOutsideRoots):
			st.State, st.Reason = output.StateModified, KeptOutsideRoots
		case err != nil:
			p.log.WarnContext(ctx, "解析輸出檔的資料夾失敗", "path", rec.Path, "error", err)
			st.State, st.Reason = output.StateModified, KeptUnreadable
		default:
			name := filepath.Base(rec.Path)
			if remove {
				st.State, err = output.Remove(dir, name, rec.SHA256)
			} else {
				st.State, err = output.Inspect(dir, name, rec.SHA256)
			}
			if err != nil {
				p.log.WarnContext(ctx, "檢查或刪除輸出檔失敗", "path", rec.Path, "error", err)
				st.State, st.Reason = output.StateModified, KeptUnreadable
			}
		}
		out = append(out, st)
	}
	return out
}

// deleteOutputs 刪除影音沒被改過的輸出檔（DEL-R9），回傳保留下來的檔案。紀錄本身不動：
// 移到垃圾桶時保留，還原後重新發布會更新；永久刪除時隨影音 cascade。已經不存在的檔案略過。
// git vault 的檔案依 vault 分組，每個 vault 以一個 commit 刪除並推送（VAULT-R12）。
func (p *Pipeline) deleteOutputs(ctx context.Context, it store.Item) ([]KeptOutput, error) {
	recs, err := p.st.Read().Outputs(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	var plain []store.Output
	git := map[string][]store.Output{}
	for _, rec := range recs {
		if def, ok := p.vaults[rec.Vault]; ok && rec.Target == output.TargetVault && def.Adapter == vault.AdapterGit {
			git[def.ID] = append(git[def.ID], rec)
			continue
		}
		plain = append(plain, rec)
	}
	kept := []KeptOutput{}
	for _, st := range p.outputStatuses(ctx, plain, true) {
		if reason := st.keepReason(); reason != "" {
			kept = append(kept, KeptOutput{Path: st.Path, Reason: reason})
		}
	}
	ids := make([]string, 0, len(git))
	for id := range git {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		kept = append(kept, p.deleteGitVaultOutputs(ctx, it, p.vaults[id], git[id])...)
	}
	return kept, nil
}

// deleteGitVaultOutputs 從 git vault 刪除這部影音沒被改過的檔案（VAULT-R12、DEL-R9）：取得 vault 的鎖、
// 先 pull --ff-only（遠端有使用者改過的版本時 hash 不同，檔案保留），在 clone 中只刪 hash 與紀錄相同的檔案，
// 以一個 commit 提交（訊息「notesteme: 刪除 <標題>」），再推送。回傳保留的檔案。
//
// 失敗時不回滾（clone 中可能有使用者還沒提交的檔案，VAULT-R7 不自動 reset）：同步失敗時什麼都沒動，
// 全部保留；commit 或推送失敗時這次刪除的檔案也列為保留（遠端仍有它們），原因依 VAULT-R9 分類。
func (p *Pipeline) deleteGitVaultOutputs(ctx context.Context, it store.Item, def vault.Def, recs []store.Output) []KeptOutput {
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gitDeleteTimeout)
	defer cancel()
	keepAll := func(list []store.Output, reason string) []KeptOutput {
		out := make([]KeptOutput, 0, len(list))
		for _, rec := range list {
			out = append(out, KeptOutput{Path: rec.Path, Reason: reason})
		}
		return out
	}
	unlock, err := p.vaultLocks.lock(gctx, def.ID)
	if err != nil {
		p.log.WarnContext(ctx, "等待 vault 的發布結束時逾時，保留 vault 中的檔案", "vault", def.ID, "item", it.ID, "error", err)
		return keepAll(recs, KeptGitUnreachable)
	}
	defer unlock()
	g, cleanup, err := p.newGit(def)
	if err != nil {
		p.log.WarnContext(ctx, "無法執行 git，保留 vault 中的檔案", "vault", def.ID, "item", it.ID, "error", err)
		return keepAll(recs, KeptGitFailed)
	}
	defer cleanup()
	dir := p.vaultRoot(def)
	if err := g.Sync(gctx, def, dir); err != nil {
		p.log.WarnContext(ctx, "同步 git vault 失敗，保留 vault 中的檔案", "vault", def.ID, "item", it.ID, "error", err)
		return keepAll(recs, gitKeepReason(err))
	}
	var kept []KeptOutput
	var removed []store.Output
	for _, rec := range recs {
		state, reason := p.inspectVaultFile(ctx, dir, rec, true)
		switch {
		case reason != "":
			kept = append(kept, KeptOutput{Path: rec.Path, Reason: reason})
		case state == output.StateModified:
			kept = append(kept, KeptOutput{Path: rec.Path, Reason: KeptModified})
		case state == output.StateIntact:
			removed = append(removed, rec)
		}
	}
	paths := make([]string, 0, len(removed))
	for _, rec := range removed {
		paths = append(paths, rec.Path)
	}
	if _, err := g.CommitRemoval(gctx, dir, paths, "notesteme: 刪除 "+it.Title, def.Author); err != nil {
		p.log.WarnContext(ctx, "提交 git vault 的刪除失敗", "vault", def.ID, "item", it.ID, "error", err)
		return append(kept, keepAll(removed, gitKeepReason(err))...)
	}
	// 本機領先遠端就推送：也會推送之前推送失敗、留在本機的 commit（VAULT-R9）。
	ahead, err := g.Ahead(gctx, dir, def.Branch)
	if err == nil && ahead {
		err = g.Push(gctx, dir, def.Branch)
	}
	if err != nil {
		p.log.WarnContext(ctx, "推送 git vault 的刪除失敗，刪除留在本機的 commit，下一次發布時推送", "vault", def.ID, "item", it.ID, "error", err)
		return append(kept, keepAll(removed, gitKeepReason(err))...)
	}
	return kept
}

// gitKeepReason 把 git 的失敗分類成保留的原因（VAULT-R9）。
func gitKeepReason(err error) string {
	var ge *vault.GitError
	if !errors.As(err, &ge) {
		return KeptGitFailed
	}
	switch ge.Kind {
	case vault.KindNetwork:
		return KeptGitUnreachable
	case vault.KindRejected:
		return KeptGitRejected
	case vault.KindAuth:
		return KeptGitAuth
	}
	return KeptGitFailed
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
		p.log.WarnContext(ctx, "還原後重新發布輸出檔失敗", "item", itemID, "error", err)
	}
}

// vaultOutputStatus 檢查（remove 時刪除）vault 中的一個檔案（VAULT-R12、DEL-R9）：路徑相對於設定檔定義的 vault 根目錄，
// 一律在 os.Root 之內操作，不跟隨指向 vault 以外的連結。
func (p *Pipeline) vaultOutputStatus(ctx context.Context, rec store.Output, remove bool) outputStatus {
	st := outputStatus{Path: rec.Path}
	def, ok := p.vaults[rec.Vault]
	switch {
	case !ok:
		st.State, st.Reason = output.StateModified, KeptVaultMissing
		return st
	case def.Adapter != vault.AdapterFS:
		// git vault 的刪除要 commit 與推送，由 deleteGitVaultOutputs 處理；這裡只檢查 clone 中的檔案（預覽、還原）。
		st.State, st.Reason = p.inspectVaultFile(ctx, p.vaultRoot(def), rec, false)
		return st
	}
	st.State, st.Reason = p.inspectVaultFile(ctx, def.Path, rec, remove)
	return st
}

// inspectVaultFile 在 vault 根目錄 dir 中檢查（或刪除）紀錄的檔案；無法判斷時回傳保留的原因。
func (p *Pipeline) inspectVaultFile(ctx context.Context, dir string, rec store.Output, remove bool) (output.FileState, string) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return output.StateMissing, ""
	}
	if err != nil {
		p.log.WarnContext(ctx, "開啟 vault 失敗", "vault", rec.Vault, "error", err)
		return output.StateModified, KeptUnreadable
	}
	defer root.Close()
	if err := vault.CheckInside(root, rec.Path); err != nil {
		p.log.WarnContext(ctx, "vault 的輸出檔經過連結", "vault", rec.Vault, "path", rec.Path, "error", err)
		return output.StateModified, KeptModified
	}
	state, err := output.InspectIn(root, rec.Path, rec.SHA256, remove)
	if err != nil {
		p.log.WarnContext(ctx, "檢查或刪除 vault 的輸出檔失敗", "vault", rec.Vault, "path", rec.Path, "error", err)
		return output.StateModified, KeptUnreadable
	}
	return state, ""
}
