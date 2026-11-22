package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// seedVariant 以 SeedTranscript 寫入一份指定語言與變體的字幕。
func seedVariant(t *testing.T, r *rig, it store.Item, v subtitle.Variant, lang string, texts ...string) {
	t.Helper()
	var cues []subtitle.Cue
	for i, s := range texts {
		cues = append(cues, subtitle.Cue{
			Index: i + 1, StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000, Text: s, Timing: subtitle.TimingSource,
			Words: []subtitle.Word{{Text: s, StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000}},
		})
	}
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: v, Lang: lang, Cues: cues}); err != nil {
		t.Fatal(err)
	}
}

// translated 讀出某個語言現行的 translated 產出與內容。
func translated(t *testing.T, r *rig, itemID, lang string) (store.Artifact, subtitle.Transcript) {
	t.Helper()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), itemID, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Variant == "translated" && a.Lang == lang {
			tr, err := readTranscript(r.dir, a.Path, a.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			return a, tr
		}
	}
	t.Fatalf("沒有現行的 translated/%s：%+v", lang, arts)
	return store.Artifact{}, subtitle.Transcript{}
}

func rerunTranslate(t *testing.T, r *rig, it store.Item, targets []string, strategy string, extra map[string]any) {
	t.Helper()
	over := withMap(langOver(targets, strategy), extra)
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranslate, Overrides: over}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	r.run()
}

func newZhRig(t *testing.T) (*rig, store.Item) {
	t.Helper()
	r := newRig(t)
	it := r.ingest(watch, nil, "metadata")
	r.run()
	return r, it
}

// TRN-R8：簡體原文轉成多個繁體目標；一個 stage run、每個目標一份 translated 產出，不呼叫 LLM。
func TestRunTranslateOpenCC(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件的设置", "这个视频的质量很好")

	// 預設設定的 api 沒有 provider：照樣用 opencc。
	rerunTranslate(t, r, it, []string{"zh-Hant-TW", "zh-Hant-HK"}, "convert", nil)

	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusDone || st.Executor != ExecutorOpenCC || st.ImplVersion != 1 || st.CacheKey == "" {
		t.Fatalf("translate = %+v", st)
	}
	tw, twTr := translated(t, r, it.ID, "zh-Hant-TW")
	hk, hkTr := translated(t, r, it.ID, "zh-Hant-HK")
	if st.ArtifactID != tw.ID {
		t.Errorf("stage run 應指向第一個目標的產出：%s，預期 %s", st.ArtifactID, tw.ID)
	}
	if got := []string{twTr.Cues[0].Text, twTr.Cues[1].Text}; got[0] != "開啟軟體的設定" || got[1] != "這個影片的質量很好" {
		t.Errorf("zh-Hant-TW = %q", got)
	}
	if got := []string{hkTr.Cues[0].Text, hkTr.Cues[1].Text}; got[0] != "打開軟件的設置" {
		t.Errorf("zh-Hant-HK = %q", got)
	}
	// 時間、index、timing、speaker 不變；字詞級時間不保留。
	for _, c := range twTr.Cues {
		src := c.Index - 1
		if c.StartMS != int64(src)*2000 || c.EndMS != int64(src+1)*2000 || c.Timing != subtitle.TimingSource || len(c.Words) != 0 {
			t.Errorf("cue %d：%+v", c.Index, c)
		}
	}
	if twTr.Variant != subtitle.VariantTranslated || twTr.Lang != "zh-Hant-TW" {
		t.Errorf("transcript = %+v", twTr)
	}
	corrected := currentVariant(t, r, it.ID, "corrected")
	if len(tw.Parents) != 1 || tw.Parents[0] != corrected.ID || len(hk.Parents) != 1 || hk.Parents[0] != corrected.ID {
		t.Errorf("parents = %v、%v，預期 [%s]", tw.Parents, hk.Parents, corrected.ID)
	}
	// 所有目標共用同一份校正字幕，不重跑校正（TRN-R1）。
	if n, _ := r.st.Read().ArtifactVersions(context.Background(), it.ID, "transcript", "corrected", "zh-Hans"); n != 1 {
		t.Errorf("corrected 版本數 = %d", n)
	}
}

