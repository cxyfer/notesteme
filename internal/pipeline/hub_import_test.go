package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/store"
)

// 從 Hub 重新生成（HSHR-R9～R11）：Hub 是不可信的輸入，版本資訊與內容都要驗證，通過之後才新增影音。

// importOK 匯入並回傳結果。
func (r *hubRig) importOK(version string) HubImportResult {
	r.t.Helper()
	res, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: version})
	if err != nil {
		r.t.Fatalf("ImportFromHub(%s)：%v", version, err)
	}
	return res
}

// noItems 確認沒有任何影音被建立（驗證失敗時不留下半成品）。
func (r *hubRig) noItems() {
	r.t.Helper()
	if items, _ := r.p.Items(context.Background()); len(items) != 0 {
		r.t.Errorf("驗證失敗不該建立影音：%d 個", len(items))
	}
}

func (r *hubRig) pendingImport(itemID string) (store.HubImport, bool) {
	r.t.Helper()
	imp, err := r.st.Read().HubImport(context.Background(), itemID)
	if errors.Is(err, store.ErrNotFound) {
		return store.HubImport{}, false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return imp, true
}

// 新的影音：網址由 Client 依驗證過的影片 ID 組出；transcript 寫入 Hub 的字幕，不呼叫其他來源（R9、R11）。
func TestImportFromHubCreatesItemAndWritesHubTranscript(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Verified, v.TranscribeModel = true, "whisper-large"
	stub.add(v)
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newHubRigOpts(t, stub, hubRigOpts{token: hubTestToken, sd: sd})

	res := r.importOK("v1")
	if res.Ref != "yt:abc123xxxxx" || res.ItemID == "" {
		t.Fatalf("result = %+v", res)
	}
	it := r.item(res.ItemID)
	if it.URL != watch || it.Platform != "youtube" {
		t.Errorf("item = %+v", it)
	}
	imp, ok := r.pendingImport(it.ID)
	if !ok || imp.VersionID != "v1" || imp.SHA256 != hubclient.SHA256Hex(v.Content) || !bytes.Equal(imp.Content, v.Content) {
		t.Fatalf("等待中的字幕 = %+v, %v", imp, ok)
	}
	var origin hubOrigin
	if err := json.Unmarshal([]byte(imp.OriginJSON), &origin); err != nil || origin.URL != stub.srv.URL || origin.Uploader != "Alice" || !origin.ModelVerified {
		t.Errorf("origin = %+v, %v", origin, err)
	}

	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusDone || st.Executor != SourceHub {
		t.Fatalf("transcript = %+v", st)
	}
	if sd.calls() != 0 {
		t.Error("Hub 的字幕不呼叫其他字幕來源")
	}
	if n := stub.count("/api/v1/lookup"); n != 0 {
		t.Errorf("重新生成不做 ASR 前查詢：%d", n)
	}
	if got := sourcesOf(r.rig, it); got != "hub:used:" {
		t.Errorf("sources = %q", got)
	}
	prov := provenanceOf(r.rig, it)
	if prov["executor"] != "hub" || prov["source"] != "hub" || prov["cookie_required"] != false || prov["model_verified"] != true || prov["transcribe_model"] != "whisper-large" {
		t.Errorf("provenance = %v", prov)
	}
	if o, _ := prov["hub_origin"].(map[string]any); o["version_id"] != "v1" || o["sha256"] != hubclient.SHA256Hex(v.Content) || o["uploader"] != "Alice" {
		t.Errorf("hub_origin = %v", prov["hub_origin"])
	}
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Cues[0].Text != "hello from hub" || tr.Lang != "en" {
		t.Fatalf("cues = %+v, %v", tr, err)
	}
	// 寫入 commit 之後才刪掉等待中的列。
	if _, ok := r.pendingImport(it.ID); ok {
		t.Error("寫入之後應該刪掉等待中的字幕")
	}
	if _, err := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", "en"); err != nil {
		t.Error(err)
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", "en"); n != 1 {
		t.Errorf("hub 字幕應該繼續走 normalize：%d", n)
	}
}

