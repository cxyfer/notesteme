package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 這個檔案是 bundle 的匯入（BND-R3～R9）：bundle 是不可信的輸入。
// 結構、大小、manifest 與雜湊由 internal/bundle 驗證（BND-R4～R7），內容再以產品自己的解碼器解析並重新編碼，
// 檔案先寫到 <data>/tmp/import-<uuid>/，全部準備好才搬進 artifacts 並在一個交易中寫入 DB；任何一步失敗都不留下東西。

// ExecutorImport 是匯入的產出在 stage_runs 的 executor（BND-R8）。
const ExecutorImport = "import"

// BND-R9 的錯誤代碼。
const (
	CodeBundleItemExists  = "bundle_item_exists"
	CodeBundleItemInTrash = "bundle_item_in_trash"
)

// BundleConflictError 是影音已經存在或在垃圾桶中（BND-R9，409）。
type BundleConflictError struct {
	Code    string
	Message string
	// ItemID 是已存在的影音（只有 bundle_item_exists 才有；垃圾桶中的影音不回傳 ID）。
	ItemID string
}

func (e *BundleConflictError) Error() string { return e.Message }

// BundleImportRequest 是匯入的請求。
type BundleImportRequest struct {
	Data   []byte
	Limits bundle.Limits
	// AddVersions 是 on_existing=add_versions：影音已經存在時把產出加成新版本（BND-R9）。
	AddVersions bool
}

// BundleImportResult 是匯入的結果（BND-R3：{item: {id, ref, title}, imported, skipped}）。
type BundleImportResult struct {
	Item     store.Item
	Ref      string
	Imported int
	Skipped  int
}

// bundleImportSlot 讓匯入一次只做一個：bundle 與解壓後的內容都在記憶體中（最多 max_upload_bytes + max_total_bytes）。
var bundleImportSlot = make(chan struct{}, 1)

// preparedArtifact 是一個驗證過、已重新編碼的產出。
type preparedArtifact struct {
	src     bundle.Artifact
	id      string
	kind    string
	variant string
	lang    string
	format  string
	data    []byte
	sha     string
	timings []string
	// frames 是 frames 產出選中的畫面（編號 → JPEG）；framePaths 是畫面在 bundle 中的項目名稱（錯誤訊息用）。
	frames     map[int][]byte
	framePaths map[int]string
	skip       bool
}

// stageForArtifact 是產出對應的步驟（BND-R8）；edited 沒有對應的步驟。
func stageForArtifact(kind, variant string) string {
	switch kind {
	case "transcript":
		switch subtitle.Variant(variant) {
		case subtitle.VariantSource:
			return StageTranscript
		case subtitle.VariantNormalized:
			return StageNormalize
		case subtitle.VariantSegmented:
			return StageSegment
		case subtitle.VariantCorrected:
			return StageCorrect
		case subtitle.VariantTranslated:
			return StageTranslate
		}
		return ""
	case KindNote:
		return StageNote
	case KindChapters:
		return StageChapters
	case KindSponsorSegments:
		return StageSponsorBlock
	case KindFrames:
		return StageFrames
	}
	return ""
}

func bundleInvalid(entry, format string, args ...any) *bundle.Error {
	s := bundle.ShowEntry(entry)
	return &bundle.Error{Code: bundle.CodeInvalid, Entry: s, Message: fmt.Sprintf(format, append([]any{s}, args...)...)}
}

