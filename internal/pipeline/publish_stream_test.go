package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// renderOutputWhole 是發佈輸出改成串流之前的實作（整份載入、整份渲染），只用來當差分測試的基準。
func (p *Pipeline) renderOutputWhole(ctx context.Context, itemID, format, lang, style string) ([]byte, subtitle.Transcript, error) {
	q := CuesQuery{Lang: lang, ToMS: -1}
	var buf bytes.Buffer
	if format == subtitle.FormatASS || format == subtitle.FormatBilingualSRT {
		ass := subtitle.ASSStyle{}
		if format == subtitle.FormatASS {
			name := style
			if name == "" {
				name = "default"
			}
			st, err := assstyle.New(p.st).Get(ctx, name)
			if errors.Is(err, assstyle.ErrNotFound) {
				return nil, subtitle.Transcript{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "publish",
					Detail: "找不到 ASS 樣式 " + name, Message: "找不到 ASS 樣式「" + name + "」。"}
			}
			if err != nil {
				return nil, subtitle.Transcript{}, err
			}
			ass = st.ASS()
		}
		ri, err := p.RenderInput(ctx, itemID, q)
		if err != nil {
			return nil, subtitle.Transcript{}, err
		}
		var orig []subtitle.Cue
		origLang := ri.Main.Lang
		if ri.Orig != nil {
			orig, origLang = ri.Orig.Cues, ri.Orig.Lang
		}
		pairs := subtitle.PairCues(ri.Main.Cues, orig)
		if format == subtitle.FormatASS {
			err = subtitle.RenderASS(&buf, pairs, assstyle.ResolveFonts(ass, ri.Main.Lang, origLang), ri.Item.Title)
		} else {
			err = subtitle.RenderBilingualSRT(&buf, pairs)
		}
		return buf.Bytes(), ri.Main, err
	}
	_, tr, err := p.CuesWithItem(ctx, itemID, q)
	if err != nil {
		return nil, subtitle.Transcript{}, err
	}
	err = subtitle.Render(&buf, format, tr)
	return buf.Bytes(), tr, err
}

// seedCues 寫入一份字幕；order 為 false 時把句子倒過來排（沒有依開始時間排序，打到 OpenRender 的整份載入退路）。
func seedCues(t *testing.T, r *rig, it store.Item, v subtitle.Variant, lang string, n int, sorted bool) {
	t.Helper()
	cues := make([]subtitle.Cue, 0, n)
	for i := range n {
		start := int64(i)*1700 + int64(i%3)*90
		text := fmt.Sprintf("%s 第 %d 句 <b>&</b> --> 結尾", lang, i+1)
		if i%7 == 0 {
			text += "\n\n第二行"
		}
		c := subtitle.Cue{Index: i + 1, StartMS: start, EndMS: start + 1500 + int64(i%5)*300, Text: text, Timing: subtitle.TimingSource}
		if i%4 == 0 {
			sp := "S1"
			c.Speaker = &sp
		}
		cues = append(cues, c)
	}
	if !sorted {
		for i, j := 0, len(cues)-1; i < j; i, j = i+1, j-1 {
			cues[i], cues[j] = cues[j], cues[i]
		}
	}
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: v, Lang: lang, Cues: cues}); err != nil {
		t.Fatal(err)
	}
}

// 發佈輸出改成串流（NFR-RES-3）之後，每種格式、每種語言選擇的內容與語言都和整份載入時逐位元組相同；錯誤也相同。
func TestOpenOutputMatchesWholeRender(t *testing.T) {
	ctx := context.Background()
	for _, sorted := range []bool{true, false} {
		t.Run(fmt.Sprintf("sorted=%v", sorted), func(t *testing.T) {
			r := newRig(t)
			it := newRenderItem(t, r)
			seedCues(t, r, it, subtitle.VariantCorrected, "en", 400, sorted)
			seedCues(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", 380, sorted)
			// 確認 fixture 真的走到兩條路：排序的逐句配對、沒排序的整份載入退路。
			if rs, err := r.p.OpenRender(ctx, it.ID, CuesQuery{ToMS: -1}); err != nil || (rs.pairs == nil) != sorted || !rs.Bilingual {
				t.Fatalf("OpenRender：退路 %v、雙語 %v，%v", rs != nil && rs.pairs != nil, rs != nil && rs.Bilingual, err)
			}
			for _, format := range []string{"srt", "vtt", "txt", "cues-json", "ass", "bilingual-srt"} {
				for _, lang := range []string{"", "en", "zh-Hant-TW", "ja"} {
					want, wtr, werr := r.p.renderOutputWhole(ctx, it.ID, format, lang, "")
					out, gerr := r.p.openOutput(ctx, it.ID, format, lang, "")
					var got bytes.Buffer
					if gerr == nil {
						gerr = out.render(&got)
					}
					if (werr == nil) != (gerr == nil) || (werr != nil && werr.Error() != gerr.Error()) {
						t.Fatalf("%s lang=%q：錯誤不同：整份 %v、串流 %v", format, lang, werr, gerr)
					}
					if werr != nil {
						if lang != "ja" || !errors.Is(gerr, ErrNoTranscript) {
							t.Errorf("%s lang=%q：%v", format, lang, gerr)
						}
						continue
					}
					if !bytes.Equal(got.Bytes(), want) || out.lang != wtr.Lang {
						t.Errorf("%s lang=%q：內容或語言不同（%d／%d bytes，%q／%q）", format, lang, got.Len(), len(want), out.lang, wtr.Lang)
					}
					if len(want) < 1000 {
						t.Errorf("%s lang=%q：內容太短（%d bytes），fixture 不對", format, lang, len(want))
					}
				}
			}
			// 找不到樣式：錯誤在寫出任何東西之前。
			_, _, werr := r.p.renderOutputWhole(ctx, it.ID, "ass", "", "nope")
			_, gerr := r.p.openOutput(ctx, it.ID, "ass", "", "nope")
			var ce *errclass.Error
			if !errors.As(gerr, &ce) || ce.Class != errclass.ClassPermanent || werr.Error() != gerr.Error() {
				t.Errorf("找不到樣式：整份 %v、串流 %v", werr, gerr)
			}
		})
	}
}
