package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// summaryPrompts 把每個引用都解析成 summary 類型的第 1 版。
type summaryPrompts struct{}

func (summaryPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	return ResolvedPrompt{ID: id, Kind: StageSummary, Version: 1, SHA256: "sha-sum1", Body: "摘要成 {{summary_lang}}。{{title}}"}, nil
}

const goodSummary = `{"summary":"這部影片介紹工具鏈。\n然後講開機。","key_points":[" 工具鏈 ","開機"],"title_translation":"標題譯文"}`

// newSummaryRig 建立會走 LLM 摘要的 rig：summary 用 api，settings 併進 stages.summary。片長 600 秒。
func newSummaryRig(t *testing.T, fake *chapLLM, settings map[string]any, tweak ...func(*rig)) (*rig, store.Item) {
	t.Helper()
	r := newRig(t, func(d *Deps) {
		reg := llm.NewRegistry(nil, d.Clock)
		reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
		reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
		d.LLM, d.Prompts = reg, summaryPrompts{}
	})
	dur := 600
	r.yt.info.DurationS = &dur
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	for _, f := range tweak {
		f(r)
	}
	stage := map[string]any{"executor": "api", "provider": "anthropic", "model": "m"}
	for k, v := range settings {
		stage[k] = v
	}
	it := r.ingest(watch, stagesOver(StageSummary, stage), "")
	r.run()
	return r, it
}

// DIG-R2～R7：摘要完成後暫停在下一個步驟（awaiting_user），繼續之後往下做；
// 原文是英文、摘要是正體中文時保留標題譯文；資料區塊不含時間。
func TestSummaryPausesAndContinues(t *testing.T) {
	ctx := context.Background()
	fake := &chapLLM{out: goodSummary}
	r, it := newSummaryRig(t, fake, map[string]any{"language": "zh-Hant", "pause_after": true})
	st := latestStage(t, r, it.ID, StageSummary)
	if st.Status != store.StatusDone || st.ArtifactID == "" || fake.calls() != 1 {
		t.Fatalf("summary = %+v，calls = %d", st, fake.calls())
	}
	if req := fake.data[0]; !strings.Contains(req, "zh-Hant") || strings.Contains(req, "00:00:") {
		t.Errorf("請求：%s", req)
	}
	sum, ok, err := r.p.ItemSummary(ctx, it.ID)
	if err != nil || !ok || sum.Text != "這部影片介紹工具鏈。\n然後講開機。" || len(sum.KeyPoints) != 2 || sum.KeyPoints[0] != "工具鏈" || sum.TitleTranslation == nil || sum.Language != "zh-Hant" {
		t.Fatalf("ItemSummary = %+v %v %v", sum, ok, err)
	}
	art, _ := r.st.Read().Artifact(ctx, st.ArtifactID)
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if s, _ := prov["summary"].(map[string]any); s["language"] != "zh-Hant" || len(art.Parents) != 1 {
		t.Errorf("provenance = %v parents = %v", prov, art.Parents)
	}
	seg := latestStage(t, r, it.ID, StageSegment)
	if seg.Status != store.StatusBlocked || seg.BlockReason != BlockAwaitingUser || seg.Message != msgAwaitingUser || strings.Join(seg.Actions, ",") != "繼續處理,取消" {
		t.Fatalf("segment = %+v", seg)
	}
	if hasTask(t, r, it.ID, StageSegment) {
		t.Error("暫停時不建任務")
	}
	if ok, err := AwaitingUser(ctx, r.st.Read(), it.ID); !ok || err != nil {
		t.Errorf("AwaitingUser = %v %v", ok, err)
	}
	d, err := r.p.Item(ctx, it.ID)
	if err != nil || d.Summary == nil || d.Summary.Text != sum.Text {
		t.Errorf("Detail.Summary = %+v %v", d.Summary, err)
	}

	stage, err := r.p.Continue(ctx, it.ID)
	if err != nil || stage != StageSegment {
		t.Fatalf("Continue = %q %v", stage, err)
	}
	r.run()
	if seg := latestStage(t, r, it.ID, StageSegment); seg.Status == store.StatusBlocked {
		t.Errorf("繼續之後不再暫停：%+v", seg)
	}
	if ok, _ := AwaitingUser(ctx, r.st.Read(), it.ID); ok {
		t.Error("繼續之後不再等你決定")
	}
	if _, err := r.p.Continue(ctx, it.ID); !errors.Is(err, ErrNotAwaitingUser) {
		t.Errorf("第二次繼續：%v", err)
	}
	if _, err := r.p.Continue(ctx, "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
	// 從 segment 重跑不經過 summary，不會再暫停。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSegment}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if seg := latestStage(t, r, it.ID, StageSegment); seg.Status == store.StatusBlocked {
		t.Errorf("從 segment 重跑不暫停：%+v", seg)
	}
}

