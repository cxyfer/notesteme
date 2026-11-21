package audio

import (
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const mib = 1 << 20

func TestChunkCount(t *testing.T) {
	limit := int64(25 * mib)
	tests := []struct {
		name  string
		size  int64
		limit int64
		want  int
	}{
		{"契約場景：40 MiB、上限 25 MiB", 40 * mib, limit, 2},
		{"0 byte 至少 1 塊", 0, limit, 1},
		{"負數至少 1 塊", -5, limit, 1},
		{"遠小於上限", mib, limit, 1},
		{"剛好是上限的 90%", 23592960, limit, 1},
		{"超過上限的 90% 一個 byte", 23592961, limit, 2},
		{"剛好是上限的兩倍 90%", 2 * 23592960, limit, 2},
		{"兩倍 90% 多一個 byte", 2*23592960 + 1, limit, 3},
		{"100 MiB", 100 * mib, limit, 5},
		{"上限為 0 時不切", 40 * mib, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChunkCount(tt.size, tt.limit, Safety); got != tt.want {
				t.Errorf("ChunkCount(%d, %d) = %d，預期 %d", tt.size, tt.limit, got, tt.want)
			}
		})
	}
	if got := ChunkCount(10, 10, 0); got != 1 {
		t.Errorf("safety 為 0 時 = %d，預期 1", got)
	}
}

func TestPickCut(t *testing.T) {
	tests := []struct {
		name     string
		target   float64
		silences []media.Silence
		want     float64
	}{
		{"契約場景：靜音 1501.8–1502.2", 1502, []media.Silence{{StartS: 1501.8, EndS: 1502.2}}, 1502},
		{"取最近的靜音中點", 1502, []media.Silence{{StartS: 1480, EndS: 1481}, {StartS: 1505, EndS: 1507}, {StartS: 1520, EndS: 1521}}, 1506},
		{"沒有靜音就用 target", 1502, nil, 1502},
		{"視窗外的靜音不採用", 1502, []media.Silence{{StartS: 1400, EndS: 1401}, {StartS: 1600, EndS: 1601}}, 1502},
		{"視窗邊界（±30 秒）採用", 1000, []media.Silence{{StartS: 969.5, EndS: 970.5}}, 970},
		{"超過視窗邊界一點點不採用", 1000, []media.Silence{{StartS: 969.4, EndS: 970.4}}, 1000},
		{"距離相同取較早的", 100, []media.Silence{{StartS: 109.5, EndS: 110.5}, {StartS: 89.5, EndS: 90.5}}, 90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PickCut(tt.target, tt.silences, CutWindow); got != tt.want {
				t.Errorf("PickCut = %v，預期 %v", got, tt.want)
			}
		})
	}
}

func TestChunksContractScenario(t *testing.T) {
	// D=3004、B=1502：塊 [0,1507]、[1497,3004]。
	got := Chunks(3004, []float64{1502}, Overlap)
	want := []Chunk{
		{StartS: 0, EndS: 1507, OwnStartS: 0, OwnEndS: 1502},
		{StartS: 1497, EndS: 3004, OwnStartS: 1502, OwnEndS: 3004},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunks = %+v，預期 %+v", got, want)
	}
}

func TestChunksShapes(t *testing.T) {
	tests := []struct {
		name string
		d    float64
		cuts []float64
		want []Chunk
	}{
		{"沒有切點是一整塊", 100, nil, []Chunk{{0, 100, 0, 100}}},
		{"三塊：中間塊兩側都有重疊", 300, []float64{100, 200}, []Chunk{
			{0, 105, 0, 100}, {95, 205, 100, 200}, {195, 300, 200, 300},
		}},
		{"切點未排序", 300, []float64{200, 100}, []Chunk{
			{0, 105, 0, 100}, {95, 205, 100, 200}, {195, 300, 200, 300},
		}},
		{"範圍外與重複的切點被略過", 300, []float64{-1, 0, 100, 100, 300, 400}, []Chunk{
			{0, 105, 0, 100}, {95, 300, 100, 300},
		}},
		{"切點靠近開頭：不取負的時間", 300, []float64{3}, []Chunk{
			{0, 8, 0, 3}, {0, 300, 3, 300},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Chunks(tt.d, tt.cuts, Overlap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Chunks = %+v，預期 %+v", got, tt.want)
			}
		})
	}
}

