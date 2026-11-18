package faketool

import "testing"

// 下載音訊時 production 會帶 --max-filesize（GRD-R5 的音檔上限）；假 yt-dlp 要接受並忽略它，
// 否則遇到未知旗標以結束碼 2 結束，所有走 ASR 的場景都會失敗。
func TestParseYtDlpAcceptsMaxFilesize(t *testing.T) {
	for _, args := range [][]string{
		{"--max-filesize", "2147483648", "-f", "bestaudio", "--", "https://www.youtube.com/watch?v=abc123xxxxx"},
		{"--max-filesize=2147483648", "--", "https://www.youtube.com/watch?v=abc123xxxxx"},
	} {
		a, err := parseYtDlp(args)
		if err != nil {
			t.Errorf("parseYtDlp(%v) = %v", args, err)
		}
		if a.url != "https://www.youtube.com/watch?v=abc123xxxxx" {
			t.Errorf("url = %q（旗標的值不該被當成網址）", a.url)
		}
	}
}
