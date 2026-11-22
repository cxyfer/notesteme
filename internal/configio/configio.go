// Package configio 是 `notesteme config export|import` 的檔案格式（SEC-R10）：
// 把「DB 管理的設定」寫成 YAML，或把 YAML 讀回結構。
//
// 它不碰資料庫，也不判斷內容對不對（處理方案的驗證在 pipeline）：只負責
// 「不可信的 YAML 輸入」變成 JSON 相容的值，並把每一個結構問題指到欄位路徑。
// 解析器刻意比 YAML 標準嚴格：不接受別名與合併鍵（展開炸彈）、重複的鍵、非字串的鍵、
// 多份文件、NaN／Inf、不認識的型別標籤，輸入大小與巢狀深度都有上限。
package configio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/internal/validate"
)

// MaxBytes 是匯入檔案的大小上限（位元組）。處理方案只有幾 KiB，1 MiB 已經非常寬裕；
// 上限同時限制了 YAML 解析器的記憶體用量。
const MaxBytes = 1 << 20

// maxDepth 是 YAML 巢狀的深度上限：處理方案最深也不到 6 層，64 層只為了擋惡意輸入。
const maxDepth = 64

// 頂層的鍵。
const (
	KeyProfiles       = "profiles"
	KeyDefaultProfile = "default_profile"
)

// Document 是設定檔的內容。目前只涵蓋處理方案與預設處理方案（保守範圍，見 SEC 規格的待決問題）。
type Document struct {
	// DefaultProfile 是預設處理方案的名稱；空字串表示檔案沒有這個鍵（匯入時不改動目前的預設）。
	DefaultProfile string
	// Profiles 是處理方案的名稱對應內容，和 `POST /api/profiles` 的 config 同形。
	// 值是 JSON 相容的型別：map[string]any、[]any、string、整數、float64、bool、nil。
	Profiles map[string]map[string]any
}

// Parse 解析匯入的 YAML。回傳的 errors 依序列出每一個結構問題，沒有問題時為 nil。
//
// YAML 語法錯誤、檔案太大、沒有內容這類整份無法讀取的情況，doc 為 nil，錯誤的 Path 是空字串；
// 其他結構問題（不認識的頂層鍵、型別不對的值）doc 仍會回傳（盡量保留讀得懂的部分），讓呼叫端把
// 處理方案內容的錯誤一起列出來，使用者一次看完所有問題。
func Parse(data []byte) (*Document, []validate.FieldError) {
	if len(data) > MaxBytes {
		return nil, whole("檔案太大（%d 位元組），上限是 %d 位元組（1 MiB）。", len(data), MaxBytes)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, whole("檔案是空的，沒有可以匯入的內容。")
		}
		return nil, whole("YAML 格式不正確：%s", cleanYAMLError(err))
	}
	// 第二份文件會被默默忽略是危險的（使用者以為整個檔案都生效），一律拒絕。
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, whole("檔案只能有一份 YAML 文件（不可以用 --- 分成多份）。")
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, whole("YAML 格式不正確：找不到文件內容。")
	}

	w := &walker{}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, whole("最上層必須是物件（鍵為 %s 與 %s）。", KeyProfiles, KeyDefaultProfile)
	}
	v, ok := w.value(top, "", 0)
	m, _ := v.(map[string]any)
	if !ok || m == nil {
		// 轉換時就有錯誤（重複的鍵、不支援的型別…）：以收集到的錯誤為準。
		return nil, w.errs
	}

	doc := &Document{Profiles: map[string]map[string]any{}}
	errs := w.errs

	// 不認識的頂層鍵依字母排序，輸出才穩定。
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case KeyProfiles, KeyDefaultProfile:
		default:
			errs = append(errs, validate.FieldError{Path: k, Message: fmt.Sprintf("不認識的頂層鍵；目前只支援 %s 與 %s。", KeyProfiles, KeyDefaultProfile)})
		}
	}

	if raw, present := m[KeyDefaultProfile]; present {
		if s, ok := raw.(string); ok && s != "" {
			doc.DefaultProfile = s
		} else {
			errs = append(errs, validate.FieldError{Path: KeyDefaultProfile, Message: "必須是處理方案的名稱（非空字串）。"})
		}
	}

	if raw, present := m[KeyProfiles]; present {
		pm, ok := raw.(map[string]any)
		if !ok {
			errs = append(errs, validate.FieldError{Path: KeyProfiles, Message: "必須是物件（處理方案名稱 → 處理方案內容）。"})
		} else {
			names := make([]string, 0, len(pm))
			for name := range pm {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				cfg, ok := pm[name].(map[string]any)
				if !ok {
					errs = append(errs, validate.FieldError{Path: KeyProfiles + "." + name, Message: "必須是物件（處理方案的內容）。"})
					continue
				}
				doc.Profiles[name] = cfg
			}
		}
	}
	return doc, errs
}

