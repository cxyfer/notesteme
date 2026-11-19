package toolexec

import (
	"bytes"
	"regexp"
)

// cookieEntryLine 比對 yt-dlp 讀取 cookie 檔時，因為某筆 cookie 格式有問題而輸出的警告行：
// "WARNING: skipping cookie file entry due to …: '<整行 cookie>'"，行尾會原樣印出 cookie 的值。
var cookieEntryLine = regexp.MustCompile(`(?i)skipping cookie file entry`)

// redactedCookieLine 取代被遮蔽的行。
const redactedCookieLine = "[已遮蔽：含 cookie 內容的行]"

// RedactCookieLines 把 yt-dlp 的 stderr 中含 "skipping cookie file entry" 的整行遮蔽。
// 這種行會夾帶 cookie 的值；stderr 進入錯誤訊息（errclass.Error.Detail）與 log 之前一定要先過這個函式（SEC-R4）。
// 其他行與行數都不變；沒有命中時原樣回傳。
func RedactCookieLines(stderr []byte) []byte {
	if !cookieEntryLine.Match(stderr) {
		return stderr
	}
	lines := bytes.Split(stderr, []byte("\n"))
	for i, l := range lines {
		if !cookieEntryLine.Match(l) {
			continue
		}
		red := []byte(redactedCookieLine)
		if bytes.HasSuffix(l, []byte("\r")) {
			red = append(red, '\r')
		}
		lines[i] = red
	}
	return bytes.Join(lines, []byte("\n"))
}
