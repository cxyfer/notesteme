package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// 用 natural key 讀取：頻道還不存在不是錯誤（ChannelID 為空、overrides 是 {}）；用 UUID 讀不到是 store.ErrNotFound。
func TestChannelOverrides_ReadAbsent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	got, err := r.p.ChannelOverrides(ctx, "youtube:UCnew")
	if err != nil || got.ChannelID != "" || got.Platform != "youtube" || got.ExternalID != "UCnew" || got.Overrides == nil || len(got.Overrides) != 0 {
		t.Fatalf("ChannelOverrides = %+v, %v", got, err)
	}
	if _, err := r.p.ChannelOverrides(ctx, "0190-no-such-uuid"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UUID 找不到 = %v，預期 store.ErrNotFound", err)
	}
	// 讀取不建立頻道。
	if chs, _ := r.st.Read().ChannelsWithOverrides(ctx); len(chs) != 0 {
		t.Errorf("讀取不該建立頻道：%+v", chs)
	}
	if _, err := r.st.Read().ChannelByKey(ctx, "youtube", "UCnew"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("讀取不該建立頻道：%v", err)
	}
}

// natural key 找不到時建立頻道（title 留空）；之後可以用 UUID 或 natural key 讀寫；merge patch 的 null 刪除該鍵。
func TestChannelOverrides_NaturalKeyCreatesChannelAndPatch(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	first, err := r.p.PatchChannelOverrides(ctx, "youtube:UCfix", map[string]any{
		"language": map[string]any{"targets": []any{"ja"}, "fallback": []any{"groq"}},
		"timing":   map[string]any{"split_timing": "forbid"},
	})
	if err != nil || first.ChannelID == "" || first.Platform != "youtube" || first.ExternalID != "UCfix" {
		t.Fatalf("PatchChannelOverrides = %+v, %v", first, err)
	}
	ch, err := r.st.Read().ChannelByID(ctx, first.ChannelID)
	if err != nil || ch.Title != "" {
		t.Errorf("建立的頻道 = %+v, %v，預期 title 為空", ch, err)
	}

	// 用 UUID 再 patch：null 刪除一個鍵，物件逐欄位合併，陣列取代。
	second, err := r.p.PatchChannelOverrides(ctx, first.ChannelID, map[string]any{
		"language": map[string]any{"targets": []any{"fr", "de"}, "fallback": nil},
		"timing":   nil,
	})
	if err != nil || second.ChannelID != first.ChannelID {
		t.Fatalf("以 UUID patch = %+v, %v", second, err)
	}
	want := map[string]any{"language": map[string]any{"targets": []any{"fr", "de"}}}
	if !reflect.DeepEqual(second.Overrides, want) {
		t.Errorf("overrides = %v，預期 %v", second.Overrides, want)
	}
	back, err := r.p.ChannelOverrides(ctx, "youtube:UCfix")
	if err != nil || back.ChannelID != first.ChannelID || !reflect.DeepEqual(back.Overrides, want) {
		t.Errorf("以 natural key 讀回 = %+v, %v", back, err)
	}
	// 沒有重複建立頻道。
	if chs, _ := r.st.Read().ChannelsWithOverrides(ctx); len(chs) != 1 {
		t.Errorf("有覆寫的頻道 = %d，預期 1", len(chs))
	}
	// 把最後一個鍵也刪掉：回到 {}。
	empty, err := r.p.PatchChannelOverrides(ctx, "youtube:UCfix", map[string]any{"language": nil})
	if err != nil || len(empty.Overrides) != 0 {
		t.Errorf("全部刪除 = %+v, %v", empty, err)
	}
}

// local 沒有頻道、缺 external_id、UUID 不存在：都不寫入。
func TestChannelOverrides_Rejections(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for _, key := range []string{"local:/tmp/a.mp4", "foo:bar", "youtube:"} {
		var inv *InvalidError
		if _, err := r.p.PatchChannelOverrides(ctx, key, timingCfg("free")); !errors.As(err, &inv) {
			t.Errorf("%q：err = %v，預期 *InvalidError", key, err)
		}
		// 讀取與修改用同一套規則：讀取不合法的 natural key 也是 422，不會回空的覆寫。
		if _, err := r.p.ChannelOverrides(ctx, key); !errors.As(err, &inv) {
			t.Errorf("讀取 %q：err = %v，預期 *InvalidError", key, err)
		}
	}
	if _, err := r.p.PatchChannelOverrides(ctx, "no-such-uuid", timingCfg("free")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UUID 不存在 = %v，預期 store.ErrNotFound", err)
	}
	// 合併後的設定語意不正確（language.targets 是空的）：422，而且新建的頻道一併回復。
	var inv *InvalidError
	bad := map[string]any{"language": map[string]any{"targets": []any{}}}
	if _, err := r.p.PatchChannelOverrides(ctx, "youtube:UCbad", bad); !errors.As(err, &inv) {
		t.Errorf("不合法的覆寫 = %v，預期 *InvalidError", err)
	}
	if _, err := r.st.Read().ChannelByKey(ctx, "youtube", "UCbad"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("失敗的 patch 不該留下頻道：%v", err)
	}
}
