package store

import (
	"context"
	"errors"
	"testing"
)

func TestHubSharesAndImports(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	w := st.Write()
	item, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "hubxxxxxxxx", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	it := item.ID
	if err := w.InsertHubShare(ctx, HubShare{ItemID: it, HubURL: "https://hub", ShareID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertHubShare(ctx, HubShare{ItemID: it, HubURL: "https://hub", ShareID: "s2", VersionsJSON: `[{"x":1}]`}); err != nil {
		t.Fatal(err)
	}
	shares, err := st.Read().ItemHubShares(ctx, it)
	if err != nil || len(shares) != 2 || shares[0].VersionsJSON != "[]" || shares[1].ShareID != "s2" {
		t.Fatalf("shares = %+v %v", shares, err)
	}
	if _, err := st.Read().HubImport(ctx, it); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有待處理的匯入：%v", err)
	}
	for _, sha := range []string{"a", "b"} {
		if err := w.UpsertHubImport(ctx, HubImport{ItemID: it, VersionID: "v-" + sha, SHA256: sha, OriginJSON: "{}", Content: []byte(sha)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Read().HubImport(ctx, it)
	if err != nil || got.VersionID != "v-b" || string(got.Content) != "b" {
		t.Fatalf("got = %+v %v", got, err)
	}
	if err := w.DeleteHubImport(ctx, it, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read().HubImport(ctx, it); err != nil {
		t.Fatal("sha256 不同的匯入要保留")
	}
	if err := w.DeleteHubImport(ctx, it, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read().HubImport(ctx, it); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪除後應該沒有")
	}
}
