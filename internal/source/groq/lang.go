package groq

import (
	"regexp"
	"strings"
)

// whisperLanguages 是 Whisper 回報的語言全名（小寫）對應到 BCP-47 的表，
// 內容為 Whisper 支援的語言（依 openai/whisper 的 tokenizer.LANGUAGES）。
// 中文一律對應 zh（Whisper 不分簡繁）；粵語為 yue。
var whisperLanguages = map[string]string{
	"afrikaans": "af", "albanian": "sq", "amharic": "am", "arabic": "ar", "armenian": "hy",
	"assamese": "as", "azerbaijani": "az", "bashkir": "ba", "basque": "eu", "belarusian": "be",
	"bengali": "bn", "bosnian": "bs", "breton": "br", "bulgarian": "bg", "burmese": "my",
	"cantonese": "yue", "castilian": "es", "catalan": "ca", "chinese": "zh", "croatian": "hr",
	"czech": "cs", "danish": "da", "dutch": "nl", "english": "en", "estonian": "et",
	"faroese": "fo", "finnish": "fi", "flemish": "nl", "french": "fr", "galician": "gl",
	"georgian": "ka", "german": "de", "greek": "el", "gujarati": "gu", "haitian": "ht",
	"haitian creole": "ht", "hausa": "ha", "hawaiian": "haw", "hebrew": "he", "hindi": "hi",
	"hungarian": "hu", "icelandic": "is", "indonesian": "id", "italian": "it", "japanese": "ja",
	"javanese": "jv", "kannada": "kn", "kazakh": "kk", "khmer": "km", "korean": "ko",
	"lao": "lo", "latin": "la", "latvian": "lv", "letzeburgesch": "lb", "lingala": "ln",
	"lithuanian": "lt", "luxembourgish": "lb", "macedonian": "mk", "malagasy": "mg", "malay": "ms",
	"malayalam": "ml", "maltese": "mt", "maori": "mi", "marathi": "mr", "moldavian": "ro",
	"moldovan": "ro", "mongolian": "mn", "myanmar": "my", "nepali": "ne", "norwegian": "no",
	"nynorsk": "nn", "occitan": "oc", "panjabi": "pa", "pashto": "ps", "persian": "fa",
	"polish": "pl", "portuguese": "pt", "punjabi": "pa", "pushto": "ps", "romanian": "ro",
	"russian": "ru", "sanskrit": "sa", "serbian": "sr", "shona": "sn", "sindhi": "sd",
	"sinhala": "si", "sinhalese": "si", "slovak": "sk", "slovenian": "sl", "somali": "so",
	"spanish": "es", "sundanese": "su", "swahili": "sw", "swedish": "sv", "tagalog": "tl",
	"tajik": "tg", "tamil": "ta", "tatar": "tt", "telugu": "te", "thai": "th",
	"tibetan": "bo", "turkish": "tr", "turkmen": "tk", "ukrainian": "uk", "urdu": "ur",
	"uzbek": "uz", "valencian": "ca", "vietnamese": "vi", "welsh": "cy", "yiddish": "yi",
	"yoruba": "yo",
}

// langCode 比對已經是語言代碼的值：2 到 3 個字母，可接 BCP-47 的子標籤（例如 zh-TW）。
var langCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// BCP47 把 Whisper 回報的語言轉成 BCP-47：接受全名（english、Japanese）與已經是代碼的值（en、zh-TW）。
// 查不到的全名回傳空字串（不猜測），由呼叫端改用其他語言偵測方式（SRC-R11）。
func BCP47(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	if code, ok := whisperLanguages[s]; ok {
		return code
	}
	// 全名都在上面的表中；其餘只接受看起來像代碼的值。
	if langCode.MatchString(s) {
		// 子標籤維持原本的大小寫（zh-TW），主語言小寫。
		raw := strings.TrimSpace(name)
		primary, rest, _ := strings.Cut(raw, "-")
		if rest == "" {
			return strings.ToLower(primary)
		}
		return strings.ToLower(primary) + "-" + rest
	}
	return ""
}

// whisperCodes 是 Whisper 支援的語言代碼集合。
var whisperCodes = func() map[string]bool {
	m := make(map[string]bool, len(whisperLanguages))
	for _, code := range whisperLanguages {
		m[code] = true
	}
	return m
}()

// LangHint 把影音的原文語言（BCP-47）轉成要送給 Whisper 的語言提示：只取主要語言，
// 且必須是 Whisper 支援的代碼；未知（空字串、und）或不支援時回傳空字串，讓 Whisper 自行偵測。
func LangHint(lang string) string {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")
	if whisperCodes[primary] {
		return primary
	}
	return ""
}
