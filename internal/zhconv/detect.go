package zhconv

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// 字形判斷用的字集：只在 TSCharacters 鍵中、而且會被轉成別的字的是繁體專用字，只在 STCharacters 鍵中、
// 而且會被轉成別的字的是簡體專用字；兩邊都有的字（簡繁同形但各自有對應）不參與判斷。
type scriptSet struct{ hant, hans map[rune]bool }

var scriptSets = sync.OnceValues(func() (scriptSet, error) {
	ts, err := loadDict("TSCharacters.txt")
	if err != nil {
		return scriptSet{}, err
	}
	st, err := loadDict("STCharacters.txt")
	if err != nil {
		return scriptSet{}, err
	}
	hant, hans := map[rune]bool{}, map[rune]bool{}
	// 轉換結果與自己相同的字（只、回、家、了：簡繁同形，只是另有其他對應）沒有區分能力，不參與判斷。
	for k, v := range ts.m {
		if r, n := utf8.DecodeRuneInString(k); n == len(k) && v != k {
			if _, both := st.m[k]; !both {
				hant[r] = true
			}
		}
	}
	for k, v := range st.m {
		if r, n := utf8.DecodeRuneInString(k); n == len(k) && v != k {
			if _, both := ts.m[k]; !both {
				hans[r] = true
			}
		}
	}
	return scriptSet{hant, hans}, nil
})

// DetectScript 依字元判斷文字是簡體還是繁體：回傳 "Hans" 或 "Hant"。
// 可以區分的字少於 3 個、或較多的一方不到另一方的 2 倍時回傳 ""（無法判斷）。
func DetectScript(text string) string {
	sets, err := scriptSets()
	if err != nil {
		return ""
	}
	var nT, nS int
	for _, r := range text {
		switch {
		case sets.hant[r]:
			nT++
		case sets.hans[r]:
			nS++
		}
	}
	switch {
	case nT+nS < 3:
		return ""
	case nT >= 2*nS:
		return "Hant"
	case nS >= 2*nT:
		return "Hans"
	}
	return ""
}

// DetectScriptOf 同 DetectScript，輸入是多段文字（例如所有 cue），不必先串成一個字串。
func DetectScriptOf(parts []string) string {
	return DetectScript(strings.Join(parts, "\n"))
}
