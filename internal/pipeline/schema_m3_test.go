package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/internal/validate"
)

// m3Catalog 是 M3 測試用的 provider、vault 與 hook 目錄：provider 另外有 type 為 gemini 的 gemini。
type m3Catalog struct{ vaults, hooks []string }

func (m3Catalog) HasLLMProvider(id string) bool {
	return id == "anthropic" || id == "openai" || id == "gemini"
}

func (m3Catalog) LLMProviderType(id string) (string, bool) {
	switch id {
	case "anthropic", "openai", "gemini":
		return id, true
	}
	return "", false
}

func (c m3Catalog) HasVault(id string) bool { return slices.Contains(c.vaults, id) }
func (c m3Catalog) HasHook(id string) bool  { return slices.Contains(c.hooks, id) }

var m3Deps = ValidateDeps{Providers: m3Catalog{}, Outputs: m3Catalog{vaults: []string{"main", "notes"}, hooks: []string{"notify"}}}

// 每個 M3 欄位：有效的值沒有錯誤，無效的值回報欄位路徑與訊息（PRF-R4）。
func TestValidateLayer_M3Fields(t *testing.T) {
	valid := []string{
		`{ "stages": { "sponsorblock": { "enabled": false } } }`,
		`{ "stages": { "sponsorblock": { "categories": ["sponsor", "intro", "music_offtopic", "filler"] } } }`,
		`{ "stages": { "sponsorblock": { "categories": [] } } }`,
		`{ "stages": { "chapters": { "executor": "off" } } }`,
		`{ "stages": { "chapters": { "executor": "agent", "max_chapters": 100 } } }`,
		`{ "stages": { "chapters": { "provider": "anthropic", "model": "m", "max_chapters": 1 } } }`,
		`{ "stages": { "frames": { "executor": "rules", "scene_threshold": 0.3, "min_interval_s": 60, "dedupe_window": 5, "dedupe_threshold": 0.05, "max_frames": 20, "per_minute": 1, "hint_words": ["如圖"] } } }`,
		`{ "stages": { "frames": { "scene_threshold": 0.5 } } }`, // 預設 off，處理方案不需要 provider
		`{ "stages": { "frames": { "executor": "api", "provider": "openai", "model": "m" } } }`,
		`{ "stages": { "note": { "executor": "off" } } }`,
		`{ "stages": { "note": { "provider": "anthropic", "model": "m", "template": "video-notes", "lang": "zh-Hant-TW", "include_translation": true, "map_reduce_after": "60m" } } }`,
		`{ "stages": { "note": { "engine": "gemini", "provider": "gemini", "model": "m", "gemini": { "max_duration": "1h" } } } }`,
		`{ "outputs": [ { "target": "vault", "vault": "main" } ] }`,
		`{ "outputs": [ { "target": "vault", "vault": "main", "formats": [], "lang": ["en"], "folder": "Channel/{channel}", "on_user_change": "skip", "hooks": ["notify"] } ] }`,
	}
	for _, doc := range valid {
		for _, kind := range []LayerKind{KindProfile, KindOverride} {
			if errs := ValidateLayer(t.Context(), jsonDoc(t, doc), kind, m3Deps); errs != nil {
				t.Errorf("kind=%d %s：%+v", kind, doc, errs)
			}
		}
	}

	invalid := []struct{ doc, path, msg string }{
		{`{ "stages": { "sponsorblock": { "enabled": "yes" } } }`, "stages.sponsorblock.enabled", msgNotBool},
		{`{ "stages": { "sponsorblock": { "categories": ["sponsor", "ads"] } } }`, "stages.sponsorblock.categories.1",
			"必須是 sponsor、selfpromo、interaction、intro、outro、preview、music_offtopic、filler 其中之一。"},
		{`{ "stages": { "sponsorblock": { "categories": "sponsor" } } }`, "stages.sponsorblock.categories", msgNotStringList},
		{`{ "stages": { "sponsorblock": { "executor": "api" } } }`, "stages.sponsorblock.executor", msgUnknownField},
		{`{ "stages": { "sponsorblock": { "base_url": "http://x" } } }`, "stages.sponsorblock.base_url", msgUnknownField},
		{`{ "stages": { "chapters": { "executor": "rules" } } }`, "stages.chapters.executor", "chapters 不支援 rules；可用 api、agent、off。"},
		{`{ "stages": { "chapters": { "executor": "off", "max_chapters": 0 } } }`, "stages.chapters.max_chapters", "必須是 1 到 100 的整數。"},
		{`{ "stages": { "chapters": { "executor": "off", "max_chapters": 101 } } }`, "stages.chapters.max_chapters", "必須是 1 到 100 的整數。"},
		{`{ "stages": { "chapters": { "executor": "off", "title_max": 80 } } }`, "stages.chapters.title_max", msgUnknownField},
		{`{ "stages": { "frames": { "executor": "agent" } } }`, "stages.frames.executor", "選圖還不支援交給 Agent。"},
		{`{ "stages": { "frames": { "executor": "opencc" } } }`, "stages.frames.executor", "frames 不支援 opencc；可用 rules、api、agent、off。"},
		{`{ "stages": { "frames": { "scene_threshold": 1.5 } } }`, "stages.frames.scene_threshold", "必須是 0 到 1 之間的數字。"},
		{`{ "stages": { "frames": { "min_interval_s": 0 } } }`, "stages.frames.min_interval_s", "必須是 1 到 3600 的整數。"},
		{`{ "stages": { "frames": { "dedupe_window": 2.5 } } }`, "stages.frames.dedupe_window", "必須是 1 到 100 的整數。"},
		{`{ "stages": { "frames": { "dedupe_threshold": -0.1 } } }`, "stages.frames.dedupe_threshold", "必須是 0 到 1 之間的數字。"},
		{`{ "stages": { "frames": { "max_frames": 0 } } }`, "stages.frames.max_frames", "必須是 1 到 100 的整數。"},
		{`{ "stages": { "frames": { "per_minute": 0 } } }`, "stages.frames.per_minute", "必須是大於 0、不超過 60 的數字。"},
		{`{ "stages": { "frames": { "hint_words": ["ok", ""] } } }`, "stages.frames.hint_words.1", msgEmptyString},
		{`{ "stages": { "note": { "executor": "off", "engine": "magic" } } }`, "stages.note.engine", "必須是 pipeline、gemini 其中之一。"},
		{`{ "stages": { "note": { "executor": "off", "lang": "english language" } } }`, "stages.note.lang", "不是正確的語言標記（BCP-47，例如 en、zh-Hant-TW）。"},
		{`{ "stages": { "note": { "executor": "off", "include_translation": "no" } } }`, "stages.note.include_translation", msgNotBool},
		{`{ "stages": { "note": { "executor": "off", "map_reduce_after": "soon" } } }`, "stages.note.map_reduce_after", msgNotDuration},
		{`{ "stages": { "note": { "executor": "off", "gemini": { "max_duration": 60 } } } }`, "stages.note.gemini.max_duration", msgNotDuration},
		{`{ "stages": { "note": { "executor": "off", "gemini": { "api_key": "x" } } } }`, "stages.note.gemini.api_key", msgUnknownField},
		{`{ "stages": { "note": { "executor": "off", "template": "" } } }`, "stages.note.template", msgEmptyString},
		{`{ "stages": { "note": { "executor": "api", "engine": "gemini", "provider": "anthropic", "model": "m" } } }`, "stages.note.provider",
			"engine: gemini 需要 type 為 gemini 的 provider。"},
		{`{ "outputs": [ { "target": "vault", "vault": "elsewhere" } ] }`, "outputs.0.vault", "沒有這個 vault：elsewhere。vault 只能在設定檔定義。"},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "path": "/etc" } ] }`, "outputs.0.path", msgUnknownField},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "remote": "https://x" } ] }`, "outputs.0.remote", msgUnknownField},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "folder": "../outside" } ] }`, "outputs.0.folder", "folder 不能是絕對路徑或包含 ..。"},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "folder": "/abs" } ] }`, "outputs.0.folder", "folder 不能是絕對路徑或包含 ..。"},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "folder": "a/../../b" } ] }`, "outputs.0.folder", "folder 不能是絕對路徑或包含 ..。"},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "hooks": ["missing"] } ] }`, "outputs.0.hooks.0", "沒有這個 hook：missing。hook 只能在設定檔定義。"},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "hooks": [ { "argv": ["sh"] } ] } ] }`, "outputs.0.hooks.0", msgNotString},
		{`{ "outputs": [ { "target": "vault", "vault": "main", "hooks": "notify" } ] }`, "outputs.0.hooks", msgNotStringList},
		{`{ "outputs": [ { "target": "sidecar", "folder": "x" } ] }`, "outputs.0.folder", msgUnknownField},
		{`{ "outputs": [ { "target": "folder", "path": "/x", "vault": "main" } ] }`, "outputs.0.vault", msgUnknownField},
	}
	for _, c := range invalid {
		errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), KindOverride, m3Deps)
		if !hasFieldError(errs, c.path, c.msg) {
			t.Errorf("%s：預期 %s「%s」，實際 %+v", c.doc, c.path, c.msg, errs)
		}
	}
}

// 處理方案的完整性（PRF-R5）也適用於 M3 的 LLM 步驟：chapters、note 預設 api，沒有 provider 時在欄位回報；
// frames 預設 off，只有明確寫 api 才要求。
func TestValidateLayer_M3ProfileCompleteness(t *testing.T) {
	cases := []struct{ doc, path string }{
		{`{ "stages": { "chapters": { "max_chapters": 5 } } }`, "stages.chapters.provider"},
		{`{ "stages": { "note": { "provider": "anthropic" } } }`, "stages.note.model"},
		{`{ "stages": { "frames": { "executor": "api", "model": "m" } } }`, "stages.frames.provider"},
	}
	for _, c := range cases {
		if errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), KindProfile, m3Deps); !slices.Contains(errPaths(errs), c.path) {
			t.Errorf("%s：預期 %s，實際 %+v", c.doc, c.path, errs)
		}
		if errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), KindOverride, m3Deps); errs != nil {
			t.Errorf("覆寫不檢查完整性 %s：%+v", c.doc, errs)
		}
	}
}

// Compose 接受 M3 的步驟（Effective.validateStages）；M3 的預設值不寫進快照，既有設定的快照不變。
func TestCompose_M3StagesAndSnapshotUnchanged(t *testing.T) {
	base, err := BuildEffective(nil)
	if err != nil {
		t.Fatal(err)
	}
	sha, cfg, err := base.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{StageSponsorBlock, StageChapters, StageFrames, StageNote} {
		if strings.Contains(cfg, `"`+s+`"`) {
			t.Errorf("預設快照不該含 %s：%s", s, cfg)
		}
	}
	over := map[string]any{"stages": map[string]any{
		StageSponsorBlock: map[string]any{"enabled": false},
		StageFrames:       map[string]any{"executor": "rules"},
		StageNote:         map[string]any{"executor": "off"},
	}}
	eff, err := BuildEffective(over)
	if err != nil {
		t.Fatalf("Compose 應接受 M3 的步驟：%v", err)
	}
	if sb, err := eff.SponsorBlock(); err != nil || sb.Enabled {
		t.Errorf("SponsorBlock = %+v, %v", sb, err)
	}
	if sb, _ := base.SponsorBlock(); !sb.Enabled || !slices.Equal(sb.Categories, DefaultSponsorBlockCategories) {
		t.Errorf("預設 SponsorBlock = %+v", sb)
	}
	if _, err := BuildEffective(map[string]any{"stages": map[string]any{StageFrames: map[string]any{"executor": "opencc"}}}); err == nil {
		t.Error("frames 的 executor 是 opencc 應被拒絕")
	}
	if sha2, _, _ := base.Snapshot(); sha2 != sha {
		t.Error("快照 sha 不穩定")
	}
}

func hasFieldError(errs []validate.FieldError, path, msg string) bool {
	return slices.ContainsFunc(errs, func(e validate.FieldError) bool { return e.Path == path && (msg == "" || e.Message == msg) })
}

// featureDoc 是 .feature 中一個步驟的 doc string，以及它之後的 422 斷言（沒有時為空）。
type featureDoc struct {
	file, step, body  string
	wantPath, wantMsg string
	status422         bool
}

var (
	reProfileStep = regexp.MustCompile(`the profile ".*" is:$`)
	rePostProfile = regexp.MustCompile(`"POST /api/profiles" with`)
	reItemsPost   = regexp.MustCompile(`"POST /api/items[^"]*" with`)
	reConfigStep  = regexp.MustCompile(`the config file contains:$`)
	rePlaceholder = regexp.MustCompile(`<[a-z_]+>`)
)

