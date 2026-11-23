package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 筆記與模板的錯誤（API 的 404／409，NOTE-R11～R14）。
var (
	// ErrNoNote：影音沒有筆記（GET note 回 404、預覽回 409，code 是 no_note）。
	ErrNoNote = errors.New("這部影音還沒有筆記。")
	// ErrBuiltinTemplate：內建模板不能修改（409 builtin_template）。
	ErrBuiltinTemplate = errors.New("內建的筆記模板不能修改，請先複製。")
	// ErrTemplateExists：已經有同 id 的模板（409 template_exists）。
	ErrTemplateExists = errors.New("已經有同 id 的筆記模板。")
	// ErrTemplateNotFound：找不到模板（404）。
	ErrTemplateNotFound = errors.New("找不到這個筆記模板。")
)

// maxReadButton 是模板規則 read_button 的上限（位元組）。
const maxReadButton = 4096

var templateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// noteRules 是模板規則（rules_json）：「標記已閱讀」按鈕（N2）與要執行的自動檢查（nil 代表全部，NOTE-R7）。
type noteRules struct {
	ReadButton string `json:"read_button,omitempty"`
	// Checks 不加 omitempty：空清單（全部關閉）與 nil（全部）要能區分。
	Checks []string `json:"checks"`
}

// noteTemplate 是解析後的模板版本。
type noteTemplate struct {
	ID        string
	Version   int
	Name      string
	Builtin   bool
	Body      string
	Rules     noteRules
	SHA256    string
	CreatedAt time.Time
}

// Ref 是來源紀錄的 template（NOTE-R13）：<id>@<版本>。
func (t noteTemplate) Ref() string { return t.ID + "@" + strconv.Itoa(t.Version) }

// builtinNoteTemplate 是內建模板 video-notes（版本 1，不寫入資料庫）。
func builtinNoteTemplate() noteTemplate {
	return noteTemplate{
		ID: notes.BuiltinTemplateID, Version: 1, Name: "Video Notes", Builtin: true, Body: notes.BuiltinTemplateBody,
		SHA256: sha256Hex([]byte(notes.BuiltinTemplateBody)), CreatedAt: time.Unix(0, 0).UTC(),
	}
}

func noteTemplateFromStore(t store.NoteTemplate) noteTemplate {
	var rules noteRules
	_ = json.Unmarshal([]byte(t.RulesJSON), &rules) // 寫入時已驗證；讀不懂就當成沒有規則
	return noteTemplate{ID: t.ID, Version: t.Version, Name: t.Name, Body: t.Body, Rules: rules, SHA256: t.SHA256, CreatedAt: t.CreatedAt}
}

// lookupNoteTemplate 解析 "<id>" 或 "<id>@<版本>"（沒有版本時用最新版本）；找不到時回傳 ErrTemplateNotFound。
func (p *Pipeline) lookupNoteTemplate(ctx context.Context, ref string) (noteTemplate, error) {
	if ref == "" {
		ref = notes.BuiltinTemplateID
	}
	id, ver := splitPromptRef(ref)
	version := 0
	if ver != "" {
		n, err := strconv.Atoi(ver)
		if err != nil || n < 1 {
			return noteTemplate{}, ErrTemplateNotFound
		}
		version = n
	}
	if id == notes.BuiltinTemplateID {
		if version > 1 {
			return noteTemplate{}, ErrTemplateNotFound
		}
		return builtinNoteTemplate(), nil
	}
	r := p.st.Read()
	var (
		t   store.NoteTemplate
		err error
	)
	if version == 0 {
		t, err = r.LatestNoteTemplate(ctx, id)
	} else {
		t, err = r.NoteTemplateVersion(ctx, id, version)
	}
	if errors.Is(err, store.ErrNotFound) {
		return noteTemplate{}, ErrTemplateNotFound
	}
	if err != nil {
		return noteTemplate{}, err
	}
	return noteTemplateFromStore(t), nil
}

// resolveNoteTemplate 是 note 步驟用的模板：找不到時以 permanent 失敗並說明（處理方案引用了不存在的模板）。
func (p *Pipeline) resolveNoteTemplate(ctx context.Context, ref string) (noteTemplate, error) {
	t, err := p.lookupNoteTemplate(ctx, ref)
	if errors.Is(err, ErrTemplateNotFound) {
		return noteTemplate{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "找不到筆記模板 " + ref,
			Message: fmt.Sprintf("找不到筆記模板「%s」，請檢查處理方案的設定。", ref), Actions: []string{actionOpenTemplate}}
	}
	return t, err
}