func cue(startS, endS float64, text string) subtitle.Cue {
	return subtitle.Cue{StartMS: int64(startS * 1000), EndMS: int64(endS * 1000), Text: text, Timing: subtitle.TimingSource}
}

// 契約第 6 節的場景：#41（1498–1502）歸第 0 塊，#42（1502–1506）歸第 1 塊；重疊區的重複轉錄被丟掉。
func TestStitchContractScenario(t *testing.T) {
	chunks := Chunks(3004, []float64{1502}, Overlap)
	// 第 0 塊（時間相對 0）：重疊區 1502–1507 重複轉錄了 #42 的前半。
	part0 := []subtitle.Cue{
		cue(1490, 1498, "#40 before the overlap"),
		cue(1498, 1502, "and that is where the overlap starts"),
		cue(1502, 1506, "so we remove the duplicate line"),
	}
	// 第 1 塊（時間相對 1497）：重疊區 1497–1502 重複轉錄了 #41。
	part1 := []subtitle.Cue{
		cue(1498-1497, 1502-1497, "And that is where the overlap starts."),
		cue(1502-1497, 1506-1497, "So we remove the duplicate line."),
		cue(1506-1497, 1510-1497, "#43 after the overlap"),
	}
	got := Stitch(chunks, [][]subtitle.Cue{part0, part1})
	wantText := []string{
		"#40 before the overlap",
		"and that is where the overlap starts",
		"So we remove the duplicate line.",
		"#43 after the overlap",
	}
	if len(got) != len(wantText) {
		t.Fatalf("cue 數 = %d，預期 %d：%+v", len(got), len(wantText), got)
	}
	for i, c := range got {
		if c.Text != wantText[i] || c.Index != i+1 {
			t.Errorf("cue %d = %+v，預期文字 %q、編號 %d", i, c, wantText[i], i+1)
		}
	}
	if got[1].StartMS != 1498000 || got[1].EndMS != 1502000 {
		t.Errorf("#41 的時間 = %d–%d，預期 1498000–1502000", got[1].StartMS, got[1].EndMS)
	}
	if got[2].StartMS != 1502000 || got[2].EndMS != 1506000 {
		t.Errorf("#42 的時間 = %d–%d，預期 1502000–1506000", got[2].StartMS, got[2].EndMS)
	}
}

func TestStitchMonotonicAndRenumbered(t *testing.T) {
	chunks := []Chunk{{0, 105, 0, 100}, {95, 300, 100, 300}}
	part0 := []subtitle.Cue{cue(0, 4, "a"), cue(3, 6, "b"), cue(90, 99, "c")}
	// 第 1 塊起點 95：這塊的 cue 時間相對 95。
	part1 := []subtitle.Cue{cue(6, 9, "d"), cue(8, 8.5, "e"), cue(2, 1, "f")}
	got := Stitch(chunks, [][]subtitle.Cue{part0, part1})
	if len(got) == 0 {
		t.Fatal("沒有 cue")
	}
	var prevEnd int64
	for i, c := range got {
		if c.StartMS < prevEnd {
			t.Errorf("cue %d 的 start %d 早於前一句的 end %d", i, c.StartMS, prevEnd)
		}
		if c.EndMS < c.StartMS {
			t.Errorf("cue %d 的 end %d 早於 start %d", i, c.EndMS, c.StartMS)
		}
		if c.Index != i+1 {
			t.Errorf("cue %d 的編號 = %d", i, c.Index)
		}
		prevEnd = c.EndMS
	}
}

