package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/vault"
)

// 這個檔案是 vault 輸出目標的寫出（VAULT-R2～R6）：筆記、筆記引用的畫面與字幕寫進設定檔定義的 vault，
// 每個檔案各自經過 hash guard（output.Writer），路徑限制在 vault 根目錄內（os.Root）。

// vault 發布的訊息與動作。
const (
	msgVaultNoNote      = "這部影音還沒有筆記，只寫出字幕。"
	msgVaultNoSubtitles = "沒有字幕，未寫出字幕檔。"
	msgVaultStaleLink   = "筆記中的字幕連結可能不正確，請從 note 重新處理。"
	msgVaultNoteChecks  = "筆記沒有通過自動檢查，沒有寫入 vault。請重新產生筆記，或修改模板的檢查之後重新處理。"
)

// vaultOutcome 是一個 vault 目標的發布結果。
type vaultOutcome struct {
	def vault.Def
	// root 是 vault 的根目錄（fs 是設定的 path，git 是 clone）。
	root string
	// note 是筆記的絕對路徑（hook 的 {{note_path}}）；沒有寫筆記時是空字串。
	note string
	// blocked 是被使用者改過、依 skip 沒有寫入的檔案（相對路徑）。
	blocked []string
	// changed 是這次寫入或另存的檔案（相對路徑；git 只 add 這些，VAULT-R7）。
	changed []string
	// recorded 是這次有輸出紀錄的檔案（含 unchanged；git commit 要回填）。
	recorded []string
	// missing 是沒有字幕的語言。
	missing []string
	// messages 是要顯示在步驟上的提醒（沒有筆記、沒有字幕、連結可能過時）。
	messages []string
	// cfg 是這個輸出目標的設定（hook 用）；commit 是 git vault 的 HEAD（hook 的 {{commit}}）。
	cfg    output.Config
	commit string
}

// vaultItem 是決定版面用的影音資訊；日期（VAULT-R2）：平台的發布日，沒有時是影音的新增日（UTC）。
func vaultItem(it store.Item) vault.Item {
	d := it.CreatedAt
	if it.PublishedAt != nil {
		d = *it.PublishedAt
	}
	return vault.Item{Platform: it.Platform, Title: it.Title, Channel: itemChannel(it), Date: d}
}

// itemChannel 是中繼資料中的頻道名稱；沒有時是空字串。
func itemChannel(it store.Item) string {
	var md struct {
		Channel string `json:"channel"`
	}
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md) // 格式不對時當成沒有頻道
	}
	return md.Channel
}

// firstVaultOutput 回傳生效設定中第一個 vault 輸出與它的定義（VAULT V8）；沒有時 ok 為 false。
func (p *Pipeline) firstVaultOutput(eff Effective) (output.Config, vault.Def, bool) {
	raw, err := eff.Outputs()
	if err != nil {
		return output.Config{}, vault.Def{}, false
	}
	for _, m := range raw {
		c, err := output.Parse(m)
		if err != nil || c.Target != output.TargetVault {
			continue
		}
		def, ok := p.vaults[c.Vault]
		return c, def, ok
	}
	return output.Config{}, vault.Def{}, false
}

// vaultSubtitleLink 是筆記資訊表「字幕」列的 vault 路徑（VAULT-R2、V8）：依第一個 vault 輸出的版面，連到 SRT
// （formats 沒有 srt 時連到第一個格式）；沒有 vault 輸出、沒有字幕格式或版面無法決定時是空字串。
func (p *Pipeline) vaultSubtitleLink(it store.Item, eff Effective) string {
	cfg, def, ok := p.firstVaultOutput(eff)
	if !ok {
		return ""
	}
	return p.vaultSubtitleLinkFor(it, cfg, def)
}

// vaultError 把寫入 vault 時的錯誤分類：連結逃逸是 permanent（VAULT-R4），影音已刪除是 errItemGone。
func vaultError(rel string, err error) error {
	var esc *vault.EscapeError
	switch {
	case errors.As(err, &esc):
	case vault.IsEscape(err):
		esc = &vault.EscapeError{Path: rel}
	case errors.Is(err, store.ErrNotFound):
		return errItemGone
	default:
		return fmt.Errorf("寫入 vault 的 %s：%w", rel, err)
	}
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: esc.Error(), Message: esc.Error()}
}

// vaultWriter 寫出一個 vault 的檔案並更新紀錄的狀態。
type vaultWriter struct {
	p    *Pipeline
	w    *output.Writer
	it   store.Item
	cfg  output.Config
	def  vault.Def
	root *os.Root
	out  *vaultOutcome
}