// parseFeatureDocs 取出 feature 檔中每個步驟的 doc string，以及緊接著的 `the response status is 422` 與
// `the response contains:` 的第一個 errors 項目。
func parseFeatureDocs(t *testing.T, path string) []featureDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return parseFeatureLines(t, path, strings.Split(string(raw), "\n"))
}

// parseFeatureLines 同 parseFeatureDocs，輸入是已經切好的行（M4 的測試先展開 Scenario Outline）。
func parseFeatureLines(t *testing.T, path string, lines []string) []featureDoc {
	t.Helper()
	var docs []featureDoc
	readDoc := func(i int) (string, int) { // lines[i] 是開頭的 """；回傳內容與結尾 """ 的下一行
		indent := strings.Index(lines[i], `"""`)
		var b []string
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == `"""` {
				return strings.Join(b, "\n"), j + 1
			}
			l := lines[j]
			if len(l) >= indent {
				l = l[indent:]
			}
			b = append(b, l)
		}
		t.Fatalf("%s：doc string 沒有結尾", path)
		return "", 0
	}
	for i := 0; i < len(lines); i++ {
		if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != `"""` {
			continue
		}
		step := strings.TrimSpace(lines[i])
		body, next := readDoc(i + 1)
		d := featureDoc{file: filepath.Base(path), step: step, body: body}
		if next < len(lines) && strings.HasSuffix(strings.TrimSpace(lines[next]), "the response status is 422") {
			d.status422 = true
			if next+2 < len(lines) && strings.HasSuffix(strings.TrimSpace(lines[next+1]), "the response contains:") {
				resp, _ := readDoc(next + 2)
				var r struct {
					Errors []struct{ Path, Message string } `json:"errors"`
				}
				if json.Unmarshal([]byte(resp), &r) == nil && len(r.Errors) > 0 {
					d.wantPath, d.wantMsg = r.Errors[0].Path, r.Errors[0].Message
				}
			}
		}
		docs = append(docs, d)
		i = next - 1
	}
	return docs
}

