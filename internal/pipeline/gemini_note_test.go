package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/store"
)

// GEM-R5、R6：每一節與內文的每個 [[t:…]] 各有一個虛擬句號，時間由 cueStart 對應。
func TestGeminiSections(t *testing.T) {
	o := llm.GeminiNoteOutput{
		Sections: []llm.GeminiNoteSection{
			{Level: 2, Title: "A", StartMS: 0, Body: "see [[t:01:05]] and [[t:02:00]]"},
			{Level: 3, Title: "B", StartMS: 90_000, Body: "plain"},
		},
		SelfCheck: []string{"q"},
	}
	out, cs := geminiSections(o)
	if len(out.Sections) != 2 || out.Sections[0].Cue != 1 || out.Sections[1].Cue != 2 || out.Sections[1].Level != 3 {
		t.Fatalf("sections = %+v", out.Sections)
	}
	if out.Sections[0].Body != "see [[cue:4]] and [[cue:3]]" || out.Sections[1].Body != "plain" {
		t.Errorf("body = %q / %q", out.Sections[0].Body, out.Sections[1].Body)
	}
	want := map[int]int64{1: 0, 2: 90_000, 3: 120_000, 4: 65_000}
	if len(cs) != len(want) {
		t.Fatalf("cueStart = %v", cs)
	}
	for k, v := range want {
		if cs[k] != v {
			t.Errorf("cueStart[%d] = %d，預期 %d", k, cs[k], v)
		}
	}
	if out.SelfCheck[0] != "q" || out.Sections[0].Frames == nil {
		t.Errorf("out = %+v", out)
	}
}

func readNoteDoc(t *testing.T, r *rig, art *store.NewArtifact) noteDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(art.Path)))
	if err != nil {
		t.Fatal(err)
	}
	var d noteDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// GEM-R6：Gemini 的筆記套用同一份模板、跳脫與自動檢查，frontmatter 與來源紀錄標出 engine 與 timing；
// 有片段時 parents 是 sponsor_segments。模板讓檢查不通過時同樣 blocked。
func TestWriteGeminiNote(t *testing.T) {
	r, it := newNoteRig(t, &noteLLM{note: []string{goodNote}}, 600)
	ctx := context.Background()
	it, _ = r.st.Read().ItemByID(ctx, it.ID)
	n := geminiNote{
		Output: llm.GeminiNoteOutput{Sections: []llm.GeminiNoteSection{
			{Level: 2, Title: "Paging", StartMS: 0, Body: "<script>x</script> at [[t:01:05]] [bad](javascript:alert(1))"},
			{Level: 2, Title: "TLB", StartMS: 300_000, Body: "fast"},
		}, SelfCheck: []string{"Why page?"}},
		Provider: "gemini", Model: "fixture-gemini", FileURI: "https://www.youtube.com/watch?v=abc123xxxxx",
		Parents: []string{StageMetadata, KindSponsorSegments}, Requests: 2,
	}
	res, err := r.p.writeGeminiNote(ctx, it, mustEffective(t, nil), n)
	if err != nil {
		t.Fatal(err)
	}
	if res.state.Status != store.StatusDone || !res.advance || res.artifact == nil || res.artifact.Kind != KindNote || res.stage.Executor != ExecutorAPI {
		t.Fatalf("result = %+v", res)
	}
	d := readNoteDoc(t, r, res.artifact)
	for _, want := range []string{
		"notesteme_engine: \"gemini\"", "notesteme_timing: \"model\"",
		"## Paging · [00:00](https://www.youtube.com/watch?v=abc123xxxxx&t=0s)",
		"at [01:05](https://www.youtube.com/watch?v=abc123xxxxx&t=65s)",
		"&lt;script&gt;", "## 自我檢驗",
	} {
		if !strings.Contains(d.Markdown, want) {
			t.Errorf("筆記沒有 %q：\n%s", want, d.Markdown)
		}
	}
	if strings.Contains(d.Markdown, "<script>") || strings.Contains(d.Markdown, "javascript:") || strings.Contains(d.Markdown, "[[t:") {
		t.Errorf("沒有中和：\n%s", d.Markdown)
	}
	if !notes.Passed(d.Checks) || d.Engine != "gemini" || d.Frontmatter["notesteme_engine"] != "gemini" || d.Frontmatter["notesteme_timing"] != "model" {
		t.Errorf("doc = %+v", d)
	}
	var prov map[string]any
	_ = json.Unmarshal(res.artifact.Provenance, &prov)
	if prov["engine"] != "gemini" || prov["timing"] != "model" || prov["provider"] != "gemini" || prov["model"] != "fixture-gemini" ||
		prov["gemini"].(map[string]any)["file_uri"] != n.FileURI || prov["checks"].(map[string]any)["passed"] != true || prov["template"] != "video-notes@1" {
		t.Errorf("provenance = %v", prov)
	}
	sb, _, _ := r.p.currentSponsorArtifact(ctx, it.ID)
	if len(res.artifact.Parents) != 1 || res.artifact.Parents[0] != sb.ID {
		t.Errorf("parents = %v，預期 [%s]", res.artifact.Parents, sb.ID)
	}
	// 沒有片段時沒有 parents。
	n.Parents = []string{StageMetadata}
	if res, err := r.p.writeGeminiNote(ctx, it, mustEffective(t, nil), n); err != nil || len(res.artifact.Parents) != 0 {
		t.Errorf("沒有片段：%v %v", res.artifact, err)
	}

	// 模板多了沒有時間的標題：仍寫出產出，但 blocked 且不前進。
	body := "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n## 補充\n\n{{.SelfCheck}}\n"
	if _, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "extra", Body: &body}); err != nil {
		t.Fatal(err)
	}
	eff := mustEffective(t, map[string]any{"stages": map[string]any{"note": map[string]any{"template": "extra"}}})
	res, err = r.p.writeGeminiNote(ctx, it, eff, n)
	if err != nil || res.state.Status != store.StatusBlocked || res.advance || res.artifact == nil ||
		res.state.Message != "筆記沒有通過自動檢查：標題沒有時間戳（補充）。" {
		t.Fatalf("blocked = %+v %v", res, err)
	}
	// 找不到模板：permanent，附開啟筆記模板的動作。
	eff = mustEffective(t, map[string]any{"stages": map[string]any{"note": map[string]any{"template": "missing"}}})
	if _, err := r.p.writeGeminiNote(ctx, it, eff, n); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("找不到模板：%v", err)
	}
}
