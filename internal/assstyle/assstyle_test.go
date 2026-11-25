package assstyle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func newService(t *testing.T) *Service {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st)
}

func ptr[T any](v T) *T { return &v }

func TestBuiltinStyles(t *testing.T) {
	want := []string{"default", "translation-top", "translation-only", "large", "boxed", "minimal"}
	got := Builtin()
	if len(got) != len(want) {
		t.Fatalf("內建樣式 %d 個，預期 %d", len(got), len(want))
	}
	for i, s := range got {
		if s.Name != want[i] || s.ID != want[i] || !s.Builtin {
			t.Errorf("第 %d 個 = %+v，預期 %s", i, s, want[i])
		}
		if errs := Validate(s.ASS()); errs != nil {
			t.Errorf("%s 的內容不合法：%v", s.Name, errs)
		}
		if s.Translation.Font != "" || s.Primary.Font != "" {
			t.Errorf("%s 的字型應留空（依語言自動）", s.Name)
		}
	}
	if s, _ := BuiltinByName("translation-top"); s.Layout.TranslationAt != subtitle.AtTop {
		t.Error("translation-top 的譯文應放在上方")
	}
	if s, _ := BuiltinByName("translation-only"); s.Layout.Show != subtitle.ShowTranslation {
		t.Error("translation-only 只顯示譯文")
	}
	if s, _ := BuiltinByName("boxed"); s.Layout.BorderStyle != 3 {
		t.Error("boxed 應是不透明底框")
	}
	// 複本互不影響。
	a, b := Builtin(), Builtin()
	a[0].Translation.Size = 1
	if b[0].Translation.Size == 1 {
		t.Error("Builtin 應回傳新的複本")
	}
}

// 規格（OUT-R7）寫明的字型名稱：常數的值要和它一致，否則測試用常數比對時換了名稱也不會被發現。
func TestFontNamesMatchSpec(t *testing.T) {
	for got, want := range map[string]string{
		FontCJKTC: "Noto Sans CJK TC", FontCJKSC: "Noto Sans CJK SC", FontLatin: "Noto Sans",
		FontCJKJP: "Noto Sans CJK JP", FontCJKKR: "Noto Sans CJK KR",
	} {
		if got != want {
			t.Errorf("字型常數 = %q，預期 %q", got, want)
		}
	}
}

func TestFontFor(t *testing.T) {
	cases := map[string]string{
		"zh-Hant-TW": FontCJKTC, "zh-Hant": FontCJKTC, "zh-TW": FontCJKTC, "zh-HK": FontCJKTC, "zh_hant_tw": FontCJKTC, "yue": FontCJKTC, "zh-yue": FontCJKTC,
		"zh-Hans-CN": FontCJKSC, "zh-Hans": FontCJKSC, "zh-CN": FontCJKSC, "zh": FontCJKSC,
		"ja": FontCJKJP, "ko-KR": FontCJKKR,
		"en": FontLatin, "fr-CA": FontLatin, "de": FontLatin, "": FontLatin, "ru": FontLatin,
	}
	for lang, want := range cases {
		if got := FontFor(lang); got != want {
			t.Errorf("FontFor(%q) = %s，預期 %s", lang, got, want)
		}
	}
}

func TestResolveFonts(t *testing.T) {
	st := Default().ASS()
	got := ResolveFonts(st, "zh-Hant-TW", "en")
	if got.Translation.Font != FontCJKTC || got.Primary.Font != FontLatin {
		t.Errorf("ResolveFonts = %+v", got)
	}
	st.Translation.Font = "My Font"
	if got := ResolveFonts(st, "zh-Hant-TW", "en"); got.Translation.Font != "My Font" {
		t.Errorf("已指定的字型不應被覆蓋：%+v", got.Translation)
	}
}

func TestValidate(t *testing.T) {
	st := Default().ASS()
	if errs := Validate(st); errs != nil {
		t.Fatalf("內建 default 應該合法：%v", errs)
	}
	st.Translation.Size = 0
	st.Primary.Color = "red"
	st.Primary.Font = "a,b"
	st.Translation.Outline = -1
	st.Layout.Show = "none"
	st.Layout.Alignment = 10
	st.Layout.BorderStyle = 2
	st.Layout.MarginV = 5000
	paths := map[string]bool{}
	for _, e := range Validate(st) {
		paths[e.Path] = true
	}
	for _, want := range []string{"translation.size", "primary.color", "primary.font", "translation.outline", "layout.show", "layout.alignment", "layout.border_style", "layout.margin_v"} {
		if !paths[want] {
			t.Errorf("缺少 %s 的錯誤：%v", want, paths)
		}
	}
}

