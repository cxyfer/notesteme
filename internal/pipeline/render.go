package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 匯入字幕後，下游的譯文標示過時（OUT-R14）。
const (
	MsgSubtitlesUpdated = "字幕已更新，譯文可能過時"
	ActionRetranslate   = "重新翻譯"
)

// MaxImportBytes 是匯入的字幕檔大小上限（不可信的輸入）。
const MaxImportBytes = 8 << 20

// ResolveItem 以影音 ID 或短代號（yt:<id>、bili:<bvid>/p<n>、pod:…、local:…）找影音；
// 不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError。
func (p *Pipeline) ResolveItem(ctx context.Context, idOrRef string) (store.Item, error) {
	r := p.st.Read()
	var (
		it  store.Item
		err error
	)
	if platform, ext, part, ok := source.SplitRef(idOrRef); ok {
		it, err = r.ItemByKey(ctx, platform, ext, part)
	} else {
		it, err = r.ItemByID(ctx, idOrRef)
	}
	if err != nil {
		return store.Item{}, err
	}
	if it.DeletedAt != nil {
		return store.Item{}, &InTrashError{ItemID: it.ID}
	}
	return it, nil
}

// RenderInput 是 ASS 與雙語 SRT 的輸入：要輸出的字幕，以及雙語時的原文。
type RenderInput struct {
	Item store.Item
	// Main 是要輸出的字幕（雙語時是譯文）。
	Main subtitle.Transcript
	// Orig 是雙語時的原文；nil 代表單語（OUT-R5）。
	Orig *subtitle.Transcript
}

// RenderInput 依 q 選出要輸出的字幕（規則同 Cues），並找出雙語時的原文：
//   - 沒有指定語言與變體時，目標語言是最近產生的譯文的語言；沒有譯文就只輸出單語。
//   - 原文是其他語言（主語言不同，zh-Hans 與 zh-Hant 算同一種語言）、非 translated 變體中處理得最完整的字幕（OUT-R2 的順序）。
//     原文語言取自原文那一邊的產出，不看 items.original_lang：後者可能和實際的字幕不同（OUT-R5 同語言的場景）。
//   - 目標語言就是影音的原文語言時，一律只輸出單語。
//   - 找不到這樣的字幕（原文與目標語言相同，包括 convert 只差字形），Orig 為 nil，一律只輸出單語。
func (p *Pipeline) RenderInput(ctx context.Context, itemID string, q CuesQuery) (RenderInput, error) {
	r := p.st.Read()
	if q.Lang == "" && q.Variant == "" {
		arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
		if err != nil {
			return RenderInput{}, err
		}
		for i := len(arts) - 1; i >= 0; i-- {
			if arts[i].Variant == string(subtitle.VariantTranslated) {
				q.Lang = arts[i].Lang
				break
			}
		}
	}
	it, main, err := p.CuesWithItem(ctx, itemID, q)
	if err != nil {
		return RenderInput{}, err
	}
	out := RenderInput{Item: it, Main: main}
	// 要輸出的就是影音的原文語言：沒有東西可以配成雙語，不把別種語言（例如匯入的外語版）當成原文。
	if it.OriginalLang != "" && langtag.SameBase(main.Lang, it.OriginalLang) {
		return out, nil
	}
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return RenderInput{}, err
	}
	orig, ok := pickOriginal(arts, it.OriginalLang, main.Lang)
	if !ok {
		return out, nil
	}
	tr, err := readTranscript(p.dataDir, orig.Path, orig.SHA256)
	if err != nil {
		return RenderInput{}, err
	}
	tr.Cues = subtitle.FilterRange(tr.Cues, q.FromMS, q.ToMS)
	out.Orig = &tr
	return out, nil
}