// NoteTemplate 是 API 的一個筆記模板（NOTE-R11）。
type NoteTemplate struct {
	ID         string
	Name       string
	Version    int
	Builtin    bool
	Body       string
	ReadButton string
	// Checks 是要執行的自動檢查；nil 代表全部。
	Checks    []string
	SHA256    string
	CreatedAt time.Time
}

func (t noteTemplate) public() NoteTemplate {
	return NoteTemplate{
		ID: t.ID, Name: t.Name, Version: t.Version, Builtin: t.Builtin, Body: t.Body,
		ReadButton: t.Rules.ReadButton, Checks: t.Rules.Checks, SHA256: t.SHA256, CreatedAt: t.CreatedAt,
	}
}

// NoteTemplates 列出內建模板與每個使用者模板的最新版本（內建在前）。
func (p *Pipeline) NoteTemplates(ctx context.Context) ([]NoteTemplate, error) {
	out := []NoteTemplate{builtinNoteTemplate().public()}
	list, err := p.st.Read().NoteTemplates(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		out = append(out, noteTemplateFromStore(t).public())
	}
	return out, nil
}

// NoteTemplateByRef 回傳一個模板（"<id>" 或 "<id>@<版本>"）；找不到時回傳 ErrTemplateNotFound。
func (p *Pipeline) NoteTemplateByRef(ctx context.Context, ref string) (NoteTemplate, error) {
	t, err := p.lookupNoteTemplate(ctx, ref)
	if err != nil {
		return NoteTemplate{}, err
	}
	return t.public(), nil
}

// NoteTemplateInput 是建立或修改模板的內容；nil 的欄位代表沒有指定（修改時沿用上一個版本）。
type NoteTemplateInput struct {
	ID string
	// From 是要複製的模板（建立時）；沒有指定 body 時沿用它的內文與規則。
	From       string
	Name       *string
	Body       *string
	ReadButton *string
	Checks     *[]string
}

// validateNoteTemplate 檢查模板的內文與規則（NOTE-R11）。
func validateNoteTemplate(t noteTemplate) error {
	var fs []validate.FieldError
	if _, err := notes.ParseTemplate(t.Body); err != nil {
		var te *notes.TemplateError
		msg := err.Error()
		if errors.As(err, &te) {
			msg = te.Message
		}
		fs = append(fs, validate.FieldError{Path: "body", Message: msg})
	}
	if len(t.Rules.ReadButton) > maxReadButton || !utf8.ValidString(t.Rules.ReadButton) {
		fs = append(fs, validate.FieldError{Path: "read_button", Message: fmt.Sprintf("read_button 最多 %d 位元組，而且必須是 UTF-8。", maxReadButton)})
	}
	for i, c := range t.Rules.Checks {
		if !slices.Contains(notes.CheckIDs, c) {
			fs = append(fs, validate.FieldError{Path: fmt.Sprintf("checks[%d]", i), Message: fmt.Sprintf("不認識的檢查「%s」；可用的檢查：%s。", c, strings.Join(notes.CheckIDs, "、"))})
		}
	}
	if utf8.RuneCountInString(t.Name) > 120 {
		fs = append(fs, validate.FieldError{Path: "name", Message: "name 最多 120 字。"})
	}
	if len(fs) > 0 {
		return invalidFields(fs)
	}
	return nil
}

func applyTemplateInput(t *noteTemplate, in NoteTemplateInput) {
	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if in.Body != nil {
		t.Body = strings.ReplaceAll(*in.Body, "\r\n", "\n")
	}
	if in.ReadButton != nil {
		t.Rules.ReadButton = *in.ReadButton
	}
	if in.Checks != nil {
		t.Rules.Checks = slices.Clone(*in.Checks)
		if t.Rules.Checks == nil {
			t.Rules.Checks = []string{}
		}
	}
}

