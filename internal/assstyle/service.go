package assstyle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 錯誤；用 errors.Is 辨識。
var (
	// ErrBuiltin 表示對內建樣式做了修改（OUT-R6）：內建樣式只能複製。
	ErrBuiltin = errors.New("內建的樣式不能修改，請複製成自訂樣式。")
	// ErrExists 表示已經有同名的樣式（內建或自訂）。
	ErrExists = errors.New("已經有同名的樣式。")
	// ErrNotFound 表示沒有這個樣式。
	ErrNotFound = errors.New("找不到這個樣式。")
)

// Service 管理自訂樣式；內建樣式不在資料庫。
type Service struct{ st *store.Store }

// New 建立 Service。
func New(st *store.Store) *Service { return &Service{st: st} }

// stored 是存進 style_json 的內容。
type stored struct {
	Primary     subtitle.ASSLine   `json:"primary"`
	Translation subtitle.ASSLine   `json:"translation"`
	Layout      subtitle.ASSLayout `json:"layout"`
}

func fromRow(r store.AssStyle) (Style, error) {
	var s stored
	if err := json.Unmarshal([]byte(r.StyleJSON), &s); err != nil {
		return Style{}, fmt.Errorf("樣式 %s 的內容損毀：%w", r.Name, err)
	}
	return Style{ID: r.Name, Name: r.Name, Primary: s.Primary, Translation: s.Translation, Layout: s.Layout}, nil
}

func toJSON(st subtitle.ASSStyle) (string, error) {
	b, err := json.Marshal(stored{Primary: st.Primary, Translation: st.Translation, Layout: st.Layout})
	return string(b), err
}

// List 列出所有樣式：內建的在前（固定順序），接著是自訂的（依名稱）。
func (s *Service) List(ctx context.Context) ([]Style, error) {
	rows, err := s.st.Read().AssStyles(ctx)
	if err != nil {
		return nil, err
	}
	out := Builtin()
	custom := make([]Style, 0, len(rows))
	for _, r := range rows {
		st, err := fromRow(r)
		if err != nil {
			return nil, err
		}
		custom = append(custom, st)
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Name < custom[j].Name })
	return append(out, custom...), nil
}

// Get 以名稱取得樣式（內建或自訂）；不存在時回傳 ErrNotFound。
func (s *Service) Get(ctx context.Context, name string) (Style, error) {
	if b, ok := BuiltinByName(name); ok {
		return b, nil
	}
	r, err := s.st.Read().AssStyle(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return Style{}, ErrNotFound
	}
	if err != nil {
		return Style{}, err
	}
	return fromRow(r)
}

// checkName 檢查新樣式的名稱：格式與不能和內建重名。
func checkName(name string) error {
	if !ValidName(name) {
		return &InvalidError{Fields: []validate.FieldError{{Path: "name", Message: "名稱只能用小寫英文字母、數字、「.」「_」「-」，最長 64 個字元，並以字母或數字開頭。"}}}
	}
	if _, ok := BuiltinByName(name); ok {
		return fmt.Errorf("%w（%s 是內建樣式的名稱）", ErrExists, name)
	}
	return nil
}

// Create 建立自訂樣式：以 from（內建或自訂樣式；空字串用 default）為底，套用 patch；內容驗證通過才寫入。
func (s *Service) Create(ctx context.Context, name, from string, p Patch) (Style, error) {
	if err := checkName(name); err != nil {
		return Style{}, err
	}
	if from == "" {
		from = defaultName
	}
	base, err := s.Get(ctx, from)
	if errors.Is(err, ErrNotFound) {
		return Style{}, &InvalidError{Fields: []validate.FieldError{{Path: "from", Message: fmt.Sprintf("找不到要複製的樣式 %q。", from)}}}
	}
	if err != nil {
		return Style{}, err
	}
	return s.insert(ctx, name, p.Apply(base.ASS()))
}

func (s *Service) insert(ctx context.Context, name string, st subtitle.ASSStyle) (Style, error) {
	if errs := Validate(st); len(errs) > 0 {
		return Style{}, &InvalidError{Fields: errs}
	}
	js, err := toJSON(st)
	if err != nil {
		return Style{}, err
	}
	row, err := s.st.Write().CreateAssStyle(ctx, name, js)
	if errors.Is(err, store.ErrConflict) {
		return Style{}, ErrExists
	}
	if err != nil {
		return Style{}, err
	}
	return fromRow(row)
}

// Update 修改自訂樣式；內建的回傳 ErrBuiltin（先於內容驗證），不存在回傳 ErrNotFound。
func (s *Service) Update(ctx context.Context, name string, p Patch) (Style, error) {
	if _, ok := BuiltinByName(name); ok {
		return Style{}, ErrBuiltin
	}
	cur, err := s.Get(ctx, name)
	if err != nil {
		return Style{}, err
	}
	st := p.Apply(cur.ASS())
	if errs := Validate(st); len(errs) > 0 {
		return Style{}, &InvalidError{Fields: errs}
	}
	js, err := toJSON(st)
	if err != nil {
		return Style{}, err
	}
	row, err := s.st.Write().UpdateAssStyle(ctx, name, js)
	if errors.Is(err, store.ErrNotFound) {
		return Style{}, ErrNotFound
	}
	if err != nil {
		return Style{}, err
	}
	return fromRow(row)
}