func (vw *vaultWriter) write(ctx context.Context, rel, kind, format, lang string, data []byte) error {
	if err := vault.CheckInside(vw.root, rel); err != nil {
		return vaultError(rel, err)
	}
	res, err := vw.w.Write(ctx, output.File{
		ItemID: vw.it.ID, Root: vw.root, Dir: path.Dir(rel), Name: path.Base(rel), Data: data,
		Target: output.TargetVault, Format: format, Lang: lang, Policy: vw.cfg.Policy(), Kind: kind, Vault: vw.def.ID,
	})
	if err != nil {
		return vaultError(rel, err)
	}
	state := ""
	switch res.Outcome {
	case output.Blocked:
		vw.out.blocked = append(vw.out.blocked, res.Path)
		state = store.OutputStateUserChanged
	case output.Unchanged:
		vw.out.recorded = append(vw.out.recorded, res.Path)
		state = store.OutputStateUnchanged
	default:
		vw.out.changed = append(vw.out.changed, res.Path)
		vw.out.recorded = append(vw.out.recorded, res.Path)
	}
	if state != "" {
		// 被擋下的檔案保留原本的 sha256（下次才認得使用者改過）；只更新狀態。
		if err := vw.p.st.Write().SetOutputState(ctx, vw.it.ID, res.Path, state); err != nil {
			return err
		}
	}
	if kind == store.OutputKindNote && res.Outcome != output.Blocked {
		vw.out.note = filepath.Join(vw.out.root, filepath.FromSlash(res.Path))
	}
	return nil
}

// noteChecksPassed 讀取筆記來源紀錄的 checks.passed（NOTE N21）；沒有記錄時依產出中的檢查結果判斷。
func noteChecksPassed(art store.Artifact, doc noteDoc) bool {
	var prov struct {
		Checks *struct {
			Passed bool `json:"passed"`
		} `json:"checks"`
	}
	if json.Unmarshal(art.Provenance, &prov) == nil && prov.Checks != nil {
		return prov.Checks.Passed
	}
	return notes.Passed(doc.Checks)
}

