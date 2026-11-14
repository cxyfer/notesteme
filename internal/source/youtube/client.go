package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 字幕軌的種類。
const (
	KindManual             = "manual"
	KindAuto               = "auto"
	KindPlatformTranslated = "platform-translated"
)

// 逾時與大小上限。
const (
	probeTimeout   = 2 * time.Minute
	captionTimeout = 2 * time.Minute
	maxCaptionSize = 16 << 20
)

// Client 透過 yt-dlp 取得 YouTube 的中繼資料與字幕。
type Client struct {
	Runner toolexec.Runner
	Tools  toolexec.Resolver
	// Cookies 提供 YouTube 的 cookie 檔；nil 或沒有 cookie 時不帶 --cookies（SEC-R7）。
	Cookies Cookies
}

// Cookies 提供平台 cookie 的暫存檔（*cookies.Store 實作它）。
// 沒有 cookie 時 path 為空字串，release 是 no-op；每次呼叫一個新檔案，release 時刪除。
type Cookies interface {
	Acquire(ctx context.Context, platform string) (path string, release func(), err error)
}

// platformYouTube 是 Cookies.Acquire 的平台名稱。
const platformYouTube = "youtube"

// cookieResumeOn 是需要 YouTube cookie 的步驟的恢復條件（見 errclass.Error.ResumeOn）。
const cookieResumeOn = "cookie:youtube"

// Track 是平台提供的一條字幕軌。
type Track struct {
	// Lang 是 yt-dlp 回報的語言代碼，例如 en、zh-Hans、en-orig。
	Lang string
	// Kind 是 KindManual、KindAuto 或 KindPlatformTranslated。
	Kind string
}

// Chapter 是平台提供的章節。
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Info 是 yt-dlp -J 的結果中，Notesteme 用得到的部分。
type Info struct {
	ID    string
	Title string
	// DurationS 為 nil 代表平台沒有提供。
	DurationS    *int
	Language     string
	Availability string
	// LiveStatus 是 not_live、is_live、is_upcoming、was_live、post_live。
	LiveStatus  string
	ChannelID   string
	Channel     string
	PublishedAt *time.Time
	// PublishedAtApprox 為 true 代表 PublishedAt 只有日期精度（來自 upload_date，是當天 00:00 UTC），
	// 實際發布時間在當天任何時刻。
	PublishedAtApprox bool
	Description       string
	Chapters          []Chapter
	Tracks            []Track
}

// Live 回報影片是否還在直播或尚未首播（SRC-R7）。
func (i *Info) Live() bool { return i.LiveStatus == "is_live" || i.LiveStatus == "is_upcoming" }

// baseArgs 是每次呼叫 yt-dlp 都帶的旗標。
//   - --ignore-config：不讀使用者的 yt-dlp 設定檔，行為才可重現。
//   - --use-extractors youtube：只開放 YouTube 的 extractor，關閉 generic extractor（ADR-0009），
//     因為 yt-dlp 的請求不經過 netguard。
//   - --no-playlist：網址帶有播放清單參數時也只處理單一影片。
func baseArgs() []string {
	return []string{"--ignore-config", "--use-extractors", "youtube", "--no-playlist", "--no-warnings"}
}

// run 執行 yt-dlp。有 YouTube cookie 時每次呼叫都帶 --cookies（SEC-R7）：
// 取得一個專屬這次呼叫的暫存檔，子程序結束後立即釋放。
func (c *Client) run(ctx context.Context, timeout time.Duration, args []string) (toolexec.Result, error) {
	return c.exec(ctx, timeout, args, true)
}