// 在垃圾桶中的影音不能繼續。
func TestContinueInTrash(t *testing.T) {
	ctx := context.Background()
	r, it := newSummaryRig(t, &chapLLM{out: goodSummary}, map[string]any{"pause_after": true})
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var trash *InTrashError
	if _, err := r.p.Continue(ctx, it.ID); !errors.As(err, &trash) {
		t.Errorf("垃圾桶：%v", err)
	}
}

// DIG-R3：原文與摘要同語言時丟掉模型回傳的譯文；沒有 pause_after 時不暫停。
// 內容相同的重新產生沿用現行產出（DIG-R5）。
func TestSummarySameLanguageAndReuse(t *testing.T) {
	ctx := context.Background()
	fake := &chapLLM{out: goodSummary}
	r, it := newSummaryRig(t, fake, map[string]any{"language": "en-US"})
	sum, ok, err := r.p.ItemSummary(ctx, it.ID)
	if err != nil || !ok || sum.TitleTranslation != nil || sum.Language != "en-US" {
		t.Fatalf("同語言不留譯文：%+v %v %v", sum, ok, err)
	}
	if seg := latestStage(t, r, it.ID, StageSegment); seg.Status == store.StatusBlocked {
		t.Errorf("沒有 pause_after 不暫停：%+v", seg)
	}
	first := latestStage(t, r, it.ID, StageSummary).ArtifactID
	// 換 model 讓步驟層的快取不命中，輸出相同 → 沿用現行產出。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSummary, Until: StageSummary, Overrides: stagesOver(StageSummary, map[string]any{"model": "m2"})}); err != nil {
		t.Fatal(err)
	}
	r.run()
	again := latestStage(t, r, it.ID, StageSummary)
	if fake.calls() != 2 || again.ArtifactID != first {
		t.Errorf("內容相同沿用產出：calls=%d %s → %s", fake.calls(), first, again.ArtifactID)
	}
	arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, KindSummary)
	if len(arts) != 1 {
		t.Errorf("不建立新版本：%d", len(arts))
	}
}

// DIG-R4：重試用盡時步驟 done、沒有產出，訊息說明原因；之前的摘要保留。
func TestSummaryFallback(t *testing.T) {
	ctx := context.Background()
	fake := &chapLLM{out: `{"summary":"a","key_points":["1","2","3","4","5","6","7","8"],"title_translation":null}`}
	r, it := newSummaryRig(t, fake, map[string]any{"pause_after": true})
	st := latestStage(t, r, it.ID, StageSummary)
	want := "無法產生摘要：模型回傳的內容沒有通過驗證（key_points 有 8 項，上限是 7 項）。"
	if st.Status != store.StatusDone || st.Message != want || strings.Join(st.Actions, ",") != "重新產生摘要,開啟 Prompt" || st.ArtifactID != "" {
		t.Fatalf("summary = %+v", st)
	}
	if fake.calls() != 3 || !strings.Contains(fake.data[1], "key_points 有 8 項") {
		t.Errorf("重試 2 次並帶上錯誤：%d", fake.calls())
	}
	if _, ok, _ := r.p.ItemSummary(ctx, it.ID); ok {
		t.Error("沒有摘要")
	}
	// fallback 之後一樣暫停（DIG-R6：summary 結束就套用）。
	if seg := latestStage(t, r, it.ID, StageSegment); seg.BlockReason != BlockAwaitingUser {
		t.Errorf("segment = %+v", seg)
	}
}

// DIG-R2：超過片長或 token 上限時不送請求，skipped（too_long）；pause_after 照常套用。
func TestSummaryTooLong(t *testing.T) {
	cases := map[string]struct {
		settings map[string]any
		tweak    func(*rig)
		msg      string
	}{
		"片長": {map[string]any{"max_duration": "10m", "pause_after": true}, func(r *rig) { d := 601; r.yt.info.DurationS = &d },
			"影片長度 0:10:01 超過摘要的上限 0:10:00，略過摘要。"},
		"字數": {map[string]any{"max_input_tokens": 1000, "pause_after": true}, func(r *rig) { r.yt.info.Title = strings.Repeat("長標題 ", 1500) },
			"超過摘要的上限 1000 tokens，略過摘要。"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &chapLLM{out: goodSummary}
			r, it := newSummaryRig(t, fake, c.settings, c.tweak)
			st := latestStage(t, r, it.ID, StageSummary)
			if st.Status != store.StatusSkipped || st.BlockReason != SkipTooLong || !strings.Contains(st.Message, c.msg) || fake.calls() != 0 {
				t.Fatalf("summary = %+v，calls = %d", st, fake.calls())
			}
			if seg := latestStage(t, r, it.ID, StageSegment); seg.BlockReason != BlockAwaitingUser {
				t.Errorf("too_long 之後照常暫停：%+v", seg)
			}
		})
	}
}

