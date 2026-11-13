package subtitle

import "testing"

// fuzzCheck 確認解析結果的基本不變量；解析本身不得 panic。
func fuzzCheck(t *testing.T, cues []Cue, err error) {
	t.Helper()
	if err != nil {
		return
	}
	for i, c := range cues {
		if c.Index != i+1 || c.StartMS < 0 || c.EndMS < c.StartMS {
			t.Fatalf("invalid cue %+v", c)
		}
		for _, w := range c.Words {
			if w.StartMS < c.StartMS || w.EndMS > c.EndMS || w.EndMS < w.StartMS {
				t.Fatalf("word %+v outside cue %+v", w, c)
			}
		}
	}
	// 正規化也不得 panic，且結果的時間單調。
	norm := Normalize(cues)
	for i := 1; i < len(norm); i++ {
		if norm[i].StartMS < norm[i-1].EndMS {
			t.Fatalf("overlap after normalize: %+v then %+v", norm[i-1], norm[i])
		}
	}
}

func FuzzParseVTT(f *testing.F) {
	f.Add([]byte(generateVTT("abc123xxxxx", "en", 2)))
	f.Add([]byte("WEBVTT\n\nNOTE x\n\n00:00:01.000 --> 00:00:02.000 align:start position:0%\nso<00:00:01.500><c> we</c>\n"))
	f.Add([]byte("\uFEFFWEBVTT\r\n\r\n1\r\n01:02.000 --> 01:03.000\r\n<i>x</i>\r\n"))
	f.Add([]byte("00:00:01.000 --> 00:00:00.500\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cues, err := ParseVTT(data)
		fuzzCheck(t, cues, err)
	})
}

func FuzzParseSRT(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:03,500\nhello\n\n2\n00:00:04,000 --> 00:00:06,000\nworld\n"))
	f.Add([]byte("\uFEFF1\r\n00:00:01,000 --> 00:00:02,000 X1:0\r\n{\\an8}<i>a</i>\r\n"))
	f.Add([]byte("00:00:05,000 --> 00:00:01,000\nx"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cues, err := ParseSRT(data)
		fuzzCheck(t, cues, err)
	})
}
