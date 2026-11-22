package store

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// ItemsOverridingPrompt 是預先篩選：含 "prompt" 鍵、而且 overrides 含這個 id 字串的影音（垃圾桶中的也算），依建立順序。
// 沒有 "prompt" 鍵、或完全不含 id 的列不會回來；id 只是子字串的列會回來，由呼叫端解析 JSON 排除。
func TestItemsOverridingPrompt_Prefilter(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	add := func(ext, overrides string) Item {
		t.Helper()
		clk.Advance(time.Second)
		it, _, err := s.Write().InsertItemIfAbsent(ctx, NewItem{
			Platform: "youtube", ExternalID: ext, URL: "u", Overrides: json.RawMessage(overrides),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	a := add("aaaaaaaaaaa", `{"stages":{"correct":{"prompt":"my-correct@2"}}}`)
	add("bbbbbbbbbbb", `{"stages":{"correct":{"prompt":"other"}}}`)
	add("ccccccccccc", `{}`)
	add("ddddddddddd", `{"glossary":["my-correct"]}`) // 有 id、沒有 "prompt" 鍵
	e := add("eeeeeeeeeee", `{"stages":{"correct":{"prompt":"my-correct-2"}}}`)
	g := add("ggggggggggg", `{"stages":{"segment":{"prompt":"my-correct"}}}`)
	if _, _, err := s.Write().TrashItem(ctx, g.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Read().ItemsOverridingPrompt(ctx, "my-correct")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range rows {
		got = append(got, it.ID)
	}
	if want := []string{a.ID, e.ID, g.ID}; !slices.Equal(got, want) {
		t.Errorf("ItemsOverridingPrompt = %v，預期 %v（含子字串的 %s 與垃圾桶中的 %s）", got, want, e.ExternalID, g.ExternalID)
	}
	if rows, err := s.Read().ItemsOverridingPrompt(ctx, "nobody"); err != nil || len(rows) != 0 {
		t.Errorf("沒有人引用 = %v, %v", rows, err)
	}
}
