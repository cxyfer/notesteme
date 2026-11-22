package fakellm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// splitMarker 是 translate 句群的切分標記（TRN-R3）。fakellm 不引用產品的套件，這裡另外寫一份。
const splitMarker = "⟨|⟩"

// targetRule 是 translate 系統指示中的目標語言宣告（internal/llm 的 TranslateSpec 寫在輸出規則中）。
const targetRulePrefix = "目標語言："

func init() {
	RegisterGenerator("translate_by_target", Generator{Check: checkByTargetArgs, Run: runByTarget})
}

// byTargetArgs 是 translate_by_target 的 args：texts 的 key 是目標語言，value 是每一句的譯文模板，
// {i} 換成 cue 的編號（句群的每一段各自編號）。
type byTargetArgs struct {
	Texts map[string]string `json:"texts"`
}

func parseByTargetArgs(raw json.RawMessage) (byTargetArgs, error) {
	var a byTargetArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	if len(a.Texts) == 0 {
		return a, errors.New("texts 不可為空")
	}
	return a, nil
}

func checkByTargetArgs(raw json.RawMessage) error {
	_, err := parseByTargetArgs(raw)
	return err
}

// runByTarget 依系統指示中的「目標語言：<lang>。」選出模板，為每個輸入的 cue 產生譯文：
// 輸入的 text 含有 n-1 個切分標記時，輸出也有 n 段、以相同的標記串接。找不到、或同時符合多個目標時回傳錯誤。
func runByTarget(in GeneratorInput, raw json.RawMessage) (any, error) {
	a, err := parseByTargetArgs(raw)
	if err != nil {
		return nil, err
	}
	if !in.HasData {
		return nil, errors.New("translate_by_target 需要請求帶有 <transcript_data> 區塊")
	}
	langs := make([]string, 0, len(a.Texts))
	for lang := range a.Texts {
		if strings.Contains(in.Prefix, targetRulePrefix+lang+"。") {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	if len(langs) != 1 {
		return nil, fmt.Errorf("請求的目標語言符合 %d 個模板 %v（系統指示中要有「%s<lang>。」）", len(langs), langs, targetRulePrefix)
	}
	tmpl := a.Texts[langs[0]]
	cues := make([]map[string]any, 0, len(in.Cues))
	for _, c := range in.Cues {
		text, _ := c["text"].(string)
		first, err := strconv.Atoi(fmt.Sprint(c["i"]))
		if err != nil {
			return nil, fmt.Errorf("cue 的 i 不是整數：%v", c["i"])
		}
		n := strings.Count(text, splitMarker) + 1
		parts := make([]string, n)
		for k := range parts {
			parts[k] = strings.ReplaceAll(tmpl, "{i}", strconv.Itoa(first+k))
		}
		cues = append(cues, map[string]any{"i": c["i"], "text": strings.Join(parts, splitMarker)})
	}
	return map[string]any{"cues": cues}, nil
}