func TestRunTranslateOpenCCProvenance(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件的设置")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "convert", nil)

	a, _ := translated(t, r, it.ID, "zh-Hant-TW")
	var prov map[string]any
	if err := json.Unmarshal(a.Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	st := latestStage(t, r, it.ID, StageTranslate)
	corrected := currentVariant(t, r, it.ID, "corrected")
	for k, want := range map[string]any{
		"stage": "translate", "executor": "opencc", "table": "s2twp", "strategy": "convert", "opencc_data": zhconv.DataVersion,
		"source_lang": "zh-Hans", "target": "zh-Hant-TW", "impl_version": float64(1), "cache_key": st.CacheKey + "/zh-Hant-TW",
	} {
		if prov[k] != want {
			t.Errorf("provenance.%s = %v，預期 %v", k, prov[k], want)
		}
	}
	df, _ := prov["derived_from"].([]any)
	if len(df) != 1 || df[0].(map[string]any)["artifact"] != corrected.ID || df[0].(map[string]any)["sha256"] != corrected.SHA256 {
		t.Errorf("derived_from = %v", prov["derived_from"])
	}
	if prov["created_at"] == nil || prov["tool_versions"] == nil {
		t.Errorf("缺少統一加上的欄位：%v", prov)
	}
}

// PIPE-R5：同一份上游、同一組目標與字典版本時快取命中，不建立新版本；目標改變或步驟改版後重新轉換。
func TestRunTranslateOpenCCCacheHit(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件的设置")
	versions := func(lang string) int {
		n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "translated", lang)
		return n
	}

	rerunTranslate(t, r, it, []string{"zh-Hant-TW", "zh-Hant-HK"}, "", nil)
	first := latestStage(t, r, it.ID, StageTranslate)
	rerunTranslate(t, r, it, []string{"zh-Hant-TW", "zh-Hant-HK"}, "", nil)
	second := latestStage(t, r, it.ID, StageTranslate)
	if second.ID == first.ID || second.Status != store.StatusDone || second.CacheKey != first.CacheKey || second.ArtifactID != first.ArtifactID {
		t.Errorf("第二次應命中快取：%+v，原本 %+v", second, first)
	}
	if versions("zh-Hant-TW") != 1 || versions("zh-Hant-HK") != 1 {
		t.Errorf("快取命中不應產生新版本：%d、%d", versions("zh-Hant-TW"), versions("zh-Hant-HK"))
	}

	// 目標改變：params 不同，快取鍵不同，兩個目標都重新轉換。
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	third := latestStage(t, r, it.ID, StageTranslate)
	if third.CacheKey == first.CacheKey || versions("zh-Hant-TW") != 2 {
		t.Errorf("目標改變應重新轉換：%+v，zh-Hant-TW 版本數 %d", third, versions("zh-Hant-TW"))
	}

	// impl_version 改變（PIPE-R6）。
	if err := r.p.SetImplVersion(StageTranslate, 2); err != nil {
		t.Fatal(err)
	}
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	if fourth := latestStage(t, r, it.ID, StageTranslate); fourth.ImplVersion != 2 || fourth.CacheKey == third.CacheKey || versions("zh-Hant-TW") != 3 {
		t.Errorf("步驟改版應重新轉換：%+v，版本數 %d", fourth, versions("zh-Hant-TW"))
	}
}

