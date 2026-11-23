package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/store"
)

// reviewSetup 建立兩個頻道與三個候選（UC1：Alpha、Beta；UC2：Gamma），回傳 term → id。
func reviewSetup(t *testing.T) (*itemsRig, map[string]string, map[string]string) {
	t.Helper()
	r, tok := glossaryRig(t)
	ctx := context.Background()
	ids := map[string]string{}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range []struct{ key, term string }{{"UC1", "Alpha"}, {"UC1", "Beta"}, {"UC2", "Gamma"}} {
		rec := r.do("PUT", "/api/glossaries/channel/youtube:"+c.key, `{"terms":[]}`, withBearer(tok[auth.ScopeAdmin]))
		chID, _ := decodeBody(t, rec)["id"].(string)
		if err := r.st.Tx(ctx, func(w *store.Repo) error {
			_, err := w.AddTermCandidateAt(ctx, chID, "", c.term, at.Add(time.Duration(i)*time.Second))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		cands, err := r.st.Read().TermCandidates(ctx, chID)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range cands {
			ids[x.Term] = x.ID
		}
	}
	return r, tok, ids
}

func firstPath(t *testing.T, got map[string]any) string {
	t.Helper()
	errs, _ := got["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	p, _ := errs[0].(map[string]any)["path"].(string)
	return p
}

// GLOS-R5：跨頻道的審核清單（read）：回應形狀、篩選、游標、state 的 422。
func TestTermCandidatesReviewAPI(t *testing.T) {
	r, tok, _ := reviewSetup(t)
	read := withBearer(tok[auth.ScopeRead])
	if rec := r.do("GET", "/api/term-candidates", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 = %d", rec.Code)
	}
	rec := r.do("GET", "/api/term-candidates", "", read)
	got := decodeBody(t, rec)
	list, _ := got["candidates"].([]any)
	if rec.Code != http.StatusOK || got["total"] != float64(3) || len(list) != 3 {
		t.Fatalf("清單 = %d %s", rec.Code, rec.Body)
	}
	c := list[0].(map[string]any)
	ch, _ := c["channel"].(map[string]any)
	if c["term"] != "Alpha" || c["state"] != "pending" || c["item"] != nil || c["decided_term"] != nil || c["decided_at"] != nil ||
		ch["key"] != "youtube:UC1" || ch["id"] == "" {
		t.Errorf("第一列 = %v", c)
	}
	if _, ok := got["next_cursor"]; ok {
		t.Errorf("最後一頁不該有 next_cursor：%s", rec.Body)
	}

	rec = r.do("GET", "/api/term-candidates?channel=youtube:UC2", "", read)
	if got := decodeBody(t, rec); got["total"] != float64(1) {
		t.Errorf("頻道篩選 = %s", rec.Body)
	}
	for _, q := range []string{"state=maybe", "state=PENDING"} {
		rec = r.do("GET", "/api/term-candidates?"+q, "", read)
		if got := decodeBody(t, rec); rec.Code != http.StatusUnprocessableEntity || got["code"] != CodeInvalidInput || firstPath(t, got) != "state" {
			t.Errorf("%s = %d %s", q, rec.Code, rec.Body)
		}
	}

	// 游標：每頁 2 筆；換了篩選條件或被竄改的游標回 422。
	rec = r.do("GET", "/api/term-candidates?limit=2", "", read)
	got = decodeBody(t, rec)
	cur, _ := got["next_cursor"].(string)
	if cur == "" || len(got["candidates"].([]any)) != 2 {
		t.Fatalf("第一頁 = %s", rec.Body)
	}
	rec = r.do("GET", "/api/term-candidates?limit=2&cursor="+url.QueryEscape(cur), "", read)
	got = decodeBody(t, rec)
	if page := got["candidates"].([]any); rec.Code != http.StatusOK || len(page) != 1 || page[0].(map[string]any)["term"] != "Gamma" || got["total"] != float64(3) {
		t.Errorf("第二頁 = %s", rec.Body)
	}
	for _, q := range []string{"state=all&cursor=" + url.QueryEscape(cur), "cursor=" + url.QueryEscape(cur+"x")} {
		if rec := r.do("GET", "/api/term-candidates?"+q, "", read); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := r.do("GET", "/api/term-candidates?limit=201", "", read); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("limit=201 = %d", rec.Code)
	}
	// 影音列表的游標不能拿來這裡用（指紋有前綴）。
	if rec := r.do("GET", "/api/items?limit=1", "", read); rec.Code == http.StatusOK {
		if c, _ := decodeBody(t, rec)["next_cursor"].(string); c != "" {
			if rec := r.do("GET", "/api/term-candidates?cursor="+url.QueryEscape(c), "", read); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("影音列表的游標 = %d", rec.Code)
			}
		}
	}
}

// GLOS-R1、R2：PATCH 的 term、scope；錯誤的 path 與訊息照規格；glossary_full 是 409。
func TestTermCandidateDecideWithTermAPI(t *testing.T) {
	r, tok, ids := reviewSetup(t)
	admin := withBearer(tok[auth.ScopeAdmin])
	patch := func(term, body string) (*int, map[string]any) {
		rec := r.do("PATCH", "/api/channels/youtube:UC1/term-candidates/"+ids[term], body, admin)
		code := rec.Code
		return &code, decodeBody(t, rec)
	}
	cases := []struct{ body, path, msg string }{
		{`{"state":"rejected","term":"x"}`, "term", "拒絕時不能指定 term 或 scope。"},
		{`{"state":"rejected","scope":"global"}`, "scope", "拒絕時不能指定 term 或 scope。"},
		{`{"state":"accepted","scope":"profile"}`, "scope", ""},
		{`{"state":"accepted","term":"\u0007\u0007"}`, "term", "術語不能是空白。"},
		{`{"state":"accepted","term":"` + strings.Repeat("k", 101) + `"}`, "term", "術語最多 100 個字元。"},
	}
	for _, c := range cases {
		code, got := patch("Alpha", c.body)
		if *code != http.StatusUnprocessableEntity || got["code"] != CodeInvalidInput || firstPath(t, got) != c.path || (c.msg != "" && got["message"] != c.msg) {
			t.Errorf("%s = %d %v", c.body, *code, got)
		}
	}
	code, got := patch("Alpha", `{"state":"accepted","term":"  Alpha\tOne ","scope":"global"}`)
	if *code != http.StatusOK || got["decided_term"] != "Alpha One" || got["decided_scope"] != "global" || got["decided_at"] == nil || got["term"] != "Alpha" {
		t.Fatalf("改寫並加到全域 = %d %v", *code, got)
	}
	// C4 的 GET 也有 decided_*。
	rec := r.do("GET", "/api/channels/youtube:UC1/term-candidates", "", withBearer(tok[auth.ScopeRead]))
	if c := decodeBody(t, rec)["candidates"].([]any)[0].(map[string]any); c["decided_term"] != "Alpha One" {
		t.Errorf("頻道的候選 = %v", c)
	}
	// 全域詞彙表已滿：409 glossary_full。
	many := make([]string, 500)
	for i := range many {
		many[i] = fmt.Sprintf(`"t%03d"`, i)
	}
	if rec := r.do("PUT", "/api/glossaries/global/default", `{"terms":[`+strings.Join(many, ",")+`]}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("填滿全域 = %d %s", rec.Code, rec.Body)
	}
	code, got = patch("Beta", `{"state":"accepted","scope":"global"}`)
	if *code != http.StatusConflict || got["code"] != CodeGlossaryFull || got["message"] != "詞彙表已有 500 個詞，請先刪除不用的詞再接受新的術語。" {
		t.Errorf("已滿 = %d %v", *code, got)
	}
}

// GLOS-R4：批次決定（admin）；錯誤的 path 指到那一項，整批不套用。
func TestTermCandidatesDecideBatchAPI(t *testing.T) {
	r, tok, ids := reviewSetup(t)
	admin := withBearer(tok[auth.ScopeAdmin])
	post := func(body string) (int, map[string]any) {
		rec := r.do("POST", "/api/term-candidates/decide", body, admin)
		return rec.Code, decodeBody(t, rec)
	}
	if rec := r.do("POST", "/api/term-candidates/decide", "", withBearer(tok[auth.ScopeRead])); rec.Code != http.StatusForbidden || decodeBody(t, rec)["required_scope"] != "admin" {
		t.Errorf("read 不能批次決定 = %d %s", rec.Code, rec.Body)
	}
	d := func(term, extra string) string {
		return `{"candidate":"` + ids[term] + `","state":"accepted"` + extra + `}`
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = `{"candidate":"c` + fmt.Sprint(i) + `","state":"accepted"}`
	}
	cases := []struct {
		body   string
		status int
		code   string
		path   string
	}{
		{`{"decisions":[]}`, 422, CodeInvalidInput, "decisions"},
		{`{"decisions":[` + strings.Join(many, ",") + `]}`, 422, CodeInvalidInput, "decisions"},
		{`{"decisions":[` + d("Alpha", "") + `,` + d("Beta", "") + `,` + d("Alpha", "") + `]}`, 422, CodeInvalidInput, "decisions.2.candidate"},
		{`{"decisions":[` + d("Alpha", "") + `,` + d("Beta", `,"term":" "`) + `]}`, 422, CodeInvalidInput, "decisions.1.term"},
		{`{"decisions":[` + d("Alpha", `,"scope":"x"`) + `]}`, 422, CodeInvalidInput, "decisions.0.scope"},
		{`{"decisions":[{"candidate":"` + ids["Alpha"] + `","state":"maybe"}]}`, 422, CodeInvalidInput, "decisions.0.state"},
		{`{"decisions":[` + d("Alpha", "") + `,{"candidate":"nope","state":"accepted"}]}`, 404, CodeNotFound, "decisions.1.candidate"},
	}
	for _, c := range cases {
		status, got := post(c.body)
		if status != c.status || got["code"] != c.code || firstPath(t, got) != c.path {
			t.Errorf("%.80s = %d %v", c.body, status, got)
		}
	}
	// 失敗的批次沒有套用任何一項。
	if rec := r.do("GET", "/api/term-candidates", "", withBearer(tok[auth.ScopeRead])); decodeBody(t, rec)["total"] != float64(3) {
		t.Errorf("失敗之後 = %s", rec.Body)
	}
	status, got := post(`{"decisions":[` + d("Gamma", `,"term":"Gamma Ray","scope":"global"`) + `,{"candidate":"` + ids["Beta"] + `","state":"rejected"}]}`)
	list, _ := got["candidates"].([]any)
	if status != http.StatusOK || len(list) != 2 || list[0].(map[string]any)["decided_term"] != "Gamma Ray" || list[1].(map[string]any)["state"] != "rejected" {
		t.Fatalf("批次 = %d %v", status, got)
	}
	status, got = post(`{"decisions":[` + d("Alpha", "") + `,` + d("Beta", "") + `]}`)
	if status != http.StatusConflict || got["code"] != CodeCandidateDecided || firstPath(t, got) != "decisions.1" {
		t.Errorf("已拒絕的改成接受 = %d %v", status, got)
	}
}

// GLOS-R5：OpenAPI 的 TermCandidateReview.item 必須允許 null（來源影音已永久刪除），欄位仍然必有。
func TestTermCandidateReviewItemNullableSchema(t *testing.T) {
	b, err := OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string                  `json:"required"`
				Properties map[string]map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Components.Schemas["TermCandidateReview"]
	if !slices.Contains(s.Required, "item") {
		t.Errorf("item 應該必有：%v", s.Required)
	}
	one, _ := s.Properties["item"]["oneOf"].([]any)
	var hasRef, hasNull bool
	for _, o := range one {
		m, _ := o.(map[string]any)
		hasRef = hasRef || m["$ref"] == "#/components/schemas/TermCandidateItem"
		hasNull = hasNull || m["type"] == "null"
	}
	if !hasRef || !hasNull || len(one) != 2 {
		t.Errorf("item 的 schema = %v", s.Properties["item"])
	}
}
