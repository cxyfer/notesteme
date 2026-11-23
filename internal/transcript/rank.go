package transcript

// RuleIndex 回傳平台字幕軌 t 在評估順序中第一條符合的 caption_priority 規則（從 0 起算），
// 用來比較兩個來源的先後（SRC-R18、R19：「更好」是評估順序排在現行來源之前）。
// 規則的比對與 SelectFor 相同：original 以 originalLang 代入（不知道時那一條不符合），
// kind 為 auto 的規則在 allowTranslated 時也接受平台翻譯軌（SRC-R2）。沒有任何規則符合時回傳 len(rules)。
func RuleIndex(t Track, originalLang string, rules []Rule, allowTranslated bool) int {
	for i, rule := range rules {
		lang := rule.Lang
		if lang == OriginalLang {
			if originalLang == "" {
				continue
			}
			lang = originalLang
		}
		kindOK := t.Kind == rule.Kind || (rule.Kind == KindAuto && allowTranslated && t.Kind == KindPlatformTranslated)
		if kindOK && rankLang(lang, t.Lang) > rankNone {
			return i
		}
	}
	return len(rules)
}