// Marshal 把 doc 寫成 YAML。輸出是穩定的：同樣的內容一定產生同樣的位元組（鍵依字母排序，縮排 2 格），
// 所以可以放進版本控制，也可以直接用 Parse 讀回來。DefaultProfile 為空時不寫 default_profile。
func Marshal(doc *Document) ([]byte, error) {
	profiles := make(map[string]any, len(doc.Profiles))
	for name, cfg := range doc.Profiles {
		if cfg == nil {
			cfg = map[string]any{}
		}
		profiles[name] = cfg
	}
	top := map[string]any{KeyProfiles: profiles}
	if doc.DefaultProfile != "" {
		top[KeyDefaultProfile] = doc.DefaultProfile
	}
	n, err := toNode(top)
	if err != nil {
		return nil, fmt.Errorf("configio: 輸出 YAML 失敗：%w", err)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, fmt.Errorf("configio: 輸出 YAML 失敗：%w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("configio: 輸出 YAML 失敗：%w", err)
	}
	return buf.Bytes(), nil
}

// toNode 自己組 yaml.Node，才能固定鍵的順序與字串的寫法：
// yaml.v3 對只含換行等特殊字元的字串會選 literal 區塊，讀回來內容會不同（fuzz 找到的）。
func toNode(v any) (*yaml.Node, error) {
	switch x := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(x) == 0 {
			n.Style = yaml.FlowStyle // 輸出 {}
			return n, nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val, err := toNode(x[k])
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, stringNode(k), val)
		}
		return n, nil
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if len(x) == 0 {
			n.Style = yaml.FlowStyle // 輸出 []
			return n, nil
		}
		for _, e := range x {
			val, err := toNode(e)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, val)
		}
		return n, nil
	case string:
		return stringNode(x), nil
	default:
		n := &yaml.Node{}
		if err := n.Encode(v); err != nil {
			return nil, err
		}
		return n, nil
	}
}

// stringNode 建立字串節點。含非可見字元（換行、控制字元、特殊空白）或頭尾有空白的字串一律用雙引號，
// 讓內容經過 YAML 讀回來一字不差；其餘交給 yaml.v3 挑寫法（看起來像數字、布林、null 的字串它會加引號）。
func stringNode(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if needsDoubleQuotes(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

func needsDoubleQuotes(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return true
	}
	return strings.ContainsFunc(s, func(r rune) bool { return r != ' ' && !unicode.IsPrint(r) })
}

func whole(format string, a ...any) []validate.FieldError {
	return []validate.FieldError{{Message: fmt.Sprintf(format, a...)}}
}

// cleanYAMLError 去掉 yaml.v3 錯誤訊息的 "yaml: " 前綴，只留下行號與原因。
func cleanYAMLError(err error) string {
	return strings.TrimPrefix(err.Error(), "yaml: ")
}

// walker 把 yaml.Node 轉成 JSON 相容的值，並收集路徑明確的錯誤。
type walker struct{ errs []validate.FieldError }

func (w *walker) fail(path, format string, a ...any) {
	w.errs = append(w.errs, validate.FieldError{Path: path, Message: fmt.Sprintf(format, a...)})
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// value 轉換一個節點；轉換失敗時回傳 ok=false（錯誤已記下）。
func (w *walker) value(n *yaml.Node, path string, depth int) (any, bool) {
	if depth > maxDepth {
		w.fail(path, "巢狀太深（上限 %d 層）。", maxDepth)
		return nil, false
	}
	switch n.Kind {
	case yaml.AliasNode:
		w.fail(path, "不支援 YAML 別名（*%s）；請把內容直接寫出來。", n.Value)
		return nil, false
	case yaml.MappingNode:
		return w.mapping(n, path, depth)
	case yaml.SequenceNode:
		if t := n.ShortTag(); t != "!!seq" {
			w.fail(path, "不支援的 YAML 型別 %s。", t)
			return nil, false
		}
		out := make([]any, 0, len(n.Content))
		ok := true
		for i, c := range n.Content {
			v, cok := w.value(c, join(path, strconv.Itoa(i)), depth+1)
			ok = ok && cok
			out = append(out, v)
		}
		return out, ok
	case yaml.ScalarNode:
		return w.scalar(n, path)
	}
	w.fail(path, "不支援的 YAML 節點。")
	return nil, false
}

func (w *walker) mapping(n *yaml.Node, path string, depth int) (any, bool) {
	if t := n.ShortTag(); t != "!!map" {
		w.fail(path, "不支援的 YAML 型別 %s。", t)
		return nil, false
	}
	out := make(map[string]any, len(n.Content)/2)
	ok := true
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
			// 鍵本身可能是 merge（<<）、數字、布林或 null：JSON 的物件鍵一律是字串，不能默默轉換。
			if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
				w.fail(path, "不支援 YAML 合併鍵（<<）；請把內容直接寫出來。")
			} else {
				w.fail(path, "物件的鍵必須是字串（第 %d 行）。", k.Line)
			}
			ok = false
			continue
		}
		cp := join(path, k.Value)
		if _, dup := out[k.Value]; dup {
			w.fail(cp, "鍵重複（第 %d 行）。", k.Line)
			ok = false
			continue
		}
		val, vok := w.value(v, cp, depth+1)
		ok = ok && vok
		out[k.Value] = val
	}
	return out, ok
}

func (w *walker) scalar(n *yaml.Node, path string) (any, bool) {
	switch tag := n.ShortTag(); tag {
	case "!!str", "!!timestamp":
		// 沒加引號的日期（2024-01-01）yaml.v3 會當成 timestamp；設定裡沒有日期型別，保留原文字。
		return n.Value, true
	case "!!null":
		return nil, true
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			w.fail(path, "不是合法的布林值。")
			return nil, false
		}
		return b, true
	case "!!int":
		var v any
		if err := n.Decode(&v); err != nil {
			w.fail(path, "不是合法的整數。")
			return nil, false
		}
		return v, true
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			w.fail(path, "不是合法的數字（不接受 NaN 與無限大）。")
			return nil, false
		}
		return f, true
	default:
		w.fail(path, "不支援的 YAML 型別 %s。", tag)
		return nil, false
	}
}