func (p *Pipeline) saveNoteTemplate(ctx context.Context, create bool, t noteTemplate) (NoteTemplate, error) {
	if err := validateNoteTemplate(t); err != nil {
		return NoteTemplate{}, err
	}
	rules, err := json.Marshal(t.Rules)
	if err != nil {
		return NoteTemplate{}, err
	}
	saved, err := p.st.Write().AddNoteTemplateVersion(ctx, create, store.NoteTemplate{
		ID: t.ID, Name: t.Name, Body: t.Body, RulesJSON: string(rules), SHA256: sha256Hex([]byte(t.Body)),
	})
	switch {
	case errors.Is(err, store.ErrNoteTemplateExists):
		return NoteTemplate{}, ErrTemplateExists
	case errors.Is(err, store.ErrNotFound):
		return NoteTemplate{}, ErrTemplateNotFound
	case err != nil:
		return NoteTemplate{}, err
	}
	return noteTemplateFromStore(saved).public(), nil
}

// CreateNoteTemplate 建立模板（版本 1，NOTE-R11）：有 from 時複製該模板，再套用指定的欄位；
// 內建模板的 id 不能使用（ErrBuiltinTemplate），已存在的 id 回傳 ErrTemplateExists。
func (p *Pipeline) CreateNoteTemplate(ctx context.Context, in NoteTemplateInput) (NoteTemplate, error) {
	if !templateIDPattern.MatchString(in.ID) {
		return NoteTemplate{}, invalidFields([]validate.FieldError{{Path: "id", Message: "id 只能用小寫英數字與 -（1～64 字元），開頭必須是英數字。"}})
	}
	if in.ID == notes.BuiltinTemplateID {
		return NoteTemplate{}, ErrBuiltinTemplate
	}
	t := noteTemplate{ID: in.ID}
	switch {
	case in.From != "":
		src, err := p.lookupNoteTemplate(ctx, in.From)
		if errors.Is(err, ErrTemplateNotFound) {
			return NoteTemplate{}, invalidFields([]validate.FieldError{{Path: "from", Message: fmt.Sprintf("找不到筆記模板「%s」。", in.From)}})
		}
		if err != nil {
			return NoteTemplate{}, err
		}
		t.Body, t.Rules, t.Name = src.Body, src.Rules, src.Name
	case in.Body == nil:
		return NoteTemplate{}, invalidFields([]validate.FieldError{{Path: "body", Message: "需要 body 或 from。"}})
	}
	t.ID = in.ID
	applyTemplateInput(&t, in)
	return p.saveNoteTemplate(ctx, true, t)
}

// UpdateNoteTemplate 以上一個版本為基礎套用指定的欄位，建立新版本（NOTE-R11）。內建模板回傳 ErrBuiltinTemplate。
func (p *Pipeline) UpdateNoteTemplate(ctx context.Context, id string, in NoteTemplateInput) (NoteTemplate, error) {
	if id == notes.BuiltinTemplateID {
		return NoteTemplate{}, ErrBuiltinTemplate
	}
	cur, err := p.lookupNoteTemplate(ctx, id)
	if err != nil {
		return NoteTemplate{}, err
	}
	if strings.Contains(id, "@") {
		return NoteTemplate{}, ErrTemplateNotFound
	}
	applyTemplateInput(&cur, in)
	return p.saveNoteTemplate(ctx, false, cur)
}

// ---- 讀取筆記（NOTE-R10、R13、R14） ----

// noteRender 是重新套用模板需要的資料（預覽，NOTE-R12）：時間、畫面與資訊表的附加列，寫筆記時一併記錄。
type noteRender struct {
	Status   string              `json:"status"`
	CueStart map[int]int64       `json:"cue_start"`
	Frames   map[int]noteFrameAt `json:"frames"`
	Extra    []notes.InfoRow     `json:"extra"`
}

type noteFrameAt struct {
	AtMS    int64  `json:"at_ms"`
	Caption string `json:"caption"`
}

// NoteView 是 GET /api/items/:id/note 的內容（NOTE-R13）。
type NoteView struct {
	Lang        string
	Markdown    string
	Frontmatter map[string]string
	Checks      []notes.Check
	Stale       bool
	StaleReason string
}