// 既有的影音從 transcript 重跑，新的 source 版本取代現行版本，舊版本保留（R11）。
func TestImportFromHubRerunsExistingItem(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Content = hubCues("en", "from the hub", "two", "three")
	stub.add(v)
	r := newHubRig(t, stub)
	it := r.processedItem()
	if r.sourceVersions(it) != 1 {
		t.Fatal("前提：已有一個 source 版本")
	}

	res := r.importOK("v1")
	if res.ItemID != it.ID || res.Ref != "yt:abc123xxxxx" {
		t.Fatalf("result = %+v", res)
	}
	r.run()
	if n := r.sourceVersions(it); n != 2 {
		t.Errorf("source 版本數 = %d，預期 2（舊版本保留）", n)
	}
	cur := r.artifact(r.item(it.ID), "source")
	if executorOf(cur.Provenance) != SourceHub {
		t.Errorf("現行版本的 executor = %q", executorOf(cur.Provenance))
	}
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 3 || tr.Cues[0].Text != "from the hub" {
		t.Errorf("cues = %+v, %v", tr, err)
	}
	if _, ok := r.pendingImport(it.ID); ok {
		t.Error("應該刪掉等待中的字幕")
	}
	if items, _ := r.p.Items(ctx); len(items) != 1 {
		t.Errorf("不該新增影音：%d", len(items))
	}
}

// 重跑失敗（處理方案不存在）時不留下等待中的字幕，否則下次 transcript 會被悄悄套用。
func TestImportFromHubRerunFailureRemovesPendingImport(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r := newHubRig(t, stub)
	it := r.processedItem()
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1", Profile: "no-such-profile"})
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := r.pendingImport(it.ID); ok {
		t.Error("重跑失敗時應該刪掉等待中的字幕")
	}
}

// 新的影音但新增失敗（處理方案不存在）：沒有影音，沒有等待中的字幕。
func TestImportFromHubNewItemInvalidProfile(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r := newHubRig(t, stub)
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1", Profile: "no-such-profile"})
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v", err)
	}
	r.noItems()
}

func TestImportFromHubItemInTrash(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r := newHubRig(t, stub)
	it := r.processedItem()
	r.trashDirect(it.ID, r.clk.Now())
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	var tr *InTrashError
	if !errors.As(err, &tr) || tr.ItemID != it.ID {
		t.Fatalf("err = %v", err)
	}
	if _, ok := r.pendingImport(it.ID); ok {
		t.Error("垃圾桶中的影音不該有等待中的字幕")
	}
}

func TestImportFromHubNotConfigured(t *testing.T) {
	for name, r := range map[string]*hubRig{
		"沒有 token": newHubRigOpts(t, newHubStub(t), hubRigOpts{}),
		"沒有網址":     newHubRigOpts(t, newHubStub(t), hubRigOpts{token: hubTestToken, noURL: true}),
	} {
		_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
		if !errors.Is(err, hubclient.ErrNotConfigured) {
			t.Errorf("%s：%v", name, err)
		}
		if r.stub.total() != 0 {
			t.Errorf("%s：不該連線", name)
		}
	}
	// 讀取 token 失敗。
	boom := errors.New("token store down")
	stub := newHubStub(t)
	r := newRig(t, func(d *Deps) {
		d.Hub = hubclient.New(stub.srv.URL, func(context.Context) (string, bool, error) { return "", false, boom }, stub.srv.Client(), nil)
	})
	if _, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"}); !errors.Is(err, boom) {
		t.Errorf("token 錯誤：%v", err)
	}
}

