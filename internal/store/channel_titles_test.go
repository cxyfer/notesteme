package store

import (
	"context"
	"maps"
	"testing"
)

// ChannelTitles：一次查多個頻道；不存在的 ID 不出現，交易中看得到尚未提交的頻道。
func TestChannelTitles(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a, err := s.Write().UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UCa", Title: "頻道 A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Write().UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UCb"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().ChannelTitles(ctx, []string{a, b, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{a: "頻道 A", b: ""}; !maps.Equal(got, want) {
		t.Fatalf("ChannelTitles = %v，預期 %v", got, want)
	}
	if got, err := s.Read().ChannelTitles(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("空的 ID 列表 = %v, %v", got, err)
	}
	err = s.Tx(ctx, func(r *Repo) error {
		c, err := r.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UCc", Title: "C"})
		if err != nil {
			return err
		}
		got, err := r.ChannelTitles(ctx, []string{c})
		if err != nil {
			return err
		}
		if got[c] != "C" {
			t.Errorf("交易中 ChannelTitles = %v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