func (c *Client) exec(ctx context.Context, timeout time.Duration, args []string, withCookies bool) (toolexec.Result, error) {
	bin, err := c.Tools.Resolve(toolexec.YtDlp)
	if err != nil {
		// 找不到工具要使用者先處理，重試也沒有用。
		return toolexec.Result{}, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: err, Message: msgNoYtDlp, Actions: []string{actionInstallYtDlp},
		}
	}
	if withCookies && c.Cookies != nil {
		path, release, err := c.Cookies.Acquire(ctx, platformYouTube)
		if err != nil {
			if ctx.Err() != nil {
				return toolexec.Result{}, ctx.Err()
			}
			// 錯誤訊息由 cookies 套件負責不帶 cookie 內容；修好（例如重新上傳）後重試會成功。
			return toolexec.Result{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "無法取得 YouTube cookie", Err: err}
		}
		defer release()
		if path != "" {
			// 放在最前面：args 結尾有 "--" 加網址，選項不能在它之後。
			args = append([]string{"--cookies", path}, args...)
		}
	}
	res, err := c.Runner.Run(ctx, toolexec.Cmd{Path: bin, Args: args, Timeout: timeout})
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err() // 被取消：不是工具的錯，由呼叫端處理
		}
		return res, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: err}
	}
	if res.ExitCode != 0 {
		return res, classify(res)
	}
	return res, nil
}

// videoGoneLine 比對 yt-dlp 回報「影片不存在」的錯誤行。YouTube extractor 在 stderr 輸出
// "ERROR: [youtube] <id>: <原因>"（結束碼 1），原因的開頭有：
//   - "Video unavailable"：影片不存在；後面常接「This video has been removed by the uploader」、
//     「…the YouTube account associated with this video has been terminated」，地區封鎖也是這個開頭
//     （ADR-0009 同樣視為 permanent）。
//   - "Private video"：私人影片（「Private video. Sign in if you've been granted access to this video」）。
//   - "This video has been removed"、"This video is no longer available"：已移除的影片（舊版訊息）。
//
// 這些原因的依據是 yt-dlp 的 YouTube extractor 原始碼，沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）。
// 會員限定（"Join this channel"，見 membersOnlyLine）、年齡限制、"Sign in to confirm you're not a bot" 可以靠 cookie 解決，
// 不屬於這一類；其中會員限定另有 needs_action 的分類，其餘維持 retryable。
var videoGoneLine = regexp.MustCompile(
	`(?im)^ERROR: \[youtube[^\]]*\] \S+: (?:Video unavailable|Private video|This video has been removed|This video is no longer available)(.*)$`)

// 被限流時 yt-dlp 也會輸出「Video unavailable. This content isn't available, try again later.」，
// 那是暫時性的限制，不是影片不存在。
var videoGoneExceptions = []string{"try again later", "rate-limited"}

// videoGone 回報 yt-dlp 的 stderr 是否表示影片不存在（已刪除、私人、無法取得）。
func videoGone(stderr []byte) bool {
	for _, m := range videoGoneLine.FindAllSubmatch(stderr, -1) {
		rest := strings.ToLower(string(m[1]))
		transient := false
		for _, s := range videoGoneExceptions {
			transient = transient || strings.Contains(rest, s)
		}
		if !transient {
			return true
		}
	}
	return false
}

// forbiddenLine 比對 yt-dlp 回報 HTTP 403 的錯誤行：stderr 中以 "ERROR:" 開頭、含 "HTTP Error 403"。
// 下載媒體時是 "ERROR: unable to download video data: HTTP Error 403: Forbidden"，
// 抓取網頁時是 "ERROR: [youtube] <id>: Unable to download webpage: HTTP Error 403: Forbidden"，兩者都涵蓋。
// 只看 ERROR 行：WARNING 或重試中的訊息不代表最後失敗。
//
// 訊息格式的依據是 yt-dlp 的原始碼與 SRC-R9，沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）；
// 實際的 403 多半在下載媒體時發生（YouTube 對沒有 cookie 或過舊的 yt-dlp 回 403）。
var forbiddenLine = regexp.MustCompile(`(?im)^ERROR:.*HTTP Error 403\b`)

// 403 要使用者處理時，顯示的訊息與修正動作（SRC-R9，逐字）。
const (
	msgForbidden       = "YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。"
	actionUpdateYtDlp  = "更新 yt-dlp"
	actionUploadCookie = "上傳 cookie"
)

// membersOnlyLine 比對會員限定影片的錯誤行：
// "ERROR: [youtube] <id>: Join this channel to get access to members-only content like this video, and other exclusive perks."
// 文字依 yt-dlp 的 YouTube extractor 原始碼，沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）。
var membersOnlyLine = regexp.MustCompile(`(?im)^ERROR:.*Join this channel to get access to members-only content`)

