package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

type searchBody struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
	Next    string         `json:"next_cursor"`
}

func newSearchRig(t *testing.T) (*itemsRig, string) {
	t.Helper()
	var p *pipeline.Pipeline
	r := newItemsRigAPI(t, func(d *Deps) { p = d.Pipeline })
	id := r.ingestOne(abcURL)
	cues := []subtitle.Cue{
		{StartMS: 0, EndMS: 1000, Text: "Kafka one"},
		{StartMS: 1000, EndMS: 2000, Text: "<b>kafka</b> two"},
		{StartMS: 2000, EndMS: 3000, Text: "kafka three"},
	}
	if err := p.SeedTranscript(context.Background(), pipeline.SeedRequest{ItemID: id, Variant: subtitle.VariantCorrected, Lang: "en", Cues: cues}); err != nil {
		t.Fatal(err)
	}
	return r, id
}

func (r *itemsRig) search(t *testing.T, query string) searchBody {
	t.Helper()
	rec := r.owner("GET", "/api/search?"+query, "")
	if rec.Code != 200 {
		t.Fatalf("GET /api/search?%s = %d %s", query, rec.Code, rec.Body)
	}
	var b searchBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSearchRouteResultsAndPaging(t *testing.T) {
	r, id := newSearchRig(t)
	all := r.search(t, "q=KAFKA")
	if all.Total != 3 || len(all.Results) != 3 || all.Next != "" {
		t.Fatalf("結果 = %+v", all)
	}
	h := all.Results[1]
	// 原文中的標記照字面回傳，不插入任何 HTML。
	if h.Item.ID != id || h.Item.Ref != "yt:abc123xxxxx" || h.Cue != 2 || h.Text != "<b>kafka</b> two" || h.Start != "00:00:01.000" ||
		h.Variant != "corrected" || h.Lang != "en" || len(h.Highlights) != 1 || h.Highlights[0].Start != 3 || h.Highlights[0].End != 8 ||
		h.Before == nil || *h.Before != "Kafka one" || h.After == nil || *h.After != "kafka three" {
		t.Fatalf("第 2 句 = %+v", h)
	}
	if all.Results[0].Before != nil || all.Results[2].After != nil {
		t.Fatal("第一句沒有 before、最後一句沒有 after")
	}
	p1 := r.search(t, "q=kafka&limit=2")
	if len(p1.Results) != 2 || p1.Next == "" || p1.Total != 3 {
		t.Fatalf("第一頁 = %+v", p1)
	}
	p2 := r.search(t, "q=kafka&limit=2&cursor="+url.QueryEscape(p1.Next))
	if len(p2.Results) != 1 || p2.Results[0].Cue != 3 || p2.Next != "" {
		t.Fatalf("第二頁 = %+v", p2)
	}
	// 游標只在相同的查詢條件下有效。
	for _, q := range []string{"q=kafk&cursor=" + url.QueryEscape(p1.Next), "q=kafka&lang=en&cursor=" + url.QueryEscape(p1.Next)} {
		if rec := r.owner("GET", "/api/search?"+q, ""); rec.Code != 422 || !strings.Contains(rec.Body.String(), "游標不正確或已失效") {
			t.Fatalf("%s = %d %s", q, rec.Code, rec.Body)
		}
	}
	// 2 個字走掃描，結果相同。
	if b := r.search(t, "q=kA"); b.Total != 3 {
		t.Fatalf("掃描 = %+v", b)
	}
}

func TestSearchRouteRefusesBadInput(t *testing.T) {
	r, _ := newSearchRig(t)
	cases := []struct{ query, code, msg, path string }{
		{"q=", CodeInvalidInput, "請輸入要搜尋的文字。", "q"},
		{"mode=fulltext", CodeInvalidInput, "請輸入要搜尋的文字。", "q"},
		{"q=%20%09%20", CodeInvalidInput, "請輸入要搜尋的文字。", "q"},
		{"q=a%00b", CodeInvalidInput, "搜尋文字含有無法使用的字元。", "q"},
		{"q=a%ffb", CodeInvalidInput, "搜尋文字含有無法使用的字元。", "q"},
		{"q=" + strings.Repeat("%E5%AD%97", 201), CodeInvalidInput, "搜尋文字最多 200 個字。", "q"},
		{"q=kafka&limit=0", CodeInvalidInput, "limit", "limit"},
		{"q=kafka&limit=101", CodeInvalidInput, "limit", "limit"},
		{"q=kafka&limit=abc", CodeInvalidInput, "limit", "limit"},
		{"q=kafka&lang=" + strings.Repeat("x", 300), CodeInvalidInput, "lang", "lang"},
		{"q=kafka&mode=semantic", CodeUnsupportedMode, "不支援這種搜尋方式：semantic。", ""},
		{"q=kafka&mode=" + strings.Repeat("m", 100), CodeUnsupportedMode, strings.Repeat("m", maxModeEcho) + "…", ""},
		{"q=kafka&cursor=not!base64", CodeInvalidInput, "游標不正確或已失效", ""},
	}
	for _, c := range cases {
		rec := r.owner("GET", "/api/search?"+c.query, "")
		e := errBody(t, rec)
		if rec.Code != 422 || e.Code != c.code || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("%s = %d %s", c.query, rec.Code, rec.Body)
		}
		if c.path != "" && (len(e.Errors) != 1 || e.Errors[0].Path != c.path) {
			t.Errorf("%s 的 errors = %+v", c.query, e.Errors)
		}
	}
	// FTS5 的語法照字面比對：一律 200。
	for _, q := range []string{`kafka OR one`, `NEAR(kafka one)`, `text_fold:kafka`, `kafka*`, `"kafka`, `(kafka)`, `^kafka`, `-one`, `"`, `""`} {
		r.search(t, "q="+url.QueryEscape(q))
	}
}