// publishVault 把一個 vault 目標寫進 root（fs vault 的 path，或 git vault 的 clone）。
func (p *Pipeline) publishVault(ctx context.Context, it store.Item, cfg output.Config, def vault.Def, rootDir string, w *output.Writer) (*vaultOutcome, error) {
	out := &vaultOutcome{def: def, root: rootDir, cfg: cfg}
	art, doc, err := p.currentNote(ctx, it.ID, "")
	hasNote := err == nil
	switch {
	case errors.Is(err, ErrNoNote):
		out.messages = append(out.messages, msgVaultNoNote)
	case err != nil:
		return nil, err
	case !noteChecksPassed(art, doc):
		// NOTE-R7：沒有通過自動檢查的筆記不寫入 vault。
		return nil, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "publish", Detail: "筆記沒有通過自動檢查",
			Message: msgVaultNoteChecks, Actions: []string{ActionRegenerateNote},
		}
	}

	layout, err := vault.Plan(def.Base, cfg.Folder, vaultItem(it))
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: err.Error(),
			Message: "vault 的資料夾路徑太長，請縮短 base 或 folder。"}
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("建立 vault 的根目錄：%w", err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("開啟 vault 的根目錄：%w", err)
	}
	defer root.Close()
	vw := &vaultWriter{p: p, w: w, it: it, cfg: cfg, def: def, root: root, out: out}

	if hasNote {
		if err := vw.write(ctx, layout.Note, store.OutputKindNote, "md", doc.Lang, []byte(doc.Markdown)); err != nil {
			return nil, err
		}
		if err := p.writeVaultImages(ctx, vw, layout, art, doc); err != nil {
			return nil, err
		}
		if link := doc.Render.SubtitleLink; link != "" && link != p.vaultSubtitleLinkFor(it, cfg, def) {
			out.messages = append(out.messages, msgVaultStaleLink)
		}
	}

	// 字幕（VAULT-R5）：formats 預設 [srt]，`formats: []` 時不寫。
	multi := len(cfg.Lang) > 1
	noSubs := false
	for _, lang := range langsOf(cfg) {
		for _, format := range cfg.VaultFormats() {
			data, tr, err := p.renderOutput(ctx, it.ID, format, lang, cfg.Style)
			if errors.Is(err, ErrNoTranscript) {
				if lang == "" {
					noSubs = true
				} else if !contains(out.missing, lang) {
					out.missing = append(out.missing, lang)
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := vw.write(ctx, layout.Subtitle(format, lang, multi), store.OutputKindSubtitle, format, tr.Lang, data); err != nil {
				return nil, err
			}
		}
	}
	if noSubs {
		out.messages = append(out.messages, msgVaultNoSubtitles)
	}
	return out, nil
}

// vaultSubtitleLinkFor 同 vaultSubtitleLink，但用指定的輸出目標。
func (p *Pipeline) vaultSubtitleLinkFor(it store.Item, cfg output.Config, def vault.Def) string {
	formats := cfg.VaultFormats()
	if len(formats) == 0 {
		return ""
	}
	format := formats[0]
	for _, f := range formats {
		if f == subtitle.FormatSRT {
			format = f
		}
	}
	l, err := vault.Plan(def.Base, cfg.Folder, vaultItem(it))
	if err != nil {
		return ""
	}
	lang := ""
	if len(cfg.Lang) > 0 {
		lang = cfg.Lang[0]
	}
	return l.Subtitle(format, lang, len(cfg.Lang) > 1)
}

// writeVaultImages 寫出筆記嵌入的畫面（VAULT-R5、NOTE-R9），並把筆記與它引用的 frames 版本標成 pinned（ADR-0020 第 4 點）。
func (p *Pipeline) writeVaultImages(ctx context.Context, vw *vaultWriter, layout vault.Layout, note store.Artifact, doc noteDoc) error {
	r := p.st.Read()
	var frames *store.Artifact
	for _, id := range note.Parents {
		a, err := r.Artifact(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if a.Kind == KindFrames {
			frames = &a
			break
		}
	}
	if len(doc.Images) > 0 && frames == nil {
		return errPermanent("找不到筆記引用的關鍵畫面，請從 frames 重新處理。")
	}
	for _, img := range doc.Images {
		data, err := os.ReadFile(filepath.Join(framesDir(p.dataDir, note.ItemID, frames.ID), frameFile(img.N)))
		if err != nil {
			return errPermanent(fmt.Sprintf("讀取第 %d 張畫面失敗：%v", img.N, err))
		}
		if err := vw.write(ctx, path.Join(layout.Assets, vault.Segment(img.File)), store.OutputKindAsset, "jpg", "", data); err != nil {
			return err
		}
	}
	return p.st.Tx(ctx, func(tx *store.Repo) error {
		if err := tx.PinArtifact(ctx, note.ID); err != nil {
			return err
		}
		if frames != nil {
			return tx.PinArtifact(ctx, frames.ID)
		}
		return nil
	})
}

// vault 的 git 與 hook 的訊息與動作（VAULT-R9、R11）。
const (
	actionRepublishVault  = "重新發布"
	actionUpdateVaultCred = "更新憑證"
)

// publishVaultTarget 寫出一個 vault 目標（VAULT-R1、R5、R7）：fs 直接寫進設定的 path；git 先同步 clone，
// 寫入後只 add 這次寫入或另存的檔案、有變更才 commit，本機有還沒推送的 commit 時 push。
// 同一個 vault 的發布互斥（同一個 clone 不能同時有兩個 git 操作）。
func (p *Pipeline) publishVaultTarget(ctx context.Context, it store.Item, cfg output.Config, w *output.Writer) (*vaultOutcome, error) {
	def, ok := p.vaults[cfg.Vault]
	if !ok {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: "沒有這個 vault " + cfg.Vault,
			Message: fmt.Sprintf("沒有這個 vault：%s。vault 只能在設定檔定義。", cfg.Vault)}
	}
	unlock, err := p.vaultLocks.lock(ctx, def.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if def.Adapter != vault.AdapterGit {
		return p.publishVault(ctx, it, cfg, def, def.Path, w)
	}

	g, cleanup, err := p.newGit(def)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	dir := p.vaultRoot(def)
	if err := g.Sync(ctx, def, dir); err != nil {
		return nil, gitError(def, err)
	}
	out, err := p.publishVault(ctx, it, cfg, def, dir, w)
	if err != nil {
		return nil, err
	}
	// 被擋下的檔案以外，這次寫入或另存的檔案照樣提交（VAULT V7：已寫入的檔案保留）。
	commit, err := g.Commit(ctx, dir, out.changed, "notesteme: "+it.Title, def.Author)
	if err != nil {
		return nil, gitError(def, err)
	}
	if commit != "" {
		if err := p.st.Tx(ctx, func(r *store.Repo) error {
			for _, path := range out.changed {
				if err := r.SetOutputCommit(ctx, it.ID, path, commit); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	// 重試時沿用上一次已經建立的本機 commit，不重複 commit（VAULT-R9）：只要本機領先遠端就推送。
	ahead, err := g.Ahead(ctx, dir, def.Branch)
	if err != nil {
		return nil, gitError(def, err)
	}
	if ahead {
		if err := g.Push(ctx, dir, def.Branch); err != nil {
			return nil, gitError(def, err)
		}
	}
	if out.commit, err = g.Head(ctx, dir); err != nil {
		return nil, gitError(def, err)
	}
	return out, nil
}

// newGit 準備一次 git 操作：git 的位置、任務的暫存目錄（HOME 與 askpass）與憑證（NOTESTEME_VAULT_<ID>_TOKEN）。
func (p *Pipeline) newGit(def vault.Def) (*vault.Git, func(), error) {
	if p.runner == nil {
		return nil, nil, errPermanent("沒有可以執行 git 的環境。")
	}
	path, err := toolexec.LookPath("git", p.env["PATH"])
	if err != nil {
		return nil, nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: err.Error(),
			Message: "找不到 git，無法使用 git vault。請在執行環境安裝 git。"}
	}
	home, cleanup, err := p.taskTempDir("vault-git-")
	if err != nil {
		return nil, nil, err
	}
	return &vault.Git{Runner: p.runner, Path: path, Vars: p.env, Home: home, Token: p.env[vault.TokenEnvVar(def.ID)], Remote: def.Remote}, cleanup, nil
}

// taskTempDir 在 <data>/tmp 建立一個任務專用的暫存目錄（權限 0700），回傳刪除它的函式。
func (p *Pipeline) taskTempDir(prefix string) (string, func(), error) {
	base := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// gitError 把 git 的失敗分類成 publish 的錯誤（VAULT-R9）。
func gitError(def vault.Def, err error) error {
	var ge *vault.GitError
	if !errors.As(err, &ge) {
		return &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: err.Error(),
			Message: fmt.Sprintf("vault %s 的 git repo 無法使用：%v", def.ID, err)}
	}
	switch ge.Kind {
	case vault.KindNetwork:
		return &errclass.Error{Class: errclass.ClassRetryable, Source: "publish", Detail: ge.Error(),
			Message: fmt.Sprintf("無法連線到 vault %s 的遠端，稍後會重試。", def.ID)}
	case vault.KindRejected:
		return &errclass.Error{Class: errclass.ClassNeedsAction, Source: "publish", Detail: ge.Error(),
			Message: fmt.Sprintf("vault 的遠端分支 %s 有本機沒有的提交，Notesteme 不會自動合併。請先同步 vault repo 再重新發布。", def.Branch),
			Actions: []string{actionRepublishVault}}
	case vault.KindAuth:
		return &errclass.Error{Class: errclass.ClassNeedsAction, Source: "publish", Detail: ge.Error(),
			Message: fmt.Sprintf("無法登入 vault 的遠端（認證失敗）。請更新 vault %s 的憑證。", def.ID),
			Actions: []string{actionUpdateVaultCred, actionRepublishVault}}
	}
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: ge.Error(),
		Message: fmt.Sprintf("vault %s 的 git %s 失敗。", def.ID, ge.Op)}
}

// runVaultHooks 在所有檔案寫入（與 push）成功之後執行每個列出的 hook 一次（VAULT-R11）。
// hook 失敗或逾時不影響已寫入的檔案，回傳要顯示的提醒。
func (p *Pipeline) runVaultHooks(ctx context.Context, it store.Item, cfg output.Config, out *vaultOutcome) ([]string, error) {
	var msgs []string
	for _, id := range cfg.Hooks {
		h, ok := p.hooks[id]
		if !ok {
			return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: "沒有這個 hook " + id,
				Message: fmt.Sprintf("沒有這個 hook：%s。hook 只能在設定檔定義。", id)}
		}
		if p.runner == nil {
			return nil, errPermanent("沒有可以執行 hook 的環境。")
		}
		dir, cleanup, err := p.taskTempDir("hook-")
		if err != nil {
			return nil, err
		}
		res := vault.RunHook(ctx, p.runner, h, vault.HookValues{
			NotePath: out.note, VaultRoot: out.root, ItemID: it.ID, Title: it.Title, Commit: out.commit,
		}, p.env, dir)
		cleanup()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		p.log.InfoContext(ctx, "hook 執行結束", "hook", id, "item", it.ID, "exit", res.ExitCode, "timed_out", res.TimedOut,
			"truncated", res.Truncated, "output", string(res.Output))
		if res.Err != nil {
			p.log.WarnContext(ctx, "hook 無法執行", "hook", id, "error", res.Err)
		}
		if m := res.Message(h); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// vaultRoot 是 vault 的根目錄：fs 是設定的 path，git 是 <data>/vault-repo/<id> 的 clone（VAULT-R1）。
func (p *Pipeline) vaultRoot(def vault.Def) string {
	if def.Adapter == vault.AdapterGit {
		return filepath.Join(p.dataDir, "vault-repo", def.ID)
	}
	return def.Path
}

// Vaults 回傳設定檔定義的 vault，依 id 排列（GET /api/vaults，VAULT-R1）。
func (p *Pipeline) Vaults() []vault.Def {
	out := make([]vault.Def, 0, len(p.vaults))
	for _, v := range p.vaults {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b vault.Def) int { return strings.Compare(a.ID, b.ID) })
	return out
}
