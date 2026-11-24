package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 每個 M4 的處理方案欄位：有效的值沒有錯誤，無效的值回報欄位路徑與訊息（PRF-R4、DIG-R1、BATCH-R1、SRC-R22）。
func TestValidateLayer_M4Fields(t *testing.T) {
	valid := []string{
		`{ "stages": { "summary": { "executor": "off" } } }`,
		`{ "stages": { "summary": { "language": "zh-Hant" } } }`, // 預設 off，處理方案不需要 provider
		`{ "stages": { "summary": { "executor": "agent", "pause_after": true } } }`,
		`{ "stages": { "summary": { "executor": "api", "provider": "anthropic", "model": "m", "language": "zh-Hant-TW", "pause_after": false, "max_duration": "10m", "max_input_tokens": 1000, "batch": "auto" } } }`,
		`{ "stages": { "summary": { "executor": "api", "provider": "anthropic", "model": "m", "max_duration": "6h", "max_input_tokens": 200000 } } }`,
		`{ "stages": { "segment": { "batch": "off" }, "correct": { "batch": "auto" }, "translate": { "batch": "off" } } }`,
		`{ "stages": { "chapters": { "executor": "off", "batch": "auto" }, "note": { "executor": "off", "batch": "off" } } }`,
		`{ "language": { "better_source": "notify" } }`,
		`{ "language": { "better_source": "rerun" } }`,
		`{ "language": { "better_source": "off" } }`,
	}
	for _, doc := range valid {
		for _, kind := range []LayerKind{KindProfile, KindOverride} {
			if kind == KindProfile && strings.Contains(doc, `"correct": { "batch"`) {
				continue // correct 預設 api，處理方案要有 provider（PRF-R5），和 batch 無關
			}
			if errs := ValidateLayer(t.Context(), jsonDoc(t, doc), kind, m3Deps); errs != nil {
				t.Errorf("kind=%d %s：%+v", kind, doc, errs)
			}
		}
	}
	invalid := []struct{ doc, path, msg string }{
		{`{ "stages": { "summary": { "executor": "rules" } } }`, "stages.summary.executor", ""},
		{`{ "stages": { "summary": { "executor": "opencc" } } }`, "stages.summary.executor", ""},
		{`{ "stages": { "summary": { "executor": "off", "pause_after": true } } }`, "stages.summary.pause_after", msgSummaryPauseOff},
		{`{ "stages": { "summary": { "pause_after": "yes" } } }`, "stages.summary.pause_after", msgNotBool},
		{`{ "stages": { "summary": { "language": "not a tag" } } }`, "stages.summary.language", ""},
		{`{ "stages": { "summary": { "max_duration": "9m" } } }`, "stages.summary.max_duration", "必須是 10m 到 6h 之間的期間。"},
		{`{ "stages": { "summary": { "max_duration": "7h" } } }`, "stages.summary.max_duration", "必須是 10m 到 6h 之間的期間。"},
		{`{ "stages": { "summary": { "max_duration": "soon" } } }`, "stages.summary.max_duration", msgNotDuration},
		{`{ "stages": { "summary": { "max_input_tokens": 999 } } }`, "stages.summary.max_input_tokens", ""},
		{`{ "stages": { "summary": { "max_input_tokens": 200001 } } }`, "stages.summary.max_input_tokens", ""},
		{`{ "stages": { "summary": { "chunk": 1 } } }`, "stages.summary.chunk", msgUnknownField},
		{`{ "stages": { "correct": { "batch": "sometimes" } } }`, "stages.correct.batch", msgBatchValue},
		{`{ "stages": { "correct": { "batch": true } } }`, "stages.correct.batch", msgBatchValue},
		{`{ "stages": { "frames": { "batch": "auto" } } }`, "stages.frames.batch", msgBatchUnsupported},
		{`{ "stages": { "sponsorblock": { "batch": "auto" } } }`, "stages.sponsorblock.batch", msgBatchUnsupported},
		{`{ "stages": { "transcript": { "batch": "auto" } } }`, "stages.transcript.batch", msgBatchUnsupported},
		{`{ "stages": { "transcript": { "batch": "auto", "x": 1 } } }`, "stages.transcript", msgNoStageFields},
		{`{ "stages": { "discover": { "batch": "auto" } } }`, "stages.discover", msgNoStageFields},
		{`{ "language": { "better_source": "sometimes" } }`, "language.better_source", ""},
	}
	for _, c := range invalid {
		errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), KindOverride, m3Deps)
		if !hasFieldError(errs, c.path, c.msg) {
			t.Errorf("%s：預期 %s「%s」，實際 %+v", c.doc, c.path, c.msg, errs)
		}
	}
	// 只有 batch 的 transcript：唯一的錯誤是 batch（BATCH 的 Scenario Outline 依賴 errors[0]）。
	errs := ValidateLayer(t.Context(), jsonDoc(t, `{ "stages": { "transcript": { "batch": "auto" } } }`), KindOverride, m3Deps)
	if len(errs) != 1 {
		t.Errorf("transcript.batch 應該只有一個錯誤：%+v", errs)
	}
	// sponsorblock 的其他欄位照常檢查。
	errs = ValidateLayer(t.Context(), jsonDoc(t, `{ "stages": { "sponsorblock": { "batch": "auto", "enabled": "x" } } }`), KindOverride, m3Deps)
	if !hasFieldError(errs, "stages.sponsorblock.enabled", msgNotBool) || !hasFieldError(errs, "stages.sponsorblock.batch", msgBatchUnsupported) {
		t.Errorf("sponsorblock：%+v", errs)
	}
}

