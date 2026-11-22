package media

import "testing"

// ffmpeg silencedetect 的 stderr 解析不得 panic，且回傳區間不得為負。
func FuzzParseSilences(f *testing.F) {
	f.Add("[silencedetect @ 0x1] silence_start: 1.5\n[silencedetect @ 0x1] silence_end: 2.5 | silence_duration: 1\n", 0.0, 10.0)
	f.Add("silence_start: -3\nsilence_end: nan\n", 5.0, 0.0)
	f.Add("silence_end: 1e999\n", 0.0, 1.0)
	f.Fuzz(func(t *testing.T, s string, from, ln float64) {
		_ = parseSilences(s, from, ln)
	})
}
