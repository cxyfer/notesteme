package pipeline

import (
	"context"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// walkGroups 以 limit 筆一頁讀完分組列表，回傳依序的 (影音 ID, 組)；同時檢查沒有重複、每頁的 total 相同。
func walkGroups(t *testing.T, e *listEnv, q ItemQuery, limit int) (ids, groups []string) {
	t.Helper()
	q.Limit = limit
	seen := map[string]bool{}
	total := -1
	for range 100 {
		page, err := e.p.ListItems(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if total >= 0 && page.Total != total {
			t.Fatalf("total 跨頁改變：%d → %d", total, page.Total)
		}
		total = page.Total
		if len(page.Groups) != len(page.Items) {
			t.Fatalf("groups 與 items 不一致：%v", page.Groups)
		}
		for i, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("重複列出 %s", it.ID)
			}
			seen[it.ID] = true
			ids, groups = append(ids, it.ID), append(groups, page.Groups[i])
		}
		if page.Next == nil {
			if len(ids) != total {
				t.Fatalf("列出 %d 筆，total %d", len(ids), total)
			}
			return ids, groups
		}
		q.After = page.Next
	}
	t.Fatal("分頁沒有結束")
	return nil, nil
}

// contiguous 檢查同一組的影音連續出現（一組不會被切散到不連續的位置）。
func contiguous(t *testing.T, groups []string) []string {
	t.Helper()
	var order []string
	for i, g := range groups {
		if i == 0 || groups[i-1] != g {
			if slices.Contains(order, g) {
				t.Fatalf("組 %q 不連續：%v", g, groups)
			}
			order = append(order, g)
		}
	}
	return order
}

// LIB-R4：依頻道、發布日、狀態分組；不論每頁幾筆，連起來的順序都相同，同一組連續，組內依建立時間由新到舊。
func TestListItemsGrouped(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(
		lItem{platform: "youtube", ext: "a1", title: "a1", channel: "Beta", published: tp("2026-03-01T10:00:00Z"), stages: done(StageMetadata)},
		lItem{platform: "youtube", ext: "a2", title: "a2", channel: "Alpha", published: tp("2026-03-02T10:00:00Z"),
			stages: map[string]string{StageMetadata: "done", StageTranscript: "failed"}},
		lItem{platform: "youtube", ext: "a3", title: "a3", channel: "Beta", stages: map[string]string{StageMetadata: "blocked"}},
		lItem{platform: "local", ext: "a4", title: "a4", published: tp("2026-03-01T08:00:00Z"), stages: map[string]string{StageMetadata: "running"}},
		lItem{platform: "youtube", ext: "a5", title: "a5", channel: "Alpha", published: tp("2026-03-02T10:00:00Z"), stages: done(StageMetadata)},
		lItem{platform: "youtube", ext: "a6", title: "a6", channel: "Beta", published: tp("2026-02-01T00:00:00Z"),
			stages: map[string]string{StageMetadata: "done", StageTranscript: "failed", StageNormalize: "blocked"}},
	)
	id := func(i int) string { return it[i-1].ID }
	ch := func(i int) string {
		x, err := e.st.Read().ItemByID(context.Background(), id(i))
		if err != nil {
			t.Fatal(err)
		}
		return x.ChannelID
	}
	tests := []struct {
		groupBy    string
		wantIDs    []string
		wantGroups []string
	}{
		// 頻道名稱 Alpha、Beta，沒有頻道的最後；組內新到舊。
		{store.GroupChannel, []string{id(5), id(2), id(6), id(3), id(1), id(4)}, []string{ch(5), ch(5), ch(1), ch(1), ch(1), ""}},
		// 發布時間新到舊（同一時間依建立時間新到舊），沒有發布時間的最後。
		{store.GroupPublished, []string{id(5), id(2), id(1), id(4), id(6), id(3)},
			[]string{"2026-03-02", "2026-03-02", "2026-03-01", "2026-03-01", "2026-02-01", ""}},
		// failed（任何步驟的最新執行失敗，優先於 blocked）> blocked > processing > idle。
		{store.GroupStatus, []string{id(6), id(2), id(3), id(4), id(5), id(1)},
			[]string{GroupFailed, GroupFailed, GroupBlocked, GroupProcessing, GroupIdle, GroupIdle}},
	}
	for _, tt := range tests {
		t.Run(tt.groupBy, func(t *testing.T) {
			for _, limit := range []int{1, 2, 4, 50} {
				ids, groups := walkGroups(t, e, ItemQuery{GroupBy: tt.groupBy}, limit)
				if !slices.Equal(ids, tt.wantIDs) || !slices.Equal(groups, tt.wantGroups) {
					t.Errorf("limit %d：ids = %v groups = %v，預期 %v %v", limit, ids, groups, tt.wantIDs, tt.wantGroups)
				}
				contiguous(t, groups)
			}
		})
	}
	// 分組也套用篩選條件，total 只算符合的。
	ids, _ := walkGroups(t, e, ItemQuery{GroupBy: store.GroupChannel, Platform: "local"}, 1)
	if !slices.Equal(ids, []string{id(4)}) {
		t.Errorf("篩選後 = %v", ids)
	}
	// 頻道名稱另外提供（列表本身沒有頻道名稱）。
	page, err := e.p.ListItems(context.Background(), ItemQuery{})
	if err != nil || page.ChannelNames[ch(1)] != "Beta" || page.ChannelNames[ch(5)] != "Alpha" || len(page.ChannelNames) != 2 || page.Groups != nil {
		t.Errorf("不分組時 = %+v, %v", page.ChannelNames, err)
	}
	if err := (ItemQuery{GroupBy: "title"}).Validate(); err == nil {
		t.Error("不認識的 group_by 應回 422")
	}
}

// 有未結束的任務（例如等待中的 pending 步驟）算處理中；只是 pending 而沒有任務（until 之後）算其他。
func TestListItemsGroupStatusUsesTasks(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(
		lItem{platform: "youtube", ext: "b1", title: "b1", stages: map[string]string{StageMetadata: "pending"}},
		lItem{platform: "youtube", ext: "b2", title: "b2", stages: map[string]string{StageMetadata: "pending"}},
	)
	ctx := context.Background()
	runs, err := e.st.Read().StageRunsByItem(ctx, it[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Tx(ctx, func(tx *store.Repo) error {
		_, err := insertStageTask(ctx, tx, it[0].ID, runs[0], 3, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ids, groups := walkGroups(t, e, ItemQuery{GroupBy: store.GroupStatus}, 50)
	if !slices.Equal(ids, []string{it[0].ID, it[1].ID}) || !slices.Equal(groups, []string{GroupProcessing, GroupIdle}) {
		t.Errorf("ids = %v groups = %v", ids, groups)
	}
}
