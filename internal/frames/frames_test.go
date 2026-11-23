package frames

import (
	"bytes"
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"
)

const showinfo = `[Parsed_showinfo_1 @ 0x0] n:   0 pts:    5000 pts_time:5.000 duration:1 fmt:yuv420p
[Parsed_metadata_2 @ 0x0] frame:0 pts:5000 pts_time:5.000
[Parsed_metadata_2 @ 0x0] lavfi.scene_score=0.900000
[Parsed_showinfo_1 @ 0x0] n:   1 pts:   40000 pts_time:40.000 duration:1 fmt:yuv420p
[Parsed_metadata_2 @ 0x0] frame:1 pts:40000 pts_time:40.000
[Parsed_metadata_2 @ 0x0] lavfi.scene_score=0.550000
garbage line pts_time:abc
[Parsed_showinfo_1 @ 0x0] n:   2 pts:   1 pts_time:NaN
`

func TestParseShowinfoPairsTimesWithScores(t *testing.T) {
	got := ParseShowinfo(showinfo)
	want := []Scene{{5000, 0.9}, {40000, 0.55}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestCandidatesFillQuietStretches(t *testing.T) {
	// FRM-R2 的例子：1 秒有場景、其餘低於門檻，片長 300 秒。
	scenes := []Scene{{1000, 0.9}, {60000, 0.1}, {120000, 0.1}, {180000, 0.1}, {240000, 0.1}}
	got := Candidates(scenes, 300_000, Defaults())
	var at []int64
	for _, c := range got {
		at = append(at, c.AtMS)
	}
	if want := []int64{1000, 61000, 121000, 181000, 241000}; !slices.Equal(at, want) {
		t.Fatalf("at = %v，預期 %v", at, want)
	}
	if got[0].Fill || !got[1].Fill {
		t.Errorf("Fill 標記不對：%+v", got)
	}
}

func TestCandidatesNoFillWhenDense(t *testing.T) {
	scenes := []Scene{{5000, .9}, {40000, .55}, {75000, .8}, {80000, .35}, {120000, .7}}
	got := Candidates(scenes, 150_000, Defaults())
	if len(got) != 5 {
		t.Fatalf("got %+v", got)
	}
	// 門檻以下（等於也不算）的場景不是候選。
	p := Defaults()
	p.SceneThreshold = 0.35
	if got := Candidates(scenes, 150_000, p); len(got) != 4 {
		t.Fatalf("門檻 0.35：%+v", got)
	}
	// 第一個場景太晚：從片頭 0 起補。
	got = Candidates([]Scene{{100_000, .9}}, 130_000, Defaults())
	if len(got) != 2 || got[0].AtMS != 60_000 || got[1].AtMS != 100_000 {
		t.Fatalf("片頭補圖：%+v", got)
	}
}

func TestBandAndTarget(t *testing.T) {
	cases := []struct {
		sec            int64
		target, lo, hi int
	}{{360, 6, 5, 8}, {599, 8, 5, 8}, {600, 10, 8, 12}, {1200, 12, 8, 12}, {2700, 16, 12, 16}, {5400, 20, 16, 20}, {30, 1, 5, 8}}
	for _, c := range cases {
		b := BandFor(c.sec * 1000)
		if n := Target(c.sec*1000, Defaults()); n != c.target || b.Min != c.lo || b.Max != c.hi {
			t.Errorf("%d 秒：target %d band %v，預期 %d %d–%d", c.sec, n, b, c.target, c.lo, c.hi)
		}
	}
	p := Defaults()
	p.MaxFrames = 3
	if n := Target(5400_000, p); n != 3 {
		t.Errorf("max_frames 3：%d", n)
	}
}

func solid(c color.RGBA, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestDedupeDropsRepeatsButKeepsDifferentSolids(t *testing.T) {
	black, white := color.RGBA{A: 255}, color.RGBA{255, 255, 255, 255}
	blue := color.RGBA{0x1e, 0x3a, 0x5f, 255}
	sigs := []Signature{SignatureOf(solid(blue, 64, 36)), SignatureOf(solid(white, 64, 36)), SignatureOf(solid(black, 64, 36)), SignatureOf(solid(black, 64, 36)), SignatureOf(solid(white, 128, 72))}
	got := Dedupe(sigs, 5, 0.05)
	if !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("kept = %v", got)
	}
	// window 1：只和最近保留的一張比，白色再出現就保留。
	if got := Dedupe(sigs, 1, 0.05); !slices.Equal(got, []int{0, 1, 2, 4}) {
		t.Fatalf("window 1：kept = %v", got)
	}
	if d := Diff(SignatureOf(solid(black, 10, 10)), SignatureOf(solid(white, 10, 10))); d != 1 {
		t.Errorf("黑白差 = %v", d)
	}
}

func TestSelectRulesTopNByScoreThenTime(t *testing.T) {
	scores := []float64{.9, .55, .8, .7, .45, .6, .65, .4, .5}
	all := []bool{true, true, true, true, true, true, true, true, true}
	if got := SelectRules(scores, all, 6); !slices.Equal(got, []int{0, 1, 2, 3, 5, 6}) {
		t.Fatalf("got %v", got)
	}
	ex := slices.Clone(all)
	ex[5] = false // SponsorBlock 片段內
	if got := SelectRules(scores, ex, 6); !slices.Equal(got, []int{0, 1, 2, 3, 6, 8}) {
		t.Fatalf("排除後 got %v", got)
	}
	// 同分取較早的。
	if got := SelectRules([]float64{.5, .5, .5}, []bool{true, true, true}, 2); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("同分 got %v", got)
	}
}

func TestHintScoreAndSegments(t *testing.T) {
	cues := []Cue{{288_000, 293_000, "As you can SEE in this diagram"}}
	if s := HintScore(Candidate{AtMS: 290_000, Score: .4}, cues, DefaultHintWords); s != .9 {
		t.Errorf("提示詞加權 = %v", s)
	}
	if s := HintScore(Candidate{AtMS: 310_000, Score: .4}, cues, DefaultHintWords); s != .4 {
		t.Errorf("10 秒外不加權 = %v", s)
	}
	segs := []Segment{{180_000, 210_000}}
	if !InSegment(190_000, segs) || !InSegment(180_000, segs) || InSegment(210_000, segs) {
		t.Error("片段判斷應為 [start, end)")
	}
}

func TestCaptionsAndChapters(t *testing.T) {
	cues := []Cue{{4000, 7000, "Caches trade memory for latency."}, {120_000, 123_000, "LRU"}}
	if c := CueAt(5000, cues); c != "Caches trade memory for latency." {
		t.Errorf("CueAt = %q", c)
	}
	if c := CueAt(60_000, cues); c != "" {
		t.Errorf("遠處沒有 cue：%q", c)
	}
	chs := []Chapter{{0, "Why cache"}, {120_000, "Eviction"}}
	if ChapterAt(5000, chs) != "Why cache" || ChapterAt(120_000, chs) != "Eviction" || ChapterAt(5000, nil) != "" {
		t.Error("ChapterAt 不對")
	}
	long := strings.Repeat("字", 200)
	if got := CleanCaption("a\nb\x00c​ " + long); len([]rune(got)) != MaxCaptionRunes || !strings.HasPrefix(got, "a b c 字") {
		t.Errorf("CleanCaption = %q", got)
	}
}

func TestValidatePicks(t *testing.T) {
	sizes := []int{9}
	if err := ValidatePicks([]Pick{{1, 1, ""}, {1, 9, ""}}, sizes, 6); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]Pick{
		"picks[0]：第 1 張拼圖沒有編號 12":   {{1, 12, ""}},
		"picks[1]：第 1 張拼圖的編號 2 重複":  {{1, 2, ""}, {1, 2, ""}},
		"picks[0]：沒有第 2 張拼圖（共 1 張）": {{2, 1, ""}},
		"picks：選了 3 張，最多 2 張":       {{1, 1, ""}, {1, 2, ""}, {1, 3, ""}},
	}
	for want, picks := range cases {
		err := ValidatePicks(picks, sizes, 2)
		if err == nil || err.Error() != want {
			t.Errorf("%v：err = %v，預期 %q", picks, err, want)
		}
	}
	if _, err := ParsePicks([]byte(`{"picks":[{"sheet":1,"n":1,"caption":"x","at":5}]}`)); err == nil {
		t.Error("多出的欄位要拒絕")
	}
	if _, err := ParsePicks([]byte(`{}`)); err == nil {
		t.Error("沒有 picks 要拒絕")
	}
}