// ImportOptions 指定匯入時使用 .ass 檔中的哪些樣式。
type ImportOptions struct {
	// TranslationStyle 是譯文行使用的樣式名稱；空字串時用名為 Default 的樣式，沒有就用第一個。
	TranslationStyle string
	// PrimaryStyle 是原文行使用的樣式名稱；空字串時用檔案中的下一個樣式，只有一個樣式時由譯文行縮小產生。
	PrimaryStyle string
}

// Import 從 .ass 檔的 [V4+ Styles] 匯入成自訂樣式（OUT-R8）。檔案沒有這個段落、或段落中沒有任何樣式時回傳 *InvalidError。
func (s *Service) Import(ctx context.Context, data []byte, name string, opt ImportOptions) (Style, error) {
	if err := checkName(name); err != nil {
		return Style{}, err
	}
	st, err := StyleFromASS(data, opt)
	if err != nil {
		return Style{}, err
	}
	return s.insert(ctx, name, st)
}

// StyleFromASS 把 .ass 檔的樣式轉成 ASSStyle（還沒驗證、也還沒存）。沒有 [V4+ Styles] 或沒有任何樣式時回傳 *InvalidError。
// 欄位依欄名對應；沒有出現的欄位用內建 default 的值。PlayResY 有寫時，字級、描邊、陰影與邊距換算成 1080 高的畫布。
func StyleFromASS(data []byte, opt ImportOptions) (subtitle.ASSStyle, error) {
	f, err := subtitle.ParseASS(data)
	if err != nil {
		return subtitle.ASSStyle{}, &InvalidError{Msg: "無法解析這個 ASS 檔。"}
	}
	if !f.HasStyles {
		return subtitle.ASSStyle{}, &InvalidError{Msg: "這個 ASS 檔沒有 [V4+ Styles] 段落。"}
	}
	if len(f.Styles) == 0 {
		return subtitle.ASSStyle{}, &InvalidError{Msg: "這個 ASS 檔的 [V4+ Styles] 沒有任何樣式。"}
	}
	find := func(name string) (int, bool) {
		for i, r := range f.Styles {
			if equalFold(r.Name, name) {
				return i, true
			}
		}
		return 0, false
	}
	ti := 0
	if opt.TranslationStyle != "" {
		i, ok := find(opt.TranslationStyle)
		if !ok {
			return subtitle.ASSStyle{}, &InvalidError{Fields: []validate.FieldError{{Path: "translation_style", Message: fmt.Sprintf("檔案中沒有樣式 %q。", opt.TranslationStyle)}}}
		}
		ti = i
	} else if i, ok := find("Default"); ok {
		ti = i
	}
	pi := -1
	if opt.PrimaryStyle != "" {
		i, ok := find(opt.PrimaryStyle)
		if !ok {
			return subtitle.ASSStyle{}, &InvalidError{Fields: []validate.FieldError{{Path: "primary_style", Message: fmt.Sprintf("檔案中沒有樣式 %q。", opt.PrimaryStyle)}}}
		}
		pi = i
	} else {
		for i := range f.Styles {
			if i != ti {
				pi = i
				break
			}
		}
	}

	base := Default().ASS()
	scale := 1.0
	if f.PlayResY > 0 {
		scale = 1080 / float64(f.PlayResY)
	}
	out := base
	row := f.Styles[ti]
	out.Translation = lineFromRow(base.Translation, row, scale)
	applyLayout(&out.Layout, row, scale)
	if pi >= 0 {
		out.Primary = lineFromRow(base.Primary, f.Styles[pi], scale)
	} else {
		// 只有一個樣式：原文行沿用它，字級縮小成約 70%（與內建 default 的 36／52 接近）。
		out.Primary = out.Translation
		out.Primary.Size = max(1, out.Translation.Size*7/10)
	}
	return out, nil
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// shortColour 把完全不透明的 #RRGGBBFF 簡寫成 #RRGGBB。
func shortColour(c string) string {
	if len(c) == 9 && c[7:] == "FF" {
		return c[:7]
	}
	return c
}

func lineFromRow(base subtitle.ASSLine, r subtitle.ASSStyleRow, scale float64) subtitle.ASSLine {
	l := base
	l.Font = r.Font // 匯入的字型一律明確指定（不跟著語言自動換）
	if r.Size > 0 {
		l.Size = max(1, int(r.Size*scale+0.5))
	}
	if r.Color != "" {
		l.Color = shortColour(r.Color)
	}
	if r.OutlineColor != "" {
		l.OutlineColor = shortColour(r.OutlineColor)
	}
	if r.BackColor != "" {
		l.BackColor = shortColour(r.BackColor)
	}
	if r.HasBold {
		l.Bold = r.Bold
	}
	if r.HasOutline {
		l.Outline = round1(r.Outline * scale)
	}
	if r.HasShadow {
		l.Shadow = round1(r.Shadow * scale)
	}
	return l
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func applyLayout(l *subtitle.ASSLayout, r subtitle.ASSStyleRow, scale float64) {
	if r.HasBorderStyle {
		l.BorderStyle = r.BorderStyle
	}
	if r.HasAlignment {
		l.Alignment = r.Alignment
	}
	if r.HasMargin {
		l.MarginV = min(1080, int(float64(r.MarginV)*scale+0.5))
	}
}
