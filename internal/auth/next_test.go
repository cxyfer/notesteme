package auth

import (
	"strings"
	"testing"
)

func TestSafeNext(t *testing.T) {
	long := "/" + strings.Repeat("a", MaxNextBytes-1)
	cases := []struct {
		in, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/library", "/library"},
		{"/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx", "/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx"},
		{"/items/yt:abc#frag", "/items/yt:abc#frag"},
		{long, long},
		{long + "a", "/"},
		// 開放轉址
		{"https://evil.example.com/", "/"},
		{"//evil.example.com/x", "/"},
		{`/\evil.example.com/x`, "/"},
		{`/x\y`, "/"},
		{"javascript:alert(1)", "/"},
		{"evil.example.com/x", "/"},
		{"///evil.example.com", "/"},
		{"/%2F%2Fevil.example.com", "/"},
		{"/%2fevil.example.com", "/"},
		// 控制字元
		{"/\tevil", "/"},
		{"/\x00", "/"},
		{"/a\x7f", "/"},
		{"/a\r\nSet-Cookie: x", "/"},
		// 無法解析
		{"/%zz", "/"},
		{" /library", "/"},
	}
	for _, c := range cases {
		if got := SafeNext(c.in); got != c.want {
			t.Errorf("SafeNext(%q) = %q，want %q", c.in, got, c.want)
		}
	}
}

func FuzzSafeNext(f *testing.F) {
	for _, s := range []string{"/", "//x", "/\\x", "https://x", "/add?url=x", "/%2F%2Fx"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := SafeNext(s)
		if got == "/" {
			return
		}
		if got != s {
			t.Fatalf("SafeNext 改寫了輸入：%q → %q", s, got)
		}
		if len(got) < 1 || got[0] != '/' || (len(got) > 1 && (got[1] == '/' || got[1] == '\\')) || strings.ContainsAny(got, "\\") {
			t.Fatalf("SafeNext(%q) 放行了不安全的值", s)
		}
	})
}
