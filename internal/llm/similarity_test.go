package llm

import (
	"math"
	"strings"
	"testing"
)

func TestSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"", "", 1},
		{"abc", "abc", 1},
		{"abc", "", 0},
		{"", "abc", 0},
		{"abc", "ABC", 1},                // 只改大小寫不算差異
		{"kitten", "sitting", 1 - 3.0/7}, // 經典的編輯距離 3、較長者 7 個字元
		{"我們用卡夫卡", "我們用 Kafka", 1 - 6.0/9}, // 以 rune 計算，不是 byte
		{"we use cafca streams", "Stream processing is a way of cooking pasta", 0.1860},
	}
	for _, c := range cases {
		if got := Similarity(c.a, c.b); math.Abs(got-c.want) > 0.001 {
			t.Errorf("Similarity(%q, %q) = %.4f，預期 %.4f", c.a, c.b, got, c.want)
		}
		if ab, ba := Similarity(c.a, c.b), Similarity(c.b, c.a); ab != ba {
			t.Errorf("Similarity 不對稱：%q %q：%v %v", c.a, c.b, ab, ba)
		}
	}
	// 過長而且不相同：視為差異過大，不花 O(n·m) 的時間；相同時不受影響。
	long := strings.Repeat("字", 5000)
	if got := Similarity(long, long+"x"); got != 0 {
		t.Errorf("過長的句子 = %v，預期 0", got)
	}
	if got := Similarity(long, long); got != 1 {
		t.Errorf("相同的長句 = %v，預期 1", got)
	}
}

func FuzzSimilarity(f *testing.F) {
	for _, s := range []string{"", "a", "We use Kafka Streams.", "我們用卡夫卡", "\x00\xff", "İ"} {
		f.Add(s, "we use cafca")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		got := Similarity(a, b)
		if math.IsNaN(got) || got < 0 || got > 1 {
			t.Fatalf("Similarity(%q, %q) = %v，必須在 0～1", a, b, got)
		}
		if Similarity(a, a) != 1 {
			t.Fatalf("Similarity(a, a) 必須是 1：%q", a)
		}
		if Similarity(b, a) != got {
			t.Fatalf("Similarity 必須對稱：%q %q", a, b)
		}
	})
}
