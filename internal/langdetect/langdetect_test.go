package langdetect

import "testing"

func TestMatches(t *testing.T) {
	cases := []struct {
		target, text string
		want         bool
	}{
		{"zh-Hant-TW", "我們之所以改用 Kafka，", true},
		{"zh-Hant-TW", "我們把事件存在日誌裡。", true},
		{"zh-Hant-TW", "私たちはイベントをログに保存します。", false},        // 日文
		{"zh-Hant-TW", "We store events in a log.", false}, // 沒有翻譯
		{"zh-Hant-TW", "这是一个关于数据库与网络的视频", false},           // 簡體
		{"zh-Hans", "这是一个关于数据库与网络的视频", true},
		{"zh-Hant-TW", "Kafka Streams API 很好", true}, // 產品名稱居多，仍有中文
		{"zh-Hant-TW", "This is English mostly 好", false},
		{"zh-Hant-TW", "2024", true}, // 沒有字母：判斷不了
		{"ja", "私たちはイベントをログに保存します。", true},
		{"ja", "我們把事件存在日誌裡。", false},
		{"ja", "東京", true}, // 太短，不判斷為中文
		{"ko", "우리는 이벤트를 로그에 저장합니다.", true},
		{"ko", "We store events.", false},
		{"en", "We store events in a log.", true},
		{"en", "我們把事件存在日誌裡。", false},
		{"ru", "Мы храним события в журнале.", true},
		{"ru", "We store events.", false},
		{"sr-Latn", "Čuvamo događaje u dnevniku.", true},
		{"zh-Latn-TW", "Wǒmen bǎ shìjiàn cún zài rìzhì lǐ.", true},
		{"xx", "anything", true}, // 不認識的語言不檢查
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := Matches(c.target, c.text); got != c.want {
			t.Errorf("Matches(%q, %q) = %v，預期 %v（counts %v）", c.target, c.text, got, c.want, Count(c.text))
		}
	}
}

func TestCountWords(t *testing.T) {
	c := Count("Hello, world! 你好 ありがとう 123")
	if c[Latin] != 2 || c[Han] != 2 || c[Kana] != 5 {
		t.Errorf("Count = %v", c)
	}
}

func FuzzMatches(f *testing.F) {
	f.Add("zh-Hant-TW", "我們之所以改用 Kafka，")
	f.Add("ja", "私たちは")
	f.Add("en", "\xff\xfe")
	f.Fuzz(func(t *testing.T, target, text string) {
		_ = Matches(target, text)
		for s, n := range Count(text) {
			if n < 0 || s == "" {
				t.Fatalf("Count 回傳不合理的值：%v", Count(text))
			}
		}
	})
}
