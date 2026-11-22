package subtitle

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testStyle() ASSStyle {
	return ASSStyle{
		Primary:     ASSLine{Font: "Noto Sans", Size: 36, Color: "#FFFFFF", OutlineColor: "#000000", Outline: 2},
		Translation: ASSLine{Font: "Noto Sans CJK TC", Size: 52, Color: "#FFFF00", OutlineColor: "#000000", Outline: 2.5, Bold: true},
		Layout:      ASSLayout{Show: ShowBoth, Order: OrderTranslationFirst, TranslationAt: AtStacked, Alignment: 2, MarginV: 48, BorderStyle: 1},
	}
}

func renderASS(t *testing.T, pairs []Pair, st ASSStyle) string {
	t.Helper()
	var b bytes.Buffer
	if err := RenderASS(&b, pairs, st, "T"); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func dialogues(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "Dialogue:") {
			out = append(out, l)
		}
	}
	return out
}

func TestPairCues(t *testing.T) {
	main := []Cue{{StartMS: 1000, EndMS: 3500, Text: "譯一"}, {StartMS: 4000, EndMS: 6000, Text: "譯二"}, {StartMS: 9000, EndMS: 9500, Text: "沒有原文"}}
	orig := []Cue{{StartMS: 900, EndMS: 3400, Text: "one"}, {StartMS: 3500, EndMS: 6100, Text: "two"}}
	got := PairCues(main, orig)
	if len(got) != 3 || got[0].Orig != "one" || got[1].Orig != "two" || got[2].Orig != "" {
		t.Fatalf("PairCues = %+v", got)
	}
	if !Bilingual(got) {
		t.Error("有配到原文時 Bilingual 應為 true")
	}
	if Bilingual(PairCues(main, nil)) {
		t.Error("沒有原文時 Bilingual 應為 false")
	}
	// 重疊相同時取較早的原文。
	tie := PairCues([]Cue{{StartMS: 1000, EndMS: 2000, Text: "x"}}, []Cue{{StartMS: 1000, EndMS: 1500, Text: "a"}, {StartMS: 1500, EndMS: 2000, Text: "b"}})
	if tie[0].Orig != "a" {
		t.Errorf("平手應取較早的原文：%+v", tie)
	}
}