// pickOriginal 在現行產出中挑雙語的原文：非 translated、語言與 mainLang 的主語言不同，變體依 OUT-R2 的順序；
// 同一個變體有多個語言時，優先用影音的原文語言，否則取最近產生的。
func pickOriginal(arts []store.Artifact, origLang, mainLang string) (store.Artifact, bool) {
	var cands []store.Artifact
	var have []subtitle.Variant
	for _, a := range arts {
		if a.Variant == string(subtitle.VariantTranslated) || langtag.SameBase(a.Lang, mainLang) {
			continue
		}
		cands = append(cands, a)
		have = append(have, subtitle.Variant(a.Variant))
	}
	best, ok := subtitle.PickVariant(have)
	if !ok {
		return store.Artifact{}, false
	}
	cands = filterArtifacts(cands, func(a store.Artifact) bool { return a.Variant == string(best) })
	chosen := cands[len(cands)-1]
	for _, a := range cands {
		if origLang != "" && sameLang(a.Lang, origLang) {
			chosen = a
		}
	}
	return chosen, true
}

// ---- 匯入外部編輯過的字幕（OUT-R13～R15）----

// ImportRequest 是匯入字幕的請求。
type ImportRequest struct {
	ItemID string
	// Data 是字幕檔的內容（SRT、VTT 或 ASS）。
	Data []byte
	// Format 是 srt、vtt、ass；空字串時從內容判斷。
	Format string
	// Lang 是字幕的語言；空字串時用影音的原文語言。
	Lang string
	// Name 是來源檔名，只取檔名部分（不含目錄）寫進來源紀錄。
	Name string
}

// ImportResult 是匯入的結果。
type ImportResult struct {
	Artifact store.Artifact
	Format   string
	Cues     int
	// Flagged 為 true 代表既有的譯文已標示過時。
	Flagged bool
}

var importLang = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// ImportSubtitle 把外部編輯過的字幕存成新的 edited 版本（來源紀錄的 executor 是 human，OUT-R13）。
// 同一個交易中：既有的譯文（語言與匯入的不同）標示「字幕已更新，譯文可能過時」並提供「重新翻譯」（OUT-R14）；
// 不自動重跑任何下游步驟。檔案無法解析、時間不單調時回傳 *subtitle.ParseError，這時不寫入任何東西（OUT-R15）。
func (p *Pipeline) ImportSubtitle(ctx context.Context, req ImportRequest) (ImportResult, error) {
	if len(req.Data) > MaxImportBytes {
		return ImportResult{}, invalidf("字幕檔超過 %d MiB 的上限。", MaxImportBytes>>20)
	}
	it, err := p.st.Read().ItemByID(ctx, req.ItemID)
	if err != nil {
		return ImportResult{}, err
	}
	if it.DeletedAt != nil {
		return ImportResult{}, &InTrashError{ItemID: it.ID}
	}
	lang := strings.ReplaceAll(strings.TrimSpace(req.Lang), "_", "-")
	if lang == "" {
		lang = it.OriginalLang
	}
	if lang == "" {
		return ImportResult{}, invalidf("lang：影音還不知道原文語言，請指定字幕的語言。")
	}
	if !importLang.MatchString(lang) {
		return ImportResult{}, invalidf("lang：不是正確的語言標記。")
	}
	lang = langtag.Parse(lang).String()
	if req.Format != "" && req.Format != subtitle.ImportSRT && req.Format != subtitle.ImportVTT && req.Format != subtitle.ImportASS {
		return ImportResult{}, invalidf("format：必須是 srt、vtt 或 ass。")
	}
	cues, format, err := subtitle.ParseImport(req.Data, req.Format)
	if err != nil {
		return ImportResult{}, err
	}

	sum := sha256.Sum256(req.Data)
	from := map[string]any{"format": format, "sha256": hex.EncodeToString(sum[:])}
	if name := filepath.Base(filepath.ToSlash(req.Name)); req.Name != "" && name != "." && name != "/" {
		from["name"] = name
	}
	var res ImportResult
	res.Format, res.Cues = format, len(cues)

	// 先寫產出檔，交易失敗時再刪掉；來源紀錄的 parents 在交易內才知道現行的產出，所以在這裡先讀一次。
	arts, err := p.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return ImportResult{}, err
	}
	fields := map[string]any{"stage": "import", "executor": "human", "imported_from": from}
	var parents []string
	if base, ok := importBase(arts, lang); ok {
		parents = []string{base.ID}
		fields["derived_from"] = []map[string]string{{"artifact": base.ID, "variant": base.Variant, "sha256": base.SHA256}}
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return ImportResult{}, err
	}
	art, err := p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: lang, Variant: subtitle.VariantEdited, Cues: cues}, parents, prov)
	if err != nil {
		return ImportResult{}, err
	}
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		cur, err := r.ItemByID(ctx, it.ID)
		if err != nil {
			return err
		}
		if cur.DeletedAt != nil {
			return &InTrashError{ItemID: cur.ID}
		}
		a, err := r.InsertArtifact(ctx, *art)
		if err != nil {
			return err
		}
		res.Artifact = a
		res.Flagged, err = flagStaleTranslation(ctx, r, it.ID, lang)
		return err
	})
	if err != nil {
		_ = os.Remove(filepath.Join(p.dataDir, filepath.FromSlash(art.Path)))
		return ImportResult{}, err
	}
	return res, nil
}

