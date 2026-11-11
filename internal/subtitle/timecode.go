package subtitle

import (
	"fmt"
	"strings"
)

// ParseTimecode 解析時間碼，回傳毫秒。
// 支援 HH:MM:SS、HH:MM:SS.mmm、HH:MM:SS,mmm、MM:SS.mmm（小數 1 到 3 位）。
func ParseTimecode(s string) (int64, error) {
	s = strings.TrimSpace(s)
	main, frac := s, ""
	if i := strings.IndexAny(s, ".,"); i >= 0 {
		main, frac = s[:i], s[i+1:]
		if frac == "" || len(frac) > 3 || !allDigits(frac) {
			return 0, fmt.Errorf("invalid timecode %q", s)
		}
	}
	parts := strings.Split(main, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid timecode %q", s)
	}
	var nums [3]int64
	for i, p := range parts {
		limit := 2
		if len(parts) == 3 && i == 0 {
			limit = 4 // 小時最多 4 位，避免溢位
		}
		if len(p) > limit || !allDigits(p) {
			return 0, fmt.Errorf("invalid timecode %q", s)
		}
		var n int64
		for _, c := range p {
			n = n*10 + int64(c-'0')
		}
		nums[i] = n
	}
	var h, m, sec int64
	if len(parts) == 3 {
		h, m, sec = nums[0], nums[1], nums[2]
	} else {
		m, sec = nums[0], nums[1]
	}
	if m > 59 || sec > 59 {
		return 0, fmt.Errorf("invalid timecode %q: minutes or seconds out of range", s)
	}
	var ms int64
	if frac != "" {
		for len(frac) < 3 {
			frac += "0"
		}
		for _, c := range frac {
			ms = ms*10 + int64(c-'0')
		}
	}
	return ((h*60+m)*60+sec)*1000 + ms, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func formatTime(ms int64, sep byte) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := ms % 3600000 / 60000
	s := ms % 60000 / 1000
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", h, m, s, sep, ms%1000)
}

// FormatSRTTime 格式化為 SRT 時間碼，例如 00:00:01,000。
func FormatSRTTime(ms int64) string { return formatTime(ms, ',') }

// FormatVTTTime 格式化為 VTT 時間碼，例如 00:00:01.000。
func FormatVTTTime(ms int64) string { return formatTime(ms, '.') }

// FormatDisplayTime 格式化為顯示用的時間碼，例如 00:00:01.000（API 與介面使用）。
func FormatDisplayTime(ms int64) string { return formatTime(ms, '.') }

// ParseWords 解析 words 欄位，格式為以空白分隔的 `text@start-end`，
// 例如 `hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900`。
func ParseWords(s string) ([]Word, error) {
	var words []Word
	for _, f := range strings.Fields(s) {
		at := strings.LastIndexByte(f, '@')
		if at <= 0 {
			return nil, fmt.Errorf("invalid word %q", f)
		}
		rng := f[at+1:]
		dash := strings.IndexByte(rng, '-')
		if dash <= 0 {
			return nil, fmt.Errorf("invalid word %q", f)
		}
		start, err := ParseTimecode(rng[:dash])
		if err != nil {
			return nil, err
		}
		end, err := ParseTimecode(rng[dash+1:])
		if err != nil {
			return nil, err
		}
		words = append(words, Word{Text: f[:at], StartMS: start, EndMS: end})
	}
	return words, nil
}

// FormatWords 為 ParseWords 的反向，時間以顯示格式輸出。
func FormatWords(words []Word) string {
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w.Text + "@" + FormatDisplayTime(w.StartMS) + "-" + FormatDisplayTime(w.EndMS)
	}
	return strings.Join(parts, " ")
}