// DIG-R1：處理方案沒有寫 executor 時是預設的 off，pause_after: true 一樣拒絕；覆寫不知道下層，不拒絕。
func TestValidateLayer_SummaryPauseNeedsSummary(t *testing.T) {
	doc := `{ "stages": { "summary": { "pause_after": true } } }`
	if errs := ValidateLayer(t.Context(), jsonDoc(t, doc), KindProfile, m3Deps); !hasFieldError(errs, "stages.summary.pause_after", msgSummaryPauseOff) {
		t.Errorf("處理方案：%+v", errs)
	}
	if errs := ValidateLayer(t.Context(), jsonDoc(t, doc), KindOverride, m3Deps); errs != nil {
		t.Errorf("覆寫：%+v", errs)
	}
	// executor: api 的處理方案要有 provider 與 model（PRF-R5）。
	api := `{ "stages": { "summary": { "executor": "api", "pause_after": true } } }`
	if errs := ValidateLayer(t.Context(), jsonDoc(t, api), KindProfile, m3Deps); !hasFieldError(errs, "stages.summary.provider", "") || hasFieldError(errs, "stages.summary.pause_after", "") {
		t.Errorf("api：%+v", errs)
	}
}

// Compose 接受 M4 的欄位（Effective.validateStages、Language.Validate），而且不改變快照中沒寫的部分。
func TestCompose_M4Fields(t *testing.T) {
	eff, err := BuildEffective(map[string]any{
		"language": map[string]any{"better_source": "rerun"},
		"stages": map[string]any{
			StageSummary: map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "pause_after": true, "batch": "auto"},
			StageCorrect: map[string]any{"batch": "auto"},
		},
	})
	if err != nil {
		t.Fatalf("Compose 應接受 M4 的欄位：%v", err)
	}
	if lang, err := eff.Language(); err != nil || lang.BetterSource != BetterSourceRerun {
		t.Errorf("better_source = %+v, %v", lang, err)
	}
	if _, err := BuildEffective(map[string]any{"language": map[string]any{"better_source": "x"}}); err == nil {
		t.Error("不合法的 better_source 應被拒絕")
	}
	base, _ := BuildEffective(nil)
	if lang, _ := base.Language(); lang.BetterSource != "" {
		t.Errorf("預設不寫入 better_source：%q", lang.BetterSource)
	}
}

var (
	reGlobalStep   = regexp.MustCompile(`the global settings are:$`)
	reSummaryStep  = regexp.MustCompile(`the summary profile is:$`)
	reChannelOver  = regexp.MustCompile(`the channel of ".*" has the overrides:$`)
	reOutlineStart = regexp.MustCompile(`^\s*Scenario Outline:`)
)

