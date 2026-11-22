package subtitle

import (
	"strings"
	"testing"
)

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

func FuzzParseASS(f *testing.F) {
	f.Add([]byte(sampleASS))
	f.Add([]byte("[V4+ Styles]\nFormat: Name, Fontsize\nStyle: a,1e999\n[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,{\\p1}m 0 0{\\p0}\n"))
	f.Add([]byte("[Events]\nFormat: Start, End, Style, Text\nDialogue: 9999:59:59.999,0:00:00.00,x,y\n"))
	f.Add([]byte("[V4+ Styles]\nStyle: orphan,1\n[Events]\nDialogue: x\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := ParseASS(data)
		if err != nil {
			return
		}
		for _, e := range file.Events {
			if e.StartMS < 0 || e.EndMS < e.StartMS || e.Text == "" {
				t.Fatalf("invalid event %+v", e)
			}
		}
		for _, s := range file.Styles {
			if s.Name == "" || s.Size < 0 {
				t.Fatalf("invalid style %+v", s)
			}
			for _, c := range []string{s.Color, s.OutlineColor, s.BackColor} {
				if c != "" && !ValidColour(c) {
					t.Fatalf("invalid colour %q in %+v", c, s)
				}
			}
		}
		file.Cues()
	})
}

// FuzzParseImport 檢查匯入的不變量：成功時至少一句、編號從 1 連號、開始時間不遞減、每句有文字。
func FuzzParseImport(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:03,500\nhi\n\n2\n00:00:04,000 --> 00:00:06,000\nthere\n"))
	f.Add([]byte("WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n"))
	f.Add([]byte(sampleASS))
	f.Add([]byte("1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:01,000\nsecond\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cues, _, err := ParseImport(data, "")
		if err != nil {
			return
		}
		if len(cues) == 0 {
			t.Fatal("成功卻沒有任何 cue")
		}
		var prev int64
		for i, c := range cues {
			if c.Index != i+1 || c.StartMS < prev || c.EndMS < c.StartMS || strings.TrimSpace(c.Text) == "" {
				t.Fatalf("invalid cue %+v（前一句開始 %d）", c, prev)
			}
			prev = c.StartMS
		}
	})
}
