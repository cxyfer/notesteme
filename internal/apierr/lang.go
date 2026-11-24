package apierr

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// 錯誤訊息的語言（NFR-I18N-2）：預設繁中，另有简中與 English。code 永遠不隨語言改變，程式只依 code 判斷。
const (
	LangZhHant = "zh-Hant"
	LangZhHans = "zh-Hans"
	LangEn     = "en"
)

// Negotiate 依 Accept-Language（RFC 9110 第 12.5.4 節，含 q 值）選出錯誤訊息的語言；沒有標頭、都不支援或格式不正確時是繁中。
//
// 對應：zh-Hant、zh-TW、zh-HK、zh-MO → 繁中；zh-Hans、zh-CN、zh-SG、zh-MY → 简中；只有 zh 時是繁中（預設語言）；
// en 與 en-* → English；* 代表「任何語言」，取預設。q 相同時依出現的順序；q=0 代表不接受。
func Negotiate(header string) string {
	type cand struct {
		lang string
		q    float64
		pos  int
	}
	var cands []cand
	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil || f < 0 || f > 1 {
					f = 0 // 不正確的 q 值：當成不接受，不猜
				}
				q = f
			}
		}
		if q <= 0 {
			continue
		}
		if l := langOf(tag); l != "" {
			cands = append(cands, cand{l, q, i})
		}
	}
	if len(cands) == 0 {
		return LangZhHant
	}
	slices.SortStableFunc(cands, func(a, b cand) int {
		switch {
		case a.q > b.q:
			return -1
		case a.q < b.q:
			return 1
		}
		return a.pos - b.pos
	})
	return cands[0].lang
}

// langOf 把一個語言標籤（小寫）對應到支援的語言；不支援時是空字串。
func langOf(tag string) string {
	switch {
	case tag == "*":
		return LangZhHant
	case tag == "en" || strings.HasPrefix(tag, "en-"):
		return LangEn
	case tag == "zh":
		return LangZhHant
	case strings.HasPrefix(tag, "zh-"):
		for _, sub := range strings.Split(tag, "-")[1:] {
			switch sub {
			case "hant", "tw", "hk", "mo":
				return LangZhHant
			case "hans", "cn", "sg", "my":
				return LangZhHans
			}
		}
		return LangZhHant
	}
	return ""
}

// LangFrom 是請求的錯誤訊息語言。
func LangFrom(r *http.Request) string { return Negotiate(r.Header.Get("Accept-Language")) }
