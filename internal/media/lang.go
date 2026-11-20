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

// IsTextSubtitle 回報字幕 codec 是否為文字格式（可轉成 SRT）；點陣字幕（hdmv_pgs_subtitle、dvd_subtitle、dvb_subtitle）不是。
func IsTextSubtitle(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "ass", "ssa", "mov_text", "webvtt", "text":
		return true
	}
	return false
}
