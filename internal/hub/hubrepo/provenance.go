// Package hubrepo 是 Hub 內容倉庫的純邏輯（HREP）：來源紀錄的白名單、模型欄位與 model_verified、
// 媒體檔的判斷、能不能分享，以及可見度。不讀寫資料庫，handler（hubapi）與驗收步驟共用。
package hubrepo

import (
	"encoding/json"
	"regexp"
)

// MaxProvenanceBytes 是每個產出的來源紀錄 JSON 的上限（HREP-R4）；以 bundle 中收到的內容計算。
const MaxProvenanceBytes = 16 << 10

var (
	shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idRe  = regexp.MustCompile(`^[0-9a-fA-F-]{1,64}$`)
)

// maxField 是白名單中字串欄位的長度上限；超過的欄位丟掉（白名單只保存短的識別資訊）。
const maxField = 512

// Provenance 是白名單中 Hub 會用到的欄位。
type Provenance struct {
	Executor       string
	Provider       string
	Model          string
	ModelReported  string
	ModelVerified  *bool
	Source         string
	CookieRequired *bool
	HubOrigin      HubOrigin
	ParentsSHA256  []string
}

// HubOrigin 是「以 Hub 上某個版本的原始字幕重新生成」的來源（ADR-0031 第 3 點）。
type HubOrigin struct{ VersionID, SHA256 string }

// FilterProvenance 只保留 ADR-0032 第 2 點的白名單欄位（HREP-R4），型別不對的值一併丟掉；
// 回傳過濾後的 JSON 物件與解析出的欄位。raw 不是物件時回傳空物件。
func FilterProvenance(raw json.RawMessage) (json.RawMessage, Provenance) {
	var in map[string]any
	var p Provenance
	if json.Unmarshal(raw, &in) != nil || in == nil {
		return json.RawMessage(`{}`), p
	}
	out := map[string]any{}
	for _, k := range []string{"executor", "provider", "model", "model_reported", "params_sha256", "source", "timing", "created_at"} {
		if s, ok := shortString(in[k]); ok {
			out[k] = s
		}
	}
	for _, k := range []string{"model_verified", "cookie_required"} {
		if b, ok := in[k].(bool); ok {
			out[k] = b
		}
	}
	if m := pick(in["prompt"], "id", "version", "sha256"); m != nil {
		out["prompt"] = m
	}
	if m := pick(in["track"], "lang", "kind"); m != nil {
		out["track"] = m
	}
	if tv, ok := in["tool_versions"].(map[string]any); ok {
		m := map[string]any{}
		for k, v := range tv {
			if s, ok := shortString(v); ok && len(k) <= 64 {
				m[k] = s
			}
		}
		if len(m) > 0 {
			out["tool_versions"] = m
		}
	}
	if ho, ok := in["hub_origin"].(map[string]any); ok {
		m := map[string]any{}
		if s, ok := ho["version_id"].(string); ok && idRe.MatchString(s) {
			m["version_id"] = s
			p.HubOrigin.VersionID = s
		}
		if s, ok := ho["sha256"].(string); ok && shaRe.MatchString(s) {
			m["sha256"] = s
			p.HubOrigin.SHA256 = s
		}
		if len(m) > 0 {
			out["hub_origin"] = m
		}
	}
	if ps, ok := in["parents_sha256"].([]any); ok {
		list := []any{}
		for _, x := range ps {
			if s, ok := x.(string); ok && shaRe.MatchString(s) && len(list) < 64 {
				list = append(list, s)
				p.ParentsSHA256 = append(p.ParentsSHA256, s)
			}
		}
		out["parents_sha256"] = list
	}
	p.Executor, _ = out["executor"].(string)
	p.Provider, _ = out["provider"].(string)
	p.Model, _ = out["model"].(string)
	p.ModelReported, _ = out["model_reported"].(string)
	p.Source, _ = out["source"].(string)
	if b, ok := out["model_verified"].(bool); ok {
		p.ModelVerified = &b
	}
	if b, ok := out["cookie_required"].(bool); ok {
		p.CookieRequired = &b
	}
	b, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`), p
	}
	return b, p
}

func shortString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && len(s) <= maxField
}

// pick 從物件中取出 keys 中的純量欄位（字串、數字、布林）；沒有任何欄位時回 nil。
func pick(v any, keys ...string) map[string]any {
	in, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range keys {
		switch x := in[k].(type) {
		case string:
			if len(x) <= maxField {
				out[k] = x
			}
		case float64, bool:
			out[k] = x
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StepModel 是這一步的模型：api 的 provider/model（只有 model 時就是 model），agent 用 model_reported（HREP-R7）。
func (p Provenance) StepModel() string {
	if p.Executor == "agent" {
		if p.ModelReported != "" {
			return p.ModelReported
		}
		return p.Model
	}
	switch {
	case p.Provider != "" && p.Model != "":
		return p.Provider + "/" + p.Model
	case p.Model != "":
		return p.Model
	}
	return ""
}

// TranscribeModel 是 source 這一步的模型；平台字幕沒有模型時用來源 ID（例如 youtube-manual，HREP-R7、R2）。
func (p Provenance) TranscribeModel() string {
	if m := p.StepModel(); m != "" {
		return m
	}
	return p.Source
}

// Unverified 回報這一步本身讓版本變成 model_verified: false（HREP-R7）：agent 回報的模型，或明確的 model_verified: false。
// executor: hub 的步驟另外看 hub_origin 指到的版本（由呼叫端判斷）。
func (p Provenance) Unverified() bool {
	return p.Executor == "agent" || (p.ModelVerified != nil && !*p.ModelVerified)
}