// 只能用原始字幕（source、normalized）重新生成（R9 的 not_raw）；內容不會被下載。
func TestImportFromHubOnlyRawTranscripts(t *testing.T) {
	for _, tt := range []struct{ kind, variant string }{
		{"transcript", "translated"}, {"transcript", "corrected"}, {"transcript", "edited"}, {"transcript", "segmented"},
		{"transcript", ""}, {"transcript", "Source"}, {"chapters", "source"}, {"note", "normalized"}, {"frames", ""},
	} {
		stub := newHubStub(t)
		v := ytVer("v1")
		v.Kind, v.Variant = tt.kind, tt.variant
		stub.add(v)
		r := newHubRig(t, stub)
		_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
		e := hubErrIs(t, err, 422, "not_raw")
		if e.Msg != msgNotRaw {
			t.Errorf("%s/%s：訊息 = %q", tt.kind, tt.variant, e.Msg)
		}
		if stub.count("/api/v1/versions/v1/content") != 0 {
			t.Errorf("%s/%s：不該下載內容", tt.kind, tt.variant)
		}
		r.noItems()
	}
	// normalized 可以。
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Variant = "normalized"
	stub.add(v)
	r := newHubRig(t, stub)
	r.importOK("v1")
}

// 內容的 sha256 與版本資訊不符：拒絕（R10），不建立影音。
func TestImportFromHubRejectsShaMismatch(t *testing.T) {
	stub := newHubStub(t)
	v := ytVer("v1")
	v.VersionSHA = hubclient.SHA256Hex([]byte("something else"))
	stub.add(v)
	r := newHubRig(t, stub)
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	he := hubErrOf(t, err)
	if he.Code() != "hub_integrity" || he.HTTPStatus() != 502 || !strings.Contains(he.Error(), "sha256 不符") {
		t.Errorf("err = %+v", he)
	}
	r.noItems()

	// 版本資訊的 sha256 沒問題，但是內容在傳輸中被換掉（只差一個位元組）。
	stub2 := newHubStub(t)
	v2 := ytVer("v1")
	stub2.add(v2)
	tampered := slicesClone(v2.Content)
	tampered[len(tampered)-3] ^= 1
	stub2.contentRaw["v1"] = tampered
	r2 := newHubRig(t, stub2)
	_, err = r2.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	if he := hubErrOf(t, err); he.Code() != "hub_integrity" {
		t.Errorf("內容被換掉：%+v", he)
	}
	r2.noItems()
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

// sha256 吻合但內容不是合法的字幕：拒絕（R10）。
func TestImportFromHubRejectsInvalidCues(t *testing.T) {
	long := strings.Repeat("字", 2001)
	cases := map[string][]byte{
		"不是 JSON":     []byte(`<html>not json</html>`),
		"空的":          []byte(``),
		"不是 UTF-8":    {0xff, 0xfe, 0xfd},
		"沒有句子":        []byte(`{"lang":"en","variant":"source","cues":[]}`),
		"全是空白句":       []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"  \n"}]}`),
		"結束早於開始":      []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":5,"end_ms":1,"text":"x"}]}`),
		"負的開始時間":      []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":-5,"end_ms":1,"text":"x"}]}`),
		"開始時間倒退":      []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":9,"end_ms":10,"text":"a"},{"index":2,"start_ms":1,"end_ms":2,"text":"b"}]}`),
		"一句超過 2000 字": []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"` + long + `"}]}`),
		"不認得的變體":      []byte(`{"lang":"en","variant":"evil","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"a"}]}`),
		"不認得的 timing": []byte(`{"lang":"en","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"a","timing":"???"}]}`),
		"不合法的語言標記":    []byte(`{"lang":"not a lang!","variant":"source","cues":[{"index":1,"start_ms":0,"end_ms":1,"text":"a"}]}`),
		"cues 的型別錯誤":  []byte(`{"lang":"en","variant":"source","cues":"AAAA"}`),
	}
	for name, content := range cases {
		stub := newHubStub(t)
		v := ytVer("v1")
		v.Content = content
		stub.add(v)
		r := newHubRig(t, stub)
		_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
		he := hubErrOf(t, err)
		if he.Code() != "hub_content_invalid" || he.HTTPStatus() != 422 || !strings.HasPrefix(he.Error(), "Hub 回傳的字幕格式不正確：") {
			t.Errorf("%s：err = %+v %q", name, he, he.Error())
		}
		r.noItems()
	}
}

