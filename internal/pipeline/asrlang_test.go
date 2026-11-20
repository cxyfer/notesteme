package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// ASR 回寫原文語言（lang_source 為 asr）之後，從 transcript 重跑的快取鍵不能改變：
// 這個語言是本階段自己的產出，不是輸入；否則第二次重跑會再付一次 ASR 的費用。
func TestASRLanguageWriteBackKeepsCacheKeyStable(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.info.Language = ""
	g.lang = "ja"
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()

	got, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil || got.OriginalLang != "ja" || got.LangSource != "asr" {
		t.Fatalf("ASR 應回寫語言：%+v, %v", got, err)
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
		t.Errorf("回寫語言後重跑應命中快取：Groq 被呼叫 %d 次", g.calls())
	}
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Errorf("transcript = %+v", st)
	}
}

// 語言是輸入（lang_source 不是 asr）時，語言提示與快取鍵照舊使用它。
func TestASRLanguageFromMetadataIsStillAnInput(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.info.Language = "en"
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if g.calls() != 1 || g.hints[0] != "en" {
		t.Fatalf("呼叫 %d 次，語言提示 %v", g.calls(), g.hints)
	}
	_ = it
}
