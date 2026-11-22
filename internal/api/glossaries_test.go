package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/store"
)

func glossaryRig(t *testing.T) (*itemsRig, map[string]string) {
	t.Helper()
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { svc = d.Auth })
	toks := map[string]string{}
	for _, sc := range []string{auth.ScopeRead, auth.ScopeAdmin} {
		ct, err := svc.CreateToken(context.Background(), "only-"+sc, []string{sc}, nil)
		if err != nil {
			t.Fatal(err)
		}
		toks[sc] = ct.Token
	}
	return r, toks
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("回應不是 JSON 物件：%s", rec.Body)
	}
	return m
}

// COR-R4：全域與頻道詞彙表。讀取用 read、寫入用 admin；沒有儲存過時 terms 是空陣列；整份取代。
func TestGlossariesAPI(t *testing.T) {
	r, tok := glossaryRig(t)
	const key = "youtube:UCfixture000000000000000"

	// 沒有身分 401；read 不能寫入 403。
	if rec := r.do("GET", "/api/glossaries/global/default", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 = %d", rec.Code)
	}
	if rec := r.do("PUT", "/api/glossaries/global/default", `{"terms":["a"]}`, withBearer(tok[auth.ScopeRead])); rec.Code != http.StatusForbidden {
		t.Errorf("read token 寫入 = %d %s", rec.Code, rec.Body)
	}

	// 還沒有儲存過：200，terms 是 []，updated_at 是 null。
	rec := r.do("GET", "/api/glossaries/global/default", "", withBearer(tok[auth.ScopeRead]))
	got := decodeBody(t, rec)
	if rec.Code != http.StatusOK || !reflect.DeepEqual(got["terms"], []any{}) || got["updated_at"] != nil || got["scope"] != "global" {
		t.Fatalf("GET 全域（空）= %d %s", rec.Code, rec.Body)
	}
	// 全域詞彙表的 id 固定是 default。
	if rec := r.do("GET", "/api/glossaries/global/other", "", withBearer(tok[auth.ScopeRead])); rec.Code != http.StatusNotFound {
		t.Errorf("global/other = %d，預期 404", rec.Code)
	}
	if rec := r.do("GET", "/api/glossaries/team/x", "", withBearer(tok[auth.ScopeRead])); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("不認識的 scope = %d，預期 4xx", rec.Code)
	}

	// 整份取代，去前後空白與重複。
	rec = r.do("PUT", "/api/glossaries/global/default", `{"terms":[" KRaft ","kraft","Confluent"]}`, withBearer(tok[auth.ScopeAdmin]))
	if got := decodeBody(t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got["terms"], []any{"KRaft", "Confluent"}) || got["updated_at"] == nil {
		t.Fatalf("PUT 全域 = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("GET", "/api/glossaries/global/default", "", withBearer(tok[auth.ScopeRead]))
	if got := decodeBody(t, rec); !reflect.DeepEqual(got["terms"], []any{"KRaft", "Confluent"}) {
		t.Errorf("GET 全域 = %s", rec.Body)
	}

	// 頻道：natural key 找不到時，GET 回空、PUT 建立頻道；之後用 UUID 讀得到同一份。
	rec = r.do("GET", "/api/glossaries/channel/"+key, "", withBearer(tok[auth.ScopeRead]))
	if got := decodeBody(t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got["terms"], []any{}) {
		t.Fatalf("GET 尚未建立的頻道 = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("PUT", "/api/glossaries/channel/"+key, `{"terms":["微服務"]}`, withBearer(tok[auth.ScopeAdmin]))
	put := decodeBody(t, rec)
	id, _ := put["id"].(string)
	if rec.Code != http.StatusOK || id == "" || id == key || !reflect.DeepEqual(put["terms"], []any{"微服務"}) {
		t.Fatalf("PUT 頻道 = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("GET", "/api/glossaries/channel/"+id, "", withBearer(tok[auth.ScopeRead]))
	if got := decodeBody(t, rec); !reflect.DeepEqual(got["terms"], []any{"微服務"}) {
		t.Errorf("用 UUID 讀取 = %s", rec.Body)
	}
	// 全域與頻道互不影響。
	rec = r.do("GET", "/api/glossaries/global/default", "", withBearer(tok[auth.ScopeRead]))
	if got := decodeBody(t, rec); !reflect.DeepEqual(got["terms"], []any{"KRaft", "Confluent"}) {
		t.Errorf("全域被頻道的寫入影響：%s", rec.Body)
	}
	// UUID 找不到 404（訊息是頻道）；local 平台沒有頻道 422。
	if rec := r.do("GET", "/api/glossaries/channel/nope", "", withBearer(tok[auth.ScopeRead])); rec.Code != http.StatusNotFound {
		t.Errorf("UUID 找不到 = %d", rec.Code)
	}
	if rec := r.do("PUT", "/api/glossaries/channel/local:x", `{"terms":[]}`, withBearer(tok[auth.ScopeAdmin])); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("local 平台 = %d %s，預期 422", rec.Code, rec.Body)
	}
	// owner session 也可以。
	if rec := r.owner("GET", "/api/glossaries/global/default", ""); rec.Code != http.StatusOK {
		t.Errorf("owner = %d", rec.Code)
	}
}

func TestGlossariesAPI_Validation(t *testing.T) {
	r, tok := glossaryRig(t)
	for name, body := range map[string]string{
		"空字串":  `{"terms":["a",""]}`,
		"控制字元": `{"terms":["a\nb"]}`,
		"不是字串": `{"terms":[1]}`,
	} {
		rec := r.do("PUT", "/api/glossaries/global/default", body, withBearer(tok[auth.ScopeAdmin]))
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s，預期 4xx（422）", name, rec.Code, rec.Body)
		}
	}
	// 失敗的寫入不改變既有內容。
	if rec := r.do("GET", "/api/glossaries/global/default", "", withBearer(tok[auth.ScopeRead])); !reflect.DeepEqual(decodeBody(t, rec)["terms"], []any{}) {
		t.Errorf("失敗的 PUT 不該有副作用：%s", rec.Body)
	}
}

// COR-R6：術語候選唯讀；頻道還不存在時是空的。
func TestTermCandidatesAPI(t *testing.T) {
	r, tok := glossaryRig(t)
	rec := r.do("GET", "/api/channels/youtube:UCnone/term-candidates", "", withBearer(tok[auth.ScopeRead]))
	if got := decodeBody(t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got["candidates"], []any{}) {
		t.Fatalf("尚未建立的頻道 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("GET", "/api/channels/nope/term-candidates", "", withBearer(tok[auth.ScopeRead])); rec.Code != http.StatusNotFound {
		t.Errorf("UUID 找不到 = %d", rec.Code)
	}
	if rec := r.do("GET", "/api/channels/youtube:UCnone/term-candidates", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 = %d", rec.Code)
	}
	// 建立頻道並寫入候選，API 列出（pending）。
	ctx := context.Background()
	rec = r.do("PUT", "/api/glossaries/channel/youtube:UC1", `{"terms":[]}`, withBearer(tok[auth.ScopeAdmin]))
	id, _ := decodeBody(t, rec)["id"].(string)
	if err := r.st.Tx(ctx, func(w *store.Repo) error {
		_, err := w.AddTermCandidate(ctx, id, "", "KRaft")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rec = r.do("GET", "/api/channels/youtube:UC1/term-candidates", "", withBearer(tok[auth.ScopeRead]))
	got := decodeBody(t, rec)
	list, _ := got["candidates"].([]any)
	if rec.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("候選 = %d %s", rec.Code, rec.Body)
	}
	c := list[0].(map[string]any)
	if c["term"] != "KRaft" || c["state"] != "pending" || c["item_id"] != "" || c["id"] == "" {
		t.Errorf("候選 = %v", c)
	}
}

// COR-R6：核可把詞加進頻道詞彙表，拒絕不動詞彙表；結果不能被改成另一個，同一個結果重複送出是冪等的。
func TestTermCandidateDecideAPI(t *testing.T) {
	r, tok := glossaryRig(t)
	ctx := context.Background()
	rec := r.do("PUT", "/api/glossaries/channel/youtube:UC1", `{"terms":["Kafka"]}`, withBearer(tok[auth.ScopeAdmin]))
	chID, _ := decodeBody(t, rec)["id"].(string)
	for _, term := range []string{"KRaft", "Foo"} {
		if err := r.st.Tx(ctx, func(w *store.Repo) error {
			_, err := w.AddTermCandidate(ctx, chID, "", term)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	rec = r.do("GET", "/api/channels/youtube:UC1/term-candidates", "", withBearer(tok[auth.ScopeRead]))
	for _, c := range decodeBody(t, rec)["candidates"].([]any) {
		m := c.(map[string]any)
		ids[m["term"].(string)] = m["id"].(string)
	}
	patch := func(key, id, body string, scope string) *httptest.ResponseRecorder {
		var mod func(*http.Request)
		if scope != "" {
			mod = withBearer(tok[scope])
		}
		return r.do("PATCH", "/api/channels/"+key+"/term-candidates/"+id, body, mod)
	}
	terms := func() []any {
		return decodeBody(t, r.do("GET", "/api/glossaries/channel/youtube:UC1", "", withBearer(tok[auth.ScopeRead])))["terms"].([]any)
	}

	if rec := patch("youtube:UC1", ids["KRaft"], `{"state":"accepted"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 = %d", rec.Code)
	}
	if rec := patch("youtube:UC1", ids["KRaft"], `{"state":"accepted"}`, auth.ScopeRead); rec.Code != http.StatusForbidden {
		t.Errorf("read 不能核可 = %d", rec.Code)
	}
	rec = patch("youtube:UC1", ids["KRaft"], `{"state":"accepted"}`, auth.ScopeAdmin)
	if got := decodeBody(t, rec); rec.Code != http.StatusOK || got["state"] != "accepted" || got["term"] != "KRaft" {
		t.Fatalf("核可 = %d %s", rec.Code, rec.Body)
	}
	if got := terms(); !reflect.DeepEqual(got, []any{"Kafka", "KRaft"}) {
		t.Errorf("核可之後的詞彙表 = %v", got)
	}
	if rec := patch("youtube:UC1", ids["KRaft"], `{"state":"accepted"}`, auth.ScopeAdmin); rec.Code != http.StatusOK || len(terms()) != 2 {
		t.Errorf("同一個結果重送應該冪等 = %d %v", rec.Code, terms())
	}
	rec = patch("youtube:UC1", ids["KRaft"], `{"state":"rejected"}`, auth.ScopeAdmin)
	if got := decodeBody(t, rec); rec.Code != http.StatusConflict || got["code"] != CodeCandidateDecided {
		t.Errorf("已核可的不能改成拒絕 = %d %s", rec.Code, rec.Body)
	}
	if got := terms(); len(got) != 2 {
		t.Errorf("409 不該動詞彙表：%v", got)
	}
	rec = patch(chID, ids["Foo"], `{"state":"rejected"}`, auth.ScopeAdmin)
	if got := decodeBody(t, rec); rec.Code != http.StatusOK || got["state"] != "rejected" {
		t.Errorf("拒絕（用頻道 UUID）= %d %s", rec.Code, rec.Body)
	}
	if got := terms(); len(got) != 2 {
		t.Errorf("拒絕不該動詞彙表：%v", got)
	}
	if rec := patch("youtube:UC1", "nope", `{"state":"accepted"}`, auth.ScopeAdmin); rec.Code != http.StatusNotFound {
		t.Errorf("找不到候選 = %d", rec.Code)
	}
	if rec := patch("youtube:UCnone", ids["Foo"], `{"state":"accepted"}`, auth.ScopeAdmin); rec.Code != http.StatusNotFound {
		t.Errorf("頻道不存在 = %d", rec.Code)
	}
	if rec := patch("youtube:UC1", ids["Foo"], `{"state":"pending"}`, auth.ScopeAdmin); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Errorf("state 只能是 accepted 或 rejected = %d %s", rec.Code, rec.Body)
	}
}