// language.targets 減少語言後，不再要求的語言的 translated 產出被取代，不再是現行版本（Codex P2）：
// 產出的取代以語言為單位，不會自己被取代；舊版本仍保留（保留最近幾版）。
func TestRunTranslateOpenCCSupersedesRemovedTargets(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件的设置")
	currentLangs := func() []string {
		arts, err := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
		if err != nil {
			t.Fatal(err)
		}
		var langs []string
		for _, a := range arts {
			if a.Variant == "translated" {
				langs = append(langs, a.Lang)
			}
		}
		return langs
	}
	rerunTranslate(t, r, it, []string{"zh-Hant-TW", "zh-Hant-HK"}, "", nil)
	if got := strings.Join(currentLangs(), ","); got != "zh-Hant-TW,zh-Hant-HK" {
		t.Fatalf("兩個目標之後現行的 translated = %s", got)
	}
	hkArt, _ := translated(t, r, it.ID, "zh-Hant-HK")
	hkID := hkArt.ID
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	if got := strings.Join(currentLangs(), ","); got != "zh-Hant-TW" {
		t.Errorf("目標減少之後現行的 translated = %s，預期只有 zh-Hant-TW", got)
	}
	// 舊版本還在（只是被取代），之後把語言加回來會產生新的版本。
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "translated", "zh-Hant-HK"); n != 1 {
		t.Errorf("zh-Hant-HK 的版本數 = %d，預期被取代但保留 1 版", n)
	}
	// 撤下的譯文指向自己，不指向另一個語言的新版本：之後那個版本被清除時不會留下懸空的 superseded_by（Codex P3）。
	if hk, err := r.st.Read().Artifact(ctx, hkID); err != nil || hk.SupersededBy != hkID {
		t.Errorf("撤下的 zh-Hant-HK 的 superseded_by = %q, %v，預期指向自己 %q", hk.SupersededBy, err, hkID)
	}
	// 台灣目標重複產生超過保留的版本數：撤下的香港版本仍然存在、參照仍有效。
	for _, text := range []string{"设置", "质量", "软件", "视频", "网络"} {
		seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", text)
		rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	}
	hk, err := r.st.Read().Artifact(ctx, hkID)
	if err != nil || hk.SupersededBy != hkID {
		t.Errorf("台灣版本被清除之後，香港版本 = %+v, %v", hk, err)
	}
}

// 全部目標都與原文相同：translate 是 skipped（same_language），先前為已移除的目標產生的譯文不再是現行版本，
// 預設字幕選擇不會再回傳舊譯文（Codex P2）。
func TestSameLanguageSkipWithdrawsRemovedTargets(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件的设置")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	translated(t, r, it.ID, "zh-Hant-TW")

	rerunTranslate(t, r, it, []string{"zh-Hans"}, "", nil)
	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusSkipped || st.BlockReason != SkipSameLanguage {
		t.Fatalf("translate = %+v", st)
	}
	arts, err := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Variant == "translated" {
			t.Errorf("same_language 之後不該還有現行的 translated：%+v", a)
		}
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "translated", "zh-Hant-TW"); n != 1 {
		t.Errorf("歷史版本應保留：%d", n)
	}
}

// 排入之後目標才改成全部與原文相同：runTranslateOpenCC 回報 skipped（same_language），並撤下舊譯文，而不是沒有產出的 done。
func TestRunTranslateOpenCCNoJobsIsSkipped(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)

	res, err := r.p.runTranslateOpenCC(ctx, it, mustEffective(t, langOver([]string{"zh-Hans"}, "")))
	if err != nil {
		t.Fatal(err)
	}
	if res.state.Status != store.StatusSkipped || res.state.BlockReason != SkipSameLanguage || len(res.allArtifacts()) != 0 {
		t.Errorf("state = %+v", res.state)
	}
	if res.supersede == nil || len(res.supersede.Langs) != 1 || res.supersede.Langs[0] != "zh-Hant-TW" {
		t.Errorf("supersede = %+v，預期撤下 zh-Hant-TW", res.supersede)
	}
}