// importBase 挑匯入字幕的上游：同語言、非 translated 的現行產出中處理得最完整的（只記錄來源，不影響內容）。
func importBase(arts []store.Artifact, lang string) (store.Artifact, bool) {
	var cands []store.Artifact
	var have []subtitle.Variant
	for _, a := range arts {
		if a.Variant == string(subtitle.VariantTranslated) || a.Variant == string(subtitle.VariantEdited) || !sameLang(a.Lang, lang) {
			continue
		}
		cands = append(cands, a)
		have = append(have, subtitle.Variant(a.Variant))
	}
	best, ok := subtitle.PickVariant(have)
	if !ok {
		return store.Artifact{}, false
	}
	cands = filterArtifacts(cands, func(a store.Artifact) bool { return a.Variant == string(best) })
	return cands[len(cands)-1], true
}

// flagStaleTranslation 在影音有現行的譯文（語言與匯入的字幕的主語言不同）時，把 translate 最新一筆步驟標示過時；
// 狀態與產出都不動。回傳是否標示了。
func flagStaleTranslation(ctx context.Context, r *store.Repo, itemID, importedLang string) (bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return false, err
	}
	stale := false
	for _, a := range arts {
		if a.Variant == string(subtitle.VariantTranslated) && !langtag.SameBase(a.Lang, importedLang) {
			stale = true
		}
	}
	if !stale {
		return false, nil
	}
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	run, ok := latestRuns(runs)[StageTranslate]
	if !ok {
		return false, nil
	}
	if err := r.SetStageNote(ctx, run.ID, MsgSubtitlesUpdated, []string{ActionRetranslate}); err != nil {
		return false, err
	}
	return true, nil
}

// ---- 樣式預覽（OUT-R9）----

// PreviewRequest 是樣式預覽的請求。
type PreviewRequest struct {
	// Item 是影音的 ID 或短代號。
	Item string
	// Style 是要預覽的樣式（已驗證，字型可以是空字串）。
	Style subtitle.ASSStyle
	// AtMS 是畫面的時間（毫秒）：影片抽幀的位置，也決定預覽用哪一句字幕。
	AtMS int64
	// Lang 是目標語言；空字串時依 RenderInput 的規則。
	Lang string
}

// 沒有字幕可用時的範例文字。
const (
	previewSampleMain = "字幕樣式預覽"
	previewSampleOrig = "Subtitle style preview"
)

