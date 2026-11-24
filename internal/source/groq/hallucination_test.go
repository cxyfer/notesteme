package groq

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// SRC-R13：兩個條件都「高於」門檻才移除；剛好等於門檻、只符合一個條件、沒有回報欄位都保留。
func TestHallucinatedBoundaries(t *testing.T) {
	cases := []struct {
		nsp, cr float64
		want    bool
	}{
		{0.61, 2.41, true},
		{0.99, 10, true},
		{0.6, 2.41, false}, // no_speech_prob 剛好 0.6
		{0.61, 2.4, false}, // compression_ratio 剛好 2.4
		{0.6, 2.4, false},  // 兩者都剛好
		{0.95, 1.2, false}, // 只有 no_speech_prob 高
		{0.05, 3.5, false}, // 只有 compression_ratio 高
		{0, 0, false},      // 沒有回報
		{-1, 100, false},   // 不合理的負值
		{0.600001, 2.400001, true},
	}
	for _, c := range cases {
		if got := Hallucinated(c.nsp, c.cr); got != c.want {
			t.Errorf("Hallucinated(%v, %v) = %v，預期 %v", c.nsp, c.cr, got, c.want)
		}
	}
}

func TestTranscribeDropsHallucinatedSegments(t *testing.T) {
	body := `{"language":"english","duration":40,"segments":[
	  {"start":0,"end":5,"text":"Real speech.","no_speech_prob":0.1,"compression_ratio":1.3},
	  {"start":5,"end":10,"text":"Edge prob.","no_speech_prob":0.6,"compression_ratio":3.0},
	  {"start":10,"end":15,"text":"Edge ratio.","no_speech_prob":0.9,"compression_ratio":2.4},
	  {"start":15,"end":20,"text":"Thanks for watching!","no_speech_prob":0.9,"compression_ratio":2.5},
	  {"start":20,"end":25,"text":"Also real.","no_speech_prob":0.7,"compression_ratio":1.1}
	]}`
	srv := respond(http.StatusOK, body)
	defer srv.Close()
	res, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for i, c := range res.Cues {
		texts = append(texts, c.Text)
		if c.Index != i+1 {
			t.Errorf("cue %d 的 Index = %d，要連續編號", i, c.Index)
		}
	}
	want := []string{"Real speech.", "Edge prob.", "Edge ratio.", "Also real."}
	if len(texts) != len(want) {
		t.Fatalf("cues = %q，預期 %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("cues = %q，預期 %q", texts, want)
		}
	}
	// 片長仍是 Groq 回報的音檔長度，不因移除結尾的幻覺而縮短（SRC-R4 的涵蓋率以它為準）。
	if res.DurationS != 40 {
		t.Errorf("DurationS = %v，預期 40", res.DurationS)
	}
}

func TestTranscribeAllHallucinatedIsNoTranscript(t *testing.T) {
	srv := respond(http.StatusOK, `{"language":"english","duration":10,"segments":[
	  {"start":0,"end":5,"text":"Subtitles by the community.","no_speech_prob":0.95,"compression_ratio":2.9}]}`)
	defer srv.Close()
	_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
	if !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("err = %v，預期 ErrNoTranscript", err)
	}
}