func errContent(path, what string) *bundle.Error {
	return bundleInvalid(path, "bundle 中的檔案「%s」不是有效的%s，已拒絕匯入。", what)
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ImportBundle 匯入 bundle（BND-R3～R9）。錯誤：*bundle.Error（422）、*BundleConflictError（409）、ctx 取消。
func (p *Pipeline) ImportBundle(ctx context.Context, req BundleImportRequest) (BundleImportResult, error) {
	select {
	case bundleImportSlot <- struct{}{}:
		defer func() { <-bundleImportSlot }()
	case <-ctx.Done():
		return BundleImportResult{}, ctx.Err()
	}
	b, err := bundle.Read(req.Data, bundle.Options{Limits: req.Limits})
	if err != nil {
		return BundleImportResult{}, err
	}
	m := b.Manifest
	if m.Item.Platform == source.PlatformLocal && !sha256Pattern.MatchString(m.Item.ExternalID) {
		return BundleImportResult{}, &bundle.Error{Code: bundle.CodeInvalid, Entry: bundle.ManifestName, Path: "item.external_id",
			Message: "manifest.json 不符合格式：item.external_id：本機影音必須是內容的 sha256。"}
	}
	if m.Item.Platform == source.PlatformBilibili && m.Item.Part < 1 {
		return BundleImportResult{}, &bundle.Error{Code: bundle.CodeInvalid, Entry: bundle.ManifestName, Path: "item.part",
			Message: "manifest.json 不符合格式：item.part：Bilibili 影片必須有分P。"}
	}
	// 先以產品的解碼器解析產出，拒絕沒有被引用的檔案與超過擷取畫面尺寸的圖片，再做圖片的結構檢查；
	// 完整解碼只在 placeArtifacts 產生縮圖時（BND 待決問題 N25）。
	arts, err := prepareArtifacts(b)
	if err != nil {
		return BundleImportResult{}, err
	}
	if err := b.ValidateContentsContext(ctx); err != nil {
		return BundleImportResult{}, err
	}
	sum := sha256.Sum256(req.Data)
	bundleSHA := hex.EncodeToString(sum[:])
	now := p.clock.Now().UTC()

	existing, found, err := p.bundleTarget(ctx, p.st.Read(), m.Item)
	if err != nil {
		return BundleImportResult{}, err
	}
	if err := bundleConflict(existing, found, req.AddVersions); err != nil {
		return BundleImportResult{}, err
	}
	itemID := store.NewID()
	if found {
		itemID = existing.ID
		shas, err := p.st.Read().ItemArtifactSHAs(ctx, itemID)
		if err != nil {
			return BundleImportResult{}, err
		}
		for i := range arts {
			arts[i].skip = shas[arts[i].sha]
		}
	}
	plan, err := p.planIngest(ctx, IngestRequest{})
	if err != nil {
		return BundleImportResult{}, err
	}

	// 先把檔案寫到暫存目錄，再搬進 artifacts；交易失敗時刪除搬過去的檔案。
	tmpRoot := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return BundleImportResult{}, err
	}
	tmp, err := os.MkdirTemp(tmpRoot, "import-")
	if err != nil {
		return BundleImportResult{}, err
	}
	defer os.RemoveAll(tmp)
	placed, err := p.placeArtifacts(ctx, tmp, itemID, arts)
	cleanup := func() {
		for _, f := range placed {
			_ = os.RemoveAll(f)
		}
	}
	if err != nil {
		cleanup()
		return BundleImportResult{}, err
	}

	var res BundleImportResult
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		res = BundleImportResult{}
		cur, ok, err := p.bundleTarget(ctx, r, m.Item)
		if err != nil {
			return err
		}
		if err := bundleConflict(cur, ok, req.AddVersions); err != nil {
			return err
		}
		if ok != found || (ok && cur.ID != itemID) {
			// 檢查之後有人新增了同一部影音：依現在的狀態回報，請使用者重新匯入。
			return existsConflict(cur)
		}
		snap, err := r.EnsureSnapshotLayers(ctx, plan.sha, plan.cfgJSON, plan.layersJSON)
		if err != nil {
			return err
		}
		if !ok {
			if cur, err = p.createBundleItem(ctx, r, itemID, m.Item, plan); err != nil {
				return err
			}
		}
		res.Item = cur
		res.Ref = source.Ref(cur.Platform, cur.ExternalID, cur.Part)
		newIDs := map[string]string{}
		for _, a := range arts {
			newIDs[strings.ToLower(a.src.ID)] = a.id
		}
		stageArt := map[string]string{}
		for _, a := range arts {
			if a.skip {
				res.Skipped++
				continue
			}
			prov, err := importProvenance(a.src.Provenance, m.CreatedBy, bundleSHA, now)
			if err != nil {
				return err
			}
			var parents []string
			for _, par := range a.src.Parents {
				if id, ok := newIDs[strings.ToLower(par)]; ok {
					parents = append(parents, id)
				}
			}
			ins, err := r.InsertArtifact(ctx, store.NewArtifact{
				ID: a.id, ItemID: cur.ID, Kind: a.kind, Variant: a.variant, Lang: a.lang, Format: a.format,
				Path: artifactRel(cur.ID, a.id), SHA256: a.sha, Size: int64(len(a.data)), Parents: parents,
				Provenance: prov, Timings: a.timings,
			})
			if err != nil {
				return err
			}
			res.Imported++
			if st := stageForArtifact(a.kind, a.variant); st != "" {
				stageArt[st] = ins.ID
			}
		}
		// 有匯入產出的步驟記為 done（executor import）；不建立任務、不喚醒 worker（BND-R8）。
		first := ""
		for _, st := range Stages {
			artID, ok := stageArt[st]
			if !ok {
				continue
			}
			if first == "" {
				first = st
			}
			run, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: cur.ID, Stage: st, Status: store.StatusPending, ConfigSnapshotID: snap})
			if err != nil {
				return err
			}
			if err := r.SetStageResult(ctx, run.ID, store.StageResult{Executor: ExecutorImport, ArtifactID: artID}); err != nil {
				return err
			}
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusDone}); err != nil {
				return err
			}
		}
		// 匯入的 transcript／sponsorblock 取代了現行版本，等同從該步驟重跑：結束既有的重新 Probe（SRC-R18）。
		if first != "" {
			return p.endReprobeOnRerun(ctx, r, cur.ID, first)
		}
		return nil
	})
	if err != nil {
		cleanup()
		return BundleImportResult{}, err
	}
	return res, nil
}

