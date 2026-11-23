package entry

import (
	"strings"
	"testing"
)

func TestShareURL(t *testing.T) {
	long := "https://a.example.com/" + strings.Repeat("x", MaxURLChars-len("https://a.example.com/"))
	cases := []struct {
		name             string
		url, text, title string
		want             string
	}{
		{name: "url 參數", url: "https://youtu.be/abc123xxxxx", want: "https://youtu.be/abc123xxxxx"},
		{name: "url 參數前後空白", url: "  https://youtu.be/abc  ", want: "https://youtu.be/abc"},
		{name: "url 參數的 scheme 不分大小寫", url: "HTTPS://youtu.be/abc", want: "HTTPS://youtu.be/abc"},
		{name: "text 中的網址", text: "Watch this: https://youtu.be/abc123xxxxx 👍", title: "Kafka", want: "https://youtu.be/abc123xxxxx"},
		{name: "全形括號", text: "看這個（https://www.bilibili.com/video/BV1co411c7mA）", want: "https://www.bilibili.com/video/BV1co411c7mA"},
		{name: "第一個網址並去掉句點", text: "first https://a.example.com/x. then https://b.example.com/y", want: "https://a.example.com/x"},
		{name: "javascript 的 url 參數改看 text", url: "javascript:alert(1)", text: "https://youtu.be/abc123xxxxx", want: "https://youtu.be/abc123xxxxx"},
		{name: "title 中的網址", title: "https://youtu.be/abc123xxxxx", want: "https://youtu.be/abc123xxxxx"},
		{name: "text 優先於 title", text: "https://a.example.com/t", title: "https://b.example.com/t", want: "https://a.example.com/t"},
		{name: "反覆去掉結尾標點", text: "見 https://a.example.com/x?y=1).」』）。，、!?;:]}", want: "https://a.example.com/x?y=1"},
		{name: "引號與角括號是邊界", text: `<a href="https://a.example.com/q">x</a>`, want: "https://a.example.com/q"},
		{name: "全形空白是邊界", text: "https://a.example.com/x　後面的字", want: "https://a.example.com/x"},
		{name: "NBSP 是邊界", text: "https://a.example.com/x more", want: "https://a.example.com/x"},
		{name: "只有 scheme 的候選略過", text: "http://. 和 https://ok.example.com/", want: "https://ok.example.com/"},
		{name: "剛好上限", text: long, want: long},
		{name: "超過上限的 url 參數改看 text", url: long + "x", text: "https://ok.example.com/", want: "https://ok.example.com/"},
		{name: "超過上限的候選略過", text: long + "x https://ok.example.com/", want: "https://ok.example.com/"},
		{name: "url 參數含空白", url: "https://a.example.com/a b", text: "", want: ""},
		{name: "沒有網址", text: "just some words", title: "Hello", want: ""},
		{name: "ftp 不算", text: "ftp://a.example.com/x", want: ""},
		{name: "data URL 不算", url: "data:text/html,<script>", want: ""},
		{name: "全空", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ShareURL(c.url, c.text, c.title)
			if got != c.want || ok != (c.want != "") {
				t.Errorf("ShareURL(%q, %q, %q) = %q, %v；want %q", c.url, c.text, c.title, got, ok, c.want)
			}
		})
	}
}

func TestValidURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://youtu.be/abc":       true,
		"http://a.example.com":       true,
		"https://a.example.com/路徑":   true,
		"":                           false,
		"javascript:alert(1)":        false,
		"https://":                   false,
		"https:///path":              false,
		"//a.example.com/x":          false,
		"/add":                       false,
		"file:///etc/passwd":         false,
		"https://a.example.com/\x00": false,
		"https://a.example.com/ x":   false,
		"https://a.example.com/\x7f": false,
		"https://a.example.com/%zz":  false,
		"https://a.example.com/\xff": false,
		"https://a.example.com/" + strings.Repeat("字", MaxURLChars): false,
	} {
		if got := ValidURL(s); got != want {
			t.Errorf("ValidURL(%q) = %v，want %v", s, got, want)
		}
	}
}

func FuzzShareURL(f *testing.F) {
	f.Add("https://youtu.be/x", "", "")
	f.Add("javascript:x", "看（https://a.example.com/x）", "t")
	f.Add("", "http://. https://b", "https://c.example.com")
	f.Fuzz(func(t *testing.T, u, text, title string) {
		got, ok := ShareURL(u, text, title)
		if !ok {
			if got != "" {
				t.Fatalf("沒有網址時回傳了 %q", got)
			}
			return
		}
		if !ValidURL(got) {
			t.Fatalf("ShareURL 回傳了不合格的網址 %q", got)
		}
		if !strings.Contains(u, got) && !strings.Contains(text, got) && !strings.Contains(title, got) {
			t.Fatalf("回傳的網址 %q 不在輸入中", got)
		}
	})
}