// 版本資訊本身不可信：欄位不合法、或指向別的影片時拒絕，Hub 給的字串不會變成要抓取的網址。
func TestImportFromHubRejectsMalformedVersionInfo(t *testing.T) {
	good := hubclient.SHA256Hex([]byte("x"))
	mk := func(video, rest string) string {
		return `{"id":"v1","video":` + video + `,` + rest + `}`
	}
	yt := `{"platform":"youtube","external_id":"abc123xxxxx","part":null}`
	okRest := `"kind":"transcript","variant":"source","lang":"en","sha256":"` + good + `"`
	cases := map[string]string{
		"不是物件":               `[]`,
		"id 不同":              strings.Replace(mk(yt, okRest), `"id":"v1"`, `"id":"v2"`, 1),
		"未知平台":               mk(`{"platform":"vimeo","external_id":"abc123xxxxx"}`, okRest),
		"網址當作影片 ID":          mk(`{"platform":"youtube","external_id":"http://evil.example/x"}`, okRest),
		"路徑穿越當作影片 ID":        mk(`{"platform":"youtube","external_id":"../../etc/pw"}`, okRest),
		"影片 ID 太短":           mk(`{"platform":"youtube","external_id":"abc"}`, okRest),
		"影片 ID 太長":           mk(`{"platform":"youtube","external_id":"abc123xxxxxx"}`, okRest),
		"Bilibili 的 ID 格式不對": mk(`{"platform":"bilibili","external_id":"av12345","part":1}`, okRest),
		"YouTube 不能有分P":      mk(`{"platform":"youtube","external_id":"abc123xxxxx","part":1}`, okRest),
		"分P 為 0":             mk(`{"platform":"bilibili","external_id":"BV1xx411c7mD","part":0}`, okRest),
		"分P 太大":              mk(`{"platform":"bilibili","external_id":"BV1xx411c7mD","part":10001}`, okRest),
		"Podcast 的 ID 含控制字元": mk(`{"platform":"podcast","external_id":"feed/\u0001guid"}`, okRest),
		"sha256 太短":          mk(yt, `"kind":"transcript","variant":"source","lang":"en","sha256":"abc"`),
		"sha256 含大寫":         mk(yt, `"kind":"transcript","variant":"source","lang":"en","sha256":"`+strings.ToUpper(good)+`"`),
		"沒有 kind":            mk(yt, `"variant":"source","lang":"en","sha256":"`+good+`"`),
		"kind 太長":            mk(yt, `"kind":"`+strings.Repeat("k", 40)+`","variant":"source","lang":"en","sha256":"`+good+`"`),
		"語言標記不合法":            mk(yt, `"kind":"transcript","variant":"source","lang":"en;rm -rf","sha256":"`+good+`"`),
		"part 的型別錯誤":         mk(`{"platform":"bilibili","external_id":"BV1xx411c7mD","part":"1"}`, okRest),
	}
	for name, raw := range cases {
		stub := newHubStub(t)
		stub.versionRaw["v1"] = raw
		r := newHubRig(t, stub)
		_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
		he := hubErrOf(t, err)
		if he.Code() != "hub_content_invalid" || he.HTTPStatus() != 422 {
			t.Errorf("%s：err = %+v", name, he)
		}
		if stub.count("/api/v1/versions/v1/content") != 0 {
			t.Errorf("%s：版本資訊不合法就不該下載內容", name)
		}
		r.noItems()
	}
}

