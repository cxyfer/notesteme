package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// mixedCues 是中英夾雜的字幕（同驗收場景的 auto-zh-en-mixed：以份量計 zh 69%、en 31%）。
func mixedCues() []subtitle.Cue {
	lines := []string{
		"今天我們來聊聊 database 的 index",
		"先說明 B-tree 為什麼適合 range query",
		"然後比較 hash index 跟 covering index",
		"最後看一個 real world 的 migration 案例",
	}
	out := make([]subtitle.Cue, len(lines))
	for i, l := range lines {
		out[i] = subtitle.Cue{Index: i + 1, StartMS: int64(i) * 1000, EndMS: int64(i+1) * 1000, Text: l, Timing: subtitle.TimingSource}
	}
	return out
}

// SRC-R11 第三層：平台與 ASR 都沒有語言時，用文字偵測的主要語言（lang_source 為 detected），並標記 mixed；
// 這是 transcript 自己的產出，重跑時不當成輸入，快取命中、不再付費。
func TestTextLanguageDetectionWritesBackAndKeepsCacheKey(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.info.Language = ""
	g.respond = func(int, string, string) (*groq.Result, error) {
		return &groq.Result{Cues: mixedCues()}, nil // ASR 沒有回報語言
	}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()

	got, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil || got.OriginalLang != "zh" || got.LangSource != "detected" || !got.LangMixed {
		t.Fatalf("應寫回偵測的語言與 mixed：%+v, %v", got, err)
	}
	if g.calls() != 1 || g.hints[0] != "" {
		t.Fatalf("第一次：呼叫 %d 次，語言提示 %q", g.calls(), g.hints)
	}
	for range 2 {
		if err := r.rerunTranscript(it, nil); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	if g.calls() != 1 {
		t.Errorf("偵測的語言不是輸入，重跑應命中快取：Groq 被呼叫 %d 次，語言提示 %q", g.calls(), g.hints)
	}
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Errorf("transcript = %+v", st)
	}
}

// 平台中繼資料有語言時，文字偵測不改原文語言（第一層優先），但 mixed 仍依文字判斷。
func TestTextLanguageDetectionKeepsMetadataLanguage(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.info.Language = "en"
	g.respond = func(int, string, string) (*groq.Result, error) {
		return &groq.Result{Lang: "en", Cues: mixedCues()}, nil
	}
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	got, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil || got.OriginalLang != "en" || got.LangSource == "detected" || !got.LangMixed {
		t.Fatalf("原文語言應維持 en、標記 mixed：%+v, %v", got, err)
	}
}

func TestDetectLang(t *testing.T) {
	meta := []byte(`{"channel":"c"}`)
	en := []subtitle.Cue{{Text: "Today we talk about why the index is so important for the database and how to design it."}}
	cases := []struct {
		name     string
		it       store.Item
		in       *metaUpdate
		cues     []subtitle.Cue
		lang     string // 回傳的 OriginalLang
		src      string
		mixed    *bool
		nilMeta  bool
		keepMeta bool
	}{
		{name: "太短：不動", it: store.Item{}, cues: []subtitle.Cue{{Text: "hello"}}, nilMeta: true},
		{name: "未知：用偵測", it: store.Item{Metadata: meta}, cues: mixedCues(), lang: "zh", src: "detected", mixed: ptr(true), keepMeta: true},
		{name: "平台已知：不改語言", it: store.Item{OriginalLang: "en", LangSource: "metadata", Metadata: meta}, cues: mixedCues(), mixed: ptr(true), keepMeta: true},
		{name: "ASR 的副本（語言被清空）：不改語言", it: store.Item{LangSource: "asr", Metadata: meta}, cues: en, mixed: ptr(false), keepMeta: true},
		{name: "上次偵測的：更新", it: store.Item{OriginalLang: "ja", LangSource: "detected", Metadata: meta}, cues: en, lang: "en", src: "detected", mixed: ptr(false), keepMeta: true},
		{name: "meta 已有 ASR 語言", it: store.Item{Metadata: meta}, in: &metaUpdate{item: store.ItemMeta{OriginalLang: "ja", LangSource: "asr", Metadata: meta}}, cues: mixedCues(), lang: "ja", src: "asr", mixed: ptr(true), keepMeta: true},
	}
	for _, c := range cases {
		out := detectLang(c.it, c.in, c.cues)
		if c.nilMeta {
			if out != nil {
				t.Errorf("%s：應不改動，得到 %+v", c.name, out)
			}
			continue
		}
		if out == nil {
			t.Fatalf("%s：沒有結果", c.name)
		}
		if out.item.OriginalLang != c.lang || out.item.LangSource != c.src {
			t.Errorf("%s：語言 = %q/%q，預期 %q/%q", c.name, out.item.OriginalLang, out.item.LangSource, c.lang, c.src)
		}
		if (out.mixed == nil) != (c.mixed == nil) || (out.mixed != nil && *out.mixed != *c.mixed) {
			t.Errorf("%s：mixed = %v，預期 %v", c.name, out.mixed, c.mixed)
		}
		if c.keepMeta && string(out.item.Metadata) != string(meta) {
			t.Errorf("%s：Metadata 應原樣帶回（UpdateItemMeta 整欄覆寫），得到 %s", c.name, out.item.Metadata)
		}
	}
	if selfLangSource("metadata") || !selfLangSource("asr") || !selfLangSource("detected") {
		t.Error("selfLangSource")
	}
}

func ptr[T any](v T) *T { return &v }