// SeedSummary（驗收 harness）：寫入現行產出並把 summary 改成 done；不合格的內容與沒有 summary 步驟的影音回傳錯誤。
func TestSeedSummary(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if err := r.p.SeedSummary(ctx, it.ID, []byte(`{"summary":"種子","key_points":[],"title_translation":null}`)); err != nil {
		t.Fatal(err)
	}
	sum, ok, err := r.p.ItemSummary(ctx, it.ID)
	if err != nil || !ok || sum.Text != "種子" || sum.Language != "zh-Hant" {
		t.Fatalf("預設語言：%+v %v %v", sum, ok, err)
	}
	if st := latestStage(t, r, it.ID, StageSummary); st.Status != store.StatusDone {
		t.Errorf("summary = %+v", st)
	}
	if err := r.p.SeedSummary(ctx, it.ID, []byte(`{"summary":"x","key_points":[],"title_translation":null,"language":"ja"}`)); err != nil {
		t.Fatal(err)
	}
	if sum, _, _ := r.p.ItemSummary(ctx, it.ID); sum.Language != "ja" {
		t.Errorf("language = %q", sum.Language)
	}
	for name, doc := range map[string]string{"不是 JSON": `{`, "空摘要": `{"summary":"","key_points":[],"title_translation":null}`} {
		if err := r.p.SeedSummary(ctx, it.ID, []byte(doc)); err == nil {
			t.Errorf("%s：應該錯誤", name)
		}
	}
	if err := r.p.SeedSummary(ctx, "no-such-item", []byte(`{"summary":"a","key_points":[],"title_translation":null}`)); err == nil {
		t.Error("沒有 summary 步驟要錯誤")
	}
	// 產出檔被改過：sha256 不符要錯誤（彙整把它當成沒有摘要）。
	art, _, _ := r.p.currentSummaryArtifact(ctx, it.ID)
	if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(art.Path)), []byte(`{"text":"改過"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.p.ItemSummary(ctx, it.ID); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("改過的產出檔：%v", err)
	}
}

func TestSummaryHelpers(t *testing.T) {
	for sec, want := range map[int64]string{-5: "0:00:00", 0: "0:00:00", 59: "0:00:59", 3725: "1:02:05", 9000: "2:30:00", 360000: "100:00:00"} {
		if got := HMS(sec); got != want {
			t.Errorf("HMS(%d) = %q，預期 %q", sec, got, want)
		}
	}
	for n, want := range map[int]int{5: 5, 999: 999, 1000: 1000, 1049: 1000, 1050: 1100, 32456: 32500} {
		if got := roundTokens(n); got != want {
			t.Errorf("roundTokens(%d) = %d，預期 %d", n, got, want)
		}
	}
	same := map[[2]string]bool{
		{"en", "en-US"}: true, {"en", "ja"}: false, {"zh-Hant", "zh-TW"}: true, {"zh-Hans", "zh-Hant"}: false,
		{"zh", "zh-Hant"}: true, {"zh-CN", "zh-Hant"}: false, {"ja", "zh-Hant"}: false,
	}
	for pair, want := range same {
		if got := sameLanguage(pair[0], pair[1]); got != want {
			t.Errorf("sameLanguage(%s, %s) = %v，預期 %v", pair[0], pair[1], got, want)
		}
	}
	// 限制：沒有設定或格式不對時用預設值。
	for name, s := range map[string]summarySettings{
		"沒有設定": {}, "壞的片長": {MaxDuration: "forever"}, "負的片長": {MaxDuration: "-1h"}, "0 token": {MaxInputTokens: new(int)},
	} {
		if d, tok := s.limits(); d != defaultSummaryMaxDuration || tok != defaultSummaryMaxInputTokens {
			t.Errorf("%s：%s %d", name, d, tok)
		}
	}
	n := 100
	if d, tok := (summarySettings{MaxDuration: "1d", MaxInputTokens: &n}).limits(); d.Hours() != 24 || tok != 100 {
		t.Errorf("自訂上限：%s %d", d, tok)
	}
	if _, err := summarySettingsOf(mustEffective(t, stagesOver(StageSummary, map[string]any{"pause_after": "yes"}))); err == nil {
		t.Error("pause_after 不是布林要錯誤")
	}
	if got := summaryFallbackMessage(nil); !strings.Contains(got, "格式不正確") {
		t.Errorf("沒有 fallback 記錄：%q", got)
	}
	if got := summaryFallbackMessage([]fallbackRecord{{Reason: "schema"}}); !strings.Contains(got, "（schema）") {
		t.Errorf("只有 reason：%q", got)
	}
}