// steps.md 4.13：閘門解除之前 @M3 場景一律是 pending，所以場景本身驗證不到「schema 接受全部 M3 欄位」。
// 這個測試直接把 M3 .feature 中的處理方案、覆寫與預期 422 的請求跑過 ValidateLayer（不檢查 prompt 是否存在）。
func TestValidateLayer_M3FeatureDocs(t *testing.T) {
	files, err := filepath.Glob("../../features/*.feature")
	if err != nil {
		t.Fatal(err)
	}
	var m3 []string
	for _, f := range files {
		for _, p := range []string{"SPB-", "CHAP-", "FRM-", "NOTE-", "GEM-", "VAULT-"} {
			if strings.HasPrefix(filepath.Base(f), p) {
				m3 = append(m3, f)
			}
		}
	}
	if len(m3) != 6 {
		t.Fatalf("找到 %d 個 M3 feature 檔：%v", len(m3), m3)
	}
	checked := 0
	for _, f := range m3 {
		docs := parseFeatureDocs(t, f)
		// 設定檔中定義的 vault 與 hook id（同一個 feature 檔內）。
		cat := m3Catalog{}
		for _, d := range docs {
			if !reConfigStep.MatchString(d.step) {
				continue
			}
			var cfg struct {
				Vaults map[string]any `yaml:"vaults"`
				Hooks  map[string]any `yaml:"hooks"`
			}
			// Scenario Outline 的佔位符換成普通的字，只取 id。
			body := rePlaceholder.ReplaceAllString(d.body, "x")
			if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
				t.Fatalf("%s：設定檔不是 YAML：%v\n%s", d.file, err, d.body)
			}
			for id := range cfg.Vaults {
				cat.vaults = append(cat.vaults, id)
			}
			for id := range cfg.Hooks {
				cat.hooks = append(cat.hooks, id)
			}
		}
		deps := ValidateDeps{Providers: m3Catalog{}, Outputs: cat}
		for _, d := range docs {
			if strings.Contains(d.body, "<") && strings.Contains(d.body, ">") {
				continue // Scenario Outline 的佔位符
			}
			var doc map[string]any
			kind := KindOverride
			switch {
			case reProfileStep.MatchString(d.step):
				doc, kind = yamlDoc(t, d.body), KindProfile
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
			checked++
			errs := ValidateLayer(t.Context(), doc, kind, deps)
			switch {
			case d.status422 && d.wantPath != "":
				if !hasFieldError(errs, d.wantPath, d.wantMsg) {
					t.Errorf("%s：%s\n預期 %s「%s」，實際 %+v", d.file, d.body, d.wantPath, d.wantMsg, errs)
				}
			case !d.status422 && errs != nil:
				t.Errorf("%s：%s\n%s\n應該通過驗證：%+v", d.file, d.step, d.body, errs)
			}
		}
	}
	if checked < 20 {
		t.Errorf("只檢查了 %d 個 doc string，解析可能有誤", checked)
	}
	t.Logf("檢查了 %d 個 M3 的處理方案與覆寫", checked)
}