func TestRenderBilingualSRT(t *testing.T) {
	var b bytes.Buffer
	err := RenderBilingualSRT(&b, []Pair{{StartMS: 1000, EndMS: 3500, Main: "我們用 Kafka。", Orig: "We use Kafka."}, {StartMS: 4000, EndMS: 6000, Main: "只有譯文"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:01,000 --> 00:00:03,500\n我們用 Kafka。\nWe use Kafka.\n\n2\n00:00:04,000 --> 00:00:06,000\n只有譯文\n\n"
	if b.String() != want {
		t.Errorf("RenderBilingualSRT =\n%q\n預期\n%q", b.String(), want)
	}
}

func TestRenderASSStacked(t *testing.T) {
	out := renderASS(t, []Pair{{StartMS: 1005, EndMS: 3501, Main: "我們在這裡用 Kafka。", Orig: "We use Kafka for this."}}, testStyle())
	for _, want := range []string{
		"ScriptType: v4.00+", "PlayResX: 1920", "[V4+ Styles]",
		"Style: Translation,Noto Sans CJK TC,52,&H0000FFFF,", "Style: Primary,Noto Sans,36,&H00FFFFFF,",
		"Dialogue: 0,0:00:01.00,0:00:03.51,Translation,,0,0,0,,我們在這裡用 Kafka。\\N{\\rPrimary}We use Kafka for this.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("輸出缺少 %q\n%s", want, out)
		}
	}
	if n := len(dialogues(out)); n != 1 {
		t.Errorf("堆疊版面應只有一個事件，實際 %d", n)
	}
	// 原文在上。
	st := testStyle()
	st.Layout.Order = OrderPrimaryFirst
	got := dialogues(renderASS(t, []Pair{{EndMS: 1000, Main: "譯", Orig: "orig"}}, st))
	if len(got) != 1 || !strings.HasSuffix(got[0], ",Primary,,0,0,0,,orig\\N{\\rTranslation}譯") {
		t.Errorf("primary-first = %v", got)
	}
}

func TestRenderASSTranslationTopAndOnly(t *testing.T) {
	st := testStyle()
	st.Layout.TranslationAt = AtTop
	out := renderASS(t, []Pair{{EndMS: 1000, Main: "譯", Orig: "orig"}}, st)
	if d := dialogues(out); len(d) != 2 || !strings.Contains(d[0], ",Translation,") || !strings.Contains(d[1], ",Primary,") {
		t.Errorf("translation-top 應有兩個事件：%v", d)
	}
	if !strings.Contains(out, "Style: Translation,Noto Sans CJK TC,52,&H0000FFFF,&H0000FFFF,&H00000000,&H7F000000,-1,0,0,0,100,100,0,0,1,2.5,0,8,60,60,48,1") {
		t.Errorf("譯文行應靠上（Alignment 8）：\n%s", out)
	}

	st = testStyle()
	st.Layout.Show = ShowTranslation
	if d := dialogues(renderASS(t, []Pair{{EndMS: 1000, Main: "譯", Orig: "orig"}}, st)); len(d) != 1 || strings.Contains(d[0], "orig") {
		t.Errorf("translation-only 不應顯示原文：%v", d)
	}
	st.Layout.Show = ShowPrimary
	if d := dialogues(renderASS(t, []Pair{{EndMS: 1000, Main: "譯", Orig: "orig"}}, st)); len(d) != 1 || !strings.Contains(d[0], ",Primary,") || strings.Contains(d[0], "譯") {
		t.Errorf("primary-only = %v", d)
	}
	// 單語：用譯文行的樣式。
	if d := dialogues(renderASS(t, []Pair{{EndMS: 1000, Main: "only"}}, testStyle())); len(d) != 1 || !strings.Contains(d[0], ",Translation,") {
		t.Errorf("單語 = %v", d)
	}
}

// cue 文字與樣式欄位是不可信的輸入：不能注入覆寫碼、換行或欄位。
func TestRenderASSNeutralisesInjection(t *testing.T) {
	st := testStyle()
	st.Translation.Font = "Evil,Font\nStyle: Injected,x"
	out := renderASS(t, []Pair{{EndMS: 1000, Main: "a {\\an8\\pos(1,2)} b\\Nc\x07\nsecond", Orig: "x\\h{y}"}}, st)
	d := dialogues(out)
	if len(d) != 1 {
		t.Fatalf("應只有一個事件：%v", d)
	}
	text := d[0][strings.Index(d[0], ",,0,0,0,,")+len(",,0,0,0,,"):]
	// 除了程式自己加的 \N 與 {\rPrimary}，文字中不能有任何反斜線或大括號。
	text = strings.Replace(strings.Replace(text, `\N`, "", -1), `{\rPrimary}`, "", 1)
	if strings.ContainsAny(text, `\{}`) {
		t.Errorf("文字仍含覆寫碼字元：%q", d[0])
	}
	if strings.Count(out, "\nStyle:") != 2 || strings.Contains(out, "\nStyle: Injected") {
		t.Errorf("樣式欄位注入了新的 Style 列：\n%s", out)
	}
	if strings.Contains(out, "Evil,Font") {
		t.Errorf("字型中的逗號沒有清掉：\n%s", out)
	}
}

func TestAssColourAndTime(t *testing.T) {
	cases := map[string]string{"#FFFFFF": "&H00FFFFFF", "#FF0000": "&H000000FF", "#000000B3": "&H4C000000", "#11223344": "&HBB332211"}
	for in, want := range cases {
		if got := assColour(in, "#000000"); got != want {
			t.Errorf("assColour(%q) = %s，預期 %s", in, got, want)
		}
	}
	if got := assColour("red", "#00FF00"); got != "&H0000FF00" {
		t.Errorf("不合法的顏色應用預設值：%s", got)
	}
	if ValidColour("#12345") || !ValidColour("#123456") || !ValidColour("#12345678") || ValidColour("123456") {
		t.Error("ValidColour 判斷錯誤")
	}
	for _, c := range []struct {
		ms   int64
		up   bool
		want string
	}{{0, false, "0:00:00.00"}, {1005, false, "0:00:01.00"}, {1001, true, "0:00:01.01"}, {3723450, false, "1:02:03.45"}, {-5, false, "0:00:00.00"}} {
		if got := assTime(c.ms, c.up); got != c.want {
			t.Errorf("assTime(%d,%v) = %s，預期 %s", c.ms, c.up, got, c.want)
		}
	}
}

const sampleASS = "\xef\xbb\xbf[Script Info]\nTitle: x\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, OutlineColour, Outline, Bold\n" +
	"Style: Default,Source Han Sans TC,54,&H00FFFFFF,&H00000000,2,-1\nStyle: Second,@Arial,30,&H0000FFFF,&H80000000,1.5,0\n\n" +
	"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
	"Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,It is a log, really.\\Nsecond line\n" +
	"Comment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,ignored\n" +
	"Dialogue: 0,0:00:01.00,0:00:03.50,Default,,0,0,0,,{\\an8\\b1}We use{\\i0} Kafka.\\h\n" +
	"Dialogue: 0,0:00:01.00,0:00:03.50,Default,,0,0,0,,{\\p1}m 0 0 l 10 10{\\p0}\n"

func TestParseASSStylesByColumnName(t *testing.T) {
	f, err := ParseASS([]byte(sampleASS))
	if err != nil {
		t.Fatal(err)
	}
	if !f.HasStyles || len(f.Styles) != 2 {
		t.Fatalf("styles = %+v", f.Styles)
	}
	d := f.Styles[0]
	if d.Name != "Default" || d.Font != "Source Han Sans TC" || d.Size != 54 || d.Color != "#FFFFFFFF" || d.OutlineColor != "#000000FF" ||
		d.Outline != 2 || !d.HasOutline || !d.Bold || !d.HasBold || d.HasShadow || d.HasAlignment {
		t.Errorf("Default = %+v", d)
	}
	s := f.Styles[1]
	if s.Font != "Arial" || s.Color != "#FFFF00FF" || s.OutlineColor != "#0000007F" || s.Outline != 1.5 || s.Bold {
		t.Errorf("Second = %+v", s)
	}
}

func TestParseASSEvents(t *testing.T) {
	f, err := ParseASS([]byte(sampleASS))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 2 {
		t.Fatalf("事件數 = %d（Comment 與繪圖不算）：%+v", len(f.Events), f.Events)
	}
	if f.Events[0].Text != "It is a log, really.\nsecond line" || f.Events[1].Text != "We use Kafka." {
		t.Errorf("文字 = %q、%q", f.Events[0].Text, f.Events[1].Text)
	}
	cues := f.Cues()
	if cues[0].StartMS != 1000 || cues[1].StartMS != 4000 || cues[0].Index != 1 || cues[1].Index != 2 {
		t.Errorf("Cues 應依時間排序並重新編號：%+v", cues)
	}
}

func TestParseASSNoStylesSection(t *testing.T) {
	f, err := ParseASS([]byte("[Script Info]\nTitle: x\n\n[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.HasStyles || len(f.Events) != 1 {
		t.Errorf("%+v", f)
	}
}

func TestParseASSBadTime(t *testing.T) {
	_, err := ParseASS([]byte("[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,ok\nDialogue: 0:00:09.00,0:00:02.00,bad\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Cue != 2 || pe.Line != 4 {
		t.Errorf("err = %v，預期第 2 句、第 4 行", err)
	}
}

func TestParseASSColour(t *testing.T) {
	for in, want := range map[string]string{
		"&H00FFFFFF": "#FFFFFFFF", "&H80000000": "#0000007F", "&HFF&": "#FF0000FF", "16777215": "#FFFFFFFF",
		"": "", "&H": "", "&HZZ": "", "&H123456789": "", "-3": "",
	} {
		if got := parseASSColour(in); got != want {
			t.Errorf("parseASSColour(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestParseImport(t *testing.T) {
	cues, format, err := ParseImport([]byte("1\n00:00:01,000 --> 00:00:03,500\nWe use Apache Kafka for this.\n"), "")
	if err != nil || format != ImportSRT || len(cues) != 1 || cues[0].Timing != TimingSource || cues[0].Index != 1 {
		t.Fatalf("srt: %+v %s %v", cues, format, err)
	}
	if _, format, err = ParseImport([]byte("WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n"), ""); err != nil || format != ImportVTT {
		t.Errorf("vtt: %s %v", format, err)
	}
	if _, format, err = ParseImport([]byte(sampleASS), ""); err != nil || format != ImportASS {
		t.Errorf("ass: %s %v", format, err)
	}
	// 沒有文字的 cue 會被拿掉並重新編號。
	cues, _, err = ParseImport([]byte("1\n00:00:01,000 --> 00:00:02,000\n\n\n2\n00:00:03,000 --> 00:00:04,000\nkept\n"), "srt")
	if err != nil || len(cues) != 1 || cues[0].Index != 1 || cues[0].Text != "kept" {
		t.Errorf("empty cue: %+v %v", cues, err)
	}
}

func TestParseImportErrors(t *testing.T) {
	cases := []struct {
		name, data string
		cue        int
	}{
		{"end before start", "1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:01,000\nsecond\n", 2},
		{"start goes backwards", "1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:03,000\nsecond\n", 2},
		{"garbage timing", "1\n00:00:01,000 --> nope\nx\n", 1},
		{"third cue", "1\n00:00:01,000 --> 00:00:02,000\na\n\n2\n00:00:03,000 --> 00:00:04,000\nb\n\n3\n00:00:03,500 --> 00:00:02,000\nc\n", 3},
	}
	for _, c := range cases {
		_, _, err := ParseImport([]byte(c.data), "srt")
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Cue != c.cue {
			t.Errorf("%s: err = %v，預期第 %d 句", c.name, err, c.cue)
		}
	}
	for _, data := range []string{"", "just some text\n", "\n\n"} {
		_, _, err := ParseImport([]byte(data), "")
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Msg != ErrNoCues.Error() && pe.Cue == 0 && pe.Msg == "" {
			t.Errorf("%q: err = %v", data, err)
		}
	}
	if _, _, err := ParseImport([]byte("x"), "docx"); err == nil {
		t.Error("不支援的格式應回傳錯誤")
	}
}

func TestParseImportASSOverlapAllowed(t *testing.T) {
	// 同一時間的兩個事件（雙語分上下兩行）不算時間不單調。
	data := "[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:03.00,top\nDialogue: 0:00:01.00,0:00:03.00,bottom\nDialogue: 0:00:02.00,0:00:04.00,later\n"
	cues, _, err := ParseImport([]byte(data), "ass")
	if err != nil || len(cues) != 3 {
		t.Fatalf("%+v %v", cues, err)
	}
}

func TestDetectImportFormat(t *testing.T) {
	if DetectImportFormat([]byte("\xef\xbb\xbfWEBVTT\n")) != ImportVTT || DetectImportFormat([]byte("[Script Info]\n")) != ImportASS || DetectImportFormat([]byte("1\n00:00:01,000 --> 00:00:02,000\nx")) != ImportSRT {
		t.Error("DetectImportFormat 判斷錯誤")
	}
	if ImportFormatFromName("A.SRT") != ImportSRT || ImportFormatFromName("a.ssa") != ImportASS || ImportFormatFromName("a.vtt") != ImportVTT || ImportFormatFromName("a.txt") != "" {
		t.Error("ImportFormatFromName 判斷錯誤")
	}
}
