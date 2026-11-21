package fakellm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// fixture 是 features/fixtures/llm/<name>.json 的內容。三種形式，只能有一個頂層欄位（generator 可以另外帶 args）：
//
//	{ "output": { "cues": [ { "i": 1, "text": "…" } ] } }
//	{ "text": "this is not json" }
//	{ "generator": "echo_cues", "args": { "drop_last": 1, "extra": {…}, "replace": {…} } }
type fixture struct {
	name string

	output    json.RawMessage // 靜態輸出（compact）
	text      *string         // 「不是 JSON」的回答
	generator string
	args      json.RawMessage
}

// answer 是 fixture 對一個請求產生的回答。
type answer struct {
	isText bool
	text   string
	output json.RawMessage
}

// raw 回傳回答的字串形式：OpenAI 的 content、token 估算都用它。
func (a answer) raw() string {
	if a.isText {
		return a.text
	}
	return string(a.output)
}

// GeneratorInput 是 generator 看得到的請求內容：來自 <transcript_data> 區塊。
type GeneratorInput struct {
	Stage string
	// HasData 為 false 代表請求沒有（可解析的）資料區塊。
	HasData bool
	Chunk   int
	Chunks  int
	Context []map[string]any
	Cues    []map[string]any
}

// Generator 是 fixture 的 generator：Check 在 Answer 時驗證 args，Run 在請求時產生輸出（會被 json.Marshal）。
// S5、S6 的輸出 schema 由各自的切片以 RegisterGenerator 登記 generator。
type Generator struct {
	Check func(args json.RawMessage) error
	Run   func(in GeneratorInput, args json.RawMessage) (any, error)
}

var (
	generatorsMu sync.Mutex
	generators   = map[string]Generator{}
)

// RegisterGenerator 登記 generator；名稱重複時 panic（程式錯誤）。
func RegisterGenerator(name string, g Generator) {
	generatorsMu.Lock()
	defer generatorsMu.Unlock()
	if _, dup := generators[name]; dup {
		panic("fakellm: generator " + name + " 重複登記")
	}
	generators[name] = g
}

func init() {
	RegisterGenerator("echo_cues", Generator{Check: checkEchoArgs, Run: runEcho})
}

func generatorInput(stage string, d *dataBlock) GeneratorInput {
	in := GeneratorInput{Stage: stage}
	if d != nil {
		in.HasData, in.Chunk, in.Chunks, in.Context, in.Cues = true, d.Chunk, d.Chunks, d.Context, d.Cues
	}
	return in
}

// loadFixture 讀取 <fixturesDir>/llm/<name>；名稱沒有 .json 時自動補上。fixture 不存在或格式不正確時回傳錯誤。
func (s *Server) loadFixture(name string) (*fixture, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil, fmt.Errorf("LLM fixture 名稱 %q 不正確（不可含路徑）", name)
	}
	file := name
	if !strings.HasSuffix(file, ".json") {
		file += ".json"
	}
	path := filepath.Join(s.fixturesDir, "llm", file)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LLM fixture %q 不存在或無法讀取：%w", name, err)
	}
	f, err := parseFixture(name, b)
	if err != nil {
		return nil, fmt.Errorf("LLM fixture %q 格式不正確：%w", name, err)
	}
	return f, nil
}