func existsMessage(ref string) string {
	return fmt.Sprintf("這部影音已在媒體庫中（%s）。要把 bundle 的產出加成新版本，請加上 on_existing=add_versions。", ref)
}

// existsConflict 是 bundle_item_exists：帶已存在的影音 ID，client 可以連到它。
// it 在垃圾桶中時改回 bundle_item_in_trash（不帶 ID）；ID 是空的（競態下讀不到）時省略。
func existsConflict(it store.Item) *BundleConflictError {
	if it.DeletedAt != nil {
		return trashConflict()
	}
	return &BundleConflictError{Code: CodeBundleItemExists, Message: existsMessage(source.Ref(it.Platform, it.ExternalID, it.Part)), ItemID: it.ID}
}

func trashConflict() *BundleConflictError {
	return &BundleConflictError{Code: CodeBundleItemInTrash, Message: "這部影音在垃圾桶中。請先還原或永久刪除後再匯入。"}
}

// bundleConflict 依 BND-R9 判斷能不能匯入到既有的影音。
func bundleConflict(it store.Item, found, addVersions bool) error {
	if !found {
		return nil
	}
	if it.DeletedAt != nil {
		return trashConflict()
	}
	if !addVersions {
		return existsConflict(it)
	}
	return nil
}

// bundleTarget 找 bundle 對應的既有影音（含垃圾桶中的）：以 (platform, external_id, part) 比對；
// 本機影音的 external_id 是內容的 sha256，改以 content_sha256 比對（BND-R2、R9）。
func (p *Pipeline) bundleTarget(ctx context.Context, r *store.Repo, mi bundle.Item) (store.Item, bool, error) {
	var (
		it  store.Item
		err error
	)
	if mi.Platform == source.PlatformLocal {
		it, err = r.ItemByLocalSHA(ctx, mi.ExternalID)
	} else {
		it, err = r.ItemByKey(ctx, mi.Platform, mi.ExternalID, mi.Part)
	}
	if errors.Is(err, store.ErrNotFound) {
		return store.Item{}, false, nil
	}
	return it, err == nil, err
}

