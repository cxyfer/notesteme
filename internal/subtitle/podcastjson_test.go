package subtitle

import (
	"reflect"
	"testing"
)

func TestParsePodcastJSONSegments(t *testing.T) {
	data := `{"version":"1.0.0","segments":[
		{"speaker":"Host","startTime":0,"endTime":2.5,"body":"Hello there"},
		{"startTime":2.5,"endTime":5.1234,"body":"  Bye now  "},
		{"startTime":6,"endTime":7,"body":"   "}]}`
	cues, word, err := ParsePodcastJSON([]byte(data))
	if err != nil || word {
		t.Fatalf("err = %v，wordLevel = %v", err, word)
	}
	want := []Cue{
		{Index: 1, StartMS: 0, EndMS: 2500, Text: "Hello there", Timing: TimingSource},
		{Index: 2, StartMS: 2500, EndMS: 5123, Text: "Bye now", Timing: TimingSource},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Errorf("cues = %+v\n預期 %+v", cues, want)
	}
}

// 每個 segment 都是單字：依句末標點、換行、停頓超過 1 秒、cue 超過 7 秒合併，並填入 Words。
func TestParsePodcastJSONWordLevel(t *testing.T) {
	data := `{"segments":[
		{"startTime":0.0,"endTime":0.4,"body":"Hello"},
		{"startTime":0.5,"endTime":0.9,"body":"world."},
		{"startTime":1.0,"endTime":1.4,"body":"Next"},
		{"startTime":1.5,"endTime":1.9,"body":"one\n"},
		{"startTime":2.0,"endTime":2.4,"body":"after"},
		{"startTime":4.0,"endTime":4.4,"body":"pause"},
		{"startTime":4.5,"endTime":4.9,"body":"long"},
		{"startTime":5.0,"endTime":5.4,"body":"run"},
		{"startTime":5.5,"endTime":8.0,"body":"on"},
		{"startTime":8.1,"endTime":8.5,"body":"again?"}]}`
	cues, word, err := ParsePodcastJSON([]byte(data))
	if err != nil || !word {
		t.Fatalf("err = %v，wordLevel = %v", err, word)
	}
	var texts []string
	for _, c := range cues {
		texts = append(texts, c.Text)
		if len(c.Words) == 0 || c.Words[0].StartMS != c.StartMS || c.Words[len(c.Words)-1].EndMS != c.EndMS || c.Timing != TimingSource {
			t.Errorf("cue %d = %+v", c.Index, c)
		}
	}
	// 句末標點、換行、停頓 > 1 秒（2.4 → 4.0）、超過 7 秒（5.0 起到 8.0 為 3 秒，不斷；4.0 起到 8.0 為 4 秒，不斷）。
	want := []string{"Hello world.", "Next one", "after", "pause long run on again?"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("texts = %q\n預期 %q", texts, want)
	}
	for i, c := range cues {
		if c.Index != i+1 {
			t.Errorf("index = %d，預期 %d", c.Index, i+1)
		}
	}
}

func TestParsePodcastJSONWordCueMaxLength(t *testing.T) {
	// 沒有標點、沒有停頓的單字：一句超過 7 秒就斷開。
	data := `{"segments":[`
	for i := 0; i < 20; i++ {
		if i > 0 {
			data += ","
		}
		data += `{"startTime":` + itoa(i) + `,"endTime":` + itoa(i) + `.9,"body":"w` + itoa(i) + `"}`
	}
	data += `]}`
	cues, word, err := ParsePodcastJSON([]byte(data))
	if err != nil || !word || len(cues) < 3 {
		t.Fatalf("cues = %d，wordLevel = %v，err = %v", len(cues), word, err)
	}
	for _, c := range cues {
		if c.EndMS-c.StartMS > 7000 {
			t.Errorf("cue %d 長 %d ms，超過 7 秒", c.Index, c.EndMS-c.StartMS)
		}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestParsePodcastJSONEdgeCases(t *testing.T) {
	// 沒有 segment：沒有 cue，不是錯誤（呼叫端把它當成空字幕）。
	cues, _, err := ParsePodcastJSON([]byte(`{"segments":[]}`))
	if err != nil || len(cues) != 0 {
		t.Errorf("空：%v, %v", cues, err)
	}
	// 結束時間早於開始時間：取開始時間；亂序的 segment 依開始時間排序。
	cues, _, err = ParsePodcastJSON([]byte(`{"segments":[{"startTime":5,"endTime":4,"body":"b c"},{"startTime":1,"endTime":2,"body":"a d"}]}`))
	if err != nil || len(cues) != 2 || cues[0].Text != "a d" || cues[1].StartMS != 5000 || cues[1].EndMS != 5000 {
		t.Errorf("cues = %+v, %v", cues, err)
	}
	for _, bad := range []string{``, `{`, `[]`, `{"segments":[{"startTime":-1,"endTime":1,"body":"x"}]}`, `{"segments":"x"}`} {
		if _, _, err := ParsePodcastJSON([]byte(bad)); err == nil {
			t.Errorf("%q 應該回傳錯誤", bad)
		}
	}
}
