package subtitle

import (
	"reflect"
	"testing"
)

// yt-dlp 抓到的 YouTube 自動字幕：cue 的第一行常是單一空白，
// 字詞時間標記在下一行；中間還夾著 10ms 的過渡 cue。
const ytdlpAutoVTT = "WEBVTT\nKind: captions\nLanguage: en\n\n" +
	"00:00:00.480 --> 00:00:02.870 align:start position:0%\n" +
	" \n" +
	"so<00:00:00.800><c> today</c><00:00:01.200><c> we</c>\n\n" +
	"00:00:02.870 --> 00:00:02.880 align:start position:0%\n" +
	"so today we\n" +
	" \n\n" +
	"00:00:02.880 --> 00:00:05.000 align:start position:0%\n" +
	"so today we\n" +
	"talk<00:00:03.500><c> about</c><00:00:04.000><c> cats</c>\n\n"

func TestParseVTTKeepsWhitespaceOnlyLinesInsideCues(t *testing.T) {
	cues, err := ParseVTT([]byte(ytdlpAutoVTT))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 3 {
		t.Fatalf("got %d cues: %+v", len(cues), cues)
	}
	if cues[0].Text != "so today we" {
		t.Errorf("cue 1 text = %q", cues[0].Text)
	}
	wantWords := []Word{{"so", 480, 800}, {"today", 800, 1200}, {"we", 1200, 2870}}
	if !reflect.DeepEqual(cues[0].Words, wantWords) {
		t.Errorf("cue 1 words = %+v", cues[0].Words)
	}
	if cues[1].Text != "so today we" || cues[1].EndMS-cues[1].StartMS != 10 {
		t.Errorf("cue 2 = %+v", cues[1])
	}
	if cues[2].Text != "so today we\ntalk about cats" {
		t.Errorf("cue 3 text = %q", cues[2].Text)
	}
	wantWords = []Word{{"talk", 2880, 3500}, {"about", 3500, 4000}, {"cats", 4000, 5000}}
	if !reflect.DeepEqual(cues[2].Words, wantWords) {
		t.Errorf("cue 3 words = %+v", cues[2].Words)
	}
}

// 真正的空行（含 CRLF）仍然是區塊分隔。
func TestParseVTTTrueBlankLinesStillSeparate(t *testing.T) {
	src := "WEBVTT\r\n\r\n00:00:01.000 --> 00:00:02.000\r\na\r\n\r\n00:00:03.000 --> 00:00:04.000\r\nb\r\n"
	cues, err := ParseVTT([]byte(src))
	if err != nil || len(cues) != 2 || cues[0].Text != "a" || cues[1].Text != "b" {
		t.Fatalf("%+v %v", cues, err)
	}
}
