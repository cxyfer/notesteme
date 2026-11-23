package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// note 與 note_map 的退回原因（NOTE-R3、R6）。
const (
	ReasonNoteStructure = "note_structure" // 節數、level、self_check
	ReasonNoteCue       = "note_cue"       // cue 不在輸入中或遞減
	ReasonNoteTitle     = "note_title"     // 標題是空的、太長或含換行
	ReasonNoteFrame     = "note_frame"     // 引用沒有選中或重複的畫面
)

// 筆記的上限（NOTE-R3）。
const (
	MaxNoteTitle     = 120 // 去掉頭尾空白後的字元數
	MaxNoteSections  = 100
	MaxNoteBody      = 20000 // 每節內文的字元數
	MaxNoteSelfCheck = 30
	MaxNotePointText = 2000
)

// StageNote、StageNoteMap 是筆記的兩個 LLM 步驟名稱（tool 名稱 notesteme_note、notesteme_note_map）。
const (
	StageNote    = "note"
	StageNoteMap = "note_map"
)

var noteCueRefRe = regexp.MustCompile(`\[\[cue:([0-9]{1,9})\]\]`)

func noteSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sections": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"level":  map[string]any{"type": "integer"},
						"title":  map[string]any{"type": "string"},
						"cue":    map[string]any{"type": "integer"},
						"body":   map[string]any{"type": "string"},
						"frames": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
					},
					"required":             []any{"level", "title", "cue", "body", "frames"},
					"additionalProperties": false,
				},
			},
			"self_check": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []any{"sections", "self_check"},
		"additionalProperties": false,
	}
}

func noteMapSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"points": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cue":  map[string]any{"type": "integer"},
						"text": map[string]any{"type": "string"},
					},
					"required":             []any{"cue", "text"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"points"},
		"additionalProperties": false,
	}
}

// NoteSection 是模型寫的一節（NOTE-R1）。
type NoteSection struct {
	Level  int    `json:"level"`
	Title  string `json:"title"`
	Cue    int    `json:"cue"`
	Body   string `json:"body"`
	Frames []int  `json:"frames"`
}

// NoteOutput 是 note 的輸出。
type NoteOutput struct {
	Sections  []NoteSection `json:"sections"`
	SelfCheck []string      `json:"self_check"`
}

// NotePoint 是 note_map 的一個要點。
type NotePoint struct {
	Cue  int    `json:"cue"`
	Text string `json:"text"`
}

// NoteMapOutput 是 note_map 的輸出。
type NoteMapOutput struct {
	Points []NotePoint `json:"points"`
}

// NoteRefs 是 note 的輸出可以引用的對象。
type NoteRefs struct {
	// Cues 是可以引用的句號；nil 代表用這次輸入的 cues（map-reduce 的 reduce 要用原本的整份輸入，NOTE-R6）。
	Cues map[int]bool
	// Frames 是選中的畫面編號（NOTE-R3）。
	Frames map[int]bool
}

// NoteSpec 是 note 步驟的 Spec（NOTE-R1、R3）：LLM 只寫各節的內文並以 cue 引用時間，時間由程式換算（ADR-0003）。
func NoteSpec(refs NoteRefs) *Spec {
	return &Spec{
		Stage:  StageNote,
		Schema: noteSchema(),
		OutputRules: fmt.Sprintf("輸出 {\"sections\": [{\"level\", \"title\", \"cue\", \"body\", \"frames\"}], \"self_check\": [string]}："+
			"level 是 2（章節）或 3（小節），第一節必須是 2；cue 是這一節開始的句號（cues 中的 i），同一層依時間不遞減；"+
			"title 是 1～%d 字、不含換行的標題，不要加時間或編號；body 是這一節的 Markdown 內文，不要寫標題，要引用某句的時間時寫 [[cue:N]]；"+
			"frames 是這一節要放的畫面編號（只能用 %s 的 frames 中列出的 n，每張最多一次，沒有就給 []）；"+
			"self_check 是 1 個以上的自我檢驗問題。不要輸出任何時間。", MaxNoteTitle, MetadataOpen),
		Check: func(in ChunkInput, out json.RawMessage) *ValidationError {
			_, verr := CheckNote(in, out, refs)
			return verr
		},
	}
}

// NoteMapSpec 是 note_map 的 Spec（NOTE-R6）：每個 chunk 的要點，之後由 note 合併。
func NoteMapSpec() *Spec {
	return &Spec{
		Stage:  StageNoteMap,
		Schema: noteMapSchema(),
		OutputRules: "輸出 {\"points\": [{\"cue\", \"text\"}]}：每個要點一項，cue 是這個要點開始的句號（只能用這次 cues 中出現的 i），" +
			"text 是要點的內容（1～" + strconv.Itoa(MaxNotePointText) + " 字）；至少 1 項。不要輸出任何時間。",
		Check: func(in ChunkInput, out json.RawMessage) *ValidationError {
			_, verr := CheckNoteMap(in, out)
			return verr
		},
	}
}

func noteError(reason, msg string) *ValidationError {
	return &ValidationError{Reason: reason, Message: msg, Detail: map[string]any{"error": msg}}
}

func presentCues(in ChunkInput) map[int]bool {
	m := make(map[int]bool, len(in.Cues))
	for _, c := range in.Cues {
		m[c.I] = true
	}
	return m
}

