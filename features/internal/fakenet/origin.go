package fakenet

import (
	"fmt"
	"net/http"
	"strconv"
)

// Redirect 回傳固定以 302 轉到 target 的 handler。
func Redirect(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// 固定大小 feed 的開頭與結尾：合法的 RSS，中間以註解填充。
// 內容必須是合法的 feed，否則程式會因為「不是 feed」回 422，驗證不到大小上限。
const (
	sizedFeedHead = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<rss version="2.0"><channel><title>Sized feed</title>` +
		`<link>https://feeds.example.com/</link><description>filler</description><!--`
	sizedFeedTail = "--></channel></rss>\n"
	sizedFeedFill = 'x' // 註解內不能出現 "--"，填 'x' 最單純
)

// SizedFeedMin 是 FixedSizeFeed 可產生的最小總長度（開頭加結尾，不含填充）。
const SizedFeedMin = len(sizedFeedHead) + len(sizedFeedTail)

// FixedSizeFeed 回傳總長剛好 size bytes 的 RSS 回應：合法的開頭、註解填充、合法的結尾，
// 並帶 Content-Length。以串流寫出，不會在記憶體中組出整份內容。
// size 小於 SizedFeedMin 時回傳錯誤。
func FixedSizeFeed(size int) (http.Handler, error) {
	if size < SizedFeedMin {
		return nil, fmt.Errorf("fakenet: 固定大小的 feed 至少要 %d bytes，收到 %d", SizedFeedMin, size)
	}
	pad := size - SizedFeedMin
	chunk := make([]byte, 32*1024)
	for i := range chunk {
		chunk[i] = sizedFeedFill
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/rss+xml; charset=utf-8")
		h.Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		// 寫入失敗（對方中斷下載）就不再寫：這正是大小上限會觸發的情形。
		if _, err := w.Write([]byte(sizedFeedHead)); err != nil {
			return
		}
		for left := pad; left > 0; {
			n := min(left, len(chunk))
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			left -= n
		}
		_, _ = w.Write([]byte(sizedFeedTail))
	}), nil
}
