package vocab

import "testing"

func TestParse(t *testing.T) {
	md := "# 標題\n" +
		"| 步驟 | 意思 |\n" +
		"|---|---|\n" +
		"| `Given the clock is at {string}` | fake clock |\n" +
		"| `When the owner sends {string} with:` | 帶 body |\n" +
		"| `Then the response status is {int}` | 狀態碼 |\n" +
		"| `And not a step` | 不是句型 |\n" +
		"  | `Given indented` | 不是句型 |\n" +
		"| not `Given quoted` | 不是句型 |\n"

	got := Parse(md)
	want := []Pattern{
		{"Given", "the clock is at {string}"},
		{"When", "the owner sends {string} with:"},
		{"Then", "the response status is {int}"},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Parse()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if got[0].Full() != "Given the clock is at {string}" {
		t.Errorf("Full() = %q", got[0].Full())
	}
}

func TestTemplatesDeduplicatesKeywords(t *testing.T) {
	set := Templates([]Pattern{
		{"Given", "the {string} transcript of {string} is:"},
		{"Then", "the {string} transcript of {string} is:"},
		{"When", "the worker runs until idle"},
	})
	if len(set) != 2 {
		t.Errorf("len = %d, want 2: %v", len(set), set)
	}
}

func TestCompile(t *testing.T) {
	tests := []struct {
		template string
		text     string
		want     bool
		groups   []string
	}{
		{`the clock is at {string}`, `the clock is at "2026-11-12T10:00:00Z"`, true, []string{"2026-11-12T10:00:00Z"}},
		{`the clock is at {string}`, `the clock is at 2026`, false, nil},
		{`the clock is at {string}`, `the clock is at "a" and "b"`, false, nil},
		{`the clock is at {string}`, `x the clock is at "a"`, false, nil},
		{`the clock is at {string}`, `the clock is at "a" trailing`, false, nil},
		{`the response status is {int}`, `the response status is 404`, true, []string{"404"}},
		{`the response status is {int}`, `the response status is -1`, true, []string{"-1"}},
		{`the response status is {int}`, `the response status is abc`, false, nil},
		{`the command ran in {word} mode`, `the command ran in client mode`, true, []string{"client"}},
		{`the command ran in {word} mode`, `the command ran in two words mode`, false, nil},
		{`the library contains {int} items`, `the library contains 2 items`, true, []string{"2"}},
		{`the library contains {int} items`, `the library contains 1 item`, true, []string{"1"}},
		{`the library contains {int} items`, `the library contains 1 itemss`, false, nil},
		{`the source {string} was called {int} times`, `the source "x" was called 1 time`, true, []string{"x", "1"}},
		{`the source {string} was called {int} times`, `the source "x" was called 3 times`, true, []string{"x", "3"}},
		{`{int} tasks of {string} are running`, `1 task of "cpu" are running`, true, []string{"1", "cpu"}},
		{`{int} tasks of {string} are running`, `2 tasks of "cpu" are running`, true, []string{"2", "cpu"}},
		// 單數與複數的差別只在 {int} 之後的名詞；其他位置的 s 不能省略。
		{`the {string} chunks of {string} are:`, `the "a" chunk of "b" are:`, false, nil},
		// 動詞不是名詞，s 不能省略。
		{`migration {int} fails`, `migration 2 fail`, false, nil},
		{`migration {int} fails`, `migration 2 fails`, true, []string{"2"}},
		{`the prompt {string} version {int} is:`, `the prompt "p" version 2 i:`, false, nil},
		// 字面文字中的 regexp 特殊字元要逐字比對。
		{`the file {string} has the mode {string}`, `the file "a.b" has the mode "0600"`, true, []string{"a.b", "0600"}},
		{`yt-dlp {string} is installed`, `yt-dlp "2025" is installed`, true, []string{"2025"}},
		{`a (b) {int}`, `a (b) 1`, true, []string{"1"}},
		{`a (b) {int}`, `a b 1`, false, nil},
	}
	for _, tt := range tests {
		m := Compile(tt.template).FindStringSubmatch(tt.text)
		if (m != nil) != tt.want {
			t.Errorf("Compile(%q) on %q: match = %v, want %v", tt.template, tt.text, m != nil, tt.want)
			continue
		}
		if !tt.want {
			continue
		}
		if len(m)-1 != len(tt.groups) {
			t.Errorf("Compile(%q) on %q: groups = %q, want %q", tt.template, tt.text, m[1:], tt.groups)
			continue
		}
		for i, g := range tt.groups {
			if m[i+1] != g {
				t.Errorf("Compile(%q) on %q: group %d = %q, want %q", tt.template, tt.text, i, m[i+1], g)
			}
		}
	}
}
