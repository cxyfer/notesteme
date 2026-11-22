package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/store"
)

func fallbackProvenance(t *testing.T, r *rig, artifactID string) (prov struct {
	Fallbacks []struct {
		Chunk  int
		Reason string
		Detail map[string]any
	}
}) {
	t.Helper()
	art, err := r.st.Read().Artifact(context.Background(), artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(art.Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	return prov
}

// COR-R2、R3：相似度低於門檻的 chunk 在重試用盡後退回原文；步驟完成、附上訊息與動作，來源紀錄記下相似度。
func TestCorrectSimilarityFallbackMessage(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{2: "rewrite"}}
	r, it := newLLMRig(t, fake)
	rerunCorrect(t, r, it)
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusDone {
		t.Fatalf("步驟 = %+v", st)
	}
	if want := "第 2 段的校正結果和原文差異過大（相似度 0.04），已退回原文。"; st.Message != want {
		t.Errorf("訊息 = %q，預期 %q", st.Message, want)
	}
	if len(st.Actions) != 2 || st.Actions[0] != "重新校正這段" || st.Actions[1] != "開啟 Prompt" {
		t.Errorf("動作 = %v", st.Actions)
	}
	// 驗證重試用盡：chunk 2 試了 1 + 1 次。
	if n := fake.callCount(); n != 5 {
		t.Errorf("請求數 = %d（%v）", n, fake.calls)
	}
	prov := fallbackProvenance(t, r, st.ArtifactID)
	if len(prov.Fallbacks) != 1 || prov.Fallbacks[0].Reason != llm.ReasonSimilarity || prov.Fallbacks[0].Chunk != 2 || prov.Fallbacks[0].Detail["similarity"] != 0.04 {
		t.Errorf("fallbacks = %+v", prov.Fallbacks)
	}
	art, _ := r.st.Read().Artifact(context.Background(), st.ArtifactID)
	tr, _ := readTranscript(r.dir, art.Path, art.SHA256)
	if tr.Cues[1].Text != "bravo" || tr.Cues[0].Text != "alpha!" {
		t.Errorf("退回的 chunk 用原文：%q %q", tr.Cues[0].Text, tr.Cues[1].Text)
	}
}

// COR-R4：詞彙表依序合併全域、頻道、處理方案（含頻道覆寫）的詞，大小寫相同的重複只留第一個；
// 內容放在 <video_metadata>（資料區塊），不進系統指示。
func TestCorrectGlossaryMergedIntoVideoMetadata(t *testing.T) {
	fake := &chunkLLM{}
	r, it := newLLMRig(t, fake)
	ctx := context.Background()
	it, _ = r.st.Read().ItemByID(ctx, it.ID) // metadata 完成之後才有頻道
	if _, err := r.p.PutGlossary(ctx, "global", "default", []string{"Kafka", "KRaft"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.PutGlossary(ctx, "channel", it.ChannelID, []string{"kraft", "Confluent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.PatchChannelOverrides(ctx, it.ChannelID, map[string]any{"glossary": []any{"ksqlDB", "Kafka"}}); err != nil {
		t.Fatal(err)
	}
	rerunCorrect(t, r, it)
	if len(fake.fixed) == 0 {
		t.Fatal("沒有請求")
	}
	prefix := fake.fixed[0].Prefix
	if !strings.Contains(prefix, `"glossary":["Kafka","KRaft","Confluent","ksqlDB"]`) {
		t.Errorf("<video_metadata> 的詞彙表（全域、頻道、覆寫的順序，去重）：%s", prefix)
	}
	if sys := fake.fixed[0].System; strings.Contains(sys, "Confluent") || strings.Contains(sys, "ksqlDB") {
		t.Errorf("詞彙表的內容不該進系統指示：%s", sys)
	}
	// 詞彙表改了，快取不能命中：產出重新送出。
	before := fake.callCount()
	if _, err := r.p.PutGlossary(ctx, "channel", it.ChannelID, []string{"Confluent", "Schema Registry"}); err != nil {
		t.Fatal(err)
	}
	rerunCorrect(t, r, it)
	if fake.callCount() <= before {
		t.Error("詞彙表改變後應該重新送出（固定前綴不同，快取鍵不同）")
	}
}

// COR-R6：術語候選寫入頻道（pending），已在詞彙表中的詞與重複的詞不加入，控制字元被清掉；重跑不重複。
func TestCorrectTermCandidates(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{1: "terms", 2: "terms", 3: "terms", 4: "terms"}}
	r, it := newLLMRig(t, fake)
	ctx := context.Background()
	it, _ = r.st.Read().ItemByID(ctx, it.ID) // metadata 完成之後才有頻道
	if _, err := r.p.PutGlossary(ctx, "channel", it.ChannelID, []string{"apache kafka"}); err != nil {
		t.Fatal(err)
	}
	rerunCorrect(t, r, it)
	got, err := r.p.TermCandidates(ctx, it.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	var terms []string
	for _, c := range got {
		if c.State != store.TermCandidatePending || c.ItemID != it.ID {
			t.Errorf("候選 = %+v", c)
		}
		terms = append(terms, c.Term)
	}
	// "Apache Kafka" 已在詞彙表（大小寫不同也算）、"kraft" 與 "KRaft" 重複、控制字元被清掉。
	if want := []string{"KRaft", "[31mred"}; strings.Join(terms, "|") != strings.Join(want, "|") {
		t.Errorf("候選 = %q，預期 %q", terms, want)
	}
	// 詞彙表本身不受影響。
	if g, _ := r.p.Glossary(ctx, "channel", it.ChannelID); len(g.Terms) != 1 {
		t.Errorf("候選不會自動加入詞彙表：%v", g.Terms)
	}
	// 再跑一次（強制重送全部 chunk）：不重複。
	rerunCorrect(t, r, it, 1, 2, 3, 4)
	got2, _ := r.p.TermCandidates(ctx, it.ChannelID)
	if len(got2) != len(got) {
		t.Errorf("重跑後候選 %d 個，預期 %d 個", len(got2), len(got))
	}
}

// COR-R7：詞彙表進 Groq 的快取參數；沒有詞彙表時參數與先前相同（已付費的轉錄不因此失效）。
func TestGroqParamsIncludeGlossaryPromptOnlyWhenPresent(t *testing.T) {
	f := groqFetcher(Deps{}, &fakeGroq{baseURL: "u", model: "m"})
	it := store.Item{OriginalLang: "en"}
	plain, _ := f.params(it, Language{}).(map[string]any)
	if _, ok := plain["prompt"]; ok {
		t.Errorf("沒有詞彙表不該有 prompt：%v", plain)
	}
	with, _ := f.params(it, Language{Glossary: []string{"KRaft", "Confluent"}}).(map[string]any)
	if with["prompt"] != "KRaft, Confluent" {
		t.Errorf("params = %v", with)
	}
	if !f.supportsGlossary {
		t.Error("Groq 支援詞彙提示")
	}
}
