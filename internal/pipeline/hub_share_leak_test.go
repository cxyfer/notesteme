package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/hubshare"
	"github.com/cxyfer/notesteme/internal/store"
)

// 安全修補 B 的第 3 項：分享到 Hub 的筆記不帶本機影音 ID、處理與閱讀狀態、模板的 read button；
// 送出前解開 bundle 掃描 secret（環境變數與資料庫）、本機影音 ID 與 read button。

const testReadButton = "[開始閱讀](obsidian://open?vault=Private&file=Inbox%2Fx)"

func TestSharedNoteDropsLocalFrontmatterAndReadButton(t *testing.T) {
	ctx := context.Background()
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, 600)
	seedNoteInputs(t, r, it)
	if _, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "with-button", From: "video-notes", ReadButton: ptrTo(testReadButton)}); err != nil {
		t.Fatal(err)
	}
	if st := rerunNote(t, r, it, noteOver(map[string]any{"template": "with-button"})); st.Status != store.StatusDone {
		t.Fatalf("note = %+v", st)
	}
	notes, err := r.st.Read().CurrentArtifacts(ctx, it.ID, "note")
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %v, %v", notes, err)
	}
	raw, _ := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(notes[0].Path)))
	var doc noteDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{it.ID, testReadButton, "reading_status"} {
		if !strings.Contains(doc.Markdown, s) {
			t.Fatalf("前提：本機的筆記應含 %q", s)
		}
	}
	ha, rb, err := r.p.shareableArtifact(ctx, notes[0])
	if err != nil {
		t.Fatal(err)
	}
	md := string(ha.Data)
	if rb != testReadButton {
		t.Errorf("read button = %q", rb)
	}
	for _, bad := range []string{it.ID, "notesteme_id", "notesteme_status", "reading_status", "read_at", "\nstatus:", testReadButton, "obsidian://"} {
		if strings.Contains(md, bad) {
			t.Errorf("分享的筆記含 %q：\n%s", bad, md)
		}
	}
	for _, keep := range []string{"---\ntitle:", "source:", "Writing a Toy Kernel"} {
		if !strings.Contains(md, keep) {
			t.Errorf("分享的筆記少了 %q：\n%s", keep, md)
		}
	}

	build := func(data []byte) []byte {
		a := ha
		a.Data = data
		b, err := hubshare.Build(hubshare.Item{Platform: "youtube", ExternalID: "abc123xxxxx", Title: "T", Availability: "public"},
			[]hubshare.Artifact{a}, hubshare.Options{License: "CC-BY-4.0", AppVersion: "test", Now: r.clk.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := r.p.checkShareLeak(ctx, it, build(ha.Data), []string{rb}); err != nil {
		t.Fatalf("清理過的筆記 = %v", err)
	}
	// 沒有清理的筆記（例如之後新增了別的帶 ID 的欄位）會被掃描擋下。
	for name, data := range map[string]string{
		"本機影音 ID":     md + "\nid: " + it.ID + "\n",
		"read button": md + "\n" + testReadButton + "\n",
	} {
		if err := r.p.checkShareLeak(ctx, it, build([]byte(data)), []string{rb}); !errors.Is(err, ErrBundleLeak) {
			t.Errorf("%s：err = %v", name, err)
		}
	}
	// 資料庫中的 secret（例如 Hub token、cookie）。
	r.p.secretValues = func(context.Context) ([]string, error) { return []string{"db-secret-0123456789", "short"}, nil }
	if err := r.p.checkShareLeak(ctx, it, build([]byte(md+"\ndb-secret-0123456789\n")), nil); !errors.Is(err, ErrBundleLeak) {
		t.Errorf("資料庫的 secret：err = %v", err)
	}
	if err := r.p.checkShareLeak(ctx, it, build([]byte(md+"\nshort\n")), nil); err != nil {
		t.Errorf("少於 8 個字元的 secret 不當成 needle：%v", err)
	}
	r.p.secretValues = func(context.Context) ([]string, error) { return nil, errors.New("keyring") }
	if err := r.p.checkShareLeak(ctx, it, build([]byte(md)), nil); err == nil || errors.Is(err, ErrBundleLeak) {
		t.Errorf("讀不到 secret 時要失敗：%v", err)
	}
}

// ShareToHub 送出前一定掃描：bundle 含有資料庫中的 secret 時不連線到 Hub。
func TestShareToHubScansBeforeSending(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	src, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(r.artifact(it, "source").Path)))
	if err != nil {
		t.Fatal(err)
	}
	// 假裝字幕的一段內容剛好是資料庫中的 secret。
	i := strings.Index(string(src), `"text":"`)
	if i < 0 {
		t.Fatalf("字幕沒有 text：%s", src)
	}
	secret := string(src[i+8 : i+8+8])
	r.p.secretValues = func(context.Context) ([]string, error) { return []string{secret}, nil }
	if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); !errors.Is(err, ErrBundleLeak) {
		t.Fatalf("err = %v", err)
	}
	if n := stub.total(); n != 0 {
		t.Errorf("洩漏時不能連線到 Hub：%d 個請求", n)
	}
	r.p.secretValues = nil
	if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); err != nil {
		t.Fatal(err)
	}
}
func ptrTo[T any](v T) *T { return &v }

// 安全修補 B 的第 4(b) 項：bundle 匯入的來源紀錄自稱 executor: hub 不可信，匯入時改成 import；
// 否則分享時會略過「需要登入的字幕」檢查（HSHR-R4），還會帶上偽造的 hub_origin。
func TestImportedHubExecutorIsNotTrusted(t *testing.T) {
	forged := `{"executor":"hub","source":"bilibili-ai","cookie_required":true,"hub_origin":{"version_id":"forged","sha256":"` + strings.Repeat("a", 64) + `"}}`
	out, err := importProvenance(json.RawMessage(forged), bundle.CreatedBy{InstanceID: "i", AppVersion: "v"}, "s", bundleTestNow)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["executor"] != ExecutorImport || m["imported_executor"] != SourceHub || m["hub_origin"] != nil || m["imported_hub_origin"] == nil {
		t.Fatalf("provenance = %s", out)
	}

	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	r.setProvenance(r.artifact(it, "source"), string(out))
	_, err = r.p.ShareToHub(ctx, it.ID, shareReq)
	var he *HubError
	if !errors.As(err, &he) || he.Code != "not_shareable" || !strings.Contains(he.Msg, "需要登入") {
		t.Fatalf("err = %v", err)
	}
	if stub.total() != 0 {
		t.Error("不能連線到 Hub")
	}
}