// previewPair 選預覽用的字幕：at 所在的那一句，不在任何一句之中時取最近的一句；沒有字幕就用範例文字。
func (p *Pipeline) previewPair(ctx context.Context, it store.Item, atMS int64, lang string) (pair subtitle.Pair, mainLang, origLang string, err error) {
	in, err := p.RenderInput(ctx, it.ID, CuesQuery{Lang: lang, ToMS: -1})
	if errors.Is(err, ErrNoTranscript) {
		return subtitle.Pair{Main: previewSampleMain, Orig: previewSampleOrig}, "zh-Hant", "en", nil
	}
	if err != nil {
		return subtitle.Pair{}, "", "", err
	}
	var origCues []subtitle.Cue
	if in.Orig != nil {
		origCues, origLang = in.Orig.Cues, in.Orig.Lang
	}
	pairs := subtitle.PairCues(in.Main.Cues, origCues)
	if len(pairs) == 0 {
		return subtitle.Pair{Main: previewSampleMain, Orig: previewSampleOrig}, "zh-Hant", "en", nil
	}
	best, bestDist := 0, int64(-1)
	for i, c := range pairs {
		d := int64(0)
		switch {
		case atMS < c.StartMS:
			d = c.StartMS - atMS
		case atMS >= c.EndMS:
			d = atMS - c.EndMS + 1
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	return pairs[best], in.Main.Lang, origLang, nil
}

// maxPreviewPNG 是讀回的預覽圖大小上限。
const maxPreviewPNG = 16 << 20

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// PreviewFrame 用 ffmpeg + libass 把樣式渲染在影音指定時間的畫面上，回傳 PNG（OUT-R9）。
// 本機影片檔抽出真正的畫面；其他影音（線上影片沒有下載的媒體、只有音訊的 Podcast 與本機音檔）用合成的深灰色畫布。
// 字幕文字取自 at 所在的那一句（雙語時含原文）；影音沒有字幕時用範例文字。
func (p *Pipeline) PreviewFrame(ctx context.Context, req PreviewRequest) ([]byte, error) {
	if p.media == nil {
		return nil, &NotImplementedError{What: "樣式預覽需要 ffmpeg"}
	}
	it, err := p.ResolveItem(ctx, req.Item)
	if err != nil {
		return nil, err
	}
	pair, mainLang, origLang, err := p.previewPair(ctx, it, req.AtMS, req.Lang)
	if err != nil {
		return nil, err
	}
	st := assstyle.ResolveFonts(req.Style, mainLang, origLang)
	// 字幕涵蓋整段時間：抽幀時 ffmpeg 的時間從 0 重新開始。
	pair.StartMS, pair.EndMS = 0, 3600_000

	tmpRoot := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "s.ass"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	err = subtitle.RenderASS(f, []subtitle.Pair{pair}, st, "preview")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("寫入預覽用的 ASS：%w", err)
	}

	if err := p.media.RenderSubtitleFrame(ctx, dir, p.previewVideo(ctx, it), float64(req.AtMS)/1000); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &UpstreamError{Msg: previewFailureMsg(err), Err: err}
	}
	png, err := readPNG(filepath.Join(dir, "out.png"))
	if err != nil {
		return nil, &UpstreamError{Msg: "ffmpeg 沒有產生可用的預覽圖。", Err: err}
	}
	return png, nil
}

// previewFailureMsg 是 ffmpeg 預覽失敗時給使用者看的訊息；找不到 ffmpeg 用工具帶的訊息，
// 其他失敗（例如沒有 libass 的 ffmpeg）不回傳 stderr，細節只寫進 log。
func previewFailureMsg(err error) string {
	var ce *errclass.Error
	if errors.As(err, &ce) && ce.Class == errclass.ClassNeedsAction && ce.Message != "" {
		return ce.Message
	}
	return "ffmpeg 渲染預覽失敗，請確認 ffmpeg 含 libass（可執行 notesteme doctor）。"
}

// previewVideo 回傳可以抽幀的影片檔路徑（已解析的絕對路徑）；沒有可用的影片時回傳空字串，改用合成畫布。
func (p *Pipeline) previewVideo(ctx context.Context, it store.Item) string {
	if it.Platform != source.PlatformLocal || p.files == nil {
		return ""
	}
	a := localAdapter{files: p.files, media: p.media, dataDir: p.dataDir}
	f, abs, err := a.open(it.SourcePath, it.ExternalID)
	if err != nil {
		return ""
	}
	_ = f.Close()
	info, err := p.media.Probe(ctx, abs)
	if err != nil || info.Kind() != "video" {
		return ""
	}
	return abs
}

// readPNG 讀回 ffmpeg 輸出的預覽圖：必須是一般檔案、不超過上限，而且開頭是 PNG 的簽章。
func readPNG(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg 沒有產生預覽圖：%w", err)
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxPreviewPNG {
		return nil, errors.New("預覽圖不是一般檔案，或超過大小上限")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < len(pngMagic) || string(b[:len(pngMagic)]) != string(pngMagic) {
		return nil, errors.New("ffmpeg 輸出的不是 PNG")
	}
	return b, nil
}
