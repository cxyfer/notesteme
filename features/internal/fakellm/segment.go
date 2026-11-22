package fakellm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// segment 的 generator（S5）：輸出 {"text", "breaks"}（ADR-0019 之外由 SEG 規格定義，見 llm.SegmentSpec）。
// text 是 cues 的 text 以一個空白接起來；breaks 依 mode：
//   - keep：每句都是一個新字幕（不合併也不切分，每句原樣輸出）
//   - merge：沒有斷點，整個 chunk 合併成一句
//   - inside_word：只有一個斷點，切在第一個拉丁字詞的中間（對不回輸入，SEG-R3）
func init() {
	RegisterGenerator("segment_breaks", Generator{Check: checkSegmentArgs, Run: runSegment})
}

type segmentArgs struct {
	Mode string `json:"mode"`
}

func parseSegmentArgs(raw json.RawMessage) (segmentArgs, error) {
	var a segmentArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	switch a.Mode {
	case "keep", "merge", "inside_word":
		return a, nil
	}
	return a, fmt.Errorf("mode 必須是 keep、merge 或 inside_word，不是 %q", a.Mode)
}

func checkSegmentArgs(raw json.RawMessage) error {
	_, err := parseSegmentArgs(raw)
	return err
}

func runSegment(in GeneratorInput, raw json.RawMessage) (any, error) {
	a, err := parseSegmentArgs(raw)
	if err != nil {
		return nil, err
	}
	if !in.HasData || len(in.Cues) == 0 {
		return nil, errors.New("segment_breaks 需要請求帶有 <transcript_data> 區塊與 cues")
	}
	var text strings.Builder
	breaks := []int{}
	pos := 0
	for k, c := range in.Cues {
		t, _ := c["text"].(string)
		if k > 0 {
			text.WriteByte(' ')
			pos++
			if a.Mode == "keep" {
				breaks = append(breaks, pos)
			}
		}
		text.WriteString(t)
		pos += utf8.RuneCountInString(t)
	}
	if a.Mode == "inside_word" {
		at := insideWord([]rune(text.String()))
		if at < 0 {
			return nil, errors.New("inside_word：cues 中沒有兩個字母以上的拉丁字詞")
		}
		breaks = []int{at}
	}
	return map[string]any{"text": text.String(), "breaks": breaks}, nil
}

// insideWord 回傳第一個「前後都是拉丁字母」的位置；沒有時回傳 -1。
func insideWord(r []rune) int {
	latin := func(x rune) bool { return unicode.Is(unicode.Latin, x) }
	for i := 1; i < len(r); i++ {
		if latin(r[i-1]) && latin(r[i]) {
			return i
		}
	}
	return -1
}