func TestImagesHelpers(t *testing.T) {
	if w, h := FitWithin(3840, 2160, 1920, 1080); w != 1920 || h != 1080 {
		t.Errorf("FitWithin = %d %d", w, h)
	}
	if w, h := FitWithin(1280, 720, 1920, 1080); w != 1280 || h != 720 {
		t.Errorf("不放大 = %d %d", w, h)
	}
	var buf bytes.Buffer
	if err := EncodeJPEG(&buf, solid(color.RGBA{10, 200, 10, 255}, 1280, 720)); err != nil {
		t.Fatal(err)
	}
	img, err := DecodeJPEG(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if th := Thumb(img); th.Bounds().Dx() != ThumbWidth || th.Bounds().Dy() != 180 {
		t.Errorf("thumb = %v", th.Bounds())
	}
	sheet := ContactSheet([]image.Image{img, img, img, img})
	if sheet.Bounds().Dx() != 3*SheetCellW || sheet.Bounds().Dy() != 2*SheetCellH {
		t.Errorf("sheet = %v", sheet.Bounds())
	}
	if _, err := DecodeJPEG([]byte("not a jpeg")); err == nil {
		t.Error("不是 JPEG 要回錯誤")
	}
}

func FuzzParseShowinfo(f *testing.F) {
	f.Add(showinfo)
	f.Add("[Parsed_showinfo_1] n: 1 pts_time:1e308\nlavfi.scene_score=2")
	f.Fuzz(func(t *testing.T, s string) {
		got := ParseShowinfo(s)
		for i, sc := range got {
			if sc.AtMS < 0 || sc.Score < 0 || sc.Score > 1 || (i > 0 && got[i-1].AtMS >= sc.AtMS) {
				t.Fatalf("不合法的結果 %+v", got)
			}
		}
	})
}

func FuzzParsePicks(f *testing.F) {
	f.Add([]byte(`{"picks":[{"sheet":1,"n":12,"caption":"x"}]}`))
	f.Add([]byte(`{"picks":null}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		picks, err := ParsePicks(b)
		if err != nil {
			return
		}
		if err := ValidatePicks(picks, []int{9, 3}, 6); err == nil {
			for _, p := range picks {
				if p.Sheet < 1 || p.Sheet > 2 || p.N < 1 || p.N > 9 {
					t.Fatalf("驗證放過了 %+v", p)
				}
			}
		}
		_ = CleanCaption(string(b))
	})
}
