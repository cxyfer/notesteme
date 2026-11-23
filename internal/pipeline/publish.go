package pipeline

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// SkipNotApplicable：沒有任何輸出目標適用這部影音（例如 sidecar 只用於本機檔案，OUT-R10）。
const SkipNotApplicable = "not_applicable"

// 發布步驟的訊息與修正動作。
const (
	msgPublishBlocked = "輸出檔已被修改，依設定（on_user_change: skip）沒有寫入："
	actionRepublish   = "處理該檔案後重新發布"
	msgPublishNoLang  = "沒有這些語言的字幕，未輸出："
)

// publishDir 是一個輸出目標的資料夾，以及檔名的前綴（sidecar 是影片檔名；folder 沒有，改用影音標題）。
type publishDir struct {
	dir string // 已解析、通過路徑防護的資料夾
	// stem 是 sidecar 的檔名前綴（影片檔名去掉副檔名）；folder 為空，改用影音標題。
	stem string
}

// runPublish 把字幕寫到輸出目標（OUT-R10～R12、ADR-0010）。
// 每個目標 × 語言 × 格式寫一個檔案，寫完立刻記錄 sha256；目標檔案被改過時依 on_user_change 另存新檔或擋下。
// 沒有任何輸出目標適用這部影音（sidecar 對線上影音）時，步驟是 skipped（not_applicable）。
func (p *Pipeline) runPublish(ctx context.Context, it store.Item, eff Effective) (result, error) {
	raw, err := eff.Outputs()
	if err != nil {
		return result{}, errPermanent(err.Error())
	}
	cfgs := make([]output.Config, 0, len(raw))
	for i, m := range raw {
		c, err := output.Parse(m)
		if err != nil {
			return result{}, errPermanent(fmt.Sprintf("outputs 第 %d 項：%v", i+1, err))
		}
		cfgs = append(cfgs, c)
	}
	impl := p.implVersion(StagePublish)
	stage := &store.StageResult{Executor: ExecutorLocal, ImplVersion: impl}
	if len(cfgs) == 0 {
		return result{task: taskDone, stage: stage, state: skippedState(SkipNoOutputs)}, nil
	}

	w := &output.Writer{Records: output.StoreRecords{St: p.st}, Now: p.clock.Now}
	var (
		applicable int
		blocked    []string
		missing    []string
		dirs       = map[string]publishDir{}
	)
	var vaults []*vaultOutcome
	for _, cfg := range cfgs {
		if cfg.Target == output.TargetVault {
			vo, err := p.publishVaultTarget(ctx, it, cfg, w)
			if err != nil {
				return result{}, err
			}
			applicable++
			blocked = append(blocked, vo.blocked...)
			for _, l := range vo.missing {
				if !contains(missing, l) {
					missing = append(missing, l)
				}
			}
			vaults = append(vaults, vo)
			continue
		}
		pd, ok, err := p.publishDir(ctx, it, cfg, dirs)
		if err != nil {
			return result{}, err
		}
		if !ok {
			continue // sidecar 對不是本機檔案的影音：略過這個目標
		}
		applicable++
		for _, lang := range langsOf(cfg) {
			for _, format := range cfg.FormatList() {
				data, tr, err := p.renderOutput(ctx, it.ID, format, lang, cfg.Style)
				if errors.Is(err, ErrNoTranscript) {
					if lang != "" && !contains(missing, lang) {
						missing = append(missing, lang)
					}
					if lang == "" {
						return result{}, errPermanent(ErrNoTranscript.Error())
					}
					continue
				}
				if err != nil {
					return result{}, err
				}
				res, err := w.Write(ctx, output.File{
					ItemID: it.ID, Dir: pd.dir, Name: outputName(pd, cfg.Target, it.Title, tr.Lang, format),
					Data: data, Target: cfg.Target, Format: format, Lang: tr.Lang, Policy: cfg.Policy(),
				})
				switch {
				case errors.Is(err, store.ErrNotFound):
					return result{}, errItemGone // 影音在寫出途中被永久刪除
				case err != nil:
					return result{}, fmt.Errorf("寫出 %s：%w", format, err)
				case res.Outcome == output.Blocked:
					blocked = append(blocked, res.Path)
				}
			}
		}
	}

	if applicable == 0 {
		return result{task: taskDone, stage: stage, state: skippedState(SkipNotApplicable)}, nil
	}
	if len(blocked) > 0 {
		return result{}, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "publish", Detail: "輸出檔被修改：" + strings.Join(blocked, "、"),
			Message: msgPublishBlocked + strings.Join(blocked, "、") + "。", Actions: []string{actionRepublish},
		}
	}
	done := store.StageState{Status: store.StatusDone}
	var msgs []string
	// hook 在所有目標都寫完、沒有任何目標被擋下之後才執行（VAULT-R11、V7）；失敗只提醒，步驟仍是 done。
	hookFailed := false
	for _, vo := range vaults {
		hm, err := p.runVaultHooks(ctx, it, vo.cfg, vo)
		if err != nil {
			return result{}, err
		}
		hookFailed = hookFailed || len(hm) > 0
		msgs = append(msgs, hm...)
	}
	if len(missing) > 0 {
		msgs = append(msgs, msgPublishNoLang+strings.Join(missing, "、")+"。")
	}
	for _, vo := range vaults {
		for _, m := range vo.messages {
			if !contains(msgs, m) {
				msgs = append(msgs, m)
			}
		}
	}
	done.Message = strings.Join(msgs, "")
	if hookFailed {
		done.Actions = []string{actionRepublishVault}
	}
	return result{task: taskDone, stage: stage, state: done}, nil
}