// createBundleItem 建立影音（視同手動新增：同一個交易中移除 tombstone，DEL-R11、BND-R9），寫入 manifest 的 metadata。
// 不建立任何步驟與任務；匯入的步驟在呼叫端記為 done。
func (p *Pipeline) createBundleItem(ctx context.Context, r *store.Repo, id string, mi bundle.Item, plan ingestPlan) (store.Item, error) {
	n := store.NewItem{ID: id, Platform: mi.Platform, ExternalID: mi.ExternalID, Part: mi.Part,
		ProfileID: plan.profile.Name, ProfileVersion: plan.profile.Version, Overrides: plan.overrides}
	ref := source.Ref(mi.Platform, mi.ExternalID, mi.Part)
	if u, ok := source.ParseRef(ref); ok {
		n.URL = u
	}
	if mi.Platform == source.PlatformLocal {
		// 本機影音：key 是檔名（撞名時加 -2、-3），內容的 sha256 記在 content_sha256；沒有媒體檔（BND N8）。
		name := baseName(mi.SourceName)
		if !validLocalName(name) {
			name = mi.ExternalID
		}
		key, err := uniqueLocalKey(ctx, r, name, "")
		if err != nil {
			return store.Item{}, err
		}
		n.ExternalID, n.ContentSHA256 = key, mi.ExternalID
		if _, err := r.DeleteLocalTombstonesBySHA(ctx, mi.ExternalID); err != nil {
			return store.Item{}, err
		}
	}
	if _, err := r.DeleteTombstones(ctx, n.Platform, n.ExternalID, n.Part); err != nil {
		return store.Item{}, err
	}
	it, created, err := r.InsertItemIfAbsent(ctx, n)
	if err != nil {
		return store.Item{}, err
	}
	if !created {
		return store.Item{}, existsConflict(it)
	}
	meta := store.ItemMeta{Title: mi.Title, Availability: mi.Availability, OriginalLang: mi.OriginalLang}
	if mi.OriginalLang != "" {
		meta.LangSource = "metadata"
	}
	if mi.DurationS != nil {
		d := int(*mi.DurationS)
		meta.DurationS = &d
	}
	if t, ok := parseBundleDate(mi.PublishedAt); ok {
		meta.PublishedAt = &t
	}
	if mi.Channel.ExternalID != "" && mi.Platform != source.PlatformLocal {
		ch, err := r.UpsertChannel(ctx, store.Channel{Platform: mi.Platform, ExternalID: mi.Channel.ExternalID, Title: mi.Channel.Title})
		if err != nil {
			return store.Item{}, err
		}
		meta.ChannelID = ch
	}
	if err := r.UpdateItemMeta(ctx, it.ID, meta); err != nil {
		return store.Item{}, err
	}
	return r.ItemByID(ctx, it.ID)
}

