package groq

import (
	"testing"
)

func TestLangHint(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "zh-Hans": "zh", "ja": "ja", "pt-BR": "pt",
		// 語言提示只接受兩個字母的 ISO-639-1：三字母代碼（粵語 yue、夏威夷語 haw）不送，讓 Whisper 自行偵測。
		"yue": "", "haw": "",
		// 別名換成 Whisper 認得的代碼。
		"fil": "tl", "iw": "he", "nb": "no", "nn": "no", "in": "id", "jw": "jv",
		"": "", "und": "", "xx": "",
	} {
		if got := LangHint(in); got != want {
			t.Errorf("LangHint(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestEndpointResolvesDefaults(t *testing.T) {
	base, model := (&Client{}).Endpoint()
	if base != DefaultBaseURL || model != DefaultModel {
		t.Errorf("Endpoint = %q, %q", base, model)
	}
	base, model = (&Client{BaseURL: "https://x.test/v1", Model: "m"}).Endpoint()
	if base != "https://x.test/v1" || model != "m" {
		t.Errorf("Endpoint = %q, %q", base, model)
	}
	if mustConfigured(t, &Client{}) || !mustConfigured(t, &Client{APIKey: "k"}) {
		t.Error("Configured 應依 API key 判斷")
	}
}