func TestPatchApply(t *testing.T) {
	base := Default().ASS()
	got := Patch{Primary: &LinePatch{Size: ptr(60)}, Layout: &LayoutPatch{Alignment: ptr(8)}}.Apply(base)
	if got.Primary.Size != 60 || got.Layout.Alignment != 8 || got.Translation != base.Translation || got.Primary.Color != base.Primary.Color {
		t.Errorf("Apply = %+v", got)
	}
	if base.Primary.Size == 60 {
		t.Error("Apply 不應修改輸入")
	}
}

func TestServiceCRUD(t *testing.T) {
	ctx := context.Background()
	s := newService(t)

	list, err := s.List(ctx)
	if err != nil || len(list) != 6 {
		t.Fatalf("List = %d 個，%v", len(list), err)
	}

	// 複製內建樣式成自訂樣式，並修改一個欄位。
	mine, err := s.Create(ctx, "mine", "large", Patch{Translation: &LinePatch{Size: ptr(80)}})
	if err != nil {
		t.Fatal(err)
	}
	large, _ := BuiltinByName("large")
	if mine.Builtin || mine.ID != "mine" || mine.Translation.Size != 80 || mine.Primary != large.Primary || mine.Layout != large.Layout {
		t.Errorf("複製的樣式 = %+v", mine)
	}
	if _, err := s.Create(ctx, "mine", "", Patch{}); !errors.Is(err, ErrExists) {
		t.Errorf("重複名稱 err = %v，預期 ErrExists", err)
	} else {
		assertTranslated(t, err)
	}
	if _, err := s.Create(ctx, "default", "", Patch{}); !errors.Is(err, ErrExists) {
		t.Errorf("與內建同名 err = %v，預期 ErrExists", err)
	} else {
		assertTranslated(t, err)
	}
	var inv *InvalidError
	for _, bad := range []string{"", "Mine", "-x", "a b", strings.Repeat("a", 65)} {
		if _, err := s.Create(ctx, bad, "", Patch{}); !errors.As(err, &inv) {
			t.Errorf("名稱 %q 應回傳 InvalidError：%v", bad, err)
		}
	}
	if _, err := s.Create(ctx, "x", "nope", Patch{}); !errors.As(err, &inv) {
		t.Errorf("找不到來源樣式：%v", err)
	}
	if _, err := s.Create(ctx, "x", "", Patch{Translation: &LinePatch{Size: ptr(0)}}); !errors.As(err, &inv) || inv.Fields[0].Path != "translation.size" {
		t.Errorf("不合法的內容：%v", err)
	}

	// 修改自訂樣式。
	upd, err := s.Update(ctx, "mine", Patch{Layout: &LayoutPatch{MarginV: ptr(100)}})
	if err != nil || upd.Layout.MarginV != 100 || upd.Translation.Size != 80 {
		t.Errorf("Update = %+v，%v", upd, err)
	}
	if _, err := s.Update(ctx, "mine", Patch{Layout: &LayoutPatch{Alignment: ptr(0)}}); !errors.As(err, &inv) {
		t.Errorf("Update 不合法的內容：%v", err)
	}
	got, _ := s.Get(ctx, "mine")
	if got.Layout.MarginV != 100 {
		t.Errorf("驗證失敗不應寫入：%+v", got)
	}
	// 內建的不能改，而且先於內容驗證。
	if _, err := s.Update(ctx, "default", Patch{Primary: &LinePatch{Size: ptr(-5)}}); !errors.Is(err, ErrBuiltin) {
		t.Errorf("Update 內建 err = %v，預期 ErrBuiltin", err)
	}
	if _, err := s.Update(ctx, "ghost", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update 不存在 err = %v", err)
	}
	if _, err := s.Get(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get 不存在 err = %v", err)
	}
	if list, _ = s.List(ctx); len(list) != 7 || list[6].Name != "mine" || list[6].Builtin {
		t.Errorf("List 應在內建之後列出自訂樣式：%+v", list)
	}
}

const fansub = "[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, OutlineColour, Outline\nStyle: Default,Source Han Sans TC,54,&H00FFFFFF,&H00000000,2\n"

