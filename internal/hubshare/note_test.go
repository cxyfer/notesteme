package hubshare

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestStripNote(t *testing.T) {
	md := "---\ntitle: \"Talk\"\nsource: \"https://youtu.be/x\"\nstatus: \"not-started\"\nread_at: \"\"\nreading_status: \"⚪ 待閱讀\"\n" +
		"notesteme_id: \"0193a3b4-0000-7000-8000-000000000001\"\nnotesteme_status: \"complete\"\n\"Notesteme_Engine\": gemini\n" +
		"notesteme_extra:\n  - a\n  - b\ntags:\n  - kernel\n---\n# Talk\n\n[讀](obsidian://open?x)\n\nstatus: 正文裡的字不動\n"
	got := StripNote(md, "[讀](obsidian://open?x)", "", "   ")
	for _, bad := range []string{"notesteme", "Notesteme", "reading_status", "read_at", "\nstatus:", "  - a", "obsidian://"} {
		if strings.Contains(strings.SplitN(got, "# Talk", 2)[0], bad) || (bad == "obsidian://" && strings.Contains(got, bad)) {
			t.Errorf("留下了 %q：\n%s", bad, got)
		}
	}
	for _, keep := range []string{"---\ntitle: \"Talk\"\nsource:", "tags:\n  - kernel\n---\n# Talk", "status: 正文裡的字不動"} {
		if !strings.Contains(got, keep) {
			t.Errorf("少了 %q：\n%s", keep, got)
		}
	}
	// CRLF。
	crlf := strings.ReplaceAll(md, "\n", "\r\n")
	if g := StripNote(crlf); strings.Contains(g, "notesteme_id") || !strings.HasPrefix(g, "---\r\ntitle:") {
		t.Errorf("CRLF：\n%q", g)
	}
	// 沒有 frontmatter、沒有結尾的 ---：本文原樣（只刪 read button）。
	for _, in := range []string{"# Only body\nnotesteme_id: x\n", "---\nnotesteme_id: x\nno end"} {
		if g := StripNote(in); g != in {
			t.Errorf("StripNote(%q) = %q", in, g)
		}
	}
	if g := StripNote("body BTN", "BTN"); g != "body " {
		t.Errorf("read button = %q", g)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, d := range files {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate})
		_, _ = w.Write([]byte(d))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestScan(t *testing.T) {
	contains := func(hay []byte, n string) bool { return bytes.Contains(hay, []byte(n)) }
	n := Needles{Secrets: []string{"sekrit-token-123"}, Local: []string{"item-id-1"}, Paths: []string{"/data"}, ContainsPath: contains}
	// 壓縮過的項目也要找到（直接對 zip 的 bytes 做 Contains 找不到）。
	for name, c := range map[string]struct {
		files       map[string]string
		entry, what string
	}{
		"secret 在字幕":   {map[string]string{"manifest.json": "{}", "artifacts/a.json": strings.Repeat("x", 4096) + "sekrit-token-123"}, "artifacts/a.json", "secret"},
		"本機 ID 在筆記":    {map[string]string{"manifest.json": "{}", "artifacts/n.md": "id: item-id-1"}, "artifacts/n.md", "local"},
		"路徑在 manifest": {map[string]string{"manifest.json": `{"p":"/data/x"}`}, "manifest.json", "path"},
	} {
		raw := zipOf(t, c.files)
		if bytes.Contains(raw, []byte("sekrit-token-123")) && c.what == "secret" {
			t.Fatalf("%s：測試資料要是壓縮過的", name)
		}
		l, err := Scan(raw, n)
		if err != nil || l == nil || l.Entry != c.entry || l.What != c.what {
			t.Errorf("%s：%+v %v", name, l, err)
		}
	}
	// 字幕本文提到 /data 不算（使用者的內容）。
	if l, err := Scan(zipOf(t, map[string]string{"manifest.json": "{}", "artifacts/a.json": "see /data"}), n); l != nil || err != nil {
		t.Errorf("本文的路徑：%+v %v", l, err)
	}
	if _, err := Scan([]byte("not zip"), n); err == nil {
		t.Error("不是 zip 要回錯誤")
	}
}
