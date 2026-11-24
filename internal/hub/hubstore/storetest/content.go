package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 內容倉庫的共用測試（M5-S3，HREP-R1～R10）。

func shaN(c string) string { return strings.Repeat(c, 64) }

func newShare(userID, bundle string, versions ...hubstore.NewVersion) hubstore.NewShare {
	in := hubstore.NewShare{UserID: userID, License: "CC-BY-4.0", BundleSHA256: bundle, BundleSize: 100,
		Video: hubstore.Video{Platform: "youtube", ExternalID: "abc", Title: "T", ChannelExternalID: "UC1"}}
	for _, v := range versions {
		in.Blobs = append(in.Blobs, hubstore.Blob{SHA256: v.Version.SHA256, Size: 1, MediaType: "application/json"})
		in.Blobs = append(in.Blobs, imageBlobs(v.Images)...)
	}
	in.Versions = versions
	return in
}

func imageBlobs(shas []string) []hubstore.Blob {
	var out []hubstore.Blob
	for _, s := range shas {
		out = append(out, hubstore.Blob{SHA256: s, Size: 1, MediaType: "image/jpeg"})
	}
	return out
}

func ver(variant, sha string, prov string, parents ...string) hubstore.NewVersion {
	return hubstore.NewVersion{Version: hubstore.Version{Kind: "transcript", Variant: variant, Lang: "en", Format: "notesteme-cues+json",
		SHA256: sha, Size: 1, Provenance: []byte(prov), ModelVerified: true, Visibility: "authenticated", License: "CC-BY-4.0"},
		ParentsSHA256: parents}
}