func parseFixture(name string, b []byte) (*fixture, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("JSON 之後還有多餘的內容")
	}
	f := &fixture{name: name}
	for k := range top {
		if !slices.Contains([]string{"output", "text", "generator", "args"}, k) {
			return nil, fmt.Errorf("不認識的頂層欄位 %q（只有 output、text、generator、args）", k)
		}
	}
	_, hasOut := top["output"]
	_, hasText := top["text"]
	_, hasGen := top["generator"]
	_, hasArgs := top["args"]
	switch {
	case hasOut && !hasText && !hasGen && !hasArgs:
		var c bytes.Buffer
		if err := json.Compact(&c, top["output"]); err != nil {
			return nil, fmt.Errorf("output：%w", err)
		}
		f.output = c.Bytes()
	case hasText && !hasOut && !hasGen && !hasArgs:
		var t string
		if err := json.Unmarshal(top["text"], &t); err != nil {
			return nil, fmt.Errorf("text 必須是字串：%w", err)
		}
		f.text = &t
	case hasGen && !hasOut && !hasText:
		if err := json.Unmarshal(top["generator"], &f.generator); err != nil || f.generator == "" {
			return nil, errors.New("generator 必須是非空字串")
		}
		generatorsMu.Lock()
		g, ok := generators[f.generator]
		generatorsMu.Unlock()
		if !ok {
			return nil, fmt.Errorf("不認識的 generator %q", f.generator)
		}
		f.args = top["args"]
		if g.Check != nil {
			if err := g.Check(f.args); err != nil {
				return nil, fmt.Errorf("generator %s 的 args：%w", f.generator, err)
			}
		}
	default:
		return nil, errors.New("必須恰好是 output、text、generator（可附 args）其中一種")
	}
	return f, nil
}

// render 產生這個請求的回答。
func (f *fixture) render(in GeneratorInput) (answer, error) {
	switch {
	case f.text != nil:
		return answer{isText: true, text: *f.text}, nil
	case f.generator != "":
		generatorsMu.Lock()
		g := generators[f.generator]
		generatorsMu.Unlock()
		v, err := g.Run(in, f.args)
		if err != nil {
			return answer{}, fmt.Errorf("generator %s：%w", f.generator, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return answer{}, err
		}
		return answer{output: b}, nil
	default:
		return answer{output: f.output}, nil
	}
}

// echoArgs 是 echo_cues 的 args。
type echoArgs struct {
	DropLast int               `json:"drop_last"`
	Extra    map[string]any    `json:"extra"`
	Replace  map[string]string `json:"replace"`
}

func parseEchoArgs(raw json.RawMessage) (echoArgs, error) {
	var a echoArgs
	if len(bytes.TrimSpace(raw)) == 0 {
		return a, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	if a.DropLast < 0 {
		return a, errors.New("drop_last 不可為負數")
	}
	if _, clash := a.Extra["cues"]; clash {
		return a, errors.New("extra 不可覆蓋 cues")
	}
	return a, nil
}

func checkEchoArgs(raw json.RawMessage) error {
	_, err := parseEchoArgs(raw)
	return err
}

// runEcho 回傳 {"cues": <資料區塊的 cues，i 與 text 原樣>}，再依 args 處理：
// drop_last 拿掉最後 N 句，extra 加入頂層欄位，replace 對 text 做字串取代（依 key 排序，結果才確定）。
func runEcho(in GeneratorInput, raw json.RawMessage) (any, error) {
	a, err := parseEchoArgs(raw)
	if err != nil {
		return nil, err
	}
	if !in.HasData {
		return nil, errors.New("echo_cues 需要請求帶有 <transcript_data> 區塊")
	}
	if a.DropLast > len(in.Cues) {
		return nil, fmt.Errorf("drop_last %d 大於 cues 的數量 %d", a.DropLast, len(in.Cues))
	}
	keys := make([]string, 0, len(a.Replace))
	for k := range a.Replace {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	cues := make([]map[string]any, 0, len(in.Cues)-a.DropLast)
	for _, c := range in.Cues[:len(in.Cues)-a.DropLast] {
		out := make(map[string]any, len(c))
		for k, v := range c {
			out[k] = v
		}
		if t, ok := out["text"].(string); ok {
			for _, k := range keys {
				t = strings.ReplaceAll(t, k, a.Replace[k])
			}
			out["text"] = t
		}
		cues = append(cues, out)
	}
	res := map[string]any{"cues": cues}
	for k, v := range a.Extra {
		res[k] = v
	}
	return res, nil
}