func TestSearchCursorDecoding(t *testing.T) {
	key := []byte("k")
	fp := searchFingerprint("kafka", "")
	good := encodeSearchCursor(store.SearchCursor{CreatedAtMs: 5, ItemID: "0190a5d2-0000-7000-8000-000000000000", Translation: 1, Lang: "en", Cue: 2}, key, fp)
	c, err := decodeSearchCursor(good, key, fp)
	if err != nil || c.CreatedAtMs != 5 || c.Translation != 1 || c.Lang != "en" || c.Cue != 2 {
		t.Fatalf("decode = %+v, %v", c, err)
	}
	enc := func(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	tamper := func(k string, v any) string {
		n := map[string]any{}
		for kk, vv := range m {
			n[kk] = vv
		}
		n[k] = v
		b, _ := json.Marshal(n)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	bad := []string{
		"", "%%", good + "=", good + "\n", strings.Repeat("A", maxCursorLen+1),
		enc(`{"v":1}`), enc(string(raw) + `{}`), enc(`[]`),
		tamper("c", 3), tamper("t", 6), tamper("l", "fr"), tamper("r", 0), tamper("v", 2), tamper("r", 2),
		tamper("c", 0), tamper("id", "not-a-uuid"), tamper("id", "{0190a5d2-0000-7000-8000-000000000000}"), tamper("x", 1),
	}
	for _, s := range bad {
		if _, err := decodeSearchCursor(s, key, fp); err == nil {
			t.Errorf("接受了不正確的游標 %q", s)
		}
	}
	if _, err := decodeSearchCursor(good, key, searchFingerprint("kafka", "en")); err == nil {
		t.Error("查詢條件改變時游標仍然有效")
	}
	if _, err := decodeSearchCursor(good, []byte("other"), fp); err == nil {
		t.Error("金鑰改變（server 重啟）時游標仍然有效")
	}
}

func FuzzDecodeSearchCursor(f *testing.F) {
	key := []byte("k")
	fp := searchFingerprint("q", "")
	f.Add(encodeSearchCursor(store.SearchCursor{ItemID: "0190a5d2-0000-7000-8000-000000000000", Cue: 1}, key, fp))
	f.Add("eyJ2IjoxfQ")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := decodeSearchCursor(s, key, fp)
		if err == nil && (c.Cue < 1 || c.CreatedAtMs < 0) {
			t.Fatalf("接受了位置不正確的游標：%+v", c)
		}
	})
}
