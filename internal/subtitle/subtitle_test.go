package subtitle

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseTimecode(t *testing.T) {
	ok := map[string]int64{
		"00:00:01":     1000,
		"00:00:01.000": 1000,
		"00:00:01,500": 1500,
		"00:00:01.5":   1500,
		"01:02.250":    62250,
		"00:00:03.800": 3800,
		"00:00:10":     10000,
		"01:02:03.004": 3723004,
		"100:00:00":    360000000,
	}
	for in, want := range ok {
		got, err := ParseTimecode(in)
		if err != nil || got != want {
			t.Errorf("ParseTimecode(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1", "::", "00:60:00", "00:00:60", "00:00:01.", "00:00:01.1234",
		"-0:00:01", "00:00:0x", "00:00:01.5.5", "12345:00:00", "1:2:3:4", "00:00:01 ", "+1:00:00"} {
		if in == "00:00:01 " {
			continue // 前後空白會被修剪
		}
		if got, err := ParseTimecode(in); err == nil {
			t.Errorf("ParseTimecode(%q) = %d, want error", in, got)
		}
	}
}

func TestFormatTime(t *testing.T) {
	if got := FormatSRTTime(1000); got != "00:00:01,000" {
		t.Error(got)
	}
	if got := FormatVTTTime(3723004); got != "01:02:03.004" {
		t.Error(got)
	}
	if got := FormatDisplayTime(1000); got != "00:00:01.000" {
		t.Error(got)
	}
	if got := FormatDisplayTime(-5); got != "00:00:00.000" {
		t.Error(got)
	}
	// 解析與格式化互為反向。
	for _, ms := range []int64{0, 1, 999, 61001, 3600000, 86399999} {
		got, err := ParseTimecode(FormatSRTTime(ms))
		if err != nil || got != ms {
			t.Errorf("round trip %d -> %d, %v", ms, got, err)
		}
	}
}

func TestWordsRoundTrip(t *testing.T) {
	s := "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900"
	w, err := ParseWords(s)
	if err != nil {
		t.Fatal(err)
	}
	want := []Word{{"hello", 1000, 1800}, {"world", 2100, 2900}}
	if !reflect.DeepEqual(w, want) {
		t.Fatalf("got %+v", w)
	}
	if FormatWords(w) != s {
		t.Fatalf("format = %s", FormatWords(w))
	}
	if w, err := ParseWords(""); err != nil || w != nil {
		t.Fatalf("empty: %v %v", w, err)
	}
	for _, bad := range []string{"hello", "hello@", "@00:00:01-00:00:02", "a@00:00:01", "a@x-00:00:02", "a@00:00:01-y"} {
		if _, err := ParseWords(bad); err == nil {
			t.Errorf("ParseWords(%q) should fail", bad)
		}
	}
}

// generateVTT 複製 features/internal/faketool 的 GenerateVTT 格式
// （該套件是 features 下的 internal，這裡無法匯入）。
func generateVTT(id, lang string, n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "WEBVTT\nKind: captions\nLanguage: %s\n\n", lang)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s --> %s\nSentence %d of %s (%s).\n\n", fakeVTTTime(i*2), fakeVTTTime(i*2+2), i+1, id, lang)
	}
	return sb.String()
}

func fakeVTTTime(sec int) string {
	return fmt.Sprintf("%02d:%02d:%02d.000", sec/3600, sec%3600/60, sec%60)
}

