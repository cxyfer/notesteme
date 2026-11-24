package langdetect

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 驗收場景的 fixture（auto-zh-en-mixed.json）：漢字 29 個、英文詞 13 個，以份量計是 zh 69%、en 31%。
const mixedFixture = `今天我們來聊聊 database 的 index
先說明 B-tree 為什麼適合 range query
然後比較 hash index 跟 covering index
最後看一個 real world 的 migration 案例`

func TestDetect(t *testing.T) {
	cases := []struct {
		name, text   string
		lang, script string
		mixed        bool
		second       string
	}{
		{"中英夾雜", mixedFixture, "zh", "Hant", true, "en"},
		{"繁體", "我們今天要討論資料庫的索引為什麼這麼重要，還有怎麼設計。", "zh", "Hant", false, ""},
		{"簡體", "我们今天要讨论数据库的索引为什么这么重要，还有怎么设计。", "zh", "Hans", false, ""},
		{"中文夾少量專名", "我們之所以改用 Kafka，是因為它的吞吐量比原本的佇列高很多。", "zh", "Hant", false, ""},
		{"日文", "皆さん、こんにちは。今日はデータベースについて話します。まずは基本から始めましょう。", "ja", "", false, ""},
		{"日文的漢字多", "東京都の人口と経済、そして交通の問題について説明します。", "ja", "", false, ""},
		{"英文", "Today we talk about why the index is so important for the database and how to design it.", "en", "", false, ""},
		{"法文", "Aujourd'hui nous parlons des index et pourquoi c'est très important pour une base de données.", "fr", "", false, ""},
		{"德文", "Heute sprechen wir über die Datenbank und warum der Index nicht immer hilft, auch für uns.", "de", "", false, ""},
		{"韓文", "우리는 오늘 데이터베이스의 인덱스에 대해 이야기합니다.", "ko", "", false, ""},
		{"俄文", "Сегодня мы говорим о том, что это индекс и как он работает в базе данных.", "ru", "", false, ""},
		{"英文為主夾中文", "Today we talk about the index 索引 and why the database is so important for you and me.", "en", "", false, ""},
		{"英日夾雜", "Today we talk about the index and the plan. 今日はデータベースについて話します。", "ja", "", true, "en"},
		{"認不出的拉丁語言", "Xyzzy plugh frobnitz quux blorp zork grault garply waldo fred", "", "", false, ""},
	}
	for _, c := range cases {
		d, ok := Detect(c.text)
		if !ok {
			t.Errorf("%s：判斷不了", c.name)
			continue
		}
		if d.Lang != c.lang || d.Script != c.script || d.Mixed != c.mixed || d.Second != c.second {
			t.Errorf("%s：Detect = %+v，預期 lang=%q script=%q mixed=%v second=%q", c.name, d, c.lang, c.script, c.mixed, c.second)
		}
	}
}

// 份量不到 MinUnits、或沒有任何字母時判斷不了。
func TestDetectTooShort(t *testing.T) {
	for _, text := range []string{"", "   ", "12345 67890 !!!", "東京", "Hello world", "♪♪♪ [音樂] ♪♪♪", "七個字的短句子"} {
		if d, ok := Detect(text); ok {
			t.Errorf("Detect(%q) = %+v，應判斷不了", text, d)
		}
	}
}

// 20% 的邊界：剛好 20% 算 mixed，不到不算。
func TestDetectMixedBoundary(t *testing.T) {
	// 8 個漢字 + 2 個英文詞 = 20%。
	if d, ok := Detect("我們討論資料庫索 the index"); !ok || !d.Mixed || d.Lang != "zh" || d.Second != "en" {
		t.Errorf("剛好 20%% = %+v %v", d, ok)
	}
	// 9 個漢字 + 2 個英文詞 ≈ 18%。
	if d, ok := Detect("我們討論資料庫索引 the index"); !ok || d.Mixed {
		t.Errorf("不到 20%% = %+v %v", d, ok)
	}
}

// 結果是確定的：同一段文字每次都一樣（map 的走訪順序不影響）。
func TestDetectDeterministic(t *testing.T) {
	text := "the le der el il " + strings.Repeat("和", 5)
	first, _ := Detect(text)
	for range 50 {
		if d, _ := Detect(text); d.Lang != first.Lang || d.Second != first.Second || d.Mixed != first.Mixed {
			t.Fatalf("結果不一致：%+v / %+v", first, d)
		}
	}
}

func FuzzDetect(f *testing.F) {
	for _, s := range []string{mixedFixture, "", "\xff\xfe", "ーーーー々々々", "a b c d e f g h i"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, ok := Detect(s)
		if !ok {
			return
		}
		sum := 0.0
		for _, v := range d.Shares {
			if v < 0 || v > 1 {
				t.Fatalf("比例超出範圍：%v", d.Shares)
			}
			sum += v
		}
		if sum < 0.999 || sum > 1.001 {
			t.Fatalf("比例加總 %v", sum)
		}
		if d.Mixed && d.Lang == d.Second && d.Lang != "" {
			t.Fatalf("mixed 的兩種語言相同：%+v", d)
		}
		if !utf8.ValidString(d.Lang) {
			t.Fatal("語言不是合法的 UTF-8")
		}
	})
}