// 版本 ID 在送出之前檢查，不讓它變成別的路徑。
func TestImportFromHubRejectsBadVersionIDWithoutRequest(t *testing.T) {
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	for _, id := range []string{"", "../etc/passwd", "a/b", "a b", "a?x=1", "-leading", strings.Repeat("a", 65), "v1\n", "%2e%2e"} {
		_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: id})
		if he := hubErrOf(t, err); he.Code() != "hub_version_not_found" || he.HTTPStatus() != 404 {
			t.Errorf("%q：err = %+v", id, he)
		}
	}
	if stub.total() != 0 {
		t.Errorf("不合法的版本 ID 不該連線：%v", stub.requests())
	}
}

// Hub 回應的狀態碼對應（ADR-0032 第 5 點），訊息不含 token 與 Hub 的原文。
func TestImportFromHubMapsStatusCodes(t *testing.T) {
	tests := []struct {
		status int
		body   string
		code   string
		http   int
		msg    string
	}{
		{401, `{"message":"` + hubTestToken + `"}`, "hub_unauthorized", 502, "token 無效或已過期"},
		{403, `{"required_permission":"regenerate"}`, "hub_forbidden", 502, "「regenerate」權限"},
		{404, `{}`, "hub_version_not_found", 404, "找不到這個版本"},
		{410, `{}`, "hub_version_not_found", 404, "找不到這個版本"},
		{422, `{"code":"bad_thing","message":"不合法","errors":[{"path":"id","message":"壞掉"}]}`, "bad_thing", 422, "不合法"},
		{429, `{"retry_after_s":7}`, "hub_rate_limited", 502, "7 秒"},
		{500, `{"message":"panic at /srv/hub"}`, "hub_unavailable", 502, "HTTP 500"},
		{502, ``, "hub_unavailable", 502, "HTTP 502"},
		{503, ``, "hub_unavailable", 502, "HTTP 503"},
		{504, ``, "hub_unavailable", 502, "HTTP 504"},
		{302, ``, "hub_unavailable", 502, "HTTP 302"}, // 轉址不跟隨（token 不能被帶到別的主機）
	}
	for _, tt := range tests {
		// 在版本資訊與內容兩個步驟都測。
		for _, step := range []string{"version", "content"} {
			stub := newHubStub(t)
			stub.add(ytVer("v1"))
			stub.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if step == "content" && !strings.HasSuffix(r.URL.Path, "/content") {
					return false
				}
				if tt.status == 302 {
					w.Header().Set("Location", "http://127.0.0.1:1/steal")
				}
				hubJSON(w, tt.status, tt.body)
				return true
			}
			r := newHubRig(t, stub)
			_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
			he := hubErrOf(t, err)
			if he.Code() != tt.code || he.HTTPStatus() != tt.http || !strings.Contains(he.Error(), tt.msg) {
				t.Errorf("%d@%s：code=%s http=%d msg=%q", tt.status, step, he.Code(), he.HTTPStatus(), he.Error())
			}
			if strings.Contains(he.Error(), hubTestToken) || strings.Contains(he.Error(), "/srv/hub") {
				t.Errorf("%d@%s：訊息含 token 或 Hub 的原文：%q", tt.status, step, he.Error())
			}
			r.noItems()
		}
	}
}

// 422 的訊息會原樣轉出，但先清理：控制字元、方向控制字元與長度（Hub 的文字是不可信輸入）。
func TestImportFromHubSanitizesHubValidationMessages(t *testing.T) {
	stub := newHubStub(t)
	stub.status = 422
	stub.errBody = `{"code":"x","message":"` + strings.Repeat("長", 500) + `\u001b[31m‮","errors":[` + strings.Repeat(`{"path":"p\u0007","message":"m"},`, 30) + `{"path":"last","message":"m"}]}`
	r := newHubRig(t, stub)
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	he := hubErrOf(t, err)
	if len([]rune(he.HubMessage)) > 301 || strings.ContainsAny(he.HubMessage, "\x1b‮") {
		t.Errorf("message = %q", he.HubMessage)
	}
	if len(he.Errors) != 20 || strings.ContainsRune(he.Errors[0].Path, '\u0007') {
		t.Errorf("errors = %d 個，第一個 %+v", len(he.Errors), he.Errors[0])
	}
}

