package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// Language 是 language 設定區段（plan 第 7 節）。
type Language struct {
	Targets                 []string          `json:"targets"`
	CaptionPriority         []transcript.Rule `json:"caption_priority"`
	AllowPlatformTranslated bool              `json:"allow_platform_translated"`
	Fallback                []string          `json:"fallback"`
	VariantStrategy         string            `json:"variant_strategy"`
	// BetterSource 是 language.better_source（SRC-R22，M4）：notify、rerun 或 off；空字串代表沒有設定（notify）。
	// omitempty：沒有設定時不寫進快照，既有處理方案的快照 sha 不變。transcript 的快取鍵只取用到的欄位，不含它。
	BetterSource string `json:"better_source,omitempty"`

	// Supadata 是 supadata 區段的生效值。它不屬於 language 區段（所以不序列化），
	// 由 Effective.Language 一併讀出，讓只拿到 Language 的步驟也能取得來源的參數。
	Supadata SupadataParams `json:"-"`

	// Glossary 是合併後的詞彙表（COR-R4）。它不屬於 language 區段（所以不序列化）：runTranscript 的呼叫端在執行時補上，
	// 供支援詞彙提示的 ASR 來源使用（COR-R7）。
	Glossary []string `json:"-"`
}

// SupadataParams 是 supadata 設定區段：影響結果的參數，所以也是快取鍵的一部分。
type SupadataParams struct {
	// Mode 是 native、generate 或 auto（預設）。
	Mode string `json:"mode"`
}

// DefaultLanguage 是內建的預設值（plan 第 7 節、SRC-R3）。
func DefaultLanguage() Language {
	return Language{
		Targets: []string{"zh-Hant-TW"},
		CaptionPriority: []transcript.Rule{
			{Lang: transcript.OriginalLang, Kind: transcript.KindManual},
			{Lang: transcript.OriginalLang, Kind: transcript.KindAuto},
			{Lang: "zh-Hans", Kind: transcript.KindManual},
		},
		AllowPlatformTranslated: false,
		Fallback:                []string{"supadata", "groq"},
		VariantStrategy:         "convert",
	}
}

// Validate 檢查設定值；錯誤是 *InvalidError。
func (l Language) Validate() error {
	if len(l.Targets) == 0 {
		return invalidf("language.targets 至少要有一個目標語言。")
	}
	for _, t := range l.Targets {
		if t == "" {
			return invalidf("language.targets 不可以有空的語言。")
		}
	}
	for i, r := range l.CaptionPriority {
		if r.Lang == "" {
			return invalidf("language.caption_priority 第 %d 項沒有 lang。", i+1)
		}
		if r.Kind != transcript.KindManual && r.Kind != transcript.KindAuto {
			return invalidf("language.caption_priority 第 %d 項的 kind 必須是 manual 或 auto。", i+1)
		}
	}
	for _, f := range l.Fallback {
		if f == "" {
			return invalidf("language.fallback 不可以有空的來源。")
		}
	}
	if !slices.Contains([]string{"", "convert", "localize"}, l.VariantStrategy) {
		return invalidf("language.variant_strategy 必須是 convert 或 localize。")
	}
	if !validBetterSource(l.BetterSource) {
		return invalidf("language.better_source 必須是 notify、rerun 或 off。")
	}
	return nil
}

// Effective 是合併後的生效設定：內建預設 → 全域設定 → 單次覆寫（plan 第 7 節）。
// 處理方案與頻道這兩層屬於 M2（PRF），之後插在全域與單次覆寫之間。
//
// 它保留所有區段的原始內容（包含這個切片還不認識的），快照才完整記錄「當時用了什麼設定」。
type Effective map[string]any

// BuildEffective 依序合併 layers（後面的優先）並驗證。layers 的順序是：全域設定、單次覆寫；
// 內建預設一律在最前面。
func BuildEffective(layers ...map[string]any) (Effective, error) {
	defaults, err := toMap(DefaultLanguage())
	if err != nil {
		return nil, err
	}
	eff := Effective{
		"language": defaults, "supadata": map[string]any{"mode": supadata.ModeAuto},
		"stages": defaultStages(), "outputs": []any{},
	}
	for _, l := range layers {
		eff = Effective(merge(eff, l))
	}
	lang, err := eff.Language()
	if err != nil {
		return nil, err
	}
	if err := lang.Validate(); err != nil {
		return nil, err
	}
	if err := eff.validateStages(); err != nil {
		return nil, err
	}
	if _, err := eff.Outputs(); err != nil {
		return nil, err
	}
	return eff, nil
}