// validLocalName 檢查 source_name 能不能當成本機影音的 key（不含路徑分隔字元與控制字元）。
func validLocalName(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 255 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func parseBundleDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// importProvenance 保留原本的來源紀錄，加上 imported_from 與 verified: false；executor 是 agent 時加上 model_verified: false（BND-R8）。
// bundle 自帶的同名欄位一律被覆寫。
func importProvenance(raw json.RawMessage, by bundle.CreatedBy, bundleSHA string, now time.Time) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		obj = map[string]json.RawMessage{}
	}
	set := func(k string, v any) error {
		b, err := json.Marshal(v)
		obj[k] = b
		return err
	}
	if err := set("imported_from", map[string]string{"instance_id": by.InstanceID, "app_version": by.AppVersion,
		"bundle_sha256": bundleSHA, "imported_at": now.Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	if err := set("verified", false); err != nil {
		return nil, err
	}
	var executor string
	if e, ok := obj["executor"]; ok {
		_ = json.Unmarshal(e, &executor)
	}
	// bundle 自稱 executor: hub 不可信：分享時 hub 來源可以略過「需要登入」的檢查並帶上 hub_origin（HSHR-R4、R6），
	// 所以改成 import，原本的值與 hub_origin 改名保留（BND 待決問題 N27）。
	if executor == SourceHub {
		executor = ExecutorImport
		if err := set("executor", ExecutorImport); err != nil {
			return nil, err
		}
		if err := set("imported_executor", SourceHub); err != nil {
			return nil, err
		}
		if o, ok := obj["hub_origin"]; ok {
			obj["imported_hub_origin"] = o
			delete(obj, "hub_origin")
		}
	}
	if executor == ExecutorAgent {
		if err := set("model_verified", false); err != nil {
			return nil, err
		}
	} else {
		delete(obj, "model_verified")
	}
	return json.Marshal(obj)
}

func artifactRel(itemID, artID string) string {
	return filepath.ToSlash(filepath.Join(artifactsDir, itemID, artID+".json"))
}

// prepareArtifacts 以產品的解碼器重新解析每個產出並重新編碼（同樣的輸入產生同樣的 bytes，BND-R9 的略過才成立），
// 並確認每張 JPEG 都屬於某個 frames 產出選中的畫面。
func prepareArtifacts(b *bundle.Bundle) ([]preparedArtifact, error) {
	m := b.Manifest
	defaultLang := m.Item.OriginalLang
	if defaultLang == "" {
		defaultLang = "en"
	}
	used := map[string]bool{}
	groups := map[string]string{}
	out := make([]preparedArtifact, 0, len(m.Artifacts))
	for _, a := range m.Artifacts {
		used[a.Path] = true
		data, _ := b.File(a.Path)
		pa := preparedArtifact{src: a, id: store.NewID(), kind: a.Kind, lang: a.Lang}
		var err error
		switch a.Kind {
		case "transcript":
			err = pa.prepareTranscript(data)
		case KindNote:
			if pa.lang == "" {
				pa.lang = defaultLang
			}
			err = pa.prepareNote(data)
		case KindChapters:
			err = pa.prepareJSON(data, func() any { return &Chapters{} }, validChapters, chaptersFmt, "章節 JSON")
		case KindSponsorSegments:
			err = pa.prepareJSON(data, func() any { return &sponsorSegmentsDoc{} }, validSponsor, sponsorSegmentsFmt, "贊助片段 JSON")
		case KindFrames:
			err = pa.prepareFrames(b, used)
		}
		if err != nil {
			return nil, err
		}
		key := pa.kind + "\x00" + pa.variant + "\x00" + strings.ToLower(pa.lang)
		if other, dup := groups[key]; dup {
			return nil, bundleInvalid(a.Path, "bundle 中的檔案「%s」與「%s」是同一種產出（種類、變體與語言都相同），已拒絕匯入。", bundle.ShowEntry(other))
		}
		groups[key] = a.Path
		pa.sha = sha256Hex(pa.data)
		out = append(out, pa)
	}
	for _, p := range b.Paths() {
		if !used[p] {
			return nil, bundleInvalid(p, "bundle 中的檔案「%s」沒有被任何產出引用，已拒絕匯入。")
		}
	}
	return out, nil
}

func (pa *preparedArtifact) prepareTranscript(data []byte) error {
	tr, err := subtitle.DecodeCuesJSON(bytes.NewReader(data))
	if err != nil {
		return errContent(pa.src.Path, " notesteme-cues+json 字幕")
	}
	if !subtitle.Variant(pa.src.Variant).Valid() {
		return errContent(pa.src.Path, " notesteme-cues+json 字幕")
	}
	for i := range tr.Cues {
		if !utf8.ValidString(tr.Cues[i].Text) {
			return errContent(pa.src.Path, " notesteme-cues+json 字幕")
		}
		if tr.Cues[i].Timing == "" {
			tr.Cues[i].Timing = subtitle.TimingSource
		}
	}
	tr.Lang, tr.Variant = pa.src.Lang, subtitle.Variant(pa.src.Variant)
	var buf bytes.Buffer
	if err := subtitle.RenderCuesJSON(&buf, tr); err != nil {
		return err
	}
	pa.variant, pa.format, pa.data, pa.timings = pa.src.Variant, cuesFormat, buf.Bytes(), cueTimings(tr.Cues)
	return nil
}

func (pa *preparedArtifact) prepareNote(data []byte) error {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return errContent(pa.src.Path, " UTF-8 筆記")
	}
	md := string(data)
	front, _ := notes.ParseFrontmatter(md)
	if front == nil {
		front = map[string]string{}
	}
	doc := noteDoc{Lang: pa.lang, Markdown: md, Frontmatter: front, Checks: []notes.Check{}, Engine: ExecutorImport,
		Sections: []notes.Section{}, SelfCheck: []string{}, Images: []noteImage{}}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	pa.variant, pa.format, pa.data = KindNote, noteFmt, body
	return nil
}

