package hubclient

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
)

// Client 算出的影片雜湊必須和 Hub 存的完全相同（ADR-0032 第 3 點）。
func TestVideoKeyMatchesTheHub(t *testing.T) {
	for _, c := range []struct {
		platform, id string
		part         int
	}{{"youtube", "hshr05xxxxx", 0}, {"bilibili", "BV1hs4y1c7mD", 1}, {"bilibili", "BV1hs4y1c7mD", 12}, {"podcast", "feed/guid/1", 0}} {
		if got, want := VideoKeySHA256(c.platform, c.id, c.part), sqlite.VideoKeySHA256(c.platform, c.id, c.part); got != want {
			t.Errorf("%v：%s != %s", c, got, want)
		}
	}
	if VideoKey("youtube", "abc", 0) != "youtube:abc:" || Prefix(strings.Repeat("ab", 32)) != "abab" {
		t.Error("VideoKey／Prefix")
	}
}

func TestValidateCues(t *testing.T) {
	ok := `{"lang":"en","variant":"source","speakers":[],"cues":[{"index":7,"start_ms":0,"end_ms":10,"text":"a","timing":"merged"},{"index":8,"start_ms":5,"end_ms":10,"text":"  "},{"index":9,"start_ms":5,"end_ms":20,"text":"b"}]}`
	tr, err := ValidateCues([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Cues) != 2 || tr.Cues[1].Index != 2 || tr.Cues[0].Timing != "source" {
		t.Errorf("tr = %+v", tr)
	}
	for name, body := range map[string]string{
		"不是 JSON":  `nope`,
		"沒有字幕句":    `{"lang":"en","variant":"source","cues":[]}`,
		"結束早於開始":   `{"lang":"en","variant":"source","cues":[{"start_ms":5000,"end_ms":2000,"text":"x"}]}`,
		"不單調":      `{"lang":"en","variant":"source","cues":[{"start_ms":5000,"end_ms":6000,"text":"x"},{"start_ms":1000,"end_ms":2000,"text":"y"}]}`,
		"未知的變體":    `{"lang":"en","variant":"weird","cues":[{"start_ms":0,"end_ms":1,"text":"x"}]}`,
		"語言不合法":    `{"lang":"../x","variant":"source","cues":[{"start_ms":0,"end_ms":1,"text":"x"}]}`,
		"太長的句子":    `{"lang":"en","variant":"source","cues":[{"start_ms":0,"end_ms":1,"text":"` + strings.Repeat("x", maxCueRunes+1) + `"}]}`,
		"全部是空白":    `{"lang":"en","variant":"source","cues":[{"start_ms":0,"end_ms":1,"text":" "}]}`,
		"不是 UTF-8": "{\"lang\":\"en\",\"variant\":\"source\",\"cues\":[{\"start_ms\":0,\"end_ms\":1,\"text\":\"\xff\"}]}",
	} {
		_, err := ValidateCues([]byte(body))
		if e := asErr(t, err); e.Code() != CodeContentInvalid || !strings.HasPrefix(e.Error(), "Hub 回傳的字幕格式不正確：") {
			t.Errorf("%s：%v", name, err)
		}
	}
}

func FuzzValidateCues(f *testing.F) {
	f.Add([]byte(`{"lang":"en","variant":"source","cues":[{"start_ms":0,"end_ms":1,"text":"x"}]}`))
	f.Add([]byte(`{"cues":[{"start_ms":-1}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, err := ValidateCues(data)
		if err != nil {
			return
		}
		var prev int64
		for _, c := range tr.Cues {
			if c.EndMS < c.StartMS || c.StartMS < prev || strings.TrimSpace(c.Text) == "" {
				t.Fatalf("通過驗證的 cue 不合法：%+v", c)
			}
			prev = c.StartMS
		}
	})
}

func TestPick(t *testing.T) {
	key := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	v := func(id, variant, lang string, score int, verified bool, sha string) LookupVersion {
		return LookupVersion{ID: id, Variant: variant, Lang: lang, Score: score, ModelVerified: verified, SHA256: strings.Repeat(sha, 64)}
	}
	videos := []LookupVideo{
		{VideoKeySHA256: other, Versions: []LookupVersion{v("x", "source", "en", 99, true, "0")}},
		{VideoKeySHA256: key, Versions: []LookupVersion{
			v("norm", "normalized", "en", 9, true, "1"),
			v("raw", "source", "en", 1, false, "2"),
			v("ja", "source", "ja", 20, true, "3"),
			v("raw-verified", "source", "en-US", 0, true, "4"),
		}},
	}
	if got, _ := Pick(videos, key, "en"); got.ID != "raw-verified" {
		t.Errorf("偏好 source、model_verified：%s", got.ID)
	}
	if got, _ := Pick(videos, key, "ja"); got.ID != "ja" {
		t.Errorf("語言：%s", got.ID)
	}
	if got, _ := Pick(videos, key, ""); got.ID != "ja" {
		t.Errorf("語言不明時不限，依 source、verified、score：%s", got.ID)
	}
	if _, ok := Pick(videos, key, "de"); ok {
		t.Error("沒有相同語言時沒有候選")
	}
	if _, ok := Pick(videos, strings.Repeat("c", 64), "en"); ok {
		t.Error("影片雜湊不同時沒有候選")
	}
	tie := []LookupVideo{{VideoKeySHA256: key, Versions: []LookupVersion{v("b", "source", "en", 1, true, "9"), v("a", "source", "en", 1, true, "5")}}}
	if got, _ := Pick(tie, key, "en"); got.ID != "a" {
		t.Errorf("同分時 sha256 小的：%s", got.ID)
	}
	if got, _ := Pick(tie, key, "und"); got.ID != "a" {
		t.Errorf("und 視為不明：%s", got.ID)
	}
}