// currentNote 回傳影音現行的筆記；lang 為空時用最近寫出的一份。沒有時回傳 ErrNoNote。
func (p *Pipeline) currentNote(ctx context.Context, itemID, lang string) (store.Artifact, noteDoc, error) {
	arts, err := p.st.Read().CurrentArtifacts(ctx, itemID, KindNote)
	if err != nil {
		return store.Artifact{}, noteDoc{}, err
	}
	var pick *store.Artifact
	for i := range arts {
		a := arts[i]
		if lang != "" && !sameLang(a.Lang, lang) {
			continue
		}
		if pick == nil || !a.CreatedAt.Before(pick.CreatedAt) {
			pick = &a
		}
	}
	if pick == nil {
		return store.Artifact{}, noteDoc{}, ErrNoNote
	}
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(pick.Path)))
	if err != nil {
		return store.Artifact{}, noteDoc{}, err
	}
	if sha256Hex(b) != pick.SHA256 {
		return store.Artifact{}, noteDoc{}, fmt.Errorf("%s：%w", pick.Path, ErrCorruptArtifact)
	}
	var doc noteDoc
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		return store.Artifact{}, noteDoc{}, err
	}
	return *pick, doc, nil
}

// ItemNote 回傳影音現行的筆記（NOTE-R13）。stale 表示筆記的輸入字幕已不是現在會選的字幕（例如匯入了外部編輯的字幕，NOTE-R10）。
// 錯誤：store.ErrNotFound、*InTrashError、ErrNoNote。
func (p *Pipeline) ItemNote(ctx context.Context, itemID, lang string) (NoteView, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return NoteView{}, err
	}
	if it.DeletedAt != nil {
		return NoteView{}, &InTrashError{ItemID: it.ID}
	}
	art, doc, err := p.currentNote(ctx, it.ID, lang)
	if err != nil {
		return NoteView{}, err
	}
	v := NoteView{Lang: doc.Lang, Markdown: doc.Markdown, Frontmatter: doc.Frontmatter, Checks: doc.Checks}
	if v.Checks == nil {
		v.Checks = []notes.Check{}
	}
	if cur, ok, err := translateSource(ctx, r, it.ID, ""); err != nil {
		return NoteView{}, err
	} else if ok && len(art.Parents) > 0 && cur.ID != art.Parents[0] {
		v.Stale, v.StaleReason = true, NoteStaleReason
	}
	return v, nil
}

// NotePreview 是模板預覽的結果（NOTE-R12）。
type NotePreview struct {
	Markdown string
	Checks   []notes.Check
}

// PreviewNoteTemplate 以影音現行筆記的 sections 套用模板（NOTE-R12）：不呼叫 LLM、不建立產出、不寫入任何檔案。
// 錯誤：ErrTemplateNotFound、store.ErrNotFound、*InTrashError、ErrNoNote（影音沒有筆記）。
func (p *Pipeline) PreviewNoteTemplate(ctx context.Context, templateRef, itemRef, lang string) (NotePreview, error) {
	tpl, err := p.lookupNoteTemplate(ctx, templateRef)
	if err != nil {
		return NotePreview{}, err
	}
	it, err := p.itemByIDOrRef(ctx, itemRef)
	if err != nil {
		return NotePreview{}, err
	}
	if it.DeletedAt != nil {
		return NotePreview{}, &InTrashError{ItemID: it.ID}
	}
	_, doc, err := p.currentNote(ctx, it.ID, lang)
	if err != nil {
		return NotePreview{}, err
	}
	rd := doc.Render
	fr := make(map[int]notes.Frame, len(rd.Frames))
	for n, f := range rd.Frames {
		fr[n] = notes.Frame{AtMS: f.AtMS, Caption: f.Caption}
	}
	in := p.noteInputFor(it, doc.Lang, rd.Status, subtitle.Transcript{}, nil, fr, rd.Extra)
	in.CueStart = rd.CueStart
	out := llm.NoteOutput{SelfCheck: doc.SelfCheck}
	for _, s := range doc.Sections {
		out.Sections = append(out.Sections, llm.NoteSection{Level: s.Level, Title: s.Title, Cue: s.Cue, Body: s.Body, Frames: s.Frames})
	}
	pv, err := renderNote(in, out, tpl)
	if err != nil {
		var te *notes.TemplateError
		if errors.As(err, &te) {
			return NotePreview{}, invalidFields([]validate.FieldError{{Path: "body", Message: te.Message}})
		}
		return NotePreview{}, err
	}
	return NotePreview{Markdown: pv.Markdown, Checks: pv.Checks}, nil
}

// ---- 驗收測試的 seeding（只給 harness 使用，同 SeedTranscript） ----

// SeedChapter 是 SeedChapters 的一個章節；Cue 為 0 代表平台章節（沒有句號）。
type SeedChapter struct {
	Title   string
	StartMS int64
	Cue     int
}