func TestImport(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	st, err := s.Import(ctx, []byte(fansub), "fansub", ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "fansub" || st.Builtin || st.Translation.Font != "Source Han Sans TC" || st.Translation.Size != 54 ||
		st.Translation.Color != "#FFFFFF" || st.Translation.OutlineColor != "#000000" || st.Translation.Outline != 2 {
		t.Errorf("匯入的樣式 = %+v", st)
	}
	// 沒有出現的欄位用內建 default 的值；只有一個樣式時，原文行縮小。
	def := Default()
	if st.Translation.Shadow != def.Translation.Shadow || st.Layout != def.Layout || st.Primary.Size != 37 || st.Primary.Font != "Source Han Sans TC" {
		t.Errorf("預設值：%+v", st)
	}
	again, _ := s.Get(ctx, "fansub")
	if again != st {
		t.Errorf("存下來的與回傳的不同：%+v / %+v", again, st)
	}
	if _, err := s.Import(ctx, []byte(fansub), "fansub", ImportOptions{}); !errors.Is(err, ErrExists) {
		t.Errorf("重複匯入 err = %v", err)
	}
}

func TestImportErrors(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	var inv *InvalidError
	for name, data := range map[string]string{
		"沒有 styles 段落": "[Script Info]\nTitle: x\n\n[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,hi\n",
		"空檔案":          "",
		"段落是空的":        "[V4+ Styles]\nFormat: Name, Fontname\n",
		"只有 V4":        "[V4 Styles]\nFormat: Name\nStyle: a\n",
	} {
		_, err := s.Import(ctx, []byte(data), "x", ImportOptions{})
		if !errors.As(err, &inv) {
			t.Errorf("%s：err = %v，預期 InvalidError", name, err)
		}
	}
	if _, err := s.Import(ctx, []byte(fansub), "x", ImportOptions{TranslationStyle: "Nope"}); !errors.As(err, &inv) || inv.Fields[0].Path != "translation_style" {
		t.Errorf("指定不存在的樣式：%v", err)
	}
	if list, _ := s.List(ctx); len(list) != 6 {
		t.Errorf("匯入失敗不應留下樣式：%d", len(list))
	}
}

func TestImportTwoStylesAndPlayRes(t *testing.T) {
	data := "[Script Info]\nPlayResY: 288\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, OutlineColour, BackColour, Bold, Outline, Shadow, BorderStyle, Alignment, MarginV\n" +
		"Style: Eng,Arial,12,&H0000FFFF,&H00000000,&H80000000,0,1,0,1,2,10\n" +
		"Style: Default,Noto Sans CJK TC,18,&H00FFFFFF,&H00000000,&H80000000,-1,2,1,3,8,20\n"
	st, err := StyleFromASS([]byte(data), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 1080 / 288 = 3.75：Default（譯文行）18 → 68（67.5 進位），Eng（原文行）12 → 45。
	if st.Translation.Size != 68 || st.Primary.Size != 45 || st.Translation.Font != "Noto Sans CJK TC" || st.Primary.Font != "Arial" {
		t.Errorf("兩個樣式 = %+v", st)
	}
	if !st.Translation.Bold || st.Primary.Color != "#FFFF00" || st.Translation.Outline != 7.5 || st.Translation.Shadow != 3.8 {
		t.Errorf("欄位 = %+v", st)
	}
	if st.Layout.BorderStyle != 3 || st.Layout.Alignment != 8 || st.Layout.MarginV != 75 {
		t.Errorf("版面取自譯文行：%+v", st.Layout)
	}
	// 明確指定哪個是哪個。
	st, err = StyleFromASS([]byte(data), ImportOptions{TranslationStyle: "eng", PrimaryStyle: "DEFAULT"})
	if err != nil || st.Translation.Font != "Arial" || st.Primary.Font != "Noto Sans CJK TC" {
		t.Errorf("指定樣式 = %+v，%v", st, err)
	}
}

// assertTranslated：錯誤訊息會原樣成為 API 的 message，要在 apierr 的目錄中，英文版不含中文（NFR-I18N-2）。
func assertTranslated(t *testing.T, err error) {
	t.Helper()
	en, ok := apierr.Translate(err.Error(), apierr.LangEn)
	if !ok || strings.ContainsFunc(en, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		t.Errorf("錯誤訊息沒有完整翻譯：%q → %q", err.Error(), en)
	}
}