// 目標的拼法不同（大小寫、底線）而語言相同：產出、快取與取代都用正規化的拼法，不會留下兩份現行譯文（Codex P2）。
func TestRunTranslateOpenCCCanonicalLangKey(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件")
	rerunTranslate(t, r, it, []string{"zh_hant_tw"}, "", nil)
	first, _ := translated(t, r, it.ID, "zh-Hant-TW")
	// 換一種拼法：快取命中，沒有新版本。
	rerunTranslate(t, r, it, []string{"ZH-hant-tw"}, "", nil)
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "translated", "zh-Hant-TW"); n != 1 {
		t.Errorf("拼法不同不該產生新版本：%d", n)
	}
	cur, _ := translated(t, r, it.ID, "zh-Hant-TW")
	if cur.ID != first.ID {
		t.Errorf("現行版本改變了：%s → %s", first.ID, cur.ID)
	}
	arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	n := 0
	for _, a := range arts {
		if a.Variant == "translated" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("現行的 translated = %d 份，預期 1", n)
	}
}

// 上游字幕改變（corrected 重新產生）後，快取鍵不同，翻譯重新轉換。
func TestRunTranslateOpenCCRerunsWhenUpstreamChanges(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "打开软件")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "设置")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	if _, tr := translated(t, r, it.ID, "zh-Hant-TW"); len(tr.Cues) != 1 || tr.Cues[0].Text != "設定" {
		t.Errorf("上游改變後應重新轉換：%+v", tr.Cues)
	}
}

// 測試接縫：convertText 可以換成假的對照表。
func TestRunTranslateOpenCCUsesConvertSeam(t *testing.T) {
	old := convertText
	t.Cleanup(func() { convertText = old })
	var tables []zhconv.Table
	convertText = func(tb zhconv.Table, s string) (string, error) {
		tables = append(tables, tb)
		return "<" + string(tb) + ">" + s, nil
	}
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantNormalized, "zh-Hans", "甲")
	rerunTranslate(t, r, it, []string{"zh-Hant-HK"}, "", nil)
	if _, tr := translated(t, r, it.ID, "zh-Hant-HK"); tr.Cues[0].Text != "<s2hk>甲" || len(tables) != 1 {
		t.Errorf("假轉換表：%+v，呼叫 %v", tr.Cues, tables)
	}
}

// 轉換失敗：不留下任何產出檔，錯誤不是 permanent（會重試）。
func TestRunTranslateOpenCCConvertErrorLeavesNoFiles(t *testing.T) {
	old := convertText
	t.Cleanup(func() { convertText = old })
	calls := 0
	convertText = func(tb zhconv.Table, s string) (string, error) {
		calls++
		if tb == zhconv.S2HK {
			return "", errors.New("boom")
		}
		return s, nil
	}
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "甲")
	before := artifactFiles(r, it.ID)
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	res, err := r.p.runTranslateOpenCC(context.Background(), it, mustEffective(t, langOver([]string{"zh-Hant-TW", "zh-Hant-HK"}, "")))
	if err == nil || errors.Is(err, errNoSource) || len(res.allArtifacts()) != 0 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if c, ok := errclass.ClassOf(err); ok && c == errclass.ClassPermanent {
		t.Errorf("轉換失敗不該是 permanent：%v", err)
	}
	if n := artifactFiles(r, it.ID); n != before {
		t.Errorf("失敗後不該留下產出檔：%d，原本 %d（%d 個產出）", n, before, len(arts))
	}
}