// 超大的回應：有上限，超過就拒絕（不把它讀進記憶體）。
func TestImportFromHubRejectsOversizedResponses(t *testing.T) {
	// 版本資訊超過 4 MiB。
	stub := newHubStub(t)
	stub.hook = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"id":"v1","pad":"`)
		_, _ = io.CopyN(w, zeroReader{}, 5<<20)
		return true
	}
	r := newHubRig(t, stub)
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	if he := hubErrOf(t, err); he.Code() != "hub_content_invalid" || !strings.Contains(he.Detail, "4 MiB") {
		t.Errorf("版本資訊：%+v", he)
	}
	r.noItems()

	// 內容超過 64 MiB。
	stub2 := newHubStub(t)
	v := ytVer("v1")
	stub2.add(v)
	stub2.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/content") {
			return false
		}
		w.WriteHeader(200)
		_, _ = io.CopyN(w, zeroReader{}, hubclient.MaxContentBytes+1024)
		return true
	}
	r2 := newHubRig(t, stub2)
	_, err = r2.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	if he := hubErrOf(t, err); he.Code() != "hub_content_invalid" || !strings.Contains(he.Detail, "64 MiB") {
		t.Errorf("內容：%+v", he)
	}
	r2.noItems()
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// 逾時以注入的 clock 計算：版本資訊與內容各有 30 秒。
func TestImportFromHubTimeouts(t *testing.T) {
	for _, step := range []string{"version", "content"} {
		stub := newHubStub(t)
		stub.add(ytVer("v1"))
		arrived := make(chan struct{})
		stub.hook = func(w http.ResponseWriter, r *http.Request) bool {
			if step == "content" && !strings.HasSuffix(r.URL.Path, "/content") {
				return false
			}
			close(arrived)
			<-r.Context().Done()
			return true
		}
		r := newHubRig(t, stub)
		err := r.runWithTimeout(arrived, hubclient.DefaultTimeout, func() error {
			_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
			return err
		})
		he := hubErrOf(t, err)
		if !he.Timeout || he.Code() != "hub_unavailable" || !strings.Contains(he.Error(), "連線逾時") {
			t.Errorf("%s：%+v", step, he)
		}
		r.noItems()
	}
}

func TestImportFromHubConnectionFailure(t *testing.T) {
	stub := newHubStub(t)
	stub.add(ytVer("v1"))
	r := newHubRig(t, stub)
	stub.srv.Close()
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	he := hubErrOf(t, err)
	if he.Code() != "hub_unavailable" || !strings.Contains(he.Error(), "連線失敗") || strings.Contains(he.Error(), hubTestToken) {
		t.Errorf("err = %+v %q", he, he.Error())
	}
	r.noItems()
}

// Podcast 單集不在媒體庫中：不能從 Hub 的資訊建立（沒有辦法組出網址）。
func TestImportFromHubPodcastNotInLibrary(t *testing.T) {
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Platform, v.ExternalID = "podcast", "feedkey/episode-guid-1"
	stub.add(v)
	r := newHubRig(t, stub)
	_, err := r.p.ImportFromHub(context.Background(), HubImportRequest{Version: "v1"})
	hubErrIs(t, err, 422, "not_in_library")
	r.noItems()
}