func TestParseVTT(t *testing.T) {
	t.Run("faketool format", func(t *testing.T) {
		cues, err := ParseVTT([]byte(generateVTT("abc123xxxxx", "en", 3)))
		if err != nil {
			t.Fatal(err)
		}
		if len(cues) != 3 {
			t.Fatalf("got %d cues", len(cues))
		}
		c := cues[2]
		if c.Index != 3 || c.StartMS != 4000 || c.EndMS != 6000 || c.Text != "Sentence 3 of abc123xxxxx (en)." || c.Timing != TimingSource {
			t.Fatalf("cue = %+v", c)
		}
	})

	t.Run("header NOTE STYLE settings and identifiers", func(t *testing.T) {
		src := "\uFEFFWEBVTT - some title\r\nKind: captions\r\n\r\n" +
			"NOTE this is a comment\r\nover two lines\r\n\r\n" +
			"STYLE\r\n::cue { color: red }\r\n\r\n" +
			"intro\r\n00:00:01.000 --> 00:00:02.500 align:start position:0%\r\nHello &amp; <i>bye</i>\r\n\r\n" +
			"01:02.000 --> 01:03.000\r\nshort form\r\nsecond line\r\n"
		cues, err := ParseVTT([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		want := []Cue{
			{Index: 1, StartMS: 1000, EndMS: 2500, Text: "Hello & <i>bye</i>", Timing: TimingSource},
			{Index: 2, StartMS: 62000, EndMS: 63000, Text: "short form\nsecond line", Timing: TimingSource},
		}
		if !reflect.DeepEqual(cues, want) {
			t.Fatalf("got %+v", cues)
		}
	})

	t.Run("youtube auto captions word timings", func(t *testing.T) {
		src := "WEBVTT\nKind: captions\nLanguage: en\n\n" +
			"00:00:01.000 --> 00:00:04.000 align:start position:0%\n" +
			"so today<00:00:01.500><c> we</c><00:00:02.000><c> talk</c><00:00:03.000><c> about</c>\n\n"
		cues, err := ParseVTT([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(cues) != 1 || cues[0].Text != "so today we talk about" {
			t.Fatalf("got %+v", cues)
		}
		want := []Word{{"so today", 1000, 1500}, {"we", 1500, 2000}, {"talk", 2000, 3000}, {"about", 3000, 4000}}
		if !reflect.DeepEqual(cues[0].Words, want) {
			t.Fatalf("words = %+v", cues[0].Words)
		}
	})

	t.Run("roll-up cue with a plain first line", func(t *testing.T) {
		src := "WEBVTT\n\n00:00:02.000 --> 00:00:04.000\nold line\nnew<00:00:02.500><c.colorE5E5E5> line</c>\n"
		cues, err := ParseVTT([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if cues[0].Text != "old line\nnew line" || len(cues[0].Words) != 2 {
			t.Fatalf("got %+v", cues[0])
		}
	})

	t.Run("timestamp outside the cue is clamped", func(t *testing.T) {
		src := "WEBVTT\n\n00:00:02.000 --> 00:00:03.000\na<00:00:09.000><c> b</c><00:00:01.000><c> c</c>\n"
		cues, err := ParseVTT([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range cues[0].Words {
			if w.StartMS < 2000 || w.EndMS > 3000 || w.EndMS < w.StartMS {
				t.Fatalf("word out of cue: %+v", w)
			}
		}
	})

	t.Run("errors carry cue and line", func(t *testing.T) {
		_, err := ParseVTT([]byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nok\n\n00:00:05.000 --> 00:00:04.000\nbad\n"))
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Cue != 2 || pe.Line != 6 {
			t.Fatalf("err = %v", err)
		}
		if pe.Error() == "" {
			t.Fatal("empty message")
		}
		for _, bad := range []string{
			"WEBVTT\n\nxx --> 00:00:02.000\ntext\n",
			"WEBVTT\n\n00:00:01.000 --> nope\ntext\n",
			"WEBVTT\n00:00:01.000 --> \ntext\n",
			"WEBVTT\n\n00:00:01.000 00:00:02.000 --> 00:00:03.000\ntext\n",
		} {
			if _, err := ParseVTT([]byte(bad)); err == nil {
				t.Errorf("expected error for %q", bad)
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		cues, err := ParseVTT(nil)
		if err != nil || len(cues) != 0 {
			t.Fatalf("%v %v", cues, err)
		}
	})
}

func TestParseSRT(t *testing.T) {
	src := "\uFEFF1\r\n00:00:01,000 --> 00:00:03,500\r\nWe use Kafka for this.\r\n\r\n" +
		"2\r\n00:00:04,000 --> 00:00:06,000 X1:0 X2:1\r\nline one\r\nline two\r\n\r\n\r\n" +
		"00:00:07,000 --> 00:00:08,000\r\nno index\r\n"
	cues, err := ParseSRT([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Cue{
		{Index: 1, StartMS: 1000, EndMS: 3500, Text: "We use Kafka for this.", Timing: TimingSource},
		{Index: 2, StartMS: 4000, EndMS: 6000, Text: "line one\nline two", Timing: TimingSource},
		{Index: 3, StartMS: 7000, EndMS: 8000, Text: "no index", Timing: TimingSource},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Fatalf("got %+v", cues)
	}

	// OUT-R15：時間不合法時指出第幾句。
	_, err = ParseSRT([]byte("1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:01,000\nsecond\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Cue != 2 || pe.Line != 6 {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"1\njust text\n", "a\nb\nc\n00:00:01,000 --> 00:00:02,000\n", "1\nzz --> 00:00:02,000\nx\n"} {
		if _, err := ParseSRT([]byte(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	if cues, err := ParseSRT(nil); err != nil || len(cues) != 0 {
		t.Fatal(cues, err)
	}
}

func sampleTranscript() Transcript {
	return Transcript{Lang: "en", Variant: VariantCorrected, Cues: []Cue{
		{Index: 1, StartMS: 1000, EndMS: 3500, Text: "We use Kafka for this.", Timing: TimingSource},
		{Index: 2, StartMS: 4000, EndMS: 6000, Text: "It is a log.", Timing: TimingSource},
	}}
}

func render(t *testing.T, f func(*bytes.Buffer) error) string {
	t.Helper()
	var b bytes.Buffer
	if err := f(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderers(t *testing.T) {
	tr := sampleTranscript()
	srt := render(t, func(b *bytes.Buffer) error { return RenderSRT(b, tr.Cues) })
	wantSRT := "1\n00:00:01,000 --> 00:00:03,500\nWe use Kafka for this.\n\n2\n00:00:04,000 --> 00:00:06,000\nIt is a log.\n\n"
	if srt != wantSRT {
		t.Errorf("srt = %q", srt)
	}
	vtt := render(t, func(b *bytes.Buffer) error { return RenderVTT(b, tr.Cues) })
	wantVTT := "WEBVTT\n\n00:00:01.000 --> 00:00:03.500\nWe use Kafka for this.\n\n00:00:04.000 --> 00:00:06.000\nIt is a log.\n\n"
	if vtt != wantVTT {
		t.Errorf("vtt = %q", vtt)
	}
	txt := render(t, func(b *bytes.Buffer) error { return RenderTXT(b, tr.Cues) })
	if txt != "We use Kafka for this.\nIt is a log.\n" {
		t.Errorf("txt = %q", txt)
	}

	// 渲染結果能被解析器讀回。
	back, err := ParseSRT([]byte(srt))
	if err != nil || len(back) != 2 || back[1].Text != "It is a log." {
		t.Errorf("srt round trip: %v %+v", err, back)
	}
	back, err = ParseVTT([]byte(vtt))
	if err != nil || len(back) != 2 || back[0].StartMS != 1000 {
		t.Errorf("vtt round trip: %v %+v", err, back)
	}
}

func TestRenderSanitizesCueText(t *testing.T) {
	cues := []Cue{{StartMS: 0, EndMS: 1000, Text: "a --> b\n\n  c  \n<i>&"}}
	srt := render(t, func(b *bytes.Buffer) error { return RenderSRT(b, cues) })
	if srt != "1\n00:00:00,000 --> 00:00:01,000\na -> b\nc\n<i>&\n\n" {
		t.Errorf("srt = %q", srt)
	}
	vtt := render(t, func(b *bytes.Buffer) error { return RenderVTT(b, cues) })
	if !strings.Contains(vtt, "a --&gt; b\nc\n&lt;i&gt;&amp;\n") {
		t.Errorf("vtt = %q", vtt)
	}
	txt := render(t, func(b *bytes.Buffer) error { return RenderTXT(b, cues) })
	if txt != "a --> b c <i>&\n" {
		t.Errorf("txt = %q", txt)
	}
	if got := render(t, func(b *bytes.Buffer) error { return RenderTXT(b, nil) }); got != "" {
		t.Errorf("empty txt = %q", got)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestRenderPropagatesWriteErrors(t *testing.T) {
	tr := sampleTranscript()
	for _, f := range []string{FormatSRT, FormatVTT, FormatTXT, FormatCuesJSON} {
		if err := Render(failWriter{}, f, tr); err == nil {
			t.Errorf("%s: expected write error", f)
		}
	}
	if err := Render(&bytes.Buffer{}, "ass", tr); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("err = %v", err)
	}
}

func TestContentTypeAndExt(t *testing.T) {
	// OUT-subtitle-output.feature: Every M1 format is available
	tests := []struct{ format, contains, ext string }{
		{"srt", "application/x-subrip", "srt"},
		{"vtt", "text/vtt", "vtt"},
		{"txt", "text/plain", "txt"},
		{"cues-json", "application/json", "json"},
	}
	for _, tt := range tests {
		if got := ContentType(tt.format); !strings.Contains(got, tt.contains) {
			t.Errorf("ContentType(%q) = %q", tt.format, got)
		}
		if got := FileExt(tt.format); got != tt.ext {
			t.Errorf("FileExt(%q) = %q", tt.format, got)
		}
	}
	if ContentType("nope") != "application/octet-stream" || FileExt("nope") != "" {
		t.Error("unknown format")
	}
}

func TestCuesJSON(t *testing.T) {
	sp := "s1"
	tr := Transcript{Lang: "en", Variant: VariantNormalized, Cues: []Cue{
		{Index: 1, StartMS: 1000, EndMS: 3000, Text: "hello \"x\" 你好", Timing: TimingMerged,
			Words: []Word{{"hello", 1000, 1800}}, Speaker: &sp},
		{Index: 2, StartMS: 3000, EndMS: 4000, Text: "b", Timing: TimingSource},
	}}
	out := render(t, func(b *bytes.Buffer) error { return RenderCuesJSON(b, tr) })
	for _, want := range []string{`"start_ms":1000`, `"end_ms":3000`, `"speakers":[]`, `"speaker":null`, `"speaker":"s1"`, `"timing":"merged"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	got, err := DecodeCuesJSON(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	tr.Speakers = []Speaker{}
	if !reflect.DeepEqual(got, tr) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, tr)
	}

	empty := render(t, func(b *bytes.Buffer) error {
		return RenderCuesJSON(b, Transcript{Lang: "en", Variant: VariantSource})
	})
	if _, err := DecodeCuesJSON(strings.NewReader(empty)); err != nil {
		t.Fatalf("%s: %v", empty, err)
	}

	for _, bad := range []string{
		`{`,
		`{"lang":"en","variant":"nope","cues":[]}`,
		`{"lang":"en","variant":"source","cues":[{"start_ms":1,"end_ms":2,"text":"x","timing":"magic"}]}`,
		`{"lang":"en","variant":"source","cues":[{"start_ms":5,"end_ms":2,"text":"x","timing":"source"}]}`,
		`{"lang":"en","variant":"source","cues":[{"start_ms":-1,"end_ms":2,"text":"x"}]}`,
	} {
		if _, err := DecodeCuesJSON(strings.NewReader(bad)); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

// 場景 "A time range keeps the original timestamps"。
func TestFilterRange(t *testing.T) {
	cues := sampleTranscript().Cues
	tests := []struct {
		name         string
		from, to     int64
		wantTexts    []string
		wantStartsMS []int64
	}{
		{"scenario from 3.8s to 10s", 3800, 10000, []string{"It is a log."}, []int64{4000}},
		{"overlap at the start counts", 3000, 3900, []string{"We use Kafka for this."}, []int64{1000}},
		{"touching the end does not overlap", 3500, 4000, nil, nil},
		{"whole range", 0, 100000, []string{"We use Kafka for this.", "It is a log."}, []int64{1000, 4000}},
		{"open ended", 5000, -1, []string{"It is a log."}, []int64{4000}},
		{"before everything", 0, 500, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterRange(cues, tt.from, tt.to)
			if len(got) != len(tt.wantTexts) {
				t.Fatalf("got %+v", got)
			}
			for i, c := range got {
				if c.Text != tt.wantTexts[i] || c.StartMS != tt.wantStartsMS[i] {
					t.Errorf("cue %d = %+v", i, c)
				}
			}
		})
	}
}

// 場景 "The most processed variant is used by default"。
func TestPickVariant(t *testing.T) {
	tests := []struct {
		name string
		in   []Variant
		want Variant
		ok   bool
	}{
		{"corrected beats normalized (scenario)", []Variant{VariantNormalized, VariantCorrected}, VariantCorrected, true},
		{"edited wins", []Variant{VariantSource, VariantTranslated, VariantEdited, VariantCorrected}, VariantEdited, true},
		{"translated over corrected", []Variant{VariantCorrected, VariantTranslated}, VariantTranslated, true},
		{"segmented over normalized", []Variant{VariantNormalized, VariantSegmented}, VariantSegmented, true},
		{"normalized over source", []Variant{VariantSource, VariantNormalized}, VariantNormalized, true},
		{"source only", []Variant{VariantSource}, VariantSource, true},
		{"nothing", nil, "", false},
		{"unknown only", []Variant{"bogus"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PickVariant(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("got %q, %v", got, ok)
			}
		})
	}
}

func TestVariantAndTimingValid(t *testing.T) {
	if !VariantEdited.Valid() || Variant("x").Valid() {
		t.Error("Variant.Valid")
	}
	if !TimingModel.Valid() || TimingOrigin("x").Valid() {
		t.Error("TimingOrigin.Valid")
	}
}

// 場景 "The download file name is safe on Windows"（OUT-R4）。
func TestDownloadFileName(t *testing.T) {
	long := strings.Repeat("你", 100) // 300 位元組
	tests := []struct {
		name, title, lang, ext, want string
	}{
		{"scenario", "Kafka: in 100 seconds?", "en", "srt", "Kafka in 100 seconds.en.srt"},
		{"all forbidden chars", `a<b>c:d"e/f\g|h?i*j`, "en", "vtt", "abcdefghij.en.vtt"},
		{"control chars and runs of space", "a\x00\tb \n  c\x7f", "en", "txt", "a b c.en.txt"},
		{"trailing dots and spaces", "Title... ", "en", "srt", "Title.en.srt"},
		{"empty becomes untitled", "", "en", "srt", "untitled.en.srt"},
		{"only forbidden becomes untitled", "???", "en", "srt", "untitled.en.srt"},
		{"windows reserved", "con", "en", "srt", "_con.en.srt"},
		{"windows reserved with extension part", "CON.foo", "en", "srt", "_CON.foo.en.srt"},
		{"windows reserved with several dots", "nul.tar.gz", "en", "srt", "_nul.tar.gz.en.srt"},
		{"reserved prefix only is fine", "CONSOLE.foo", "en", "srt", "CONSOLE.foo.en.srt"},
		{"bidi controls removed", "a‮b‪c⁦d⁩e‎f‏g", "en", "srt", "abcdefg.en.srt"},
		{"cjk kept", "影片：測試", "zh-Hant-TW", "srt", "影片：測試.zh-Hant-TW.srt"},
		{"no lang", "x", "", "srt", "x.srt"},
		{"ext with dot", "x", "en", ".srt", "x.en.srt"},
		{"long title truncated on rune boundary", long, "en", "srt", strings.Repeat("你", 66) + ".en.srt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DownloadFileName(tt.title, tt.lang, tt.ext); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContentDisposition(t *testing.T) {
	got := ContentDisposition("Kafka in 100 seconds.en.srt")
	want := `attachment; filename="Kafka in 100 seconds.en.srt"; filename*=UTF-8''Kafka%20in%20100%20seconds.en.srt`
	if got != want {
		t.Fatalf("got %s", got)
	}
	got = ContentDisposition("影片\"x\\.srt")
	want = `attachment; filename="___x_.srt"; filename*=UTF-8''%E5%BD%B1%E7%89%87%22x%5C.srt`
	if got != want {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(ContentDisposition(DownloadFileName("Kafka: in 100 seconds?", "en", "srt")), "Kafka in 100 seconds.en.srt") {
		t.Fatal("scenario header text missing")
	}
}

// words 欄位的時間可以是時間碼或秒數（SEG 場景的 `We@1.0-1.3`）；格式不對的拒絕。
func TestParseWordsAcceptsSeconds(t *testing.T) {
	got, err := ParseWords("We@1.0-1.3 use@00:00:01.300-00:00:01.700 ok@2-2.25")
	if err != nil {
		t.Fatal(err)
	}
	want := []Word{{Text: "We", StartMS: 1000, EndMS: 1300}, {Text: "use", StartMS: 1300, EndMS: 1700}, {Text: "ok", StartMS: 2000, EndMS: 2250}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d = %+v，預期 %+v", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"a@1.-2", "a@1.2345-2", "a@x-2", "a@-1-2", "a@1e3-2"} {
		if _, err := ParseWords(bad); err == nil {
			t.Errorf("%q 應該失敗", bad)
		}
	}
}
