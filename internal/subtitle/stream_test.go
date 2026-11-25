package subtitle

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func streamAll(t *testing.T, in string) (Transcript, error) {
	t.Helper()
	cr, err := NewCuesReader(strings.NewReader(in))
	if err != nil {
		return Transcript{}, err
	}
	tr := cr.Header
	for {
		c, ok, err := cr.Next()
		if err != nil {
			return Transcript{}, err
		}
		if !ok {
			return tr, nil
		}
		tr.Cues = append(tr.Cues, c)
	}
}

func TestCuesReaderMatchesDecodeCuesJSON(t *testing.T) {
	sp := "s1"
	tr := Transcript{Lang: "en", Variant: VariantSegmented, Speakers: []Speaker{{ID: "s1", Label: "A"}}, Cues: []Cue{
		{Index: 1, StartMS: 0, EndMS: 1000, Text: "hello\nworld", Timing: TimingSource, Speaker: &sp},
		{Index: 2, StartMS: 1000, EndMS: 2500, Text: "a <b> & c --> d", Timing: TimingInterpolated, Words: []Word{{Text: "a", StartMS: 1000, EndMS: 1100}}},
	}}
	var buf bytes.Buffer
	if err := RenderCuesJSON(&buf, tr); err != nil {
		t.Fatal(err)
	}
	want, err := DecodeCuesJSON(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := streamAll(t, buf.String())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("串流解碼 = %+v\n整份解碼 = %+v", got, want)
	}
	// 每種單軌格式：逐句寫出與 Render 完全相同。
	for _, f := range []string{FormatSRT, FormatVTT, FormatTXT, FormatCuesJSON} {
		var a, b bytes.Buffer
		if err := Render(&a, f, want); err != nil {
			t.Fatal(err)
		}
		cw, err := NewCueWriter(&b, f, got)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got.Cues {
			if err := cw.Write(c); err != nil {
				t.Fatal(err)
			}
		}
		if err := cw.Close(); err != nil {
			t.Fatal(err)
		}
		if a.String() != b.String() {
			t.Errorf("%s：逐句輸出與 Render 不同\n%q\n%q", f, b.String(), a.String())
		}
	}
	if _, err := NewCueWriter(&buf, "ass", got); err != ErrUnsupportedFormat {
		t.Errorf("ass 應該不支援逐句輸出：%v", err)
	}
}

func TestCuesReaderRejectsWhatDecodeRejects(t *testing.T) {
	bad := map[string]string{
		"不是物件":      `[1]`,
		"變體不認識":     `{"lang":"en","variant":"bogus","cues":[]}`,
		"沒有 cues":   `{"lang":"en","variant":"source"}`,
		"timing 錯":  `{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"x","timing":"nope"}]}`,
		"時間倒過來":     `{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":5,"end_ms":1,"text":"x"}]}`,
		"負的時間":      `{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":-1,"end_ms":1,"text":"x"}]}`,
		"cues 不是陣列": `{"lang":"en","variant":"source","cues":{}}`,
		"截斷":        `{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"x"}`,
		"cues 重複":   `{"lang":"en","variant":"source","cues":[],"cues":[]}`,
		"鍵不是字串":     `{"lang":"en","variant":"source","cues":[]`,
		// 串流解碼要求 variant 在 cues 之前（RenderCuesJSON 的順序）。
		"variant 在後": `{"cues":[],"variant":"source"}`,
	}
	for name, in := range bad {
		if _, err := streamAll(t, in); err == nil {
			t.Errorf("%s：應該失敗", name)
		}
	}
	// cues 為 null、cues 之後的其他欄位、不認識的欄位：與 DecodeCuesJSON 一樣接受。
	for _, in := range []string{
		`{"lang":"en","variant":"source","cues":null}`,
		`{"x":{"y":[1]},"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"x"}],"extra":1}`,
	} {
		got, err := streamAll(t, in)
		want, werr := DecodeCuesJSON(strings.NewReader(in))
		if err != nil || werr != nil || len(got.Cues) != len(want.Cues) || got.Lang != "en" {
			t.Errorf("%s：%v %v %+v", in, err, werr, got)
		}
	}
}
