package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 探索的 yt-dlp 呼叫（SUB-R2）：只開放 YouTube 的 tab／playlist 與 Bilibili 合集的 extractor（GRD-R6），
// 不帶 cookie（公開的清單不需要登入，SUB 不在範圍內的會員限定清單除外）。
const (
	listTimeout   = 5 * time.Minute
	maxToolOutput = netguard.LimitFeed // yt-dlp 的輸出上限 10 MiB（SUB-R11）
	// extractor 名稱依 yt-dlp 的原始碼（youtube:tab、youtube:playlist、BilibiliCollectionList），
	// 沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）。
	extractorsYouTube  = "youtube:tab,youtube:playlist"
	extractorsBilibili = "BilibiliCollectionList"
)

// lister 以 yt-dlp --flat-playlist 列出清單。
type lister struct {
	Runner toolexec.Runner
	Tools  toolexec.Resolver
}

// flatEntry 是 --flat-playlist 的一個條目（不可信的輸入）。
type flatEntry struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	Duration   *float64 `json:"duration"`
	LiveStatus string   `json:"live_status"`
	Timestamp  *int64   `json:"timestamp"`
}

// flatList 是 --flat-playlist -J 的輸出中用得到的部分。
type flatList struct {
	Title     string       `json:"title"`
	Channel   string       `json:"channel"`
	ChannelID string       `json:"channel_id"`
	Entries   []*flatEntry `json:"entries"`
}

var (
	// listGoneLine：清單不存在、私人或無法檢視（youtube:tab 的訊息，以及頻道不存在的 404）。
	listGoneLine = regexp.MustCompile(`(?im)^ERROR: \[[^\]]+\] \S+: (?:The playlist does not exist|This playlist is private|This playlist type is unviewable|This channel does not exist|The channel is not currently available|Video unavailable)|^ERROR:.*HTTP Error 404\b`)
	throttleLine = regexp.MustCompile(`(?im)^ERROR:.*HTTP Error (?:429|412)\b`)
	forbidLine   = regexp.MustCompile(`(?im)^ERROR:.*HTTP Error 403\b`)
)

// run 執行 yt-dlp 並把失敗分類成 *checkError（SUB-R12）；錯誤訊息不含 yt-dlp 的輸出。
func (l *lister) run(ctx context.Context, extractors string, end int, target string) (*flatList, error) {
	bin, err := l.Tools.Resolve(toolexec.YtDlp)
	if err != nil {
		return nil, &checkError{Class: errclass.ClassNeedsAction, Message: "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"}
	}
	args := []string{"--ignore-config", "--use-extractors", extractors, "--no-warnings", "--flat-playlist", "-J",
		"--playlist-end", strconv.Itoa(max(end, 1)), "--", target}
	res, err := l.Runner.Run(ctx, toolexec.Cmd{Path: bin, Args: args, Timeout: listTimeout})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &checkError{Class: errclass.ClassRetryable, Message: msgRetryYtDlp, cause: err}
	}
	if res.ExitCode != 0 {
		switch {
		case throttleLine.Match(res.Stderr):
			return nil, &checkError{Class: errclass.ClassRetryable, Message: fmt.Sprintf(msgRetryHTTP, throttleStatus(res.Stderr)), throttled: true}
		case forbidLine.Match(res.Stderr):
			return nil, &checkError{Class: errclass.ClassNeedsAction, Message: msgForbidden}
		case listGoneLine.Match(res.Stderr):
			return nil, &checkError{Class: errclass.ClassPermanent, Message: msgListGone}
		}
		return nil, &checkError{Class: errclass.ClassRetryable, Message: msgRetryYtDlp}
	}
	if int64(len(res.Stdout)) > maxToolOutput {
		msg, _ := netguard.PolicyMessage(&netguard.TooLargeError{Limit: maxToolOutput})
		return nil, &checkError{Class: errclass.ClassPermanent, Message: msg}
	}
	var out flatList
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, &checkError{Class: errclass.ClassRetryable, Message: msgRetryYtDlp, cause: errors.New("無法解析 yt-dlp 的輸出")}
	}
	return &out, nil
}

func throttleStatus(stderr []byte) string {
	if regexp.MustCompile(`HTTP Error 412\b`).Match(stderr) {
		return "HTTP 412"
	}
	return "HTTP 429"
}

// ChannelVideosURL 是頻道「影片」分頁的網址（不含 Shorts，SUB 待決問題 S6）。
func ChannelVideosURL(id string) string { return ChannelURL(id) + "/videos" }

// LegacyCollectionURL 是 Bilibili 合集的舊格式網址；呼叫 yt-dlp 時使用它（extractor 支援較久）。
func LegacyCollectionURL(mid, sid string) string {
	return "https://space.bilibili.com/" + mid + "/channel/collectiondetail?sid=" + url.QueryEscape(sid)
}
