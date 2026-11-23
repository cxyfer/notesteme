package sponsorblock

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const vid = "spon01xxxxx"

func TestHashPrefixAndRequestURL(t *testing.T) {
	if got := HashPrefix(vid); len(got) != PrefixLen || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("HashPrefix = %q", got)
	}
	raw, err := RequestURL("https://sb.example", vid, []string{"sponsor", "intro"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, vid) {
		t.Fatalf("網址含有影片 ID：%s", raw)
	}
	u, _ := url.Parse(raw)
	if u.Path != "/api/skipSegments/"+HashPrefix(vid) {
		t.Errorf("path = %q", u.Path)
	}
	if got := u.Query().Get("categories"); got != `["sponsor","intro"]` {
		t.Errorf("categories = %q", got)
	}
	if got := u.Query().Get("actionTypes"); got != `["skip"]` {
		t.Errorf("actionTypes = %q", got)
	}
}

func TestParseFilters(t *testing.T) {
	body := `[
	 {"videoID":"other000000","segments":[{"category":"sponsor","actionType":"skip","segment":[1,5],"UUID":"x"}]},
	 {"videoID":"spon01xxxxx","segments":[
	   {"category":"sponsor","actionType":"skip","segment":[60,90],"UUID":"u1"},
	   {"category":"sponsor","actionType":"mute","segment":[100,110],"UUID":"u2"},
	   {"category":"intro","actionType":"skip","segment":[0,10],"UUID":"u3"},
	   {"category":"selfpromo","actionType":"skip","segment":[30],"UUID":"u4"},
	   {"category":"selfpromo","actionType":"skip","segment":[50,40],"UUID":"u5"},
	   {"category":"selfpromo","actionType":"skip","segment":[-5,3],"UUID":"<bad uuid>"}
	 ]}]`
	segs, found, err := Parse([]byte(body), vid, []string{"sponsor", "selfpromo"})
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	want := []Segment{{"sponsor", 60000, 90000, "u1"}, {"selfpromo", 0, 3000, ""}}
	if !slices.Equal(segs, want) {
		t.Fatalf("segs = %+v，預期 %+v", segs, want)
	}
	if _, found, _ := Parse([]byte(body), "nothere0000", []string{"sponsor"}); found {
		t.Error("沒有相符的 videoID 卻回報 found")
	}
	if _, _, err := Parse([]byte(`{"not":"array"}`), vid, nil); err == nil {
		t.Error("不是陣列應該失敗")
	}
}

func TestClean(t *testing.T) {
	in := []Segment{
		{"selfpromo", 480000, 500000, "c"},
		{"sponsor", 60000, 90000, "a"},
		{"selfpromo", 80000, 95000, "b"}, // 重疊：併入前一段，類別取最早開始的
		{"filler", 95000, 96000, "d"},    // 相接：也合併
		{"intro", 200000, 200500, "e"},   // 短於 1 秒：捨棄
		{"outro", 590000, 700000, "f"},   // 超出片長：截掉
		{"outro", 600000, 610000, "g"},   // 完全在片長之後：捨棄
	}
	got := Clean(in, 600000)
	want := []Segment{{"sponsor", 60000, 96000, "a"}, {"selfpromo", 480000, 500000, "c"}, {"outro", 590000, 600000, "f"}}
	if !slices.Equal(got, want) {
		t.Fatalf("Clean = %+v，預期 %+v", got, want)
	}
	// 截掉之後短於 1 秒也捨棄；片長未知時不截。
	if got := Clean([]Segment{{"outro", 599500, 700000, ""}}, 600000); len(got) != 0 {
		t.Errorf("截掉後短於 1 秒應捨棄：%+v", got)
	}
	if got := Clean([]Segment{{"outro", 599500, 700000, ""}}, 0); len(got) != 1 || got[0].EndMS != 700000 {
		t.Errorf("片長未知時不截：%+v", got)
	}
}

func newServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL, Timeout: 2 * time.Second}
}

func TestLookupOutcomes(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]apiVideo{{VideoID: vid, Segments: []apiSegment{{Category: "sponsor", ActionType: "skip", Segment: []float64{60, 90}, UUID: "u"}}}})
	}
	cases := []struct {
		name    string
		h       http.HandlerFunc
		outcome string
		reason  string
		n       int
	}{
		{"ok", ok, OutcomeOK, "", 1},
		{"404", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, OutcomeNotFound, "", 0},
		{"503", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }, OutcomeError, "HTTP 503", 0},
		{"429", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }, OutcomeError, "HTTP 429", 0},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }, OutcomeError, "HTTP 302", 0},
		{"bad json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }, OutcomeError, "回應格式不正確", 0},
		{"too large", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("[" + strings.Repeat(" ", int(MaxResponse)) + "]"))
		}, OutcomeError, "回應超過大小上限", 0},
		{"other video only", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"videoID":"other000000","segments":[]}]`))
		}, OutcomeNotFound, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := newServer(t, c.h).Lookup(context.Background(), vid, []string{"sponsor"}, 600000)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != c.outcome || res.Reason != c.reason || len(res.Segments) != c.n || res.Segments == nil {
				t.Fatalf("res = %+v", res)
			}
		})
	}
}

func TestLookupTimeoutAndCancel(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	c.Timeout = 50 * time.Millisecond
	res, err := c.Lookup(context.Background(), vid, []string{"sponsor"}, 0)
	if err != nil || res.Outcome != OutcomeError || res.Reason != "連線失敗" {
		t.Fatalf("逾時：res=%+v err=%v", res, err)
	}
	// 上層 ctx 被取消（關閉中）時回傳錯誤，不能當成查詢失敗寫回。
	ctx, cancel := context.WithCancel(context.Background())
	c.Timeout = 5 * time.Second
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := c.Lookup(ctx, vid, []string{"sponsor"}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消：err = %v", err)
	}
}

func TestLookupRejectsBadVideoID(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient, BaseURL: "http://127.0.0.1:1"}
	if _, err := c.Lookup(context.Background(), "../../x", nil, 0); err == nil {
		t.Fatal("格式不正確的影片 ID 應該失敗")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`[{"videoID":"spon01xxxxx","segments":[{"category":"sponsor","actionType":"skip","segment":[1,2.5],"UUID":"a"}]}]`), int64(600000))
	f.Add([]byte(`[]`), int64(0))
	f.Add([]byte(`[{"videoID":"spon01xxxxx","segments":[{"category":"sponsor","actionType":"skip","segment":[1e308,1e309]}]}]`), int64(-1))
	f.Fuzz(func(t *testing.T, body []byte, dur int64) {
		segs, _, err := Parse(body, vid, Categories)
		if err != nil {
			return
		}
		out := Clean(segs, dur)
		for i, s := range out {
			if s.StartMS < 0 || s.EndMS-s.StartMS < MinSegmentMS {
				t.Fatalf("片段不合法：%+v", s)
			}
			if dur > 0 && s.EndMS > dur {
				t.Fatalf("超出片長：%+v", s)
			}
			if i > 0 && s.StartMS <= out[i-1].EndMS {
				t.Fatalf("沒有排序或沒有合併：%+v", out)
			}
			if !slices.Contains(Categories, s.Category) {
				t.Fatalf("不認識的類別：%+v", s)
			}
		}
	})
}
