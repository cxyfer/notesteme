package groq

import "testing"

func TestLangHint(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "zh-Hans": "zh", "ja": "ja", "yue": "yue", "pt-BR": "pt",
		"": "", "und": "", "fil": "", "xx": "",
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
	if (&Client{}).Configured() || !(&Client{APIKey: "k"}).Configured() {
		t.Error("Configured 應依 API key 判斷")
	}
}