func TestImportFromHubBilibiliNewItem(t *testing.T) {
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Platform, v.ExternalID, v.Part = "bilibili", "BV1xx411c7mD", 2
	v.Lang = "zh"
	v.Content = hubCues("zh", "第一句", "第二句")
	stub.add(v)
	fb := &fakeBili{info: bilibili.Info{ID: "BV1xx411c7mD", Title: "Part", Uploader: "UP主", UploaderID: "12345"}}
	fb.parts = threeParts()
	r := newHubRig(t, stub, func(d *Deps) { d.Bilibili = fb })

	res := r.importOK("v1")
	it := r.item(res.ItemID)
	if it.Platform != "bilibili" || it.ExternalID != "BV1xx411c7mD" || it.Part != 2 || res.Ref != source.Ref("bilibili", "BV1xx411c7mD", 2) {
		t.Fatalf("item = %+v, ref = %q", it, res.Ref)
	}
	if want := "https://www.bilibili.com/video/BV1xx411c7mD?p=2"; it.URL != want {
		t.Errorf("url = %q", it.URL)
	}
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || st.Executor != SourceHub {
		t.Errorf("transcript = %+v", st)
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Lang != "zh" {
		t.Errorf("cues = %+v, %v", tr, err)
	}
}

// Hub 的文字進到來源紀錄之前先清理（上傳者、模型名稱）。
func TestImportFromHubSanitizesProvenanceText(t *testing.T) {
	stub := newHubStub(t)
	v := ytVer("v1")
	v.Uploader = "Eve‮\u0007" + strings.Repeat("e", 300)
	v.TranscribeModel = "m\x1b[31m" + strings.Repeat("m", 300)
	stub.add(v)
	r := newHubRig(t, stub)
	res := r.importOK("v1")
	r.run()
	prov := provenanceOf(r.rig, r.item(res.ItemID))
	o := prov["hub_origin"].(map[string]any)
	up, _ := o["uploader"].(string)
	if len([]rune(up)) > 101 || strings.ContainsAny(up, "‮\u0007") || !strings.HasPrefix(up, "Eve") {
		t.Errorf("uploader = %q", up)
	}
	tm, _ := prov["transcribe_model"].(string)
	if len([]rune(tm)) > 201 || strings.ContainsRune(tm, '\x1b') {
		t.Errorf("transcribe_model = %q", tm)
	}
}

// 等待中的字幕損壞時 transcript 永久失敗（不呼叫其他來源）。
func TestRunHubImportFailsPermanentlyOnCorruptPendingRows(t *testing.T) {
	ctx := context.Background()
	good := ytVer("v1").Content
	cases := map[string]store.HubImport{
		"來源紀錄損壞": {VersionID: "v1", SHA256: hubclient.SHA256Hex(good), OriginJSON: `{not json`, Content: good},
		"字幕內容損壞": {VersionID: "v1", SHA256: hubclient.SHA256Hex([]byte("x")), OriginJSON: `{"url":"u","version_id":"v1"}`, Content: []byte("x")},
	}
	for name, imp := range cases {
		sd := &fakeSupadata{lang: "en", cues: 2}
		r := newHubRigOpts(t, newHubStub(t), hubRigOpts{token: hubTestToken, sd: sd})
		it := r.ingest(watch, nil, StageTranscript)
		imp.ItemID = it.ID
		if err := r.st.Write().UpsertHubImport(ctx, imp); err != nil {
			t.Fatal(err)
		}
		r.run()
		st := r.stage(it, StageTranscript)
		if st.Status != store.StatusFailed || st.ErrorClass != "permanent" {
			t.Errorf("%s：transcript = %+v", name, st)
		}
		if sd.calls() != 0 {
			t.Errorf("%s：不該呼叫其他來源", name)
		}
	}
}

func TestOriginOfCopiesVersionFields(t *testing.T) {
	v := hubclient.Version{ID: "v9", SHA256: "s", Uploader: "u", Lang: "ja", TranscribeModel: "t", CorrectModel: "c", TranslateModel: "x", ModelVerified: true}
	o := originOf("https://hub", v)
	want := hubOrigin{URL: "https://hub", VersionID: "v9", SHA256: "s", Uploader: "u", Lang: "ja", TranscribeModel: "t", CorrectModel: "c", TranslateModel: "x", ModelVerified: true}
	if o != want {
		t.Errorf("origin = %+v", o)
	}
}
