package subscription

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
)

// 抓取 feed 的限制（SUB-R11）。轉址次數由 netguard 的 client 限制（GRD-R4）。
const (
	feedTimeout = 30 * time.Second
	maxXMLDepth = 64
	// MaxEntries 是每次列表最多採用的條目數（依文件順序，其餘忽略）。
	MaxEntries = 1000
)

// errNotFeed 是 XML 不合格（含 <!DOCTYPE>）或不是 RSS／Atom。
var errNotFeed = errors.New("not a feed")

// fetchFeed 以 netguard 的 client 抓取 feed（GET、只接受 2xx、10 MiB、30 秒），驗證 XML 之後解析。
// 錯誤一律是 *checkError，訊息是 SUB-R12 的固定文字。
func fetchFeed(ctx context.Context, c *http.Client, rawURL string) (*podcast.Feed, error) {
	if c == nil {
		return nil, &checkError{Class: errclass.ClassRetryable, Message: fmt.Sprintf(msgRetryHTTP, "連線失敗"), cause: errors.New("沒有對外 HTTP client")}
	}
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &checkError{Class: errclass.ClassPermanent, Message: msgUnsupportedURL, cause: err}
	}
	// 每次檢查都重新解析 DNS 並檢查位址（SUB-R11）：不重用連線池裡的連線，否則 DNS rebinding 之後仍沿用舊連線、不會被擋下。
	req.Close = true
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	resp, err := c.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	if err := statusError(resp.StatusCode); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		return nil, transportError(err)
	}
	return parseFeed(body, resp.Request.URL)
}

// transportError 分類連線層的錯誤：被防護擋下（內部網路、超過大小上限、轉址過多）是 permanent，訊息沿用 GRD；
// 其他（連線失敗、逾時）是 retryable。
func transportError(err error) *checkError {
	if msg, ok := netguard.PolicyMessage(err); ok {
		return &checkError{Class: errclass.ClassPermanent, Message: msg, cause: err}
	}
	return &checkError{Class: errclass.ClassRetryable, Message: fmt.Sprintf(msgRetryHTTP, "連線失敗"), cause: err}
}

// statusError 分類 HTTP 狀態碼（SUB-R12）；2xx 回傳 nil。
func statusError(code int) *checkError {
	switch {
	case code >= 200 && code <= 299:
		return nil
	case code == http.StatusTooManyRequests:
		return &checkError{Class: errclass.ClassRetryable, Message: fmt.Sprintf(msgRetryHTTP, "HTTP 429"), throttled: true}
	case code >= 500 || code == http.StatusRequestTimeout:
		return &checkError{Class: errclass.ClassRetryable, Message: fmt.Sprintf(msgRetryHTTP, fmt.Sprintf("HTTP %d", code))}
	case code == http.StatusNotFound || code == http.StatusGone:
		return &checkError{Class: errclass.ClassPermanent, Message: fmt.Sprintf(msgFeedGone, code)}
	}
	return &checkError{Class: errclass.ClassPermanent, Message: fmt.Sprintf(msgFeedStatus, code)}
}

// parseFeed 先嚴格驗證 XML（validateXML），再交給 podcast.Parse；base 用來解析相對網址。
func parseFeed(body []byte, base *url.URL) (*podcast.Feed, error) {
	if err := validateXML(body); err != nil {
		return nil, &checkError{Class: errclass.ClassPermanent, Message: msgNotFeed, cause: err}
	}
	feed, err := podcast.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, &checkError{Class: errclass.ClassPermanent, Message: msgNotFeed, cause: err}
	}
	if base != nil {
		for i := range feed.Episodes {
			ep := &feed.Episodes[i]
			ep.Link = resolve(base, ep.Link)
			if ep.Enclosure != nil {
				ep.Enclosure.URL = resolve(base, ep.Enclosure.URL)
			}
		}
	}
	if len(feed.Episodes) > MaxEntries {
		feed.Episodes = feed.Episodes[:MaxEntries]
	}
	return feed, nil
}

func resolve(base *url.URL, ref string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

// validateXML 以嚴格模式讀完整份 XML（SUB-R11）：格式錯誤、含 <!DOCTYPE>（不展開任何 entity）、
// 巢狀超過 maxXMLDepth 層都拒絕。HTML 的具名 entity（&nbsp; 等）視為合法。
// 錯誤不帶任何文件內容（SUB-R12：錯誤訊息與 log 不含 feed 的內容）。
func validateXML(body []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	// 只驗證結構：宣告的編碼照原樣讀（之後由 podcast.Parse 轉碼）。
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errNotFeed
		}
		switch t := tok.(type) {
		case xml.Directive:
			if bytes.HasPrefix(bytes.ToUpper(bytes.TrimSpace(t)), []byte("DOCTYPE")) {
				return errNotFeed
			}
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			if depth > maxXMLDepth || roots > 1 {
				return errNotFeed
			}
		case xml.EndElement:
			depth--
		}
	}
	if roots != 1 || depth != 0 {
		return errNotFeed
	}
	return nil
}

// safeLink 回報網址是不是有主機的 http／https（SUB-R11：其他的條目略過）。
func safeLink(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}
