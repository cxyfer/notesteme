package notes

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func sampleInput() Input {
	return Input{
		Front:    Frontmatter{Title: "Writing a Toy Kernel", Source: "https://www.youtube.com/watch?v=note01xxxxx", Channel: "Kernel Hackers", VideoPublished: "2025-08-09", ItemID: "01ABC", Status: StatusComplete},
		Title:    "Writing a Toy Kernel",
		Channel:  "Kernel Hackers",
		URL:      "https://www.youtube.com/watch?v=note01xxxxx",
		Platform: "YouTube",
		Lang:     "zh-Hant-TW", DurationMS: 600_000,
		Linker:   Linker{Platform: PlatformYouTube, ID: "note01xxxxx"},
		CueStart: map[int]int64{1: 5000, 2: 20000, 3: 150000, 4: 300000, 5: 540000},
		Frames:   map[int]Frame{1: {AtMS: 20000, Caption: "Toolchain diagram"}, 3: {AtMS: 300000, Caption: "Boot sequence"}},
		Sections: []Section{
			{Level: 2, Title: "Setting up the toolchain", Cue: 2, Body: "We install a cross compiler first.", Frames: []int{1}},
			{Level: 3, Title: "The boot loader", Cue: 4, Body: "It hands control to the kernel [[cue:5]].", Frames: []int{3}},
		},
		SelfCheck: []string{"What does the boot loader do?"},
	}
}

func render(t *testing.T, in Input, body string) (string, []Check) {
	t.Helper()
	md, err := Render(body, BuildData(in))
	if err != nil {
		t.Fatal(err)
	}
	return md, RunChecks(md, CheckOptions{Images: in.Images(), Title: in.Title})
}

func TestRenderBuiltinTemplatePassesEveryCheck(t *testing.T) {
	md, checks := render(t, sampleInput(), BuiltinTemplateBody)
	for _, want := range []string{
		"> [!info] 影片資訊",
		"## Setting up the toolchain · [00:20](https://www.youtube.com/watch?v=note01xxxxx&t=20s)",
		"### The boot loader · [05:00](https://www.youtube.com/watch?v=note01xxxxx&t=300s)",
		"[09:00](https://www.youtube.com/watch?v=note01xxxxx&t=540s)",
		"![[Writing a Toy Kernel - 00-20 - frame_0001.jpg]]\n*00:20 — Toolchain diagram*",
		"## 自我檢驗\n- What does the boot loader do?",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("筆記沒有 %q：\n%s", want, md)
		}
	}
	if strings.Contains(md, "[[cue:") {
		t.Errorf("cue 引用沒有換掉：\n%s", md)
	}
	if !Passed(checks) || len(checks) != 5 {
		t.Fatalf("檢查應全部通過：%+v\n%s", checks, md)
	}
}

func TestClockAndLinks(t *testing.T) {
	cases := map[int64]string{0: "00:00", 5999: "00:05", 540_000: "09:00", 3_599_999: "59:59", 5_400_000: "1:30:00", -5: "00:00"}
	for ms, want := range cases {
		if got := Clock(ms); got != want {
			t.Errorf("Clock(%d) = %q，預期 %q", ms, got, want)
		}
	}
	if got := (Linker{Platform: PlatformBilibili, ID: "BV1nt4y1c7mD", Part: 1}).Anchor(5000); got != " · [00:05](https://www.bilibili.com/video/BV1nt4y1c7mD?p=1&t=5)" {
		t.Errorf("bilibili：%q", got)
	}
	if got := (Linker{Platform: PlatformLocal}).Anchor(5000); got != " · 00:05" {
		t.Errorf("local：%q", got)
	}
	if got := (Linker{Platform: PlatformPodcast, AudioURL: "https://cdn.example.com/ep (1).mp3#old"}).URL(65_000); got != "https://cdn.example.com/ep%20%281%29.mp3#t=65" {
		t.Errorf("podcast：%q", got)
	}
	if got := (Linker{Platform: PlatformPodcast, AudioURL: "javascript:alert(1)"}).URL(1000); got != "" {
		t.Errorf("podcast 的不安全網址應沒有連結：%q", got)
	}
}