// defaultStageConfig 是 LLM 步驟的內建預設（X1 選項 C）：executor 是 api、prompt 是該步驟的預設 prompt；
// 沒有 provider 與 model 時步驟會 skipped（not_configured）。確定性步驟沒有可設定的欄位。
//
// M3 的 chapters、note 同樣預設 api（ADR-0020 第 2 點），frames 預設 off（FRM-R1）；這些預設只在讀取時補上，
// 不寫進快照（defaultStages 只含 M2 的 LLM 步驟），既有處理方案的快照 sha 與快取鍵不變。
// sponsorblock 沒有 executor，見 Effective.SponsorBlock。
func defaultStageConfig(stage string) StageConfig {
	switch {
	case IsLLMStage(stage), stage == StageChapters, stage == StageNote:
		return StageConfig{Executor: ExecutorAPI, Prompt: stage + "-default"}
	case stage == StageFrames, stage == StageSummary:
		// summary 預設 off（DIG-R1）；同樣只在讀取時補上，不寫進快照。
		return StageConfig{Executor: ExecutorOff, Prompt: stage + "-default"}
	}
	return StageConfig{}
}

// defaultStages 是 stages 區段的內建預設，寫進快照讓它完整記錄當時用了什麼設定（PIPE-R13）。
func defaultStages() map[string]any {
	out := map[string]any{}
	for _, s := range Stages {
		if IsLLMStage(s) {
			c := defaultStageConfig(s)
			out[s] = map[string]any{"executor": c.Executor, "prompt": c.Prompt}
		}
	}
	return out
}

// Language 取出 language 區段。不認識的欄位視為錯誤，避免拼錯的設定被默默忽略。
func (e Effective) Language() (Language, error) {
	b, err := json.Marshal(e["language"])
	if err != nil {
		return Language{}, err
	}
	var l Language
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Language{}, invalidf("language 設定不正確：%v", err)
	}
	if l.Supadata, err = e.supadata(); err != nil {
		return Language{}, err
	}
	return l, nil
}

// supadata 取出 supadata 區段；沒有這個區段（較早的快照）時使用預設值。
func (e Effective) supadata() (SupadataParams, error) {
	p := SupadataParams{Mode: supadata.ModeAuto}
	raw, ok := e["supadata"]
	if !ok {
		return p, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, invalidf("supadata 設定不正確：%v", err)
	}
	if !supadata.ValidMode(p.Mode) {
		return p, invalidf("supadata.mode 必須是 %s、%s 或 %s。", supadata.ModeNative, supadata.ModeGenerate, supadata.ModeAuto)
	}
	return p, nil
}

// StageConfig 是 stages.<stage> 的通用欄位（PRF-R1、LLM 介面）。步驟專屬的欄位（例如 correct 的 min_similarity）
// 由各自的切片讀取；S0 不拒絕它們，嚴格的驗證與欄位路徑屬於 PRF 切片（PRF-R4）。
type StageConfig struct {
	Executor string `json:"executor,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	// PromptVersion、PromptSHA256 是 Compose 解析 prompt 引用之後寫進快照的結果（決策 D11）；
	// 解析不了時留空。它們只出現在快照中，輸入的設定層帶這兩個欄位是未知欄位（422）。
	PromptVersion int    `json:"prompt_version,omitempty"`
	PromptSHA256  string `json:"prompt_sha256,omitempty"`
}

// Stage 取出 stages.<stage>。快照沒有這個區段（M1 的舊快照）或沒有這個步驟時，回傳內建預設值；
// executor 與 prompt 是空的也用預設值補上。確定性步驟沒有可設定的欄位，回傳零值。
func (e Effective) Stage(stage string) (StageConfig, error) {
	cfg := defaultStageConfig(stage)
	all, err := e.stages()
	if err != nil || all == nil {
		return cfg, err
	}
	entry, ok := all[stage]
	if !ok {
		return cfg, nil
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return cfg, err
	}
	var got StageConfig
	// 不拒絕不認識的欄位：步驟專屬的欄位由各自的切片讀取。
	if err := json.Unmarshal(b, &got); err != nil {
		return cfg, invalidf("stages.%s 設定不正確：%v", stage, err)
	}
	if got.Executor != "" {
		cfg.Executor = got.Executor
	}
	if got.Prompt != "" {
		cfg.Prompt = got.Prompt
	}
	cfg.Provider, cfg.Model = got.Provider, got.Model
	cfg.PromptVersion, cfg.PromptSHA256 = got.PromptVersion, got.PromptSHA256
	return cfg, nil
}

// validateStages 檢查 stages 區段：只能設定 LLM 步驟，executor 必須是該步驟支援的，provider、model、prompt 必須是字串。
func (e Effective) validateStages() error {
	all, err := e.stages()
	if err != nil || all == nil {
		return err
	}
	for stage := range all {
		if stage == StageSponsorBlock {
			if _, err := e.SponsorBlock(); err != nil {
				return err
			}
			continue
		}
		if !IsLLMStage(stage) && !gatedStage(stage) {
			return invalidf("stages 的步驟必須是 %s 其中之一，收到 %q。", strings.Join(configurableStages, "、"), stage)
		}
		cfg, err := e.Stage(stage)
		if err != nil {
			return err
		}
		if !slices.Contains(StageExecutors(stage), cfg.Executor) {
			return invalidf("stages.%s.executor 必須是 %s 其中之一，收到 %q。", stage, strings.Join(StageExecutors(stage), "、"), cfg.Executor)
		}
	}
	return nil
}

// configurableStages 是 stages 底下可以設定的步驟（M4 的 summary、M2 的 LLM 步驟與 M3 的步驟，同 Stages 的順序）。
var configurableStages = []string{StageSummary, StageSegment, StageCorrect, StageTranslate, StageSponsorBlock, StageChapters, StageFrames, StageNote}

// SponsorBlockSettings 是 stages.sponsorblock（SPB-R1、R3）。
type SponsorBlockSettings struct {
	Enabled    bool
	Categories []string
}

// DefaultSponsorBlockCategories 是 stages.sponsorblock.categories 的預設值（SPB-R3、B4）。
var DefaultSponsorBlockCategories = []string{"sponsor", "selfpromo", "interaction"}

// SponsorBlock 取出 stages.sponsorblock；沒有設定時是預設值（enabled: true、預設類別）。
// 預設值只在讀取時補上，不寫進快照。欄位的嚴格檢查在 ValidateLayer（PRF-R4）。
func (e Effective) SponsorBlock() (SponsorBlockSettings, error) {
	out := SponsorBlockSettings{Enabled: true, Categories: slices.Clone(DefaultSponsorBlockCategories)}
	all, err := e.stages()
	if err != nil || all == nil {
		return out, err
	}
	entry, ok := all[StageSponsorBlock]
	if !ok {
		return out, nil
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return out, err
	}
	var got struct {
		Enabled    *bool    `json:"enabled"`
		Categories []string `json:"categories"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return out, invalidf("stages.sponsorblock 設定不正確：%v", err)
	}
	if got.Enabled != nil {
		out.Enabled = *got.Enabled
	}
	if got.Categories != nil {
		out.Categories = got.Categories
	}
	return out, nil
}

