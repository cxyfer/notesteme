package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

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

	// Supadata 是 supadata 區段的生效值。它不屬於 language 區段（所以不序列化），
	// 由 Effective.Language 一併讀出，讓只拿到 Language 的步驟也能取得來源的參數。
	Supadata SupadataParams `json:"-"`
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
	eff := Effective{"language": defaults, "supadata": map[string]any{"mode": supadata.ModeAuto}}
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
	return eff, nil
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
