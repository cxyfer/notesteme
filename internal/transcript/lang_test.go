package transcript

import "testing"

func TestPrimaryLang(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "en-orig": "en", "zh-Hans": "zh", "pt_BR": "pt", " ja ": "ja",
		"": "", "und": "",
		// 別名一律換成同一個代碼。
		"nb": "no", "nn": "no", "nb-NO": "no", "no": "no", "fil": "tl", "iw": "he", "in": "id", "jw": "jv",
	} {
		if got := PrimaryLang(in); got != want {
			t.Errorf("PrimaryLang(%q) = %q，預期 %q", in, got, want)
		}
	}
}
