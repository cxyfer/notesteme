package zhconv

import (
	"embed"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// 內嵌的 OpenCC 設定與字典（原封不動取自上游 release，見 opencc/NOTICE.md）。
//
//go:embed opencc
var resources embed.FS

const resourceDir = "opencc/"

// matcher 是一個字典（或字典群組）的最長前綴匹配介面。
type matcher interface {
	// match 在 s 的開頭找最長的鍵；回傳鍵的位元組長度與第一個候選。
	match(s string) (keyLen int, val string, ok bool)
}

// dict 是一份文字字典：鍵對第一個候選，並記錄最長鍵的 rune 數（限制比對時要試的長度）。
type dict struct {
	m       map[string]string
	maxRune int
}

// parseDict 解析字典：每行 `鍵\t候選1 候選2…`，只取第一個候選；空行略過。
// 同一個鍵重複出現時保留第一筆。
func parseDict(name string, data []byte) (*dict, error) {
	d := &dict{m: make(map[string]string, len(data)/12)}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		key, rest, ok := strings.Cut(line, "\t")
		if !ok || key == "" {
			return nil, fmt.Errorf("zhconv: %s 第 %d 行格式不正確", name, i+1)
		}
		val, _, _ := strings.Cut(rest, " ")
		if _, dup := d.m[key]; dup {
			continue
		}
		d.m[key] = val
		if n := utf8.RuneCountInString(key); n > d.maxRune {
			d.maxRune = n
		}
	}
	return d, nil
}

func (d *dict) match(s string) (int, string, bool) {
	// 先收集前 maxRune 個 rune 的結尾位置，再由長到短查表。
	var buf [32]int
	ends := buf[:0]
	pos := 0
	for pos < len(s) && len(ends) < d.maxRune {
		_, w := utf8.DecodeRuneInString(s[pos:])
		pos += w
		ends = append(ends, pos)
	}
	for i := len(ends) - 1; i >= 0; i-- {
		if v, ok := d.m[s[:ends[i]]]; ok {
			return ends[i], v, true
		}
	}
	return 0, "", false
}

// group 是字典群組：short_circuit 取第一個有匹配的字典，union 取所有字典中最長的匹配（同長取先出現的）。
type group struct {
	union bool
	ms    []matcher
}

func (g *group) match(s string) (int, string, bool) {
	var bestLen int
	var bestVal string
	found := false
	for _, m := range g.ms {
		l, v, ok := m.match(s)
		if !ok {
			continue
		}
		if !g.union {
			return l, v, true
		}
		if !found || l > bestLen {
			bestLen, bestVal, found = l, v, true
		}
	}
	return bestLen, bestVal, found
}

// lazyDict 讓每份字典只解析一次，之後可以並行讀取。
type lazyDict struct {
	once sync.Once
	d    *dict
	err  error
}

var dictCache sync.Map // 檔名 → *lazyDict

func loadDict(file string) (*dict, error) {
	v, _ := dictCache.LoadOrStore(file, &lazyDict{})
	ld := v.(*lazyDict)
	ld.once.Do(func() {
		data, err := resources.ReadFile(resourceDir + file)
		if err != nil {
			ld.err = fmt.Errorf("zhconv: 讀取字典 %s：%w", file, err)
			return
		}
		ld.d, ld.err = parseDict(file, data)
	})
	return ld.d, ld.err
}