// CheckNote 驗證 note 的輸出（NOTE-R3），回傳去掉標題頭尾空白的結果。訊息的索引從 0 起算。
func CheckNote(in ChunkInput, out json.RawMessage, refs NoteRefs) (NoteOutput, *ValidationError) {
	var o NoteOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return NoteOutput{}, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	cues := refs.Cues
	if cues == nil {
		cues = presentCues(in)
	}
	switch {
	case len(o.Sections) == 0:
		return NoteOutput{}, noteError(ReasonNoteStructure, "sections 至少要有 1 節")
	case len(o.Sections) > MaxNoteSections:
		return NoteOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("sections 有 %d 節，最多 %d 節", len(o.Sections), MaxNoteSections))
	}
	usedFrames := map[int]bool{}
	lastCue := map[int]int{} // level → 同一層上一節的 cue；換到新的 level 2 時清掉 level 3
	for k, s := range o.Sections {
		switch {
		case k == 0 && s.Level != 2:
			return NoteOutput{}, noteError(ReasonNoteStructure, "sections[0].level 必須是 2")
		case s.Level != 2 && s.Level != 3:
			return NoteOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("sections[%d].level 只能是 2 或 3", k))
		}
		if !cues[s.Cue] {
			return NoteOutput{}, noteError(ReasonNoteCue, fmt.Sprintf("sections[%d].cue %d 不在輸入中", k, s.Cue))
		}
		if prev, ok := lastCue[s.Level]; ok && s.Cue < prev {
			return NoteOutput{}, noteError(ReasonNoteCue, fmt.Sprintf("sections[%d].cue %d 比同一層的前一節（%d）早", k, s.Cue, prev))
		}
		lastCue[s.Level] = s.Cue
		if s.Level == 2 {
			delete(lastCue, 3)
		}
		title := strings.TrimSpace(s.Title)
		switch n := utf8.RuneCountInString(title); {
		case n == 0:
			return NoteOutput{}, noteError(ReasonNoteTitle, fmt.Sprintf("sections[%d].title 是空的", k))
		case n > MaxNoteTitle:
			return NoteOutput{}, noteError(ReasonNoteTitle, fmt.Sprintf("sections[%d].title 超過 %d 字", k, MaxNoteTitle))
		case strings.ContainsFunc(title, isTitleControl):
			return NoteOutput{}, noteError(ReasonNoteTitle, fmt.Sprintf("sections[%d].title 不能含換行或控制字元", k))
		}
		o.Sections[k].Title = title
		if utf8.RuneCountInString(s.Body) > MaxNoteBody {
			return NoteOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("sections[%d].body 超過 %d 字", k, MaxNoteBody))
		}
		for _, m := range noteCueRefRe.FindAllStringSubmatch(s.Body, -1) {
			if n, err := strconv.Atoi(m[1]); err != nil || !cues[n] {
				return NoteOutput{}, noteError(ReasonNoteCue, fmt.Sprintf("sections[%d].body 引用的 cue %s 不在輸入中", k, m[1]))
			}
		}
		for j, f := range s.Frames {
			if !refs.Frames[f] {
				return NoteOutput{}, noteError(ReasonNoteFrame, fmt.Sprintf("sections[%d].frames[%d] %d 不是選中的畫面", k, j, f))
			}
			if usedFrames[f] {
				return NoteOutput{}, noteError(ReasonNoteFrame, fmt.Sprintf("sections[%d].frames[%d] %d 已經引用過", k, j, f))
			}
			usedFrames[f] = true
		}
		if o.Sections[k].Frames == nil {
			o.Sections[k].Frames = []int{}
		}
	}
	items := 0
	for _, q := range o.SelfCheck {
		if strings.TrimSpace(q) != "" {
			items++
		}
	}
	switch {
	case items == 0:
		return NoteOutput{}, noteError(ReasonNoteStructure, "self_check 至少要有 1 項")
	case len(o.SelfCheck) > MaxNoteSelfCheck:
		return NoteOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("self_check 最多 %d 項", MaxNoteSelfCheck))
	}
	return o, nil
}

// CheckNoteMap 驗證 note_map 的輸出（NOTE-R6）：至少一個要點，cue 必須是這個 chunk 的輸入中的句號。
func CheckNoteMap(in ChunkInput, out json.RawMessage) (NoteMapOutput, *ValidationError) {
	var o NoteMapOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return NoteMapOutput{}, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	if len(o.Points) == 0 {
		return NoteMapOutput{}, noteError(ReasonNoteStructure, "points 至少要有 1 項")
	}
	cues := presentCues(in)
	for k, pt := range o.Points {
		if !cues[pt.Cue] {
			return NoteMapOutput{}, noteError(ReasonNoteCue, fmt.Sprintf("points[%d].cue %d 不在輸入中", k, pt.Cue))
		}
		switch n := utf8.RuneCountInString(strings.TrimSpace(pt.Text)); {
		case n == 0:
			return NoteMapOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("points[%d].text 是空的", k))
		case n > MaxNotePointText:
			return NoteMapOutput{}, noteError(ReasonNoteStructure, fmt.Sprintf("points[%d].text 超過 %d 字", k, MaxNotePointText))
		}
	}
	return o, nil
}
