package vault

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var day = time.Date(2025, 8, 9, 23, 30, 0, 0, time.FixedZone("x", 3600*9))

func TestPlanDefaultLayout(t *testing.T) {
	l, err := Plan("Video Notes", "", Item{Platform: "youtube", Title: "Designing a Cache", Date: day})
	if err != nil {
		t.Fatal(err)
	}
	// 日期用 UTC（23:30+09:00 是 UTC 的 14:30，同一天）。
	if l.Note != "Video Notes/YouTube/Designing a Cache - 2025-08-09.md" {
		t.Errorf("Note = %q", l.Note)
	}
	if got := l.Subtitle("srt", "", false); got != "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt" {
		t.Errorf("Subtitle = %q", got)
	}
	if l.Assets != "Video Notes/YouTube/_Assets/Designing a Cache" {
		t.Errorf("Assets = %q", l.Assets)
	}
	if got := l.Subtitle("bilingual-srt", "zh-TW", true); got != "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.zh-TW.bilingual.srt" {
		t.Errorf("雙語、多語言 = %q", got)
	}
}

func TestPlanUsesUTCDate(t *testing.T) {
	d := time.Date(2025, 8, 10, 1, 0, 0, 0, time.FixedZone("x", 3600*9)) // UTC 是 8/9 16:00
	l, err := Plan("B", "F", Item{Title: "t", Date: d})
	if err != nil || l.Note != "B/F/t - 2025-08-09.md" {
		t.Fatalf("Note = %q，%v", l.Note, err)
	}
}

func TestPlanChannelLayout(t *testing.T) {
	l, err := Plan("Video Notes", "Channel/{channel}", Item{Platform: "youtube", Title: "Designing a Cache", Channel: "Systems Weekly", Date: day})
	if err != nil {
		t.Fatal(err)
	}
	if l.Note != "Video Notes/Channel/Systems Weekly/2025-08-09_Designing a Cache.md" {
		t.Errorf("Note = %q", l.Note)
	}
	if got := l.Subtitle("srt", "", false); got != "Video Notes/Channel/Systems Weekly/_Subtitles/2025-08-09_Designing a Cache.srt" {
		t.Errorf("Subtitle = %q", got)
	}
	if l.Assets != "Video Notes/Channel/Systems Weekly/_Assets/2025-08-09_Designing a Cache" {
		t.Errorf("Assets = %q", l.Assets)
	}
}

// 對抗輸入：頻道與標題不能造出新的資料夾層或離開 vault（VAULT-R3）。
func TestPlanHostileNames(t *testing.T) {
	cases := []struct {
		channel, title, note string
	}{
		{"../../etc", "../../etc/passwd", "B/Channel/....etc/2025-08-09_....etcpasswd.md"},
		{"..", "x", "B/Channel/_/2025-08-09_x.md"},
		{"", "", "B/Channel/untitled/2025-08-09_untitled.md"},
		{"a\x00b\u202e", "CON", "B/Channel/ab/2025-08-09__CON.md"},
		{`C:\Windows`, "nul.txt", "B/Channel/CWindows/2025-08-09__nul.txt.md"},
		{"x/y", "a\tb\n c", "B/Channel/xy/2025-08-09_a b c.md"},
	}
	for _, c := range cases {
		l, err := Plan("B", "Channel/{channel}", Item{Title: c.title, Channel: c.channel, Date: day})
		if err != nil {
			t.Fatal(err)
		}
		if l.Note != c.note {
			t.Errorf("頻道 %q 標題 %q：Note = %q，預期 %q", c.channel, c.title, l.Note, c.note)
		}
		for _, seg := range strings.Split(l.Note, "/") {
			if seg == "." || seg == ".." || seg == "" {
				t.Errorf("%q 有不安全的段落", l.Note)
			}
		}
	}
}

func TestPlanPlatformVariableAndDefaults(t *testing.T) {
	for platform, want := range map[string]string{"youtube": "YouTube", "bilibili": "Bilibili", "podcast": "Podcasts", "local": "Local", "": "Local"} {
		l, err := Plan("B", "", Item{Platform: platform, Title: "t", Date: day})
		if err != nil || !strings.HasPrefix(l.Note, "B/"+want+"/") {
			t.Errorf("%s：Note = %q，%v", platform, l.Note, err)
		}
	}
	l, _ := Plan("B", "All/{platform}", Item{Platform: "bilibili", Title: "t", Date: day})
	if l.Note != "B/All/Bilibili/t - 2025-08-09.md" {
		t.Errorf("{platform}：%q", l.Note)
	}
}

// 長度上限（VAULT-R3）：每一段最多 200 位元組，整個路徑最多 240 字元，超過時截短標題。
func TestPlanTruncatesLongTitles(t *testing.T) {
	for _, title := range []string{strings.Repeat("a", 500), strings.Repeat("緩", 300), strings.Repeat("ab. ", 100)} {
		l, err := Plan("Video Notes", "Channel/{channel}", Item{Title: title, Channel: strings.Repeat("c", 150), Date: day})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{l.Note, l.Subtitle("bilingual-srt", "zh-Hant-TW", true), l.Assets} {
			if n := utf8.RuneCountInString(p); n > MaxPathChars {
				t.Errorf("%q 有 %d 個字元", p, n)
			}
			for _, seg := range strings.Split(p, "/") {
				if len(seg) > MaxSegmentBytes || !utf8.ValidString(seg) {
					t.Errorf("段落 %q 有 %d 位元組", seg, len(seg))
				}
				if strings.HasSuffix(strings.TrimSuffix(seg, ".md"), ".") {
					t.Errorf("段落 %q 尾端是句點", seg)
				}
			}
		}
	}
	// base 與 folder 本身就太長時無法截短。
	if _, err := Plan(strings.Repeat("b/", 130), "", Item{Title: "t", Date: day}); !errors.Is(err, ErrPathTooLong) {
		t.Errorf("err = %v", err)
	}
}

func TestSegment(t *testing.T) {
	for in, want := range map[string]string{".": "_", "..": "_", " .. ": "_", "a.": "a", "": "untitled", "COM1": "_COM1", "x\u200fy": "xy"} {
		if got := Segment(in); got != want {
			t.Errorf("Segment(%q) = %q，預期 %q", in, got, want)
		}
	}
	if got := Segment(strings.Repeat("緩", 100)); len(got) > MaxSegmentBytes || !utf8.ValidString(got) {
		t.Errorf("長段落 %d 位元組", len(got))
	}
}
