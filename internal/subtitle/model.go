// Package subtitle 提供字幕的 cue 模型、時間碼、VTT／SRT 解析、正規化與即時渲染。
//
// 所有函式皆為純函式：不讀寫檔案、不存取網路、不依賴全域狀態。
// 時間一律以毫秒整數（int64）表示（ADR-0003、ADR-0005）。
package subtitle

import "fmt"

// TimingOrigin 記錄 cue 的時間是怎麼來的（ADR-0003）。
type TimingOrigin string

const (
	TimingSource       TimingOrigin = "source"
	TimingMerged       TimingOrigin = "merged"
	TimingSilence      TimingOrigin = "silence"
	TimingInterpolated TimingOrigin = "interpolated"
	TimingModel        TimingOrigin = "model"
)

// Valid 回傳是否為已知的時間來源。
func (t TimingOrigin) Valid() bool {
	switch t {
	case TimingSource, TimingMerged, TimingSilence, TimingInterpolated, TimingModel:
		return true
	}
	return false
}

// Variant 為字幕的處理階段版本。
type Variant string

const (
	VariantSource     Variant = "source"
	VariantNormalized Variant = "normalized"
	VariantSegmented  Variant = "segmented"
	VariantCorrected  Variant = "corrected"
	VariantTranslated Variant = "translated"
	VariantEdited     Variant = "edited"
)

// Valid 回傳是否為已知的變體。
func (v Variant) Valid() bool {
	switch v {
	case VariantSource, VariantNormalized, VariantSegmented,
		VariantCorrected, VariantTranslated, VariantEdited:
		return true
	}
	return false
}

// Word 為字詞級時間。
type Word struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// Cue 為一句字幕。Speaker 預留給之後的說話者辨識，nil 表示沒有。
type Cue struct {
	Index   int          `json:"index"`
	StartMS int64        `json:"start_ms"`
	EndMS   int64        `json:"end_ms"`
	Text    string       `json:"text"`
	Words   []Word       `json:"words,omitempty"`
	Timing  TimingOrigin `json:"timing"`
	Speaker *string      `json:"speaker"`
}

// Speaker 對應 cues JSON 頂層預留的 speakers 欄位。
type Speaker struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Transcript 為一份字幕：語言、變體與 cue 序列。
type Transcript struct {
	Lang     string    `json:"lang"`
	Variant  Variant   `json:"variant"`
	Speakers []Speaker `json:"speakers"`
	Cues     []Cue     `json:"cues"`
}

// ParseError 描述解析字幕檔時的錯誤位置。
// Cue 為第幾句（從 1 起算），Line 為檔案中的行號（從 1 起算）；兩者可能為 0。
type ParseError struct {
	Cue  int
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("subtitle: cue %d (line %d): %s", e.Cue, e.Line, e.Msg)
}