// stages 取出 stages 物件（每個步驟是物件）；沒有這個區段時回傳 nil。經 JSON 轉一次，
// 覆寫是 Go 型別（map[string]any 之外的自訂 map）或解析自 JSON 都能接受。
func (e Effective) stages() (map[string]any, error) {
	raw, ok := e["stages"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil || all == nil {
		return nil, invalidf("stages 必須是物件。")
	}
	out := make(map[string]any, len(all))
	for stage, rawEntry := range all {
		var entry map[string]any
		if err := json.Unmarshal(rawEntry, &entry); err != nil || entry == nil {
			return nil, invalidf("stages.%s 必須是物件。", stage)
		}
		out[stage] = entry
	}
	return out, nil
}

// Outputs 取出 outputs（陣列）。S0 只看長度；每個目標的結構屬於輸出切片。沒有這個區段時回傳空陣列。
func (e Effective) Outputs() ([]map[string]any, error) {
	raw, ok := e["outputs"]
	if !ok || raw == nil {
		return []map[string]any{}, nil
	}
	// 經 JSON 轉一次，覆寫是 Go 型別（[]map[string]any）或解析自 JSON（[]any）都能接受。
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var list []any
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, invalidf("outputs 必須是陣列。")
	}
	out := make([]map[string]any, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, invalidf("outputs 第 %d 項必須是物件。", i+1)
		}
		out = append(out, m)
	}
	return out, nil
}

// Snapshot 回傳快照的內容與它的 sha256。encoding/json 對 map 依 key 排序，所以相同的設定得到相同的結果。
func (e Effective) Snapshot() (sha, configJSON string, err error) {
	b, err := json.Marshal(map[string]any(e))
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), string(b), nil
}

// ParseEffective 讀回快照的內容。
func ParseEffective(configJSON string) (Effective, error) {
	var e Effective
	if err := json.Unmarshal([]byte(configJSON), &e); err != nil {
		return nil, fmt.Errorf("設定快照不是合法的 JSON：%w", err)
	}
	return e, nil
}

// merge 把 over 疊在 base 上，回傳新的 map（不修改輸入）：
// 兩邊都是物件時逐欄位合併，其他（純量、陣列）整個取代。
func merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = merge(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// toMap 把結構轉成 JSON 物件（map）。
func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}

// paramsSHA 回傳設定值的 sha256，放進快取鍵（plan 第 5.1 節的 params sha256）。
func paramsSHA(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