// 明確設定 opencc 而目標不是中文：permanent 失敗，訊息說明 opencc 只能轉換中文字形。
func TestRunTranslateOpenCCNonChineseTarget(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "你好")
	rerunTranslate(t, r, it, []string{"en"}, "", stagesOver("translate", map[string]any{"executor": "opencc"}))
	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusFailed || st.ErrorClass != string(errclass.ClassPermanent) || st.Message != "opencc 只能轉換中文字形（目標 en）。" {
		t.Errorf("translate = %+v", st)
	}
	// 原文不是中文、目標是中文。
	r2, it2 := newZhRig(t)
	seedVariant(t, r2, it2, subtitle.VariantCorrected, "en", "hello")
	rerunTranslate(t, r2, it2, []string{"zh-Hant-TW"}, "", stagesOver("translate", map[string]any{"executor": "opencc"}))
	if st := latestStage(t, r2, it2.ID, StageTranslate); st.Status != store.StatusFailed || st.Message != "opencc 只能轉換中文字形（原文 en）。" {
		t.Errorf("原文非中文：%+v", st)
	}
}

// 明確設定 opencc 時，variant_strategy: localize 也只做確定性轉換（含詞彙轉換的表）。
func TestRunTranslateOpenCCExplicitIgnoresLocalize(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "这个视频的质量很好")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "localize", stagesOver("translate", map[string]any{"executor": "opencc"}))
	a, tr := translated(t, r, it.ID, "zh-Hant-TW")
	if tr.Cues[0].Text != "這個影片的質量很好" || !json.Valid(a.Provenance) {
		t.Errorf("text = %q", tr.Cues[0].Text)
	}
}

// 沒有可用的輸入字幕：permanent 失敗。
func TestRunTranslateOpenCCNoInput(t *testing.T) {
	r, it := newZhRig(t)
	_, err := r.p.runTranslateOpenCC(context.Background(), it, mustEffective(t, nil))
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassPermanent {
		t.Errorf("err = %v", err)
	}
}

// TRN-R8 的 rerun 不再是 501（原本解析成 api 而尚未實作），而且沒有寫入以前就解析好。
func TestRerunTranslateOpenCCNo501(t *testing.T) {
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "你好")
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranslate, Overrides: langOver([]string{"zh-Hant-TW"}, "")}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	// localize 且 api 有 provider：LLM 翻譯屬於 S6b，仍是 501。
	over := withMap(langOver([]string{"zh-Hant-TW"}, "localize"), apiConfigured)
	var ni *NotImplementedError
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranslate, Overrides: over}); !errors.As(err, &ni) {
		t.Errorf("localize + api 應回 501：%v", err)
	}
}

// stageContext 用 translate 輸入字幕的語言，不是 items.original_lang（NRM-R7、OUT 的場景中兩者可能不同）。
func TestStageContextUsesArtifactLang(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	it.OriginalLang = "en" // 只影響 stageContext 的輸入；Rerun 讀的是資料庫中的影音
	sc, err := r.p.stageContext(ctx, r.st.Read(), it, "")
	if err != nil || sc.SourceLang != "en" {
		t.Fatalf("沒有字幕時退回 original_lang：%+v, %v", sc, err)
	}
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "你好")
	if sc, _ = r.p.stageContext(ctx, r.st.Read(), it, ""); sc.SourceLang != "zh-Hans" {
		t.Errorf("應使用字幕的語言：%+v", sc)
	}
	// 簡體原文：預設目標用 opencc 轉換，不看 original_lang（en）。
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	if st := latestStage(t, r, it.ID, StageTranslate); st.Status != store.StatusDone || st.Executor != ExecutorOpenCC {
		t.Errorf("translate = %+v", st)
	}
}

