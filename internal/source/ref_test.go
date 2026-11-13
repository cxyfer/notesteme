package source

import "testing"

// 測試用的 ID 一律 11 個字元，不受 YouTube ID 最短長度規則的影響。
func TestParseRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
		ok   bool
	}{
		{"yt:abcdefghijk", "https://www.youtube.com/watch?v=abcdefghijk", true},
		{"yt:dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"bili:BV1xx411c7mD/p2", "https://www.bilibili.com/video/BV1xx411c7mD?p=2", true},
		{"bili:BV1xx411c7mD/p1", "https://www.bilibili.com/video/BV1xx411c7mD?p=1", true},
		// ID 不合法：交給 Recognize 拒絕。
		{"yt:", "", false},
		{"yt:abcde fghij", "", false},
		{"yt:abcdefghijk&list=x", "", false},
		{"yt:abcde/fghij", "", false},
		{"bili:notabvid/p1", "", false},
		{"bili:BV1xx411c7mD/p0", "", false},
		{"bili:BV1xx411c7mD", "", false},
		{"bili:BV1xx411c7mD/px", "", false},
		// 沒有網址的種類與其他字串。
		{"pod:feed/guid", "", false},
		{"local:talk.mp4", "", false},
		{"https://youtu.be/abcdefghijk", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseRef(tt.ref)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseRef(%q) = %q, %v；預期 %q, %v", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}

// ParseRef 的結果必須能被 Recognize 辨識成同一部影音，且與 Ref 互為反向。
func TestParseRefRoundTrip(t *testing.T) {
	for _, ref := range []string{"yt:abcdefghijk", "bili:BV1xx411c7mD/p3"} {
		u, ok := ParseRef(ref)
		if !ok {
			t.Fatalf("ParseRef(%q) 失敗", ref)
		}
		tg, ok := Recognize(u)
		if !ok {
			t.Fatalf("Recognize(%q) 失敗", u)
		}
		if got := Ref(tg.Platform, tg.ExternalID, tg.Part); got != ref {
			t.Errorf("Ref(Recognize(ParseRef(%q))) = %q", ref, got)
		}
	}
}
