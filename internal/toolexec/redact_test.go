package toolexec

import (
	"strings"
	"testing"
)

func TestRedactCookieLines(t *testing.T) {
	in := "WARNING: some other warning\n" +
		"WARNING: skipping cookie file entry due to invalid expires at 'abc': '.youtube.com\\tTRUE\\t/\\tTRUE\\tabc\\tSID\\tsecret-value'\n" +
		"WARNING: Skipping Cookie File Entry due to something: 'secret-value-2'\r\n" +
		"ERROR: video unavailable"
	got := string(RedactCookieLines([]byte(in)))
	if strings.Contains(got, "secret-value") {
		t.Fatalf("遮蔽後仍含 cookie 的值：%q", got)
	}
	for _, keep := range []string{"WARNING: some other warning", "ERROR: video unavailable"} {
		if !strings.Contains(got, keep) {
			t.Errorf("不相關的行被遮蔽了：%q", got)
		}
	}
	if strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Errorf("行數改變：%q", got)
	}
}

func TestRedactCookieLinesWithoutMatchKeepsInput(t *testing.T) {
	in := []byte("ERROR: HTTP Error 403: Forbidden")
	if got := RedactCookieLines(in); string(got) != string(in) {
		t.Errorf("got %q", got)
	}
	if got := RedactCookieLines(nil); len(got) != 0 {
		t.Errorf("got %q", got)
	}
}
