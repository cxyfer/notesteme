package subtitle

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

// randomCues 產生依 StartMS 非遞減排列的句子：會有相同的開始時間、長度 0、彼此重疊、很長的句子與空字串。
func randomCues(rng *rand.Rand, n int) []Cue {
	cues := make([]Cue, n)
	var t int64
	for i := range cues {
		t += int64(rng.Intn(4)) * 500 // 0 代表與前一句同時開始
		dur := int64(rng.Intn(6)) * 400
		if rng.Intn(15) == 0 {
			dur = int64(rng.Intn(20000)) // 很長的句子，與後面很多句重疊
		}
		text := string(rune('a'+rng.Intn(26))) + string(rune('0'+i%10))
		if rng.Intn(10) == 0 {
			text = ""
		}
		cues[i] = Cue{Index: i + 1, StartMS: t, EndMS: t + dur, Text: text}
	}
	return cues
}

func collectPairs(t *testing.T, main, orig []Cue) ([]Pair, error) {
	t.Helper()
	var out []Pair
	var oi CueIter
	if orig != nil {
		oi = SliceIter(orig)
	}
	err := PairStream(SliceIter(main), oi, func(p Pair) error { out = append(out, p); return nil })
	return out, err
}

// TestPairStreamMatchesPairCues 是差分測試：依開始時間排序的輸入，PairStream 與 PairCues 的配對完全相同
// （平手取較早的原文句、長度 0、重疊、空字串、只有一邊有句子）。
func TestPairStreamMatchesPairCues(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for iter := 0; iter < 3000; iter++ {
		main := randomCues(rng, rng.Intn(30))
		var orig []Cue
		if rng.Intn(8) > 0 {
			orig = randomCues(rng, rng.Intn(30))
		}
		got, err := collectPairs(t, main, orig)
		if err != nil {
			t.Fatal(err)
		}
		want := PairCues(main, orig)
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 組：\nmain %+v\norig %+v\nstream %+v\nslice  %+v", iter, main, orig, got, want)
		}
	}
}

func TestPairStreamRejectsUnsorted(t *testing.T) {
	sorted := []Cue{{StartMS: 0, EndMS: 10, Text: "a"}, {StartMS: 10, EndMS: 20, Text: "b"}}
	unsorted := []Cue{{StartMS: 10, EndMS: 20, Text: "b"}, {StartMS: 0, EndMS: 10, Text: "a"}}
	for _, c := range []struct{ main, orig []Cue }{{unsorted, sorted}, {sorted, unsorted}, {sorted[:1], unsorted}} {
		if _, err := collectPairs(t, c.main, c.orig); !errors.Is(err, ErrUnsorted) {
			t.Fatalf("err = %v，want ErrUnsorted（main %+v、orig %+v）", err, c.main, c.orig)
		}
	}
	// 讀取錯誤原樣回傳。
	boom := errors.New("boom")
	bad := func() (Cue, bool, error) { return Cue{}, false, boom }
	if err := PairStream(SliceIter(sorted), bad, func(Pair) error { return nil }); !errors.Is(err, boom) {
		t.Fatalf("orig 的錯誤：%v", err)
	}
	if err := PairStream(bad, SliceIter(sorted), func(Pair) error { return nil }); !errors.Is(err, boom) {
		t.Fatalf("main 的錯誤：%v", err)
	}
	if err := PairStream(SliceIter(sorted), nil, func(Pair) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn 的錯誤：%v", err)
	}
}

// TestStreamWritersMatchRender：逐句寫出的 ASS 與雙語 SRT 與整份渲染的位元組相同。
func TestStreamWritersMatchRender(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	pairs := PairCues(randomCues(rng, 40), randomCues(rng, 40))
	for _, st := range []ASSStyle{{}, {Layout: ASSLayout{TranslationAt: AtTop}}, {Layout: ASSLayout{Order: OrderPrimaryFirst, Show: ShowBoth}},
		{Layout: ASSLayout{Show: ShowPrimary}}} {
		var a, b bytes.Buffer
		if err := RenderASS(&a, pairs, st, "t,i\ntle"); err != nil {
			t.Fatal(err)
		}
		aw := NewASSWriter(&b, st, "t,i\ntle")
		for _, p := range pairs {
			if err := aw.Write(p); err != nil {
				t.Fatal(err)
			}
		}
		if err := aw.Close(); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatalf("ASS 不同（%v）", err)
		}
	}
	var a, b bytes.Buffer
	if err := RenderBilingualSRT(&a, pairs); err != nil {
		t.Fatal(err)
	}
	sw := NewBilingualSRTWriter(&b)
	for _, p := range slices.Clone(pairs) {
		if err := sw.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := sw.Close(); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("雙語 SRT 不同（%v）", err)
	}
}