// langsOf 回傳目標要輸出的語言；沒寫時一次，語言留空代表用字幕的預設選擇。
func langsOf(cfg output.Config) []string {
	if len(cfg.Lang) == 0 {
		return []string{""}
	}
	return cfg.Lang
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// publishDir 決定目標寫到哪個資料夾（已經通過路徑防護）。ok 為 false 代表這個目標不適用這部影音。
// cache 以目標（sidecar 或 folder 的路徑）為鍵，同一次發布只解析一次。
func (p *Pipeline) publishDir(ctx context.Context, it store.Item, cfg output.Config, cache map[string]publishDir) (publishDir, bool, error) {
	key := cfg.Target + "|" + cfg.Path
	if pd, ok := cache[key]; ok {
		return pd, true, nil
	}
	var pd publishDir
	switch cfg.Target {
	case output.TargetSidecar:
		// sidecar 只用於本機檔案；上傳的檔案是資料目錄裡的副本，旁邊不是使用者的資料夾。
		if it.Platform != source.PlatformLocal || it.Origin == store.OriginUpload {
			return publishDir{}, false, nil
		}
		d, stem, err := p.sidecarDir(it)
		if err != nil {
			return publishDir{}, false, err
		}
		pd = publishDir{dir: d, stem: stem}
	case output.TargetFolder:
		d, err := p.folderDir(cfg.Path)
		if err != nil {
			return publishDir{}, false, err
		}
		pd = publishDir{dir: d}
	default:
		return publishDir{}, false, &errclass.Error{
			Class: errclass.ClassPermanent, Source: "publish", Detail: "尚未支援的輸出目標 " + cfg.Target,
			Message: "輸出目標 " + cfg.Target + " 尚未支援。",
		}
	}
	cache[key] = pd
	return pd, true, nil
}

// sidecarDir 回傳本機影片所在的資料夾（解析 symlink 並通過路徑防護）與檔名前綴。
// 影片檔本身也要能開啟（存在、是一般檔案、在允許的目錄內）；不跟隨影片檔的 symlink：字幕放在影片代號指的路徑旁邊。
func (p *Pipeline) sidecarDir(it store.Item) (dir, stem string, err error) {
	a := localAdapter{files: p.files, media: p.media, dataDir: p.dataDir}
	path, err := a.sourcePath(it.SourcePath, it.ExternalID)
	if err != nil {
		return "", "", err
	}
	f, err := a.openFile(path)
	if err != nil {
		return "", "", err
	}
	_ = f.Close()
	dir, err = p.files.Resolve(filepath.Dir(path))
	if err != nil {
		return "", "", localError(msgLocalOutside, err)
	}
	base := filepath.Base(path)
	return dir, strings.TrimSuffix(base, filepath.Ext(base)), nil
}

// folderDir 驗證 folder 目標的資料夾：絕對路徑，解析 symlink 與 ".." 之後必須在允許的目錄內（serve 為 inbox 與
// media_roots，standalone 不受限制）；不存在時建立。寫入時再以 os.Root 限制在這個資料夾之內。
func (p *Pipeline) folderDir(path string) (string, error) {
	if p.files == nil {
		return "", localError(msgLocalNoFiles, nil)
	}
	if !filepath.IsAbs(path) {
		return "", &errclass.Error{Class: errclass.ClassPermanent, Source: "publish", Detail: "folder 的 path 不是絕對路徑", Message: "輸出資料夾必須是絕對路徑。"}
	}
	dir, err := p.files.Resolve(path)
	if errors.Is(err, pathguard.ErrOutsideRoots) {
		return "", &errclass.Error{
			Class: errclass.ClassPermanent, Source: "publish", Detail: "輸出資料夾不在允許的目錄內：" + path, Err: err,
			Message: "輸出資料夾不在允許的目錄內（inbox 與 media_roots）。",
		}
	}
	if err != nil {
		return "", err
	}
	if p.insideDataInternals(dir) {
		return "", &errclass.Error{
			Class: errclass.ClassPermanent, Source: "publish", Detail: "輸出資料夾在資料目錄的內部：" + path,
			Message: "輸出資料夾不能是資料目錄本身，或它的內部目錄（artifacts、media、uploads、tmp、bin…）。",
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("建立輸出資料夾：%w", err)
	}
	return dir, nil
}

// dataInternalDirs 是資料目錄裡 notesteme 自己使用的子目錄；inbox 等其他子目錄是使用者的，仍然可以當輸出資料夾。
var dataInternalDirs = []string{"artifacts", "media", "uploads", "tmp", "bin", "yt-dlp", "backups", "config"}

// insideDataInternals 回報 dir（已解析）是不是資料目錄本身或它的內部目錄：serve 的 Files 守衛包含 <data>/uploads，
// 不排除的話 folder 目標可以寫進這些目錄。
func (p *Pipeline) insideDataInternals(dir string) bool {
	root, err := filepath.EvalSymlinks(p.dataDir)
	if err != nil {
		root = filepath.Clean(p.dataDir)
	}
	if dir == root {
		return true
	}
	for _, name := range dataInternalDirs {
		rel, err := filepath.Rel(filepath.Join(root, name), dir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// outputName 是寫出的檔名：sidecar 是 <影片檔名>.<lang>.<ext>（OUT-R10），folder 是 <標題>.<lang>.<ext>（同下載，OUT-R4）。
// bilingual-srt 的副檔名也是 srt，加上 .bilingual 才不會和單語的 SRT 互相覆蓋。
func outputName(pd publishDir, target, title, lang, format string) string {
	ext := subtitle.FileExt(format)
	tag := ""
	if format == subtitle.FormatBilingualSRT {
		tag = ".bilingual"
	}
	if target == output.TargetSidecar {
		l := lang
		if !output.ValidLang(l) {
			l = "und"
		}
		return pd.stem + "." + l + tag + "." + ext
	}
	name := subtitle.DownloadFileName(title, lang, ext)
	if tag != "" {
		name = strings.TrimSuffix(name, "."+ext) + tag + "." + ext
	}
	return name
}

// renderOutput 把字幕渲染成 format（與 GET /api/items/:id/subtitles 相同的規則），回傳內容與實際使用的字幕。
// lang 為空時用預設選擇（OUT-R2）；ass 與雙語 SRT 依 RenderInput（原文語言相同時一律單語，OUT-R5）。
func (p *Pipeline) renderOutput(ctx context.Context, itemID, format, lang, style string) ([]byte, subtitle.Transcript, error) {
	q := CuesQuery{Lang: lang, ToMS: -1}
	var buf bytes.Buffer
	if format == subtitle.FormatASS || format == subtitle.FormatBilingualSRT {
		ass := subtitle.ASSStyle{}
		if format == subtitle.FormatASS {
			name := style
			if name == "" {
				name = "default"
			}
			st, err := assstyle.New(p.st).Get(ctx, name)
			if errors.Is(err, assstyle.ErrNotFound) {
				return nil, subtitle.Transcript{}, &errclass.Error{
					Class: errclass.ClassPermanent, Source: "publish", Detail: "找不到 ASS 樣式 " + name,
					Message: "找不到 ASS 樣式「" + name + "」。",
				}
			}
			if err != nil {
				return nil, subtitle.Transcript{}, err
			}
			ass = st.ASS()
		}
		ri, err := p.RenderInput(ctx, itemID, q)
		if err != nil {
			return nil, subtitle.Transcript{}, err
		}
		var orig []subtitle.Cue
		origLang := ri.Main.Lang
		if ri.Orig != nil {
			orig, origLang = ri.Orig.Cues, ri.Orig.Lang
		}
		pairs := subtitle.PairCues(ri.Main.Cues, orig)
		if format == subtitle.FormatASS {
			err = subtitle.RenderASS(&buf, pairs, assstyle.ResolveFonts(ass, ri.Main.Lang, origLang), ri.Item.Title)
		} else {
			err = subtitle.RenderBilingualSRT(&buf, pairs)
		}
		return buf.Bytes(), ri.Main, err
	}
	_, tr, err := p.CuesWithItem(ctx, itemID, q)
	if err != nil {
		return nil, subtitle.Transcript{}, err
	}
	err = subtitle.Render(&buf, format, tr)
	return buf.Bytes(), tr, err
}

// Outputs 列出影音已經寫出的檔案（OUT-R11），依寫入時間排列。影音不存在時回傳 store.ErrNotFound，
// 在垃圾桶中時回傳 *InTrashError。
func (p *Pipeline) Outputs(ctx context.Context, itemID string) ([]store.Output, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	list, err := r.Outputs(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	// 同一個時間寫出的檔案依種類排列：筆記、圖片、字幕（VAULT-R5 的寫入順序），再依路徑。
	slices.SortStableFunc(list, func(a, b store.Output) int {
		if c := a.WrittenAt.Compare(b.WrittenAt); c != 0 {
			return c
		}
		return cmp.Compare(outputKindRank(a.Kind), outputKindRank(b.Kind))
	})
	return list, nil
}

func outputKindRank(kind string) int {
	switch kind {
	case store.OutputKindNote:
		return 0
	case store.OutputKindAsset:
		return 1
	}
	return 2
}
