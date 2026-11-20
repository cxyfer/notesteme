package media

import "strings"

// iso639ToBCP47 把 ISO 639-2 的 B 與 T 碼對照到 BCP-47 的主語言子標籤。
var iso639ToBCP47 = map[string]string{
	"eng": "en",
	"jpn": "ja",
	"zho": "zh", "chi": "zh",
	"kor": "ko",
	"fra": "fr", "fre": "fr",
	"deu": "de", "ger": "de",
	"spa": "es",
	"ita": "it",
	"por": "pt",
	"rus": "ru",
	"ara": "ar",
	"hin": "hi",
	"tha": "th",
	"vie": "vi",
	"ind": "id",
	"nld": "nl", "dut": "nl",
	"pol": "pl",
	"tur": "tr",
	"ukr": "uk",
	"swe": "sv",
	"nor": "no",
	"dan": "da",
	"fin": "fi",
	"ces": "cs", "cze": "cs",
	"ell": "el", "gre": "el",
	"heb": "he",
	"hun": "hu",
	"ron": "ro", "rum": "ro",
}

// BCP47 把容器的語言標記（通常是 ISO 639-2 三字母碼）轉成 BCP-47 主語言。
// 兩字母碼原樣回傳；und、未知與空白回傳 ""。
func BCP47(iso639 string) string {
	s := strings.TrimSpace(iso639)
	switch len(s) {
	case 2:
		return s
	case 3:
		return iso639ToBCP47[strings.ToLower(s)]
	}
	return ""
}

// CanonicalLang 把外掛字幕檔名或容器標記中的語言碼轉成 BCP-47 的標準大小寫：
// 主語言小寫（三字母的 ISO 639-2 碼經 BCP47 對照，例如 eng → en；沒有對照的三字母碼維持小寫），
// script 首字大寫（hant → Hant），region 大寫（tw → TW、419 不變），其餘子標籤小寫。
// 分隔符 _ 視同 -。und、mul、zxx 等「沒有語言」的標記與空白回傳 ""。
func CanonicalLang(tag string) string {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"), "-")
	primary := strings.ToLower(parts[0])
	switch len(primary) {
	case 2:
	case 3:
		if m := BCP47(primary); m != "" {
			primary = m
		} else if undetermined[primary] || (primary >= "qaa" && primary <= "qtz") {
			return ""
		}
	default:
		return ""
	}
	out := []string{primary}
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		p = strings.ToLower(p)
		switch {
		case len(p) == 4 && isAlpha(p):
			p = strings.ToUpper(p[:1]) + p[1:]
		case len(p) == 2 && isAlpha(p):
			p = strings.ToUpper(p)
		}
		out = append(out, p)
	}
	return strings.Join(out, "-")
}

// undetermined 是 ISO 639-2 中表示「沒有或無法判斷語言」的碼。
var undetermined = map[string]bool{"und": true, "mul": true, "zxx": true, "mis": true}

func isAlpha(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// IsTextSubtitle回報字幕 codec 是否為文字格式（可轉成 SRT）；點陣字幕（hdmv_pgs_subtitle、dvd_subtitle、dvb_subtitle）不是。
func IsTextSubtitle(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "ass", "ssa", "mov_text", "webvtt", "text":
		return true
	}
	return false
}