func TestSanitizeBodyNeutralisesHostileMarkup(t *testing.T) {
	in := "Read [the docs](https://example.com/docs) or [click here](javascript:alert(1)).\n<script>alert('x')</script>\n## Injected heading\n![[../../Private/diary.md]]\n<% tp.system.command('id') %>\n```dataviewjs\ndv.paragraph(app.vault.adapter.basePath)\n```"
	out := SanitizeBody(in, nil)
	for _, bad := range []string{"<script>", "javascript:", "![[", "```dataviewjs", "<%", "## Injected"} {
		if strings.Contains(out, bad) {
			t.Errorf("仍含 %q：\n%s", bad, out)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "**Injected heading**", "[the docs](https://example.com/docs)", "click here", "``` text"} {
		if !strings.Contains(out, want) {
			t.Errorf("沒有 %q：\n%s", want, out)
		}
	}
}

func TestSanitizeBodyCases(t *testing.T) {
	keep5 := func(n int) bool { return n == 5 }
	cases := []struct{ in, want string }{
		{"see [[cue:5]] and [[cue:6]]", "see [[cue:5]] and cue:6"},
		{"![[cue:5]]", "cue:5"},
		{"[[[[nested]]]]", "nested"},
		{"[[ [[cue:5]] ]]", `\[\[ [[cue:5]] \]\]`},
		{"[x](data:text/html,hi)", "x"},
		{"[x](file:///etc/passwd)", "x"},
		{"[x](obsidian://open?vault=a)", "x"},
		{"[mail](mailto:a@b.c)", "[mail](mailto:a@b.c)"},
		{"![img](https://e.com/a.png)", "[img](https://e.com/a.png)"},
		{"![img](javascript:x)", "img"},
		{"[id]: javascript:alert(1)", `\[id]: javascript:alert(1)`},
		{"text\n---", "text\n\n---"},
		{"text\n===", "text\n\n==="},
		{"```\nunclosed <b>", "```\nunclosed &lt;b&gt;\n```"},
		{"~~~ DataViewJS\nx\n~~~", "~~~ text\nx\n~~~"},
		{"inline `= this.file.name` and `$= dv.x`", "inline `\\= this.file.name` and `\\$= dv.x`"},
		{"# Top\n###### six", "**Top**\n**six**"},
		{"a\r\nb\x00c‮d", "a\nb c d"},
	}
	for _, c := range cases {
		if got := SanitizeBody(c.in, keep5); got != c.want {
			t.Errorf("SanitizeBody(%q)\n  得到 %q\n  預期 %q", c.in, got, c.want)
		}
	}
}

func TestQuoteYAMLRoundTrips(t *testing.T) {
	for _, s := range []string{`Kernel: "part 1" #2 --- evil: yes`, "a\nb: c\n---\nx", `\`, "tab\there", " x\u0085", "é中文🙂", "\xff\xfe", ""} {
		var out map[string]string
		if err := yaml.Unmarshal([]byte("k: "+QuoteYAML(s)+"\n"), &out); err != nil {
			t.Fatalf("%q：%v", s, err)
		}
		want := string([]rune(s))
		if out["k"] != want {
			t.Errorf("QuoteYAML(%q) 解回 %q", s, out["k"])
		}
	}
}

func TestHostileTitleKeepsFrontmatterStructure(t *testing.T) {
	in := sampleInput()
	in.Front.Title = "Kernel: \"part 1\" #2 --- evil: yes\nnotesteme_status: \"hacked\""
	in.Title = in.Front.Title
	md, checks := render(t, in, BuiltinTemplateBody)
	m, ok := ParseFrontmatter(md)
	if !ok || m["title"] != in.Front.Title || m["notesteme_status"] != StatusComplete {
		t.Fatalf("frontmatter 被改變：%v %v\n%s", ok, m, md)
	}
	if !Passed(checks) {
		t.Fatalf("檢查：%+v", checks)
	}
}

func TestTemplateValidation(t *testing.T) {
	cases := map[string]string{
		"{{.Frontmatter}}\n{{.Secret}}\n":    "不認識的變數 .Secret。",
		"{{.Item.Secret}}":                   "不認識的變數 .Item.Secret。",
		"{{template \"x\"}}":                 "模板第 1 行：不能使用 template。",
		"{{define \"x\"}}a{{end}}{{.Title}}": "模板不能使用 define 或 block。",
		"a\n{{call .Title}}":                 "模板第 2 行：不能使用 call。",
		"{{range .Sections}}x{{end}}":        "模板第 1 行：不能使用 range。",
		"{{with .Title}}x{{end}}":            "模板第 1 行：不能使用 with。",
		"{{$x := .Title}}":                   "模板第 1 行：不能宣告變數。",
		"line1\n{{.Title":                    "模板第 2 行有語法錯誤",
		"   ":                                "模板是空的。",
		"{{.}}":                              "模板第 1 行：不能直接輸出 .，請指定變數。",
	}
	for body, want := range cases {
		_, err := ParseTemplate(body)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("ParseTemplate(%q) = %v，預期以 %q 開頭", body, err, want)
		}
	}
	for _, ok := range []string{BuiltinTemplateBody, "{{if .ReadButton}}{{.ReadButton}}{{else}}none{{end}} {{.Item.URL}} {{$.Title}} {{len .Sections}}"} {
		if _, err := ParseTemplate(ok); err != nil {
			t.Errorf("ParseTemplate(%q)：%v", ok, err)
		}
	}
}

func TestChecksReportFailures(t *testing.T) {
	in := sampleInput()
	md, checks := render(t, in, "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n## 補充說明\n\n{{.SelfCheck}}\n")
	if checks[1].ID != CheckHeadingTimestamps || checks[1].OK || checks[1].Message != "標題沒有時間戳（補充說明）" {
		t.Fatalf("heading_timestamps：%+v\n%s", checks, md)
	}
	if got := FailureMessage(checks); got != "筆記沒有通過自動檢查：標題沒有時間戳（補充說明）。" {
		t.Errorf("訊息：%q", got)
	}
	// 沒有自我檢驗、圖片不存在、檔名不對、frontmatter 缺欄位。
	bad := "---\ntitle: \"x\"\n---\n## A · 00:01\n![[other.jpg]]\n"
	got := RunChecks(bad, CheckOptions{Title: "x"})
	for i, id := range CheckIDs {
		if got[i].ID != id {
			t.Fatalf("順序：%+v", got)
		}
	}
	if got[0].OK || got[1].OK == false || got[2].OK || got[3].OK || got[4].OK {
		t.Errorf("預期 frontmatter、images_exist、image_names、self_check 不通過：%+v", got)
	}
	if msg := FailureMessage(got); !strings.HasSuffix(msg, "另有 3 項沒有通過。") {
		t.Errorf("訊息：%q", msg)
	}
	// 程式碼區塊中的標題不算。
	fenced := "---\n" + strings.Join([]string{"title: \"t\"", "source: \"s\"", "channel: \"\"", "video_published: \"\"", "source_kind: \"derived\"", "status: \"not-started\"", "read_at: \"\"", "reading_status: \"r\"", "notesteme_id: \"i\"", "notesteme_status: \"complete\""}, "\n") + "\n---\n```\n## not a heading\n```\n## 自我檢驗\n- q\n"
	if c := RunChecks(fenced, CheckOptions{Title: "t"}); !Passed(c) {
		t.Errorf("程式碼區塊中的標題不應檢查：%+v", c)
	}
	if c := RunChecks(fenced, CheckOptions{Enabled: []string{CheckSelfCheck}}); len(c) != 1 || c[0].ID != CheckSelfCheck {
		t.Errorf("只執行勾選的檢查：%+v", c)
	}
}

func FuzzSanitizeBody(f *testing.F) {
	for _, s := range []string{"## h", "[a](javascript:x)", "![[x]]", "```dataviewjs\nx", "<% x %>", "[[cue:1]]", "[[[[x]]]]", "[x](<javascript:y>)", "a\n---"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := SanitizeBody(s, func(n int) bool { return n == 1 })
		lines := outsideFences(out)
		for _, l := range lines {
			if headingRe.MatchString(l) {
				t.Fatalf("內文產生了標題 %q（輸入 %q）", l, s)
			}
		}
		if strings.ContainsAny(out, "<>") {
			t.Fatalf("內文含原始 < 或 >：%q（輸入 %q）", out, s)
		}
		rest := CueRefRe.ReplaceAllString(strings.Join(lines, "\n"), "")
		if embedRe.MatchString(rest) || wikiRe.MatchString(rest) {
			t.Fatalf("內文留下 wikilink：%q（輸入 %q）", out, s)
		}
		// 連結處理是冪等的：輸出再處理一次不變，代表沒有留下不安全的連結。
		if text := strings.Join(lines, "\n"); sanitizeLinks(text) != text {
			again := sanitizeLinks(text)
			t.Fatalf("留下沒有處理的連結：%q → %q（輸入 %q）", text, again, s)
		}
		// 程式碼區塊必須成對：沒有結束的區塊會吃掉後面程式產生的標題。
		tail := outsideFences(out + "\n\n## T · 00:01")
		if tail[len(tail)-1] == "" {
			t.Fatalf("內文的程式碼區塊沒有結束：%q（輸入 %q）", out, s)
		}
	})
}

func FuzzQuoteYAML(f *testing.F) {
	for _, s := range []string{"a: b", "\"", "\n---\n", "\\", "#x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var out map[string]string
		doc := "title: " + QuoteYAML(s) + "\nnext: \"ok\"\n"
		if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
			t.Fatalf("%q：%v", s, err)
		}
		if len(out) != 2 || out["next"] != "ok" || out["title"] != string([]rune(s)) {
			t.Fatalf("QuoteYAML(%q) 改變了結構：%v", s, out)
		}
	})
}
