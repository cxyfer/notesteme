package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 列表與 ASR 前查詢的共用測試（M5-S4，HREP-R16、R19）。

func ids(vs []hubstore.ListedVersion) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Variant+"/"+v.Lang)
	}
	return strings.Join(out, ",")
}

func runListing(t *testing.T, open Open) {
	ctx := context.Background()
	public := hubstore.SeenBy{Visibilities: []string{"public", "authenticated"}}

	// seed 建立 alice 的四個版本與 bob 的一個私人版本（影片 abc 與 def，頻道 UC1 與 UC2）。
	seed := func(t *testing.T) (hubstore.Store, string, string, map[string]string) {
		s, clk := fresh(t, open)
		alice, _, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil {
			t.Fatal(err)
		}
		bob, _, err := s.SignIn(ctx, ident("github", "2", "bob"), "bob")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		add := func(user, bundle, ext, channel string, v hubstore.NewVersion) {
			t.Helper()
			clk.Advance(time.Second)
			in := newShare(user, bundle, v)
			in.Video.ExternalID, in.Video.ChannelExternalID = ext, channel
			r, err := s.CreateShare(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			got[bundle] = r.Versions[0].ID
		}
		src := ver("source", shaN("1"), `{}`)
		src.Version.Score = 3
		gpt := ver("corrected", shaN("2"), `{}`)
		gpt.Version.CorrectModel, gpt.Version.Score = "openai/gpt-4.1", 7
		ja := ver("translated", shaN("3"), `{}`)
		ja.Version.Lang, ja.Version.TranslateModel, ja.Version.ModelVerified, ja.Version.Score = "ja", "x/agent", false, 7
		other := ver("source", shaN("4"), `{}`)
		other.Version.Visibility = "group:team"
		secret := ver("normalized", shaN("5"), `{}`)
		secret.Version.Visibility = "private"
		add(alice.ID, "src", "abc", "UC1", src)
		add(alice.ID, "gpt", "abc", "UC1", gpt)
		add(alice.ID, "ja", "abc", "UC1", ja)
		add(alice.ID, "other", "def", "UC2", other)
		add(bob.ID, "secret", "abc", "UC1", secret)
		return s, alice.ID, bob.ID, got
	}

	t.Run("FiltersAndVisibility", func(t *testing.T) {
		s, alice, bob, _ := seed(t)
		list := func(f hubstore.VersionFilter) string {
			t.Helper()
			if f.Limit == 0 {
				f.Limit = 50
			}
			vs, err := s.ListVersions(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			return ids(vs)
		}
		// 依分數排序，同分新的在前；私人與群組的版本不出現。
		if got := list(hubstore.VersionFilter{ByScore: true, Seen: public}); got != "translated/ja,corrected/en,source/en" {
			t.Errorf("score = %s", got)
		}
		if got := list(hubstore.VersionFilter{Seen: public}); got != "translated/ja,corrected/en,source/en" {
			t.Errorf("new = %s", got)
		}
		if got := list(hubstore.VersionFilter{Seen: hubstore.SeenBy{Visibilities: []string{"public", "authenticated", "group:team"}}}); !strings.Contains(got, "source/en,") || strings.Count(got, ",") != 3 {
			t.Errorf("group = %s", got)
		}
		// 上傳者看得到自己的私人版本，別人看不到；moderate 看得到全部。
		if got := list(hubstore.VersionFilter{Seen: hubstore.SeenBy{Visibilities: public.Visibilities, UserID: bob}}); !strings.HasPrefix(got, "normalized/en,") {
			t.Errorf("bob = %s", got)
		}
		if got := list(hubstore.VersionFilter{Seen: hubstore.SeenBy{Visibilities: public.Visibilities, UserID: alice}}); strings.Contains(got, "normalized") {
			t.Errorf("alice 看到了 bob 的私人版本：%s", got)
		}
		if got := list(hubstore.VersionFilter{Seen: hubstore.SeenBy{SeeAll: true}}); strings.Count(got, ",") != 4 {
			t.Errorf("moderate = %s", got)
		}
		// 沒有任何可見度時什麼都看不到。
		if got := list(hubstore.VersionFilter{}); got != "" {
			t.Errorf("empty = %s", got)
		}
		for name, c := range map[string]struct {
			f    hubstore.VersionFilter
			want string
		}{
			"lang":       {hubstore.VersionFilter{Lang: "ja"}, "translated/ja"},
			"model":      {hubstore.VersionFilter{CorrectModel: "openai/gpt-4.1"}, "corrected/en"},
			"translate":  {hubstore.VersionFilter{TranslateModel: "x/agent"}, "translated/ja"},
			"verified":   {hubstore.VersionFilter{ExcludeUnverified: true}, "corrected/en,source/en"},
			"variant":    {hubstore.VersionFilter{Variant: "source"}, "source/en"},
			"kind":       {hubstore.VersionFilter{Kind: "note"}, ""},
			"channel":    {hubstore.VersionFilter{Channel: "UC2"}, ""},
			"platform":   {hubstore.VersionFilter{Platform: "bilibili"}, ""},
			"video":      {hubstore.VersionFilter{Video: &hubstore.VideoKey{Platform: "youtube", ExternalID: "abc"}}, "translated/ja,corrected/en,source/en"},
			"video part": {hubstore.VersionFilter{Video: &hubstore.VideoKey{Platform: "youtube", ExternalID: "abc", Part: 2}}, ""},
			"injection":  {hubstore.VersionFilter{Lang: "en' OR '1'='1"}, ""},
		} {
			c.f.Seen = public
			if got := list(c.f); got != c.want {
				t.Errorf("%s = %q，預期 %q", name, got, c.want)
			}
		}
	})

	t.Run("KeysetPagesAreStable", func(t *testing.T) {
		s, _, _, _ := seed(t)
		for _, byScore := range []bool{true, false} {
			all, err := s.ListVersions(ctx, hubstore.VersionFilter{ByScore: byScore, Limit: 50, Seen: public})
			if err != nil {
				t.Fatal(err)
			}
			var paged []hubstore.ListedVersion
			var after *hubstore.VersionCursor
			for range 10 {
				page, err := s.ListVersions(ctx, hubstore.VersionFilter{ByScore: byScore, Limit: 1, After: after, Seen: public})
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				paged = append(paged, page...)
				last := page[len(page)-1]
				after = &hubstore.VersionCursor{Score: last.SortScore, CreatedAt: last.CreatedAt, ID: last.ID}
			}
			if ids(paged) != ids(all) {
				t.Errorf("byScore=%v：分頁 %s，一次 %s", byScore, ids(paged), ids(all))
			}
		}
	})

	t.Run("RemovedWithdrawnAndHiddenAreNotListed", func(t *testing.T) {
		s, alice, _, got := seed(t)
		for _, st := range []string{"hidden", "removed"} {
			if err := s.SetVersionState(ctx, got["gpt"], st); err != nil {
				t.Fatal(err)
			}
			vs, err := s.ListVersions(ctx, hubstore.VersionFilter{Limit: 50, Seen: hubstore.SeenBy{SeeAll: true, UserID: alice}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(ids(vs), "corrected") {
				t.Errorf("%s 的版本出現在列表中：%s", st, ids(vs))
			}
		}
	})

	t.Run("LookupByPrefix", func(t *testing.T) {
		s, _, bob, _ := seed(t)
		abc, err := s.VideoByKey(ctx, "youtube", "abc", 0)
		if err != nil {
			t.Fatal(err)
		}
		look := func(platform, prefix string, seen hubstore.SeenBy) []hubstore.LookupVersion {
			t.Helper()
			vs, err := s.LookupVersions(ctx, platform, prefix, seen)
			if err != nil {
				t.Fatal(err)
			}
			return vs
		}
		vs := look("youtube", abc.KeySHA256[:4], public)
		if len(vs) != 1 || vs[0].Variant != "source" || vs[0].VideoKeySHA256 != abc.KeySHA256 {
			t.Fatalf("lookup = %+v", vs)
		}
		// bob 看得到自己的私人 normalized 版本（分數 0，排在後面）。
		if vs := look("youtube", abc.KeySHA256[:4], hubstore.SeenBy{Visibilities: public.Visibilities, UserID: bob}); len(vs) != 2 || vs[1].Variant != "normalized" {
			t.Errorf("bob = %+v", vs)
		}
		if vs := look("bilibili", abc.KeySHA256[:4], public); len(vs) != 0 {
			t.Errorf("其他平台 = %+v", vs)
		}
		// 不同前綴、空的可見度都沒有結果。
		other := "0000"
		if strings.HasPrefix(abc.KeySHA256, other) {
			other = "ffff"
		}
		if vs := look("youtube", other, public); len(vs) != 0 {
			t.Errorf("其他前綴 = %+v", vs)
		}
		if vs := look("youtube", abc.KeySHA256[:4], hubstore.SeenBy{}); len(vs) != 0 {
			t.Errorf("沒有可見度 = %+v", vs)
		}
	})
}