// prepareJSON 以 DisallowUnknownFields 解碼成產品的型別、檢查內容，再重新編碼。
func (pa *preparedArtifact) prepareJSON(data []byte, newDoc func() any, check func(any) bool, format, what string) error {
	doc := newDoc()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(doc); err != nil || dec.More() || !check(doc) {
		return errContent(pa.src.Path, what)
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	pa.variant, pa.format, pa.data = pa.kind, format, body
	return nil
}

// maxTimeMS 是匯入的時間上限（一週），避免不合理的時間值。
const maxTimeMS = 7 * 24 * 3600 * 1000

func validChapters(v any) bool {
	c := v.(*Chapters)
	if c.Chapters == nil {
		c.Chapters = []Chapter{}
	}
	for _, ch := range c.Chapters {
		if ch.StartMS < 0 || ch.EndMS < ch.StartMS || ch.EndMS > maxTimeMS || ch.Cue < 0 ||
			!utf8.ValidString(ch.Title) || utf8.RuneCountInString(ch.Title) > 500 {
			return false
		}
	}
	return utf8.ValidString(c.Origin) && len(c.Origin) <= 64
}

func validSponsor(v any) bool {
	d := v.(*sponsorSegmentsDoc)
	if d.Segments == nil {
		d.Segments = []sponsorblock.Segment{}
	}
	for _, s := range d.Segments {
		if s.StartMS < 0 || s.EndMS < s.StartMS || s.EndMS > maxTimeMS || len(s.Category) > 64 || len(s.UUID) > 128 ||
			!utf8.ValidString(s.Category) || !utf8.ValidString(s.UUID) {
			return false
		}
	}
	return true
}

// prepareFrames 只收選中的畫面：每個畫面必須有 artifacts/<frames-id>-<nnn>.jpg；檔名一律依畫面編號重新產生，
// 不使用 bundle 給的 file、thumb（不可信的字串不能變成路徑）。
func (pa *preparedArtifact) prepareFrames(b *bundle.Bundle, used map[string]bool) error {
	data, _ := b.File(pa.src.Path)
	var man framesManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&man); err != nil || dec.More() {
		return errContent(pa.src.Path, " frames JSON")
	}
	seenN := map[int]bool{}
	imgs := map[int][]byte{}
	paths := map[int]string{}
	var keep []frameCandidate
	for _, c := range man.Candidates {
		if !c.Selected {
			continue
		}
		if c.N < 1 || c.N > maxBundleFrame || seenN[c.N] || c.AtMS < 0 || c.AtMS > maxTimeMS ||
			!utf8.ValidString(c.Caption) || !utf8.ValidString(c.Chapter) || len(c.Caption) > 4096 || len(c.Chapter) > 2048 {
			return errContent(pa.src.Path, " frames JSON")
		}
		img := bundle.ArtifactsDir + bundleFrameName(strings.ToLower(pa.src.ID), c.N)
		jpg, ok := b.File(img)
		if !ok {
			img = bundle.ArtifactsDir + bundleFrameName(pa.src.ID, c.N)
			jpg, ok = b.File(img)
		}
		if !ok {
			return bundleInvalid(pa.src.Path, "bundle 中的檔案「%s」選中的畫面 %d 沒有圖片，已拒絕匯入。", c.N)
		}
		// 擷取畫面的解碼上限（frames.MaxDecodePixels）比 bundle 的單張上限小：在這裡先擋，否則縮圖時才失敗。
		if px, ok := bundle.JPEGPixels(jpg); !ok || px > frames.MaxDecodePixels {
			return errContent(img, " JPEG 圖片")
		}
		used[img] = true
		seenN[c.N] = true
		imgs[c.N], paths[c.N] = jpg, img
		keep = append(keep, c)
	}
	// 產品的畫面編號是 1..len(candidates)（FrameImage 依此檢查）：選中的畫面依原本的編號排序後重新編號。
	sort.Slice(keep, func(i, j int) bool { return keep[i].N < keep[j].N })
	pa.frames, pa.framePaths = map[int][]byte{}, map[int]string{}
	for i := range keep {
		n := i + 1
		pa.frames[n], pa.framePaths[n] = imgs[keep[i].N], paths[keep[i].N]
		keep[i].N, keep[i].File, keep[i].Thumb, keep[i].Excluded = n, frameFile(n), frameThumb(n), ""
	}
	man.Candidates = nonNilCandidates(keep)
	body, err := json.Marshal(man)
	if err != nil {
		return err
	}
	pa.variant, pa.format, pa.data = KindFrames, framesFmt, body
	return nil
}

