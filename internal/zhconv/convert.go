package zhconv

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrUnknownTable 表示轉換表不是內嵌的十份設定之一。
var ErrUnknownTable = errors.New("zhconv: 不認識的轉換表")

// dictConfig 是上游設定中的字典節點：type 為 text（單一字典檔）或 group（字典群組）。
type dictConfig struct {
	Type        string       `json:"type"`
	File        string       `json:"file"`
	MatchPolicy string       `json:"match_policy"`
	Dicts       []dictConfig `json:"dicts"`
}

// config 是上游 config/<table>.json 中本實作用到的欄位。may_output_tofu 不影響轉換，忽略。
type config struct {
	Normalization []struct {
		Dict dictConfig `json:"dict"`
	} `json:"normalization"`
	Segmentation *struct {
		Type string     `json:"type"`
		Dict dictConfig `json:"dict"`
	} `json:"segmentation"`
	ConversionChain []struct {
		Dict dictConfig `json:"dict"`
	} `json:"conversion_chain"`
}

func buildMatcher(c dictConfig) (matcher, error) {
	switch c.Type {
	case "text":
		return loadDict(c.File)
	case "group":
		g := &group{}
		switch c.MatchPolicy {
		case "union", "":
			g.union = true
		case "short_circuit":
		default:
			return nil, fmt.Errorf("zhconv: 不支援的 match_policy %q", c.MatchPolicy)
		}
		for _, sub := range c.Dicts {
			m, err := buildMatcher(sub)
			if err != nil {
				return nil, err
			}
			g.ms = append(g.ms, m)
		}
		return g, nil
	}
	return nil, fmt.Errorf("zhconv: 不支援的字典類型 %q", c.Type)
}

// converter 是一份設定組出來的轉換器：normalization → mmseg segmentation → conversion_chain。
type converter struct {
	normalization []matcher
	segmentation  matcher // nil 代表不分段，整段文字當成一個 segment
	chain         []matcher
}

// composite 組出繁體地區之間的轉換器：normalization、來源地區的反向字典（轉成 OpenCC Standard）、目標地區的字典。
// 與上游 t2tw、t2hk 一樣不分段。
func composite(t Table) (*converter, error) {
	var steps [2][]string
	switch t {
	case HK2TW:
		steps = [2][]string{{"HKVariantsRevPhrases.txt", "HKVariantsRev.txt"}, {"TWVariantsPhrases.txt", "TWVariants.txt"}}
	case TW2HK:
		steps = [2][]string{{"TWVariantsRevPhrases.txt", "TWVariantsRev.txt"}, {"HKVariantsPhrases.txt", "HKVariants.txt"}}
	}
	norm, err := loadDict("CJK_Compatibility_Ideographs.txt")
	if err != nil {
		return nil, err
	}
	c := &converter{normalization: []matcher{norm}}
	for _, files := range steps {
		g := &group{} // short_circuit：詞組在字之前
		for _, f := range files {
			d, err := loadDict(f)
			if err != nil {
				return nil, err
			}
			g.ms = append(g.ms, d)
		}
		c.chain = append(c.chain, g)
	}
	return c, nil
}

func newConverter(t Table) (*converter, error) {
	if slices.Contains(compositeTables, t) {
		return composite(t)
	}
	data, err := resources.ReadFile(resourceDir + string(t) + ".json")
	if err != nil {
		return nil, fmt.Errorf("%w：%s", ErrUnknownTable, t)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("zhconv: 解析 %s.json：%w", t, err)
	}
	c := &converter{}
	for _, n := range cfg.Normalization {
		m, err := buildMatcher(n.Dict)
		if err != nil {
			return nil, err
		}
		c.normalization = append(c.normalization, m)
	}
	if cfg.Segmentation != nil {
		if cfg.Segmentation.Type != "mmseg" {
			return nil, fmt.Errorf("zhconv: 不支援的 segmentation %q", cfg.Segmentation.Type)
		}
		if c.segmentation, err = buildMatcher(cfg.Segmentation.Dict); err != nil {
			return nil, err
		}
	}
	for _, s := range cfg.ConversionChain {
		m, err := buildMatcher(s.Dict)
		if err != nil {
			return nil, err
		}
		c.chain = append(c.chain, m)
	}
	if len(c.chain) == 0 {
		return nil, fmt.Errorf("zhconv: %s.json 沒有 conversion_chain", t)
	}
	return c, nil
}

// replace 以最長前綴匹配逐位置取代；沒有匹配的字元原樣輸出。
func replace(m matcher, s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if l, v, ok := m.match(s[i:]); ok && l > 0 {
			b.WriteString(v)
			i += l
			continue
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+w])
		i += w
	}
	return b.String()
}

// segment 是上游 MaxMatchSegmentation：命中字典的詞自成一段，沒有命中的連續字元合成一段。
func segment(m matcher, s string) []string {
	var segs []string
	start := 0 // 目前沒有命中的片段的起點
	for i := 0; i < len(s); {
		l, _, ok := m.match(s[i:])
		if !ok || l == 0 {
			_, w := utf8.DecodeRuneInString(s[i:])
			i += w
			continue
		}
		if i > start {
			segs = append(segs, s[start:i])
		}
		segs = append(segs, s[i:i+l])
		i += l
		start = i
	}
	if start < len(s) {
		segs = append(segs, s[start:])
	}
	return segs
}

func (c *converter) convert(s string) string {
	for _, n := range c.normalization {
		s = replace(n, s)
	}
	segs := []string{s}
	if c.segmentation != nil {
		segs = segment(c.segmentation, s)
	}
	// 每個轉換步驟逐段套用：段落邊界在整條 chain 中維持不變（與上游一致）。
	for _, step := range c.chain {
		for i, seg := range segs {
			segs[i] = replace(step, seg)
		}
	}
	return strings.Join(segs, "")
}

type lazyConverter struct {
	once sync.Once
	c    *converter
	err  error
}

var converters sync.Map // Table → *lazyConverter

// Convert 依轉換表轉換 s。字典在第一次使用時才載入，之後可以並行呼叫。
func Convert(t Table, s string) (string, error) {
	if !Valid(t) {
		return "", fmt.Errorf("%w：%q", ErrUnknownTable, string(t))
	}
	v, _ := converters.LoadOrStore(t, &lazyConverter{})
	lc := v.(*lazyConverter)
	lc.once.Do(func() { lc.c, lc.err = newConverter(t) })
	if lc.err != nil {
		return "", lc.err
	}
	return lc.c.convert(s), nil
}
