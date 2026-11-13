package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// purgeRig 建立一組 (item, kind, variant, lang) 的版本，依序由舊到新。
type purgeRig struct {
	t   *testing.T
	s   *Store
	n   NewArtifact
	ids []string
}

func newPurgeRig(t *testing.T) *purgeRig {
	t.Helper()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	return &purgeRig{t: t, s: s, n: NewArtifact{
		ItemID: it.ID, Kind: "transcript", Variant: "source", Lang: "en", Format: "notesteme-cues+json",
		SHA256: "abc", Size: 3, Provenance: json.RawMessage(`{"source":"x"}`),
	}}
}

func (g *purgeRig) add(parents ...string) Artifact {
	g.t.Helper()
	n := g.n
	n.ID = NewID()
	n.Path = fmt.Sprintf("artifacts/%s/%s.json", n.ItemID, n.ID)
	n.Parents = parents
	a, err := g.s.Write().InsertArtifact(context.Background(), n)
	if err != nil {
		g.t.Fatal(err)
	}
	g.ids = append(g.ids, a.ID)
	return a
}

func (g *purgeRig) purge(keep int) []Artifact {
	g.t.Helper()
	out, err := g.s.Write().PurgeOldArtifacts(context.Background(), g.n.ItemID, g.n.Kind, g.n.Variant, g.n.Lang, keep)
	if err != nil {
		g.t.Fatal(err)
	}
	return out
}

// exists 回報產出是否還在資料庫中。
func (g *purgeRig) exists(id string) bool {
	_, err := g.s.Read().Artifact(context.Background(), id)
	return err == nil
}

// consistent 檢查 superseded_by 不指向不存在的列，且恰有一個現行版本。
func (g *purgeRig) consistent() {
	g.t.Helper()
	cur := 0
	for _, id := range g.ids {
		if !g.exists(id) {
			continue
		}
		a, _ := g.s.Read().Artifact(context.Background(), id)
		if a.SupersededBy == "" {
			cur++
		} else if !g.exists(a.SupersededBy) {
			g.t.Errorf("%s 的 superseded_by 指向不存在的 %s", id, a.SupersededBy)
		}
	}
	if cur != 1 {
		g.t.Errorf("現行版本應恰有 1 個，實際 %d", cur)
	}
}

func TestPurgeKeepsTheLatestThreeVersions(t *testing.T) {
	g := newPurgeRig(t)
	for range 5 {
		g.add()
	}
	purged := g.purge(3)
	if len(purged) != 2 || purged[0].ID != g.ids[0] && purged[1].ID != g.ids[0] {
		t.Fatalf("應清除最舊的兩版：%+v", purged)
	}
	for i, id := range g.ids {
		if want := i >= 2; g.exists(id) != want {
			t.Errorf("第 %d 版 exists = %v，預期 %v", i+1, !want, want)
		}
	}
	if n, _ := g.s.Read().ArtifactVersions(context.Background(), g.n.ItemID, "transcript", "source", "en"); n != 3 {
		t.Errorf("版本數 = %d，預期 3", n)
	}
	g.consistent()
	// 再清一次不會再有東西。
	if again := g.purge(3); len(again) != 0 {
		t.Errorf("第二次清除：%+v", again)
	}
}

// 現行版本不可清除：恢復舊版本之後，現行版本可能是最舊的一版。
func TestPurgeNeverRemovesTheCurrentVersion(t *testing.T) {
	g := newPurgeRig(t)
	for range 5 {
		g.add()
	}
	if err := g.s.Write().RestoreArtifact(context.Background(), g.ids[0]); err != nil {
		t.Fatal(err)
	}
	purged := g.purge(3)
	if !g.exists(g.ids[0]) {
		t.Fatal("現行版本被清除了")
	}
	if len(purged) != 1 || purged[0].ID != g.ids[1] {
		t.Errorf("只應清除第 2 版（最近 3 版是 3、4、5，現行版本是 1）：%+v", purged)
	}
	g.consistent() // 第 5 版原本指向被恢復的第 1 版；第 2 版被清掉也不能留下懸空的指向
}

func TestPurgeKeepsPinnedVersions(t *testing.T) {
	g := newPurgeRig(t)
	for range 5 {
		g.add()
	}
	// schema 的 pinned 欄位目前沒有對應的寫入 API；直接設定以驗證保護。
	if _, err := g.s.w.ExecContext(context.Background(), `UPDATE artifacts SET pinned = 1 WHERE id = ?`, g.ids[0]); err != nil {
		t.Fatal(err)
	}
	purged := g.purge(3)
	if len(purged) != 1 || purged[0].ID != g.ids[1] || !g.exists(g.ids[0]) {
		t.Errorf("被引用（pinned）的版本不可清除：%+v", purged)
	}
	g.consistent()
}

// 其他產出的 parents 引用的版本不可清除，否則來源紀錄會指向不存在的產出。
func TestPurgeKeepsVersionsOthersWereDerivedFrom(t *testing.T) {
	g := newPurgeRig(t)
	for range 5 {
		g.add()
	}
	derived := NewArtifact{ItemID: g.n.ItemID, Kind: "transcript", Variant: "translated", Lang: "zh-TW", Format: "notesteme-cues+json",
		Path: "artifacts/d.json", SHA256: "d", Size: 1, Parents: []string{g.ids[0]}, Provenance: json.RawMessage(`{"source":"llm"}`)}
	if _, err := g.s.Write().InsertArtifact(context.Background(), derived); err != nil {
		t.Fatal(err)
	}
	purged := g.purge(3)
	if len(purged) != 1 || purged[0].ID != g.ids[1] || !g.exists(g.ids[0]) {
		t.Errorf("被別的產出引用為 parent 的版本不可清除：%+v", purged)
	}
}

// 已完成步驟的紀錄不能指向被清除的產出（外鍵），之後的快取檢查也不能把它當成可重用。
func TestPurgeDetachesStageRunsFromRemovedArtifacts(t *testing.T) {
	ctx := context.Background()
	g := newPurgeRig(t)
	runs, err := g.s.Read().StageRunsByItem(ctx, g.n.ItemID)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	first := g.add()
	if err := g.s.Write().SetStageResult(ctx, runs[0].ID, StageResult{Executor: "local", CacheKey: "k", ImplVersion: 1, ArtifactID: first.ID}); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		g.add()
	}
	if purged := g.purge(3); len(purged) != 2 {
		t.Fatalf("purged = %+v", purged)
	}
	got, err := g.s.Read().StageRun(ctx, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArtifactID != "" {
		t.Errorf("步驟不該再指向被清除的產出：%q", got.ArtifactID)
	}
}
