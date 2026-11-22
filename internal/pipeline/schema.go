package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/validate"
)

// LayerKind 是被驗證的設定層的種類，決定要不要檢查完整性（決策 D8）。
type LayerKind int

const (
	// KindGlobal 是全域區段，以 {section: body} 傳入。
	KindGlobal LayerKind = iota
	// KindProfile 是處理方案的完整文件：檢查 PRF-R5 的完整性。
	KindProfile
	// KindOverride 是頻道、新增、重跑的覆寫：不檢查完整性。
	KindOverride
)

// ValidateDeps 是驗證時查詢外部資源用的依賴；nil 的欄位代表略過對應的檢查。
type ValidateDeps struct {
	// Providers 為 nil 時不檢查 provider 是否存在。
	Providers ProviderCatalog
	// Prompts 為 nil 時不檢查 prompt 是否存在。
	Prompts PromptResolver
}

// 驗證訊息（固定的句子，測試與 API 回應都依賴它們）。
const (
	msgNull          = "不能是 null。"
	msgUnknownField  = "不認識的欄位。"
	msgNoStageFields = "這個步驟沒有可設定的欄位。"
	msgNotObject     = "必須是物件。"
	msgNotString     = "必須是字串。"
	msgEmptyString   = "不可以是空字串。"
	msgNotStringList = "必須是字串陣列。"
	msgNotBool       = "必須是 true 或 false。"
	msgNotPosInt     = "必須是大於 0 的整數。"
	msgNotNonNegInt  = "必須是 0 以上的整數。"
	msgNotPosNumber  = "必須是大於 0 的數字。"
	msgNotDuration   = "必須是有效期間字串，例如 24h、10m、2d。"
	msgRequired      = "必填。"
)

// stageOrder 是 stages 底下已知步驟的檢查順序。
var stageOrder = []string{StageSegment, StageCorrect, StageTranslate}

// stageCommonFields 是每個 LLM 步驟都有的欄位，依檢查順序；步驟專屬欄位接在後面（stageFields）。
var stageCommonFields = []string{"executor", "provider", "model", "prompt"}

// stageFields 登記步驟專屬的欄位；之後的切片在同一張表中加上自己的欄位。
var stageFields = map[string][]fieldSpec{
	StageCorrect: {
		{"min_similarity", checkNumberRange(0, 1)},
	},
	StageTranslate: {
		{"length_ratio", checkLengthRatio},
	},
}

// ValidateLayer 依固定順序檢查 doc，回傳全部錯誤（沒有錯誤時回傳 nil）。
// doc 可以是 JSON 或 YAML 解出來的 map，開頭先經過一次 JSON 轉換，數字一律以 float64 處理。
//
// 順序：先依 schema 的欄位順序，未知欄位排在同一層的已知欄位之後並依字母排序；
// 所以 errors[0] 是穩定的（PRF-R4 的範例 1 是 stages.correct.provider）。
// 對「輸入的層」做嚴格檢查；KindProfile 另外檢查 PRF-R5 的完整性，而且只看文件中寫出來的 stage。
func ValidateLayer(ctx context.Context, doc map[string]any, kind LayerKind, deps ValidateDeps) []validate.FieldError {
	norm, err := normalizeDoc(doc)
	if err != nil {
		return []validate.FieldError{{Message: "設定不是合法的 JSON 物件：" + err.Error()}}
	}
	v := &layerValidator{ctx: ctx, kind: kind, deps: deps}
	v.walk("", norm, topLevelFields, msgUnknownField)
	return v.errs
}