// 字幕語言是 zh（沒有 script）時用字元判斷簡繁（S6-Q3）。
func TestStageContextDetectsScriptForBareZh(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"簡體": {"打开软件的设置，这个视频的质量很好", "zh-Hans"},
		"繁體": {"打開軟件的設置，這個視頻的質量很好", "zh-Hant"},
		"不明": {"好", "zh"},
	} {
		r, it := newZhRig(t)
		seedVariant(t, r, it, subtitle.VariantNormalized, "zh", c.text)
		sc, err := r.p.stageContext(context.Background(), r.st.Read(), it, "")
		if err != nil || sc.SourceLang != c.want {
			t.Errorf("%s：%+v, %v，預期 %s", name, sc, err, c.want)
		}
	}
	// 簡體：translate 以 s2twp 轉換；產出的 source_lang 帶著判斷出的 script。
	r, it := newZhRig(t)
	seedVariant(t, r, it, subtitle.VariantNormalized, "zh", "打开软件的设置，这个视频的质量很好")
	rerunTranslate(t, r, it, []string{"zh-Hant-TW"}, "", nil)
	a, tr := translated(t, r, it.ID, "zh-Hant-TW")
	var prov struct {
		SourceLang string `json:"source_lang"`
		Table      string `json:"table"`
	}
	_ = json.Unmarshal(a.Provenance, &prov)
	if prov.SourceLang != "zh-Hans" || prov.Table != "s2twp" || tr.Cues[0].Text != "開啟軟體的設定，這個影片的質量很好" {
		t.Errorf("prov = %+v，text = %q", prov, tr.Cues[0].Text)
	}
}

// 完整流程：簡體原文、預設設定。translate 前進時由 pipeline 解析成 opencc 並完成，publish 仍然 skipped。
func TestFullRunTranslatesChineseWithOpenCC(t *testing.T) {
	r := newRig(t)
	r.yt.info.Language = "zh-Hans"
	r.yt.info.Tracks = []youtube.Track{{Lang: "zh-Hans", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	for _, s := range []string{StageSegment, StageCorrect} {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusSkipped || st.BlockReason != SkipNotConfigured {
			t.Errorf("%s = %+v", s, st)
		}
	}
	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusDone || st.Executor != ExecutorOpenCC || st.BlockReason != "" || st.ArtifactID == "" {
		t.Fatalf("translate = %+v", st)
	}
	if hasTask(t, r, it.ID, StageTranslate) {
		tk, _ := r.st.Read().TaskByStageRun(context.Background(), st.ID)
		if tk.ResourceClass != "local" {
			t.Errorf("opencc 是本機處理：%+v", tk)
		}
	}
	if pub := latestStage(t, r, it.ID, StagePublish); pub.Status != store.StatusSkipped || pub.BlockReason != SkipNoOutputs {
		t.Errorf("publish = %+v", pub)
	}
	a, _ := translated(t, r, it.ID, "zh-Hant-TW")
	norm := currentVariant(t, r, it.ID, "normalized")
	if len(a.Parents) != 1 || a.Parents[0] != norm.ID {
		t.Errorf("沒有校正時以 normalized 為輸入：%v，預期 [%s]", a.Parents, norm.ID)
	}
}

// 完整流程：原文已經是目標語言 → translate skipped（same_language），沒有任務。
func TestFullRunSkipsTranslateWhenSameLanguage(t *testing.T) {
	r := newRig(t)
	r.yt.info.Language = "zh-Hant-TW"
	r.yt.info.Tracks = []youtube.Track{{Lang: "zh-Hant-TW", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusSkipped || st.BlockReason != SkipSameLanguage || st.Message != "" || hasTask(t, r, it.ID, StageTranslate) {
		t.Errorf("translate = %+v", st)
	}
	if arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript"); len(arts) != 2 {
		t.Errorf("不該有 translated 產出：%+v", arts)
	}
}

// 英文原文、預設設定：translate 仍然 skipped（not_configured），行為與 M1 相同。
func TestFullRunEnglishStillNotConfigured(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if st := latestStage(t, r, it.ID, StageTranslate); st.Status != store.StatusSkipped || st.BlockReason != SkipNotConfigured {
		t.Errorf("translate = %+v", st)
	}
}

// PIPE-R7：每個目標語言各自保留最近 3 個版本；被清除版本的檔案一併刪除。
func TestApplyMultiArtifactsPurgesPerLang(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "你好")
	for v := 1; v <= 5; v++ {
		if err := r.p.SetImplVersion(StageTranslate, v); err != nil {
			t.Fatal(err)
		}
		rerunTranslate(t, r, it, []string{"zh-Hant-TW", "zh-Hant-HK"}, "", nil)
	}
	for _, lang := range []string{"zh-Hant-TW", "zh-Hant-HK"} {
		if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "translated", lang); n != keepArtifactVersions {
			t.Errorf("%s 版本數 = %d，預期 %d", lang, n, keepArtifactVersions)
		}
	}
	// 檔案：seed 的 corrected 1 個 + 兩個語言各 3 個。
	files, _ := os.ReadDir(filepath.Join(r.dir, artifactsDir, it.ID))
	var count int
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			count++
		}
	}
	// metadata 之後沒有 source／normalized；corrected 1 個。
	if count != 1+2*keepArtifactVersions {
		t.Errorf("產出檔 = %d 個，預期 %d", count, 1+2*keepArtifactVersions)
	}
}