// 會員限定影片的訊息（逐字）。修正動作沿用 actionUploadCookie。
const msgMembersOnly = "這部影片需要登入才能取得。上傳 YouTube cookie 後會自動繼續。"

// 找不到 yt-dlp 時顯示的訊息與修正動作（與播放清單展開、doctor 的文案一致）。
const (
	msgNoYtDlp         = "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"
	actionInstallYtDlp = "安裝 yt-dlp"
)

// classify 把 yt-dlp 的非 0 結束碼對應到錯誤類別（ADR-0009）。
//
// 先判斷影片不存在、私人或已移除：permanent，重試也不會成功（SRC 規格）。
// 再判斷 HTTP 403：needs_action，訊息建議先更新 yt-dlp，仍然失敗再上傳 cookie（SRC-R9）。
// TODO(ADR-0009)：429、5xx → retryable 並依 Retry-After 退避。
// 其他非 0 結束碼一律視為 retryable：有次數上限，用盡後步驟為 failed。
func classify(res toolexec.Result) error {
	e := &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ExitCode: res.ExitCode, Detail: tail(res.Stderr, 300)}
	switch {
	case videoGone(res.Stderr):
		e.Class = errclass.ClassPermanent
	case tooLargeLine.Match(res.Stderr):
		// 超過 --max-filesize（GRD-R5 的音檔上限）：重試也不會變小。
		e.Class, e.Err = errclass.ClassPermanent, ErrAudioTooLarge
	case membersOnlyLine.Match(res.Stderr):
		// 會員限定的影片要登入才看得到：上傳 cookie 後自動繼續。
		e.Class, e.Message, e.Actions, e.ResumeOn = errclass.ClassNeedsAction, msgMembersOnly, []string{actionUploadCookie}, cookieResumeOn
	case forbiddenLine.Match(res.Stderr):
		// 403 的最後手段是 cookie；上傳後自動恢復（PIPE-R4）。
		e.Class, e.Message, e.Actions, e.ResumeOn = errclass.ClassNeedsAction, msgForbidden,
			[]string{actionUpdateYtDlp, actionUploadCookie}, cookieResumeOn
	}
	return e
}

// tail 回傳輸出最後 n 個位元組的單行摘要。
func tail(b []byte, n int) string {
	b = bytes.TrimSpace(b)
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// Version 回傳 yt-dlp 的版本，寫進來源紀錄（ADR-0009）。
func (c *Client) Version(ctx context.Context) (string, error) {
	res, err := c.exec(ctx, probeTimeout, []string{"--version"}, false) // 與 cookie 無關
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(res.Stdout))
	if v == "" {
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "--version 沒有輸出"}
	}
	return v, nil
}

// Probe 以 yt-dlp -J 取得影片的中繼資料與可用的字幕軌，不下載任何內容。
func (c *Client) Probe(ctx context.Context, videoID string) (*Info, error) {
	args := append(baseArgs(), "-J", "--", WatchURL(videoID))
	res, err := c.run(ctx, probeTimeout, args)
	if err != nil {
		return nil, err
	}
	info, err := parseInfo(res.Stdout)
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "無法解析 -J 的輸出", Err: err}
	}
	return info, nil
}

type rawFormat struct {
	URL string `json:"url"`
}

type rawInfo struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	Duration          *float64               `json:"duration"`
	Language          *string                `json:"language"`
	Availability      *string                `json:"availability"`
	LiveStatus        *string                `json:"live_status"`
	ChannelID         *string                `json:"channel_id"`
	Channel           *string                `json:"channel"`
	Timestamp         *int64                 `json:"timestamp"`
	UploadDate        *string                `json:"upload_date"`
	Description       *string                `json:"description"`
	Chapters          []rawChapter           `json:"chapters"`
	Subtitles         map[string][]rawFormat `json:"subtitles"`
	AutomaticCaptions map[string][]rawFormat `json:"automatic_captions"`
}

type rawChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseInfo(b []byte) (*Info, error) {
	var r rawInfo
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.ID == "" {
		return nil, errors.New("缺少 id")
	}
	info := &Info{
		ID: r.ID, Title: r.Title, Language: str(r.Language), Availability: str(r.Availability),
		LiveStatus: str(r.LiveStatus), ChannelID: str(r.ChannelID), Channel: str(r.Channel), Description: str(r.Description),
	}
	if r.Duration != nil && *r.Duration >= 0 {
		d := int(math.Round(*r.Duration))
		info.DurationS = &d
	}
	switch {
	case r.Timestamp != nil:
		t := time.Unix(*r.Timestamp, 0).UTC()
		info.PublishedAt = &t
	case r.UploadDate != nil:
		if t, err := time.Parse("20060102", *r.UploadDate); err == nil {
			// 只有日期：當天 00:00 UTC，實際時間不確定。
			info.PublishedAt, info.PublishedAtApprox = &t, true
		}
	}
	for _, c := range r.Chapters {
		info.Chapters = append(info.Chapters, Chapter{Title: c.Title, Start: c.StartTime, End: c.EndTime})
	}

	for lang, fmts := range r.Subtitles {
		if len(fmts) > 0 {
			info.Tracks = append(info.Tracks, Track{Lang: lang, Kind: KindManual})
		}
	}
	for lang, fmts := range r.AutomaticCaptions {
		if len(fmts) == 0 {
			continue
		}
		// 機器翻譯的字幕軌，網址帶有 tlang 參數；沒有的是影片原文的自動字幕。
		kind := KindAuto
		if translated(fmts) {
			kind = KindPlatformTranslated
		}
		info.Tracks = append(info.Tracks, Track{Lang: lang, Kind: kind})
	}
	// map 的走訪順序不固定；排序讓結果可重現。
	sort.Slice(info.Tracks, func(i, j int) bool {
		a, b := info.Tracks[i], info.Tracks[j]
		if a.Lang != b.Lang {
			return a.Lang < b.Lang
		}
		return a.Kind < b.Kind
	})
	return info, nil
}

func translated(fmts []rawFormat) bool {
	for _, f := range fmts {
		if u, err := url.Parse(f.URL); err == nil && u.Query().Get("tlang") != "" {
			return true
		}
	}
	return false
}

// 語言代碼來自不可信的平台資料，要放進命令列：只接受一般的 BCP-47 字元，
// 而且不能以 - 開頭（yt-dlp 的 --sub-langs 以 - 表示排除）。
var safeLang = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// ErrNoCaptionFile 表示 yt-dlp 結束碼為 0，卻沒有寫出字幕檔（字幕軌消失或下載被略過）。
var ErrNoCaptionFile = errors.New("yt-dlp 沒有寫出字幕檔")

// FetchCaptions 下載影片的一條字幕軌（VTT），暫存檔寫在 dir，讀完後呼叫端清除 dir。
// 人工字幕只用 --write-subs，自動字幕與機器翻譯只用 --write-auto-subs，確保取得的就是要的那一種。
func (c *Client) FetchCaptions(ctx context.Context, videoID string, t Track, dir string) ([]byte, error) {
	if !safeLang.MatchString(t.Lang) || strings.EqualFold(t.Lang, "all") { // all 在 yt-dlp 代表全部字幕軌
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: fmt.Sprintf("不合法的語言代碼 %q", t.Lang)}
	}
	write := "--write-auto-subs"
	if t.Kind == KindManual {
		write = "--write-subs"
	}
	args := append(baseArgs(), "--skip-download", write, "--sub-langs", t.Lang, "--sub-format", "vtt",
		// -o 是 yt-dlp 的輸出模板，目錄中的 % 要跳脫成 %%；檔案實際寫在原本的 dir。
		"-o", filepath.Join(strings.ReplaceAll(dir, "%", "%%"), "sub.%(ext)s"), "--", WatchURL(videoID))
	if _, err := c.run(ctx, captionTimeout, args); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "sub."+t.Lang+".vtt")
	fi, err := os.Stat(path)
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: ErrNoCaptionFile}
	}
	if fi.Size() > maxCaptionSize {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "字幕檔過大"}
	}
	return os.ReadFile(path)
}