// normalizeDoc 經一次 JSON 轉換：YAML 解出來的型別、整數型別都統一成 JSON 的形狀（數字是 float64）。
func normalizeDoc(doc map[string]any) (map[string]any, error) {
	if doc == nil {
		return nil, nil
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type layerValidator struct {
	ctx  context.Context
	kind LayerKind
	deps ValidateDeps
	errs []validate.FieldError
}

// fieldSpec 是一個已知欄位：名稱與檢查函式。檢查函式要自己處理 null。
type fieldSpec struct {
	name  string
	check func(v *layerValidator, path string, val any)
}

// topLevelFields 是頂層已知的區段，依檢查順序。
var topLevelFields = []fieldSpec{
	{"stages", (*layerValidator).checkStages},
	{"language", checkObject(languageFields, msgUnknownField)},
	{"timing", checkObject(timingFields, msgUnknownField)},
	{"llm", checkObject(llmFields, msgUnknownField)},
	{"agent", checkObject(agentFields, msgUnknownField)},
	{"subtitle_rules", (*layerValidator).checkSubtitleRules},
	{"outputs", (*layerValidator).checkOutputs},
	{"glossary", checkStringList(0, nil)},
	{"supadata", checkObject([]fieldSpec{{"mode", checkEnum(supadata.ModeNative, supadata.ModeGenerate, supadata.ModeAuto)}}, msgUnknownField)},
}

var languageFields = []fieldSpec{
	{"targets", checkStringList(1, nil)},
	{"caption_priority", (*layerValidator).checkCaptionPriority},
	{"allow_platform_translated", checkBool},
	{"fallback", checkStringList(0, nil)},
	{"variant_strategy", checkEnum("convert", "localize")},
}

var timingFields = []fieldSpec{
	{"split_timing", checkEnum("forbid", "when_needed", "free")},
	{"split_timing_audio", checkEnum("auto", "never")},
}

// LLM 設定的上限：重試次數決定一個 chunk 任務最多送出幾次請求（要在租約內完成，ADR-0019），
// 前文與重疊句會放進每個請求，max_tokens 是一個 chunk 的輸入估計。
const (
	maxLLMChunkTokens       = 200000
	maxLLMOverlapCues       = 100
	maxLLMContextCues       = 100
	maxLLMValidationRetries = 10
)

var llmFields = []fieldSpec{
	{"chunk", checkObject([]fieldSpec{
		{"max_tokens", checkIntRange(1, maxLLMChunkTokens)},
		{"overlap_cues", checkIntRange(0, maxLLMOverlapCues)},
		{"context_cues", checkIntRange(0, maxLLMContextCues)},
	}, msgUnknownField)},
	{"max_validation_retries", checkIntRange(0, maxLLMValidationRetries)},
}

var agentFields = []fieldSpec{
	{"max_claims", checkInt(1, msgNotPosInt)},
	{"lease", checkDuration},
	{"timeout", checkDuration},
	{"on_timeout", checkEnum("fallback_api", "notify")},
	{"on_validation_failure", checkEnum("requeue", "fallback_api")},
}

// 欄位名稱由 S1 暫定，SEG（S5）在發布前可以改。
var subtitleRuleFields = []fieldSpec{
	{"max_chars_per_line", checkInt(1, msgNotPosInt)},
	{"max_lines", checkInt(1, msgNotPosInt)},
	{"min_duration", checkDuration},
	{"max_duration", checkDuration},
	{"max_cps", checkPositiveNumber},
}

var outputFields = []fieldSpec{
	{"target", (*layerValidator).checkOutputTarget},
	{"formats", checkStringList(1, output.Formats)},
	{"lang", (*layerValidator).checkOutputLang},
	{"style", checkString},
	{"path", checkString},
	{"on_user_change", checkEnum(output.PolicySaveAsNew, output.PolicySkip)},
}

// checkOutputTarget 檢查 outputs[].target：sidecar 或 folder；vault 屬於 M3，給清楚的訊息而不是列舉錯誤。
func (v *layerValidator) checkOutputTarget(path string, val any) {
	if t, _ := val.(string); t == output.TargetVault {
		v.add(path, "vault 輸出目標在 M3 才提供，目前只支援 sidecar、folder。")
		return
	}
	checkEnum(output.TargetSidecar, output.TargetFolder)(v, path, val)
}

// checkOutputLang 檢查 outputs[].lang：至少一個 BCP-47 語言標記（只看形狀）。
func (v *layerValidator) checkOutputLang(path string, val any) {
	checkStringList(1, nil)(v, path, val)
	list, _ := val.([]any)
	for i, e := range list {
		if s, ok := e.(string); ok && s != "" && !output.ValidLang(s) {
			v.add(joinPath(path, strconv.Itoa(i)), "不是正確的語言標記（BCP-47，例如 en、zh-Hant-TW）。")
		}
	}
}

func (v *layerValidator) add(path, msg string) {
	v.errs = append(v.errs, validate.FieldError{Path: path, Message: msg})
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// walk 依 specs 的順序檢查 m 中存在的已知欄位，再依字母順序回報未知欄位。
func (v *layerValidator) walk(path string, m map[string]any, specs []fieldSpec, unknownMsg string) {
	for _, s := range specs {
		if val, ok := m[s.name]; ok {
			s.check(v, joinPath(path, s.name), val)
		}
	}
	v.unknown(path, m, func(k string) bool {
		return slices.ContainsFunc(specs, func(s fieldSpec) bool { return s.name == k })
	}, unknownMsg)
}

func (v *layerValidator) unknown(path string, m map[string]any, known func(string) bool, msg string) {
	var extra []string
	for k := range m {
		if !known(k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		v.add(joinPath(path, k), msg)
	}
}

// asObject 確認 val 是物件；null 與其他型別各回報一個錯誤。
func (v *layerValidator) asObject(path string, val any) (map[string]any, bool) {
	switch t := val.(type) {
	case nil:
		v.add(path, msgNull)
	case map[string]any:
		return t, true
	default:
		v.add(path, msgNotObject)
	}
	return nil, false
}

func checkObject(specs []fieldSpec, unknownMsg string) func(*layerValidator, string, any) {
	return func(v *layerValidator, path string, val any) {
		if m, ok := v.asObject(path, val); ok {
			v.walk(path, m, specs, unknownMsg)
		}
	}
}

// asString 確認 val 是非空字串。
func (v *layerValidator) asString(path string, val any) (string, bool) {
	switch t := val.(type) {
	case nil:
		v.add(path, msgNull)
	case string:
		if t == "" {
			v.add(path, msgEmptyString)
			return "", false
		}
		return t, true
	default:
		v.add(path, msgNotString)
	}
	return "", false
}

func checkString(v *layerValidator, path string, val any) { v.asString(path, val) }

func checkBool(v *layerValidator, path string, val any) {
	switch val.(type) {
	case nil:
		v.add(path, msgNull)
	case bool:
	default:
		v.add(path, msgNotBool)
	}
}

func enumMessage(allowed []string) string {
	return "必須是 " + strings.Join(allowed, "、") + " 其中之一。"
}

func checkEnum(allowed ...string) func(*layerValidator, string, any) {
	return func(v *layerValidator, path string, val any) {
		switch t := val.(type) {
		case nil:
			v.add(path, msgNull)
		case string:
			if !slices.Contains(allowed, t) {
				v.add(path, enumMessage(allowed))
			}
		default:
			v.add(path, enumMessage(allowed))
		}
	}
}

// checkStringList 檢查字串陣列：至少 minLen 個、每個都不是空字串；enum 不為 nil 時每個元素必須在其中。
func checkStringList(minLen int, enum []string) func(*layerValidator, string, any) {
	return func(v *layerValidator, path string, val any) {
		switch val.(type) {
		case nil:
			v.add(path, msgNull)
			return
		case []any:
		default:
			v.add(path, msgNotStringList)
			return
		}
		list := val.([]any)
		if len(list) < minLen {
			v.add(path, "至少要有 "+strconv.Itoa(minLen)+" 個項目。")
			return
		}
		for i, e := range list {
			p := joinPath(path, strconv.Itoa(i))
			s, ok := v.asString(p, e)
			if ok && enum != nil && !slices.Contains(enum, s) {
				v.add(p, enumMessage(enum))
			}
		}
	}
}

// isInteger 判斷 JSON 數字是不是整數。
func isInteger(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) }

// checkInt 檢查整數，且不小於 min。
func checkInt(min float64, msg string) func(*layerValidator, string, any) {
	return func(v *layerValidator, path string, val any) {
		switch t := val.(type) {
		case nil:
			v.add(path, msgNull)
		case float64:
			if !isInteger(t) || t < min {
				v.add(path, msg)
			}
		default:
			v.add(path, msg)
		}
	}
}

// checkIntRange 檢查值是 [lo, hi] 之間的整數。
func checkIntRange(lo, hi float64) func(*layerValidator, string, any) {
	msg := fmt.Sprintf("必須是 %g 到 %g 的整數。", lo, hi)
	return func(v *layerValidator, path string, val any) {
		switch t := val.(type) {
		case nil:
			v.add(path, msgNull)
		case float64:
			if !isInteger(t) || t < lo || t > hi {
				v.add(path, msg)
			}
		default:
			v.add(path, msg)
		}
	}
}

func checkPositiveNumber(v *layerValidator, path string, val any) {
	switch t := val.(type) {
	case nil:
		v.add(path, msgNull)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t <= 0 {
			v.add(path, msgNotPosNumber)
		}
	default:
		v.add(path, msgNotPosNumber)
	}
}

func checkNumberRange(lo, hi float64) func(*layerValidator, string, any) {
	msg := fmt.Sprintf("必須是 %g 到 %g 之間的數字。", lo, hi)
	return func(v *layerValidator, path string, val any) {
		switch t := val.(type) {
		case nil:
			v.add(path, msgNull)
		case float64:
			if math.IsNaN(t) || t < lo || t > hi {
				v.add(path, msg)
			}
		default:
			v.add(path, msg)
		}
	}
}

// checkDuration 檢查 duration 字串（24h、10m、2d；與 token 有效期間同一套規則）。
func checkDuration(v *layerValidator, path string, val any) {
	switch t := val.(type) {
	case nil:
		v.add(path, msgNull)
	case string:
		if _, err := auth.ParseTokenDuration(t); err != nil {
			v.add(path, msgNotDuration)
		}
	default:
		v.add(path, msgNotDuration)
	}
}

func checkLengthRatio(v *layerValidator, path string, val any) {
	checkObject([]fieldSpec{
		{"min", checkPositiveNumber},
		{"max", checkPositiveNumber},
	}, msgUnknownField)(v, path, val)
	// 同一層同時給 min 與 max 時，範圍不能是反的（min > max 會讓每一筆翻譯都驗證失敗）。
	// 只檢查同一個物件：分層各給一端的組合由使用它的 TRN 切片在有效設定上檢查。
	m, ok := val.(map[string]any)
	if !ok {
		return
	}
	lo, loOK := m["min"].(float64)
	hi, hiOK := m["max"].(float64)
	if loOK && hiOK && lo > 0 && hi > 0 && lo > hi {
		v.add(joinPath(path, "min"), "不可以大於 max。")
	}
}

func (v *layerValidator) checkCaptionPriority(path string, val any) {
	switch val.(type) {
	case nil:
		v.add(path, msgNull)
		return
	case []any:
	default:
		v.add(path, "必須是物件陣列。")
		return
	}
	for i, e := range val.([]any) {
		p := joinPath(path, strconv.Itoa(i))
		m, ok := v.asObject(p, e)
		if !ok {
			continue
		}
		// 陣列整個取代，所以每一項都必須完整。
		for _, name := range []string{"lang", "kind"} {
			if _, has := m[name]; !has {
				v.add(joinPath(p, name), msgRequired)
			}
		}
		v.walk(p, m, []fieldSpec{
			{"lang", checkString},
			{"kind", checkEnum("manual", "auto")},
		}, msgUnknownField)
	}
}

func (v *layerValidator) checkSubtitleRules(path string, val any) {
	m, ok := v.asObject(path, val)
	if !ok {
		return
	}
	specs := []fieldSpec{
		{"cjk", checkObject(subtitleRuleFields, msgUnknownField)},
		{"latin", checkObject(subtitleRuleFields, msgUnknownField)},
	}
	v.walk(path, m, specs, msgUnknownField)
}

func (v *layerValidator) checkOutputs(path string, val any) {
	switch val.(type) {
	case nil:
		v.add(path, msgNull)
		return
	case []any:
	default:
		v.add(path, "必須是物件陣列。")
		return
	}
	for i, e := range val.([]any) {
		p := joinPath(path, strconv.Itoa(i))
		m, ok := v.asObject(p, e)
		if !ok {
			continue
		}
		if _, has := m["target"]; !has {
			v.add(joinPath(p, "target"), msgRequired)
		}
		v.walk(p, m, outputFields, msgUnknownField)
		v.checkOutputPath(p, m)
	}
}

// checkOutputPath 檢查 path 與 target 的關係：folder 必須有 path，而且是絕對路徑；sidecar 寫在影片旁邊，不可以有 path。
// target 本身有錯時略過（已經回報過）。
func (v *layerValidator) checkOutputPath(p string, m map[string]any) {
	target, _ := m["target"].(string)
	path, hasPath := m["path"]
	switch target {
	case output.TargetFolder:
		if !hasPath {
			v.add(joinPath(p, "path"), "folder 目標必須指定 path。")
		} else if s, ok := path.(string); ok && s != "" && !filepath.IsAbs(s) {
			v.add(joinPath(p, "path"), "必須是絕對路徑。")
		}
	case output.TargetSidecar:
		if hasPath {
			v.add(joinPath(p, "path"), "sidecar 目標寫在影片旁邊，不可以指定 path。")
		}
	}
}

// checkStages 檢查 stages：只能有 segment、correct、translate。
func (v *layerValidator) checkStages(path string, val any) {
	m, ok := v.asObject(path, val)
	if !ok {
		return
	}
	for _, name := range stageOrder {
		if sv, has := m[name]; has {
			v.checkStage(joinPath(path, name), name, sv)
		}
	}
	v.unknown(path, m, func(k string) bool { return slices.Contains(stageOrder, k) }, msgNoStageFields)
}

// checkStage 檢查 stages.<stage>：executor、provider、model、prompt，接著是步驟專屬欄位。
// KindProfile 時，生效的 executor（沒寫就是 api）為 api 而缺 provider 或 model，在該欄位的位置回報（PRF-R5）。
func (v *layerValidator) checkStage(path, stage string, val any) {
	m, ok := v.asObject(path, val)
	if !ok {
		return
	}
	// executor 有問題時，這個 stage 的完整性檢查略過。
	needComplete := v.kind == KindProfile
	if raw, has := m["executor"]; has {
		exec, ok := v.checkExecutor(joinPath(path, "executor"), stage, raw)
		needComplete = needComplete && ok && exec == ExecutorAPI
	}

	if raw, has := m["provider"]; has {
		if id, ok := v.asString(joinPath(path, "provider"), raw); ok && v.deps.Providers != nil && !v.deps.Providers.HasLLMProvider(id) {
			v.add(joinPath(path, "provider"), fmt.Sprintf("找不到 LLM provider「%s」。", id))
		}
	} else if needComplete {
		v.add(joinPath(path, "provider"), "executor 為 api 時必須指定 provider。")
	}

	if raw, has := m["model"]; has {
		v.asString(joinPath(path, "model"), raw)
	} else if needComplete {
		v.add(joinPath(path, "model"), "executor 為 api 時必須指定 model。")
	}

	if raw, has := m["prompt"]; has {
		if ref, ok := v.asString(joinPath(path, "prompt"), raw); ok {
			v.checkPromptRef(joinPath(path, "prompt"), stage, ref)
		}
	}

	extra := stageFields[stage]
	for _, s := range extra {
		if raw, has := m[s.name]; has {
			s.check(v, joinPath(path, s.name), raw)
		}
	}
	v.unknown(path, m, func(k string) bool {
		return slices.Contains(stageCommonFields, k) || slices.ContainsFunc(extra, func(s fieldSpec) bool { return s.name == k })
	}, msgUnknownField)
}

// checkExecutor 檢查 executor 是該步驟支援的；回傳值與是否有效。
func (v *layerValidator) checkExecutor(path, stage string, val any) (string, bool) {
	exec, ok := v.asString(path, val)
	if !ok {
		return "", false
	}
	if exec == "batch" {
		v.add(path, "batch 屬於 M4，尚未支援。")
		return exec, false
	}
	if allowed := StageExecutors(stage); !slices.Contains(allowed, exec) {
		v.add(path, fmt.Sprintf("%s 不支援 %s；可用 %s。", stage, exec, strings.Join(allowed, "、")))
		return exec, false
	}
	return exec, true
}

// checkPromptRef 透過 PromptResolver 確認 prompt 存在、版本存在，而且類型與步驟相符。
func (v *layerValidator) checkPromptRef(path, stage, ref string) {
	if v.deps.Prompts == nil {
		return
	}
	rp, err := v.deps.Prompts.ResolvePrompt(v.ctx, ref)
	var invalid *InvalidError
	switch {
	case err == nil:
		if rp.Kind != stage {
			v.add(path, fmt.Sprintf("prompt「%s」的類型是 %s，不能用於 %s。", ref, rp.Kind, stage))
		}
	case errors.Is(err, ErrPromptNotFound):
		v.add(path, fmt.Sprintf("找不到 prompt「%s」。", ref))
	case errors.Is(err, ErrPromptVersionNotFound):
		if _, ver, ok := strings.Cut(ref, "@"); ok {
			v.add(path, fmt.Sprintf("prompt「%s」沒有第 %s 版。", ref[:len(ref)-len(ver)-1], ver))
		} else {
			v.add(path, fmt.Sprintf("prompt「%s」沒有這個版本。", ref))
		}
	case errors.As(err, &invalid):
		v.add(path, invalid.Msg)
	default:
		v.add(path, fmt.Sprintf("無法確認 prompt「%s」：%v", ref, err))
	}
}