// 租約已失效：結果丟棄，所有已寫出的產出檔都要清掉（不是只清第一份）。
func TestApplyMultiArtifactsDiscardsAllFiles(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "metadata")
	run := r.stage(it, StageMetadata)
	var outs []*store.NewArtifact
	for _, lang := range []string{"zh-Hant-TW", "zh-Hant-HK", "zh-Hans"} {
		a, err := r.p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: lang, Variant: subtitle.VariantTranslated,
			Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "x", Timing: subtitle.TimingSource}}}, nil, json.RawMessage(`{"source":"t"}`))
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, a)
	}
	if artifactFiles(r, it.ID) != 3 {
		t.Fatal("前置條件：三份產出檔應已寫出")
	}
	res := result{task: taskDone, artifacts: outs, stage: &store.StageResult{CacheKey: "k"}, state: store.StageState{Status: store.StatusDone}}
	// 任務不存在（租約失效）。
	if err := r.p.apply(context.Background(), store.Task{ID: "gone"}, run, it, res); err != nil {
		t.Fatal(err)
	}
	if n := artifactFiles(r, it.ID); n != 0 {
		t.Errorf("租約失效後不該留下產出檔，還有 %d 個", n)
	}
	if arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript"); len(arts) != 0 {
		t.Errorf("不該寫入任何記錄：%+v", arts)
	}
}

// Rerun 在寫入前解析執行方式：從 correct 重跑而 correct 被 off 略過時，現行的 corrected 是舊脈絡的字幕，
// 翻譯要用上游的 normalized（zh-Hans → zh-Hant-TW 要轉換），不能把舊 corrected（已經是 zh-Hant-TW）當原文而略過（agy P2）。
func TestRerunResolvesTranslateWithoutStaleDownstreamOutputs(t *testing.T) {
	r, it := newZhRig(t)
	ctx := context.Background()
	seedVariant(t, r, it, subtitle.VariantNormalized, "zh-Hans", "打开软件的设置")
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hant-TW", "舊的校正字幕")
	// 讓最新的 correct run 是 done：舊脈絡完整跑過。
	err := r.st.Tx(ctx, func(repo *store.Repo) error {
		runs, err := repo.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		for _, run := range latestRuns(runs) {
			if run.Stage == StageNormalize || run.Stage == StageSegment || run.Stage == StageCorrect {
				if err := repo.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusDone}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	over := withMap(langOver([]string{"zh-Hant-TW"}, ""), stagesOver("correct", map[string]any{"executor": "off"}))
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageCorrect, Overrides: over}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if st := latestStage(t, r, it.ID, StageTranslate); st.Status != store.StatusDone || st.Executor != ExecutorOpenCC {
		t.Fatalf("translate = %+v，預期用 normalized 轉換", st)
	}
	if _, tr := translated(t, r, it.ID, "zh-Hant-TW"); tr.Cues[0].Text != "開啟軟體的設定" {
		t.Errorf("譯文 = %q", tr.Cues[0].Text)
	}
}