func runContent(t *testing.T, open Open) {
	ctx := context.Background()
	users := func(t *testing.T, s hubstore.Store) (string, string) {
		a, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := s.SignIn(ctx, ident("github", "2", "bob"), "bob")
		if err != nil {
			t.Fatal(err)
		}
		return a.ID, b.ID
	}

	t.Run("ShareDedupesAndLinks", func(t *testing.T) {
		s, clk := fresh(t, open)
		alice, bob := users(t, s)
		src := ver("source", shaN("a"), `{}`)
		norm := ver("normalized", shaN("b"), `{"parents_sha256":["`+shaN("a")+`"]}`, shaN("a"))
		norm.Images = []string{shaN("c")}
		r, err := s.CreateShare(ctx, newShare(alice, "b1", src, norm))
		if err != nil || r.Existing || len(r.Versions) != 2 || r.Versions[0].Deduplicated || r.Video.KeySHA256 == "" || r.Video.ChannelExternalID != "UC1" {
			t.Fatalf("CreateShare = %+v, %v", r, err)
		}
		if r.Versions[0].State != "visible" || r.Versions[1].Variant != "normalized" {
			t.Fatalf("versions = %+v", r.Versions)
		}
		// 同一個人、同一個 bundle：冪等。
		again, err := s.CreateShare(ctx, newShare(alice, "b1", src, norm))
		if err != nil || !again.Existing || again.ShareID != r.ShareID || !again.Versions[0].Deduplicated {
			t.Fatalf("冪等 = %+v, %v", again, err)
		}
		clk.Advance(time.Second)
		other, err := s.CreateShare(ctx, newShare(bob, "b2", src))
		if err != nil || !other.Versions[0].Deduplicated || other.Versions[0].ID != r.Versions[0].ID {
			t.Fatalf("去重 = %+v, %v", other, err)
		}
		ups, err := s.VersionUploaders(ctx, r.Versions[0].ID)
		if err != nil || len(ups) != 2 || ups[0].DisplayName != "alice" || ups[1].DisplayName != "bob" {
			t.Fatalf("uploaders = %+v, %v", ups, err)
		}
		ps, cs, err := s.VersionDerivations(ctx, r.Versions[1].ID)
		if err != nil || len(ps) != 1 || ps[0].ID != r.Versions[0].ID || ps[0].Relation != hubstore.RelationParent || len(cs) != 0 {
			t.Fatalf("derivations = %+v %+v, %v", ps, cs, err)
		}
		if n, err := s.BlobRefs(ctx, shaN("a")); err != nil || n != 1 {
			t.Fatalf("BlobRefs(a) = %d, %v", n, err)
		}
		if n, _ := s.BlobRefs(ctx, shaN("c")); n != 1 {
			t.Fatalf("BlobRefs(圖片) = %d", n)
		}
		vs, err := s.VideoVersions(ctx, r.Video.ID)
		if err != nil || len(vs) != 2 {
			t.Fatalf("VideoVersions = %+v, %v", vs, err)
		}
		v, err := s.VideoByKey(ctx, "youtube", "abc", 0)
		if err != nil || v.ID != r.Video.ID {
			t.Fatalf("VideoByKey = %+v, %v", v, err)
		}
		if _, err := s.VideoByKey(ctx, "youtube", "abc", 1); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("其他分P = %v", err)
		}
		if _, err := s.VideoByID(ctx, "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("VideoByID = %v", err)
		}
		if _, err := s.Version(ctx, "nope"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("Version = %v", err)
		}
		u, err := s.ShareUsage(ctx, alice, epoch.Add(-time.Hour))
		if err != nil || u.Count != 1 || u.Bytes != 100 || !u.Oldest.Equal(epoch) {
			t.Fatalf("ShareUsage = %+v, %v", u, err)
		}
	})

	// 安全修補 A：去重只併入上傳者以一般成員身分看得到、可見度相同的版本；撤回的版本以新分享的值恢復。
	t.Run("DedupRespectsVisibilityAndWithdrawal", func(t *testing.T) {
		s, clk := fresh(t, open)
		alice, bob := users(t, s)
		carol, _, err := s.SignIn(ctx, ident("github", "3", "carol"), "carol")
		if err != nil {
			t.Fatal(err)
		}
		withVis := func(nv hubstore.NewVersion, vis string) hubstore.NewVersion { nv.Version.Visibility = vis; return nv }
		share := func(user, bundle, vis string, canJoin func(string) bool, sha string) hubstore.SharedVersion {
			t.Helper()
			clk.Advance(time.Second)
			in := newShare(user, bundle, withVis(ver("source", sha, `{"model":"`+bundle+`"}`), vis))
			in.CanJoin = canJoin
			r, err := s.CreateShare(ctx, in)
			if err != nil || len(r.Versions) != 1 {
				t.Fatalf("CreateShare(%s) = %+v, %v", bundle, r, err)
			}
			return r.Versions[0]
		}
		uploaders := func(id string) []string {
			t.Helper()
			ups, err := s.VersionUploaders(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, u := range ups {
				names = append(names, u.DisplayName)
			}
			return names
		}
		same := func(got []string, want ...string) bool { return strings.Join(got, ",") == strings.Join(want, ",") }

		// 別人的 private：不併入，另建一列，回應是自己的值。
		ap := share(alice, "a1", "private", nil, shaN("1"))
		bp := share(bob, "b1", "authenticated", nil, shaN("1"))
		if bp.Deduplicated || bp.ID == ap.ID || bp.Visibility != "authenticated" || !same(uploaders(ap.ID), "alice") {
			t.Fatalf("併入別人的 private：%+v（alice 的 %s 上傳者 %v）", bp, ap.ID, uploaders(ap.ID))
		}
		// 同一個人再分享相同內容（不同 bundle、不同可見度）：併入自己的版本。
		again := share(alice, "a1b", "authenticated", nil, shaN("1"))
		if !again.Deduplicated || again.ID != ap.ID || again.Visibility != "private" {
			t.Fatalf("自己的版本 = %+v，預期併入 %s", again, ap.ID)
		}
		// 可見度相同、看得到：併入（HREP-R5）。
		cp := share(carol.ID, "c1", "authenticated", nil, shaN("1"))
		if !cp.Deduplicated || cp.ID != bp.ID || !same(uploaders(bp.ID), "bob", "carol") {
			t.Fatalf("可見度相同 = %+v，預期併入 %s", cp, bp.ID)
		}
		// 反方向：指定 private 的人不併進公開的版本（名字不出現在別人看得到的 uploaders）。
		bpriv := share(bob, "b2", "private", nil, shaN("2"))
		apub := share(alice, "a2", "authenticated", nil, shaN("2"))
		if apub.Deduplicated || apub.ID == bpriv.ID {
			t.Fatalf("authenticated 併入 private：%+v", apub)
		}
		cpriv := share(carol.ID, "c2", "private", nil, shaN("2"))
		if cpriv.Deduplicated || cpriv.ID == apub.ID || cpriv.ID == bpriv.ID || !same(uploaders(apub.ID), "alice") {
			t.Fatalf("private 併入公開的版本：%+v", cpriv)
		}
		// group：看不到的群組不併入；群組成員併入。
		ag := share(alice, "a3", "group:g", func(v string) bool { return v == "group:g" }, shaN("3"))
		bg := share(bob, "b3", "group:g", func(string) bool { return false }, shaN("3"))
		if bg.Deduplicated || bg.ID == ag.ID {
			t.Fatalf("併入看不到的群組：%+v", bg)
		}
		cg := share(carol.ID, "c3", "group:g", func(v string) bool { return v == "group:g" }, shaN("3"))
		if !cg.Deduplicated || cg.ID != ag.ID {
			t.Fatalf("群組成員 = %+v，預期併入 %s", cg, ag.ID)
		}
		// hidden：不併入；removed：併入（內容已移除，不能藉重新上傳繞過）。
		ah := share(alice, "a4", "authenticated", nil, shaN("4"))
		if err := s.SetVersionState(ctx, ah.ID, "hidden"); err != nil {
			t.Fatal(err)
		}
		bh := share(bob, "b4", "authenticated", nil, shaN("4"))
		if bh.Deduplicated || bh.ID == ah.ID || bh.State != "visible" || !same(uploaders(ah.ID), "alice") {
			t.Fatalf("併入 hidden：%+v", bh)
		}
		ar := share(alice, "a5", "authenticated", nil, shaN("5"))
		if err := s.SetVersionState(ctx, ar.ID, "removed"); err != nil {
			t.Fatal(err)
		}
		if br := share(bob, "b5", "authenticated", nil, shaN("5")); !br.Deduplicated || br.ID != ar.ID {
			t.Fatalf("removed = %+v，預期併入 %s", br, ar.ID)
		}
		// withdrawn：以新分享的值恢復（可見度、來源紀錄），不是 deduplicated。
		clk.Advance(time.Second)
		old := withVis(ver("source", shaN("6"), `{"model":"old"}`, shaN("1")), "authenticated")
		old.RegeneratedFrom = bp.ID
		wr, err := s.CreateShare(ctx, newShare(alice, "a6", old))
		if err != nil {
			t.Fatal(err)
		}
		if ps, _, _ := s.VersionDerivations(ctx, wr.Versions[0].ID); len(ps) == 0 {
			t.Fatal("撤回前應該有衍生關係")
		}
		if err := s.WithdrawShare(ctx, alice, wr.ShareID); err != nil {
			t.Fatal(err)
		}
		bw := share(bob, "b6", "private", nil, shaN("6"))
		got, err := s.Version(ctx, wr.Versions[0].ID)
		if err != nil || bw.Deduplicated || bw.ID != got.ID || got.State != "visible" || got.Visibility != "private" ||
			string(got.Provenance) != `{"model":"b6"}` || bw.Visibility != "private" || !same(uploaders(got.ID), "bob") {
			t.Fatalf("恢復撤回的版本 = %+v / %+v, %v", bw, got, err)
		}
		// 舊的來源紀錄宣告的衍生關係不留在恢復的版本上（新的分享沒有宣告任何 parent）。
		if ps, _, err := s.VersionDerivations(ctx, got.ID); err != nil || len(ps) != 0 {
			t.Fatalf("恢復後的 parents = %+v, %v", ps, err)
		}
		vs, err := s.VideoVersions(ctx, wr.Video.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, v := range vs {
			if v.SHA256 == shaN("6") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("恢復時不應另建一列：%d 列", n)
		}
		if refs, _ := s.BlobRefs(ctx, shaN("1")); refs != 2 {
			t.Fatalf("另建的版本共用 blob：BlobRefs = %d，預期 2", refs)
		}
	})

	t.Run("ParentUploadedLaterAndRegenerated", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, bob := users(t, s)
		child, err := s.CreateShare(ctx, newShare(alice, "b1", ver("corrected", shaN("d"), `{"parents_sha256":["`+shaN("e")+`"]}`, shaN("e"))))
		if err != nil {
			t.Fatal(err)
		}
		parent, err := s.CreateShare(ctx, newShare(alice, "b2", ver("normalized", shaN("e"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		ps, _, _ := s.VersionDerivations(ctx, child.Versions[0].ID)
		if len(ps) != 1 || ps[0].ID != parent.Versions[0].ID {
			t.Fatalf("補上的 parent = %+v", ps)
		}
		regen := ver("corrected", shaN("f"), `{}`)
		regen.RegeneratedFrom = parent.Versions[0].ID
		r, err := s.CreateShare(ctx, newShare(bob, "b3", regen))
		if err != nil {
			t.Fatal(err)
		}
		ps, _, _ = s.VersionDerivations(ctx, r.Versions[0].ID)
		if len(ps) != 1 || ps[0].Relation != hubstore.RelationRegeneratedFrom {
			t.Fatalf("regenerated_from = %+v", ps)
		}
		_, cs, _ := s.VersionDerivations(ctx, parent.Versions[0].ID)
		if len(cs) != 2 {
			t.Fatalf("children = %+v", cs)
		}
	})

	t.Run("RemovedHashesBlockTheWholeShare", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, _ := users(t, s)
		r, err := s.CreateShare(ctx, newShare(alice, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil {
			t.Fatal(err)
		}
		id := r.Versions[0].ID
		if err := s.SetVersionState(ctx, id, "removed"); err != nil {
			t.Fatal(err)
		}
		if err := s.AddRemovedHash(ctx, r.Video.ID, shaN("a"), "t1"); err != nil {
			t.Fatal(err)
		}
		if err := s.AddRemovedHash(ctx, r.Video.ID, shaN("a"), "t1"); err != nil {
			t.Fatalf("重複記錄 = %v", err)
		}
		_, err = s.CreateShare(ctx, newShare(alice, "b2", ver("normalized", shaN("b"), `{}`), ver("source", shaN("a"), `{}`)))
		if !errors.Is(err, hubstore.ErrRemovedContent) {
			t.Fatalf("CreateShare = %v，預期 ErrRemovedContent", err)
		}
		vs, _ := s.VideoVersions(ctx, r.Video.ID)
		if u, _ := s.ShareUsage(ctx, alice, time.Time{}); len(vs) != 1 || u.Count != 1 {
			t.Fatalf("拒絕之後 versions = %d、shares = %d", len(vs), u.Count)
		}
		if err := s.SetVersionState(ctx, "nope", "hidden"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("SetVersionState(不存在) = %v", err)
		}
	})

	t.Run("WithdrawShare", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, bob := users(t, s)
		a, _ := s.CreateShare(ctx, newShare(alice, "b1", ver("source", shaN("a"), `{}`), ver("normalized", shaN("b"), `{}`)))
		b, _ := s.CreateShare(ctx, newShare(bob, "b2", ver("source", shaN("a"), `{}`)))
		if err := s.WithdrawShare(ctx, bob, a.ShareID); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("撤回別人的 = %v", err)
		}
		if err := s.WithdrawShare(ctx, alice, a.ShareID); err != nil {
			t.Fatal(err)
		}
		src, _ := s.Version(ctx, a.Versions[0].ID)
		norm, _ := s.Version(ctx, a.Versions[1].ID)
		if src.State != "visible" || norm.State != "withdrawn" {
			t.Fatalf("撤回後 = %s %s", src.State, norm.State)
		}
		if ups, _ := s.VersionUploaders(ctx, src.ID); len(ups) != 1 || ups[0].DisplayName != "bob" {
			t.Fatalf("uploaders = %+v", ups)
		}
		if err := s.WithdrawShare(ctx, alice, a.ShareID); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("撤回兩次 = %v", err)
		}
		_ = s.SetVersionState(ctx, src.ID, "removed")
		if err := s.WithdrawShare(ctx, bob, b.ShareID); err != nil {
			t.Fatal(err)
		}
		if v, _ := s.Version(ctx, src.ID); v.State != "removed" {
			t.Fatalf("removed 的版本撤回後 = %s", v.State)
		}
		// 撤回後同一個 bundle 再分享：新的分享。
		again, err := s.CreateShare(ctx, newShare(alice, "b1", ver("source", shaN("a"), `{}`)))
		if err != nil || again.Existing || again.ShareID == a.ShareID {
			t.Fatalf("撤回後再分享 = %+v, %v", again, err)
		}
	})

	t.Run("ReplaceVersionContent", func(t *testing.T) {
		s, _ := fresh(t, open)
		alice, _ := users(t, s)
		r, _ := s.CreateShare(ctx, newShare(alice, "b1", ver("source", shaN("a"), `{}`)))
		if err := s.ReplaceVersionContent(ctx, r.Versions[0].ID, hubstore.Blob{SHA256: shaN("9"), Size: 5, MediaType: "application/json"}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.Version(ctx, r.Versions[0].ID)
		if v.SHA256 != shaN("9") || v.Size != 5 {
			t.Fatalf("換掉內容 = %+v", v)
		}
		if n, _ := s.BlobRefs(ctx, shaN("a")); n != 0 {
			t.Fatalf("舊內容仍被引用 %d 次", n)
		}
		if err := s.ReplaceVersionContent(ctx, "nope", hubstore.Blob{SHA256: shaN("8"), MediaType: "x"}); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("不存在 = %v", err)
		}
	})
}
