package cookies

import "testing"

// 使用者貼上的 cookie 內容不可信，正規化不得 panic。
func FuzzNormalize(f *testing.F) {
	f.Add("youtube", "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tabc\n")
	f.Add("bilibili", "SESSDATA=abc; bili_jct=x")
	f.Add("bilibili", `[{"name":"SESSDATA","value":"v","domain":".bilibili.com"}]`)
	f.Add("youtube", "\x00\xff\t\t\t\t\t\t")
	f.Fuzz(func(t *testing.T, platform, content string) {
		_, _, _ = Normalize(platform, content)
	})
}