func TestStitchDedupe(t *testing.T) {
	chunks := []Chunk{{0, 105, 0, 100}, {95, 300, 100, 300}}
	tests := []struct {
		name  string
		part0 []subtitle.Cue
		part1 []subtitle.Cue // 相對 95 秒
		want  []string
	}{
		{
			"時間重疊且文字相同（忽略大小寫與標點）只留一句",
			[]subtitle.Cue{cue(98, 100.5, "Hello, world")},
			[]subtitle.Cue{cue(5.2, 6, "hello world!")}, // 絕對 100.2–101，中點 100.6 歸第 1 塊
			[]string{"Hello, world"},
		},
		{
			"文字相同但時間不重疊都保留",
			[]subtitle.Cue{cue(90, 92, "yes")},
			[]subtitle.Cue{cue(10, 12, "yes")},
			[]string{"yes", "yes"},
		},
		{
			"時間重疊但文字不同都保留",
			[]subtitle.Cue{cue(98, 100.5, "one")},
			[]subtitle.Cue{cue(5.2, 6, "two")},
			[]string{"one", "two"},
		},
		{
			"中文標點",
			[]subtitle.Cue{cue(98, 100.5, "你好，世界。")},
			[]subtitle.Cue{cue(5.2, 6, "你好世界")},
			[]string{"你好，世界。"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Stitch(chunks, [][]subtitle.Cue{tt.part0, tt.part1})
			var texts []string
			for _, c := range got {
				texts = append(texts, c.Text)
			}
			if !reflect.DeepEqual(texts, tt.want) {
				t.Errorf("texts = %q，預期 %q", texts, tt.want)
			}
		})
	}
}

func TestStitchOwnershipBounds(t *testing.T) {
	chunks := []Chunk{{0, 105, 0, 100}, {95, 300, 100, 300}}
	// 中點剛好是 100 秒：屬於第 1 塊（Own 為半開區間）。
	part0 := []subtitle.Cue{cue(99, 101, "edge in chunk 0")}
	part1 := []subtitle.Cue{cue(4, 6, "edge in chunk 1")} // 絕對 99–101
	got := Stitch(chunks, [][]subtitle.Cue{part0, part1})
	if len(got) != 1 || got[0].Text != "edge in chunk 1" {
		t.Fatalf("got = %+v", got)
	}
	// 第一塊的下界、最後一塊的上界不設限。
	got = Stitch(chunks, [][]subtitle.Cue{{cue(-1, 0.5, "head")}, {cue(210, 215, "tail")}})
	if len(got) != 2 {
		t.Fatalf("got = %+v", got)
	}
}

func TestStitchSingleChunkAndEdgeCases(t *testing.T) {
	one := []Chunk{{0, 10, 0, 10}}
	got := Stitch(one, [][]subtitle.Cue{{cue(0, 1, "a"), cue(1, 2, "b")}})
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 2 {
		t.Errorf("單塊 = %+v", got)
	}
	if got := Stitch(nil, nil); got != nil {
		t.Errorf("沒有塊 = %+v", got)
	}
	// parts 比 chunks 少：只處理有結果的塊。
	if got := Stitch(Chunks(100, []float64{50}, Overlap), [][]subtitle.Cue{{cue(0, 1, "a")}}); len(got) != 1 {
		t.Errorf("parts 較少 = %+v", got)
	}
}

func TestStitchShiftsWords(t *testing.T) {
	chunks := []Chunk{{0, 105, 0, 100}, {95, 300, 100, 300}}
	c := cue(6, 9, "w")
	c.Words = []subtitle.Word{{Text: "w", StartMS: 6000, EndMS: 9000}}
	got := Stitch(chunks, [][]subtitle.Cue{nil, {c}})
	if len(got) != 1 || got[0].Words[0].StartMS != 101000 || got[0].StartMS != 101000 {
		t.Fatalf("got = %+v", got)
	}
	if c.Words[0].StartMS != 6000 {
		t.Error("不應改動輸入的 Words")
	}
}