// placeArtifacts 把要匯入的產出寫到暫存目錄，再搬到 artifacts/<item>/；回傳搬過去的路徑（失敗時由呼叫端刪除）。
// 檔案一律以 0600 建立一般檔案（BND N3）。
// 縮圖要完整解碼：每張之間檢查 ctx，解碼失敗是 bundle 的內容錯誤（結構檢查不保證熵編碼的資料正確）。
func (p *Pipeline) placeArtifacts(ctx context.Context, tmp, itemID string, arts []preparedArtifact) ([]string, error) {
	type move struct{ from, to string }
	var moves []move
	for i, a := range arts {
		if a.skip {
			continue
		}
		f := filepath.Join(tmp, fmt.Sprintf("%04d.json", i))
		if err := writeFileSync(f, a.data); err != nil {
			return nil, err
		}
		moves = append(moves, move{f, filepath.Join(p.dataDir, filepath.FromSlash(artifactRel(itemID, a.id)))})
		if a.kind == KindFrames {
			dir := filepath.Join(tmp, fmt.Sprintf("%04d-frames", i))
			if err := os.Mkdir(dir, dirPerm); err != nil {
				return nil, err
			}
			for n, jpg := range a.frames {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				full := filepath.Join(dir, frameFile(n))
				if err := writeFileSync(full, jpg); err != nil {
					return nil, err
				}
				if err := writeThumb(full, filepath.Join(dir, frameThumb(n))); err != nil {
					var fe *os.PathError
					if errors.As(err, &fe) {
						return nil, err
					}
					return nil, errContent(a.framePaths[n], " JPEG 圖片")
				}
			}
			moves = append(moves, move{dir, framesDir(p.dataDir, itemID, a.id)})
		}
	}
	var placed []string
	for _, mv := range moves {
		if err := os.MkdirAll(filepath.Dir(mv.to), dirPerm); err != nil {
			return placed, err
		}
		if err := os.Rename(mv.from, mv.to); err != nil {
			return placed, err
		}
		placed = append(placed, mv.to)
	}
	return placed, nil
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
