package store

import (
	"context"
	"slices"
	"testing"
)

func TestGlossaryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	g, err := s.Read().Glossary(ctx, GlossaryGlobal, GlobalGlossaryID)
	if err != nil || len(g.Terms) != 0 || !g.UpdatedAt.IsZero() || g.Terms == nil {
		t.Fatalf("沒有儲存過 = %+v, %v（terms 要是空陣列、updated_at 零值）", g, err)
	}
	if err := w.PutGlossary(ctx, GlossaryGlobal, GlobalGlossaryID, []string{"KRaft", "微服務"}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(1000)
	if err := w.PutGlossary(ctx, GlossaryGlobal, GlobalGlossaryID, []string{"Kafka"}); err != nil { // 整份取代
		t.Fatal(err)
	}
	g, _ = s.Read().Glossary(ctx, GlossaryGlobal, GlobalGlossaryID)
	if !slices.Equal(g.Terms, []string{"Kafka"}) || g.UpdatedAt.IsZero() {
		t.Errorf("= %+v", g)
	}
	// 頻道詞彙表與全域互不影響；nil 存成空陣列。
	ch, err := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutGlossary(ctx, GlossaryChannel, ch, nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Read().Glossary(ctx, GlossaryChannel, ch); c.Terms == nil || len(c.Terms) != 0 {
		t.Errorf("頻道 = %+v", c)
	}
	if g, _ = s.Read().Glossary(ctx, GlossaryGlobal, GlobalGlossaryID); !slices.Equal(g.Terms, []string{"Kafka"}) {
		t.Errorf("全域被影響：%+v", g)
	}
}

// COR-R6：候選每個頻道每個詞只有一筆（任何狀態），pending 依建立順序列出；頻道刪除時候選一起刪除（外鍵）。
func TestTermCandidates(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	ch, _ := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC1"})
	other, _ := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC2"})

	// 依固定順序加入：列出時依建立順序，map 的走訪順序是隨機的。
	for _, term := range []string{"KRaft", "Confluent"} {
		if added, err := w.AddTermCandidate(ctx, ch, "", term); err != nil || !added {
			t.Fatalf("AddTermCandidate(%q) = %v, %v", term, added, err)
		}
	}
	if added, err := w.AddTermCandidate(ctx, ch, "", "KRaft"); err != nil || added {
		t.Errorf("同一個詞不重複加入：%v, %v", added, err)
	}
	// 被拒絕的詞（狀態不是 pending）同樣不會再被加入。
	if _, err := s.w.ExecContext(ctx, `UPDATE glossary_candidates SET state = 'rejected' WHERE term = 'Confluent'`); err != nil {
		t.Fatal(err)
	}
	if added, _ := w.AddTermCandidate(ctx, ch, "", "Confluent"); added {
		t.Error("已被拒絕的詞不重複加入")
	}
	// 別的頻道可以有同一個詞。
	if added, err := w.AddTermCandidate(ctx, other, "", "KRaft"); err != nil || !added {
		t.Errorf("別的頻道 = %v, %v", added, err)
	}
	list, err := s.Read().TermCandidates(ctx, ch)
	if err != nil || len(list) != 2 || list[0].Term != "KRaft" || list[0].State != TermCandidatePending || list[1].State != TermCandidateRejected {
		t.Fatalf("TermCandidates = %+v, %v", list, err)
	}
	if n, _ := s.Read().CountTermCandidates(ctx, ch); n != 2 {
		t.Errorf("Count = %d", n)
	}
	// 影音不存在：外鍵擋下。
	if _, err := w.AddTermCandidate(ctx, ch, "no-such-item", "x"); err == nil {
		t.Error("影音不存在時應該失敗")
	}
	if _, err := w.AddTermCandidate(ctx, "no-such-channel", "", "x"); err == nil {
		t.Error("頻道不存在時應該失敗")
	}
}
