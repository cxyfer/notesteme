package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCleanTitle(t *testing.T) {
	long := "Part one of a very long video title that keeps going and going well past any sensible push title limit, END-OF-TITLE-XYZ"
	got := CleanTitle(long)
	if utf8.RuneCountInString(got) != 100 || !strings.HasSuffix(got, "push title li…") || strings.Contains(got, "END-OF-TITLE") {
		t.Errorf("長標題 = %q", got)
	}
	if got := CleanTitle("  a\t\tb\n\x00c​\x1b[31m  "); got != "a b c[31m" {
		t.Errorf("控制字元與空白 = %q", got)
	}
	if got := CleanTitle(string([]rune(strings.Repeat("字", 100)))); got != strings.Repeat("字", 100) {
		t.Errorf("剛好 100 字不截斷：%q", got)
	}
}

func TestTitlesPerEvent(t *testing.T) {
	it := &Item{ID: "i1", Ref: "yt:x", Title: "T"}
	cases := map[string]string{
		Title(Event{Name: EventItemDone, Item: it}):                                            "完成：T",
		Title(Event{Name: EventItemFailed, Item: it}):                                          "失敗：T",
		Title(Event{Name: EventItemBlocked, Item: it}):                                         "需要你處理：T",
		Title(Event{Name: EventBetterAvailable, Item: it}):                                     "有更好的字幕來源：T",
		Title(Event{Name: EventAlert, Alert: &Alert{Summary: "yt-dlp 失敗率偏高"}}):                 "Notesteme 告警：yt-dlp 失敗率偏高",
		Title(Event{Name: EventAlert, Alert: &Alert{Summary: "yt-dlp 失敗率偏高", Resolved: true}}): "Notesteme 已恢復：yt-dlp 失敗率偏高",
		Title(Event{Name: EventItemDone, Item: &Item{Ref: "yt:x"}}):                            "完成：yt:x",
		Title(Event{Name: "test"}):                                                             TestTitle,
		Title(Event{Name: EventBudgetWarning, Summary: "本月已用 80%"}):                            "Notesteme 預算提醒：本月已用 80%",
		Title(Event{Name: EventDigest, Summary: "每日彙整"}):                                       "Notesteme 彙整：每日彙整",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("Title = %q，預期 %q", got, want)
		}
	}
}

func TestRenderDiscordEscapesAndBlocksMentions(t *testing.T) {
	ev := Event{Name: EventItemDone, Item: &Item{ID: "i", Ref: "yt:x", Title: "@everyone **Free** [nitro](https://evil.example)"}}
	b, err := render(KindDiscord, Config{"webhook_url": testDiscord}, ev, "o1", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.AllowedMentions.Parse == nil || len(v.AllowedMentions.Parse) != 0 {
		t.Fatalf("allowed_mentions：%s %v", b, err)
	}
	if !strings.Contains(v.Content, `\*\*Free\*\* \[nitro\]\(https://evil.example\)`) {
		t.Errorf("Markdown 應跳脫：%q", v.Content)
	}
}

func TestRenderWebhookBody(t *testing.T) {
	created := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	ev := Event{Name: EventItemFailed, Item: &Item{ID: "i 1", Ref: "yt:ntf02xxxxxx", Title: "Removed video", Channel: "C"}, Stage: "metadata", ErrorClass: "permanent", Message: "影片不存在。"}
	b, err := render(KindWebhook, Config{"url": "https://h/x", "secret": "s"}, ev, "o1", created, "")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	item := v["item"].(map[string]any)
	if v["id"] != "o1" || v["event"] != "item.failed" || v["instance"] != nil || v["stage"] != "metadata" || item["url"] != nil || item["title"] != "Removed video" || v["created_at"] != "2026-11-23T10:00:00Z" {
		t.Errorf("webhook body = %s", b)
	}
	if _, ok := v["instance"]; !ok {
		t.Error("instance 要明確是 null")
	}
	b, _ = render(KindWebhook, Config{}, ev, "o1", created, "https://n.example.com")
	_ = json.Unmarshal(b, &v)
	if v["instance"] != "https://n.example.com" || v["item"].(map[string]any)["url"] != "https://n.example.com/items/i%201" {
		t.Errorf("有 public_url：%s", b)
	}
	// 超過 64 KiB 的彙整去掉明細。
	big := Event{Name: EventDigest, Summary: "d", Items: json.RawMessage(`["` + strings.Repeat("x", MaxRequestBody) + `"]`)}
	b, err = render(KindWebhook, Config{}, big, "o1", created, "")
	if err != nil || len(b) > MaxRequestBody || !strings.Contains(string(b), "已截斷") {
		t.Errorf("超大 body：%d bytes %v", len(b), err)
	}
}

func TestRenderTextKinds(t *testing.T) {
	ev := Event{Name: EventItemBlocked, Item: &Item{ID: "i1", Title: "T"}, Stage: "transcript", Message: "需要 cookie。", Actions: []string{"上傳 cookie"}}
	b, _ := render(KindNtfy, Config{"topic": "t", "priority": "4"}, ev, "o", time.Now(), "https://n.example.com")
	var nt map[string]any
	_ = json.Unmarshal(b, &nt)
	if nt["topic"] != "t" || nt["title"] != "需要你處理：T" || nt["priority"] != float64(4) || nt["click"] != "https://n.example.com/items/i1" ||
		!strings.Contains(nt["message"].(string), "需要 cookie。") || !strings.Contains(nt["message"].(string), "上傳 cookie") {
		t.Errorf("ntfy = %s", b)
	}
	b, _ = render(KindTelegram, Config{"chat_id": "42"}, ev, "o", time.Now(), "")
	var tg map[string]any
	_ = json.Unmarshal(b, &tg)
	if tg["chat_id"] != "42" || tg["parse_mode"] != nil || !strings.HasPrefix(tg["text"].(string), "需要你處理：T\n") {
		t.Errorf("telegram = %s", b)
	}
	if tg["link_preview_options"].(map[string]any)["is_disabled"] != true {
		t.Errorf("telegram 預覽要關閉：%s", b)
	}
}

func TestTruncateLines(t *testing.T) {
	s := strings.Repeat("line\n", 1000)
	got := truncateLines(s, 100, runeLen)
	if runeLen(got) > 100 || !strings.HasSuffix(got, truncatedNote) || strings.Contains(strings.TrimSuffix(got, "\n"+truncatedNote), "lin\n") {
		t.Errorf("行的邊界：%q", got)
	}
	one := truncateLines(strings.Repeat("字", 5000), 4000, byteLen)
	if len(one) > 4000 || !utf8.ValidString(one) {
		t.Errorf("單行在字元邊界截斷：%d bytes", len(one))
	}
	if truncateLines("short", 10, runeLen) != "short" {
		t.Error("沒有超過時不變")
	}
}

func FuzzCleanTitle(f *testing.F) {
	for _, s := range []string{"", "a  b", "\x00\x1b", strings.Repeat("é", 200), "\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := CleanTitle(s)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > maxTitleRunes || strings.ContainsAny(got, "\x00\n\r\t") {
			t.Errorf("CleanTitle(%q) = %q", s, got)
		}
	})
}