// SeedChapters 直接寫入影音現行的 chapters 產出（steps.md 4.13 `the chapters of … are:`）：有任何章節帶句號時 origin 是 llm，
// 否則是 platform。只給驗收測試的 harness 使用（ADR-0015）；沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (p *Pipeline) SeedChapters(ctx context.Context, itemID string, list []SeedChapter) error {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	doc := Chapters{Origin: ChapterOriginPlatform, Chapters: []Chapter{}}
	for _, c := range list {
		if c.Cue > 0 {
			doc.Origin = ChapterOriginLLM
		}
		doc.Chapters = append(doc.Chapters, Chapter{Title: c.Title, StartMS: c.StartMS, Cue: c.Cue})
	}
	slices.SortStableFunc(doc.Chapters, func(a, b Chapter) int { return int(a.StartMS - b.StartMS) })
	fillChapterEnds(doc.Chapters, durationMS(it), 0)
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	prov, err := p.provenance(map[string]any{"stage": StageChapters, "impl_version": p.implVersion(StageChapters), "origin": doc.Origin, "seeded": true}, nil)
	if err != nil {
		return err
	}
	art, err := p.newChaptersArtifact(it.ID, body, nil, prov)
	if err != nil {
		return err
	}
	return p.st.Tx(ctx, func(r *store.Repo) error {
		_, err := r.InsertArtifact(ctx, *art)
		return err
	})
}

// SeedFrame 是 SeedFrames 的一張畫面（steps.md 4.14 `the frames of … are:`）。
type SeedFrame struct {
	AtMS     int64
	Selected bool
	Caption  string
	Chapter  string
	// Color 是純色圖片的顏色（#rrggbb）；空字串是灰色。
	Color string
}

// SeedFrames 直接寫入影音現行的 frames 產出與 1280x720 的純色圖片（steps.md 4.14）；編號依順序從 1 起算。
// 只給驗收測試的 harness 使用（ADR-0015）；沒有任何 HTTP 端點或 CLI 指令呼叫它。
func (p *Pipeline) SeedFrames(ctx context.Context, itemID string, list []SeedFrame) error {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	const w, h = 1280, 720
	version := store.NewID()
	dir := framesDir(p.dataDir, it.ID, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	man := framesManifest{
		SourceResolution: fmt.Sprintf("%dx%d", w, h), ExtractResolution: fmt.Sprintf("%dx%d", w, h), Width: w, Height: h,
		Select: selectRules, Candidates: []frameCandidate{},
	}
	for i, f := range list {
		n := i + 1
		img, err := seedJPEG(w, h, f.Color)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, frameFile(n)), img, filePerm); err != nil {
			return err
		}
		if err := writeThumb(filepath.Join(dir, frameFile(n)), filepath.Join(dir, frameThumb(n))); err != nil {
			return err
		}
		man.Candidates = append(man.Candidates, frameCandidate{
			N: n, AtMS: f.AtMS, File: frameFile(n), Thumb: frameThumb(n), Selected: f.Selected, Caption: f.Caption, Chapter: f.Chapter,
		})
		if f.Selected {
			man.Target++
		}
	}
	body, err := json.Marshal(man)
	if err != nil {
		return err
	}
	prov, err := p.provenance(map[string]any{"stage": StageFrames, "impl_version": p.implVersion(StageFrames), "seeded": true}, nil)
	if err != nil {
		return err
	}
	rel, sha, size, err := writeArtifactFile(p.dataDir, it.ID, version, func(wr io.Writer) error {
		_, werr := wr.Write(body)
		return werr
	})
	if err != nil {
		return err
	}
	return p.st.Tx(ctx, func(r *store.Repo) error {
		_, err := r.InsertArtifact(ctx, store.NewArtifact{
			ID: version, ItemID: it.ID, Kind: KindFrames, Variant: KindFrames, Format: framesFmt,
			Path: rel, SHA256: sha, Size: size, Provenance: prov,
		})
		return err
	})
}

// seedJPEG 產生一張純色的 JPEG；hex 是 #rrggbb，空字串是灰色。
func seedJPEG(w, h int, hex string) ([]byte, error) {
	c := color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	if hex != "" {
		v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
		if err != nil || len(strings.TrimPrefix(hex, "#")) != 6 {
			return nil, fmt.Errorf("顏色應是 #rrggbb：%q", hex)
		}
		c = color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := frames.EncodeJPEG(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