// expandOutlines 把 Scenario Outline 依 Examples 的每一列展開成一般的步驟（佔位符換成那一列的值），
// 其他行原樣保留；這樣 M4 的 422 範例（例如 BATCH-R1）也能逐列跑過 ValidateLayer。
func expandOutlines(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); i++ {
		if !reOutlineStart.MatchString(lines[i]) {
			out = append(out, lines[i])
			continue
		}
		// 收集 outline 的本體（到 Examples 為止）與表格。
		j := i + 1
		var body []string
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != "Examples:"; j++ {
			body = append(body, lines[j])
		}
		var header []string
		k := j + 1
		for ; k < len(lines); k++ {
			row := strings.TrimSpace(lines[k])
			if !strings.HasPrefix(row, "|") {
				break
			}
			cells := strings.Split(strings.Trim(row, "|"), "|")
			for n := range cells {
				cells[n] = strings.TrimSpace(cells[n])
			}
			if header == nil {
				header = cells
				continue
			}
			for _, l := range body {
				for n, h := range header {
					if n < len(cells) {
						l = strings.ReplaceAll(l, "<"+h+">", cells[n])
					}
				}
				out = append(out, l)
			}
			out = append(out, "")
		}
		i = k - 1
	}
	return out
}

// steps.md 4.17 的閘門讓 @M4 場景在 S0 之後仍是 pending，場景本身驗證不到「schema 接受全部 M4 欄位」。
// 這個測試直接把 14 個 M4 .feature 中的處理方案、覆寫、全域設定與預期 422 的請求跑過 ValidateLayer
// （Scenario Outline 逐列展開；不檢查 prompt 是否存在）。
func TestValidateLayer_M4FeatureDocs(t *testing.T) {
	files, err := filepath.Glob("../../features/*.feature")
	if err != nil {
		t.Fatal(err)
	}
	var m4 []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "@M4") {
			m4 = append(m4, f)
		}
	}
	if len(m4) != 14 { // LIB-library.feature 的 LIB-R24 場景（2026-11-25）是第 14 個
		t.Fatalf("找到 %d 個 M4 feature 檔：%v", len(m4), m4)
	}
	checked, rejected := 0, 0
	for _, f := range m4 {
		raw, _ := os.ReadFile(f)
		docs := parseFeatureLines(t, f, expandOutlines(strings.Split(string(raw), "\n")))
		for _, d := range docs {
			var doc map[string]any
			kind := KindOverride
			switch {
			case reProfileStep.MatchString(d.step), reSummaryStep.MatchString(d.step):
				doc, kind = yamlDoc(t, d.body), KindProfile
			case reChannelOver.MatchString(d.step):
				doc = yamlDoc(t, d.body)
			case reGlobalStep.MatchString(d.step):
				doc, kind = yamlDoc(t, d.body), KindGlobal
			case rePostProfile.MatchString(d.step):
				var req struct {
					Config map[string]any `json:"config"`
				}
				if err := json.Unmarshal([]byte(d.body), &req); err != nil {
					t.Fatalf("%s：%s 的 body 不是 JSON：%v", d.file, d.step, err)
				}
				doc, kind = req.Config, KindProfile
			case reItemsPost.MatchString(d.step):
				var req struct {
					Overrides map[string]any `json:"overrides"`
				}
				if err := json.Unmarshal([]byte(d.body), &req); err != nil || req.Overrides == nil {
					continue
				}
				doc = req.Overrides
			default:
				continue
			}
			if rePlaceholder.MatchString(d.body) {
				t.Errorf("%s：佔位符沒有展開：%s", d.file, d.body)
				continue
			}
			checked++
			errs := ValidateLayer(t.Context(), doc, kind, m3Deps)
			switch {
			case d.status422 && d.wantPath != "":
				rejected++
				if !hasFieldError(errs, d.wantPath, d.wantMsg) {
					t.Errorf("%s：%s\n預期 %s「%s」，實際 %+v", d.file, d.body, d.wantPath, d.wantMsg, errs)
				} else if errs[0].Path != d.wantPath {
					t.Errorf("%s：%s\nerrors[0] 應是 %s，實際 %+v", d.file, d.body, d.wantPath, errs)
				}
			case !d.status422 && errs != nil:
				t.Errorf("%s：%s\n%s\n應該通過驗證：%+v", d.file, d.step, d.body, errs)
			}
		}
	}
	if checked < 30 || rejected < 4 {
		t.Errorf("只檢查了 %d 個 doc string（%d 個預期 422），解析可能有誤", checked, rejected)
	}
	t.Logf("檢查了 %d 個 M4 的處理方案、覆寫與全域設定（%d 個預期 422）", checked, rejected)
}
