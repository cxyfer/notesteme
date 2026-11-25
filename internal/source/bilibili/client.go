// Package bilibili 透過 yt-dlp 取得 Bilibili 的中繼資料與字幕（SRC 規格）。
//
// 語意依 yt-dlp 的 BiliBili extractor 原始碼確認，沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）：
//   - 字幕只有帶 --write-subs 時才會出現在 -J 輸出的 subtitles；鍵是 Bilibili 的 lan，AI 字幕是 ai-<lang>，
//     格式是 srt，另外一律有一個 danmaku（xml，彈幕）。沒有 automatic_captions。
//   - 未登入時字幕不列出（結束碼 0），stderr 印出 WARNING: [BiliBili] Subtitles are only available when logged in.
//     --no-warnings 會吃掉這行，所以 Probe 不能帶它。
//   - 沒有指定 p 而且有多個分P 時，--flat-playlist -J 回傳 entries；有 p 時 id 是 <BV>_p<n>。
package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 逾時與大小上限。
const (
	probeTimeout   = 2 * time.Minute
	captionTimeout = 2 * time.Minute
	maxCaptionSize = 16 << 20
)

// 需要 cookie 時給使用者看的訊息與修正動作（SRC-R8，逐字）。
const (
	// MsgAICaptionsNeedLogin 是 AI 字幕需要登入的訊息（Probe 回報 NeedsLogin、又沒有選到字幕軌時使用）。
	MsgAICaptionsNeedLogin = "這部影片的 AI 字幕需要登入 Bilibili 才能取得。上傳 Bilibili cookie，或改用語音轉錄。"
	ActionUploadCookie     = "上傳 cookie"
	ActionUseASR           = "改用語音轉錄"
	// ResumeOnCookie 是補上 Bilibili cookie 後恢復步驟的條件（errclass.Error.ResumeOn）。
	ResumeOnCookie = "cookie:" + Platform
	// Platform 是 Cookies.Acquire 使用的平台名稱。
	Platform = "bilibili"

	// msgLoginRequired 是 yt-dlp 在錯誤中要求登入（不只是字幕）時的訊息。
	msgLoginRequired = "Bilibili 要求登入才能存取這部影片。上傳 Bilibili cookie 後再重試。"
	// 找不到 yt-dlp 時的訊息與動作（與 YouTube、doctor 的文案一致）。
	msgNoYtDlp         = "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"
	actionInstallYtDlp = "安裝 yt-dlp"
)

// Cookies 是 consumer 端需要的 cookie 來源（internal/cookies.Store 實作同樣的方法簽名）。
// 沒有 cookie 時 path 為空字串，release 仍然可以呼叫。release 用來清除暫存檔。
type Cookies interface {
	Acquire(ctx context.Context, platform string) (path string, release func(), err error)
}

// Client 透過 yt-dlp 取得 Bilibili 的中繼資料與字幕。
type Client struct {
	Runner toolexec.Runner
	Tools  toolexec.Resolver
	// Cookies 可以是 nil（沒有 cookie 功能）；有的話每次呼叫 yt-dlp 都取得一次、用完釋放。
	Cookies Cookies
	// Log 記錄被丟掉的字幕軌等不影響結果的狀況；nil 時用 slog.Default()。
	Log *slog.Logger
	// Calls 記錄每次呼叫的結果（OPS-R17、R18）；nil 時不記錄。
	Calls CallRecorder
}

// CallRecorder 記錄 yt-dlp 呼叫的結果（*ops.Calls 實作它）：失敗率的統計與 cookie 最近一次成功使用。
type CallRecorder interface {
	ToolCall(ctx context.Context, tool string, failed bool)
	CookieOK(ctx context.Context, platform string)
	// CookieFailed 記錄 cookie 被一次失敗的呼叫使用；expired 表示 cookie 沒有登入（SEC-R12）。
	CookieFailed(ctx context.Context, platform string, expired bool)
}

func (c *Client) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

// Info 是 yt-dlp -J 的結果中，Notesteme 用得到的部分。Bilibili 沒有語言中繼資料。
type Info struct {
	ID    string
	Title string
	// DurationS 為 nil 代表平台沒有提供。
	DurationS    *int
	Uploader     string
	UploaderID   string
	PublishedAt  *time.Time
	Description  string
	Availability string
	// Tracks 是 subtitles 的原始鍵（排序後），包含 danmaku；呼叫端自行決定怎麼對應。
	Tracks []string
	// NeedsLogin 為 true 代表 yt-dlp 回報「字幕只有登入後才看得到」：沒有列出的字幕可能存在，只是看不到。
	NeedsLogin bool
}

// PartEntry 是多分P 影片中的一個分P。
type PartEntry struct {
	Part  int
	Title string
}

// VideoURL 回傳影片分P 的正規網址；呼叫 yt-dlp 時使用它，不直接使用使用者貼上的字串。
func VideoURL(bvid string, part int) string {
	return fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", url.PathEscape(bvid), max(part, 1))
}

func bareURL(bvid string) string { return "https://www.bilibili.com/video/" + url.PathEscape(bvid) }

// baseArgs 是每次呼叫都帶的旗標，故意不含 --no-warnings（見套件說明）。
//   - --ignore-config：不讀使用者的 yt-dlp 設定檔，行為才可重現。
//   - --use-extractors BiliBili：只開放 Bilibili 的 extractor，關閉 generic extractor（GRD-R6）。
func baseArgs() []string {
	return []string{"--ignore-config", "--use-extractors", "BiliBili"}
}

// run 取得 cookie、執行 yt-dlp、釋放 cookie。args 之後會補上 --cookies（有的話）與 -- <網址>。
func (c *Client) run(ctx context.Context, timeout time.Duration, args []string, target string) (toolexec.Result, error) {
	bin, err := c.Tools.Resolve(toolexec.YtDlp)
	if err != nil {
		return toolexec.Result{}, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: err, Message: msgNoYtDlp, Actions: []string{actionInstallYtDlp},
		}
	}
	usedCookie := false
	if c.Cookies != nil {
		path, release, err := c.Cookies.Acquire(ctx, Platform)
		if err != nil {
			if ctx.Err() != nil {
				return toolexec.Result{}, ctx.Err()
			}
			var ce *errclass.Error
			if errors.As(err, &ce) {
				return toolexec.Result{}, err
			}
			return toolexec.Result{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "cookies", Err: err}
		}
		if release != nil {
			defer release()
		}
		if path != "" {
			args = append(args, "--cookies", path)
			usedCookie = true
		}
	}
	args = append(args, "--", target)
	res, err := c.Runner.Run(ctx, toolexec.Cmd{Path: bin, Args: args, Timeout: timeout})
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err() // 被取消：不是工具的錯，由呼叫端處理
		}
		c.record(ctx, true, cookieResult(usedCookie, cookieFailed))
		return res, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: err}
	}
	if res.ExitCode != 0 {
		err := classify(res)
		use := cookieFailed
		if loginLine.Match(res.Stderr) {
			use = cookieExpired // 帶了 cookie 仍被要求登入：cookie 已經失效（SEC-R12）
		}
		// 影片本身不存在或地區限制（permanent）不算 yt-dlp 失敗（OPS-R18）。
		c.record(ctx, !isPermanent(err), cookieResult(usedCookie, use))
		return res, err
	}
	use := cookieOK
	if loginWarningLine.Match(res.Stderr) {
		// 帶了 cookie 卻仍印出「只有登入才看得到字幕」：cookie 沒有登入，不算成功使用（SEC-R12）。
		use = cookieExpired
	}
	c.record(ctx, false, cookieResult(usedCookie, use))
	return res, nil
}

// cookieUse 是一次呼叫對 cookie 的結果（SEC-R12）。
type cookieUse int

const (
	cookieNotUsed cookieUse = iota // 沒有帶 cookie
	cookieOK                       // 帶 cookie 而且成功
	cookieFailed                   // 帶 cookie 但失敗，原因與 cookie 無關
	cookieExpired                  // 帶 cookie 仍被要求登入
)

// cookieResult 在沒有帶 cookie 時回傳 cookieNotUsed，否則回傳 use。
func cookieResult(used bool, use cookieUse) cookieUse {
	if !used {
		return cookieNotUsed
	}
	return use
}

// record 把一次呼叫的結果交給 Calls（沒有設定時不記錄）。
func (c *Client) record(ctx context.Context, failed bool, use cookieUse) {
	if c.Calls == nil {
		return
	}
	c.Calls.ToolCall(ctx, "yt-dlp", failed)
	switch use {
	case cookieOK:
		c.Calls.CookieOK(ctx, Platform)
	case cookieFailed, cookieExpired:
		c.Calls.CookieFailed(ctx, Platform, use == cookieExpired)
	}
}

// goneLine 比對 yt-dlp 回報「影片不存在或地區限制」的錯誤行（只看 ERROR 行）：
//   - Bilibili API 的 code -404（啥都木有，影片不存在或已刪除）。
//   - code -10403（抱歉您所在地區不可觀看）與 yt-dlp 的 GeoRestrictedError 訊息。
//
// 依 yt-dlp 原始碼推測，沒有對真實的 yt-dlp 驗證。
var goneLine = regexp.MustCompile(
	`(?im)^ERROR:.*(?:[\s:]-(?:404|10403)\b|not available from your location|geo.?restrict|啥都木有)`)

// loginLine 比對 yt-dlp 要求登入的錯誤行：Bilibili API 的 code -101（账号未登录）或訊息提到登入。
var loginLine = regexp.MustCompile(`(?im)^ERROR:.*(?:[\s:]-101\b|log(?:ged )?in\b|未登录)`)

// loginWarningLine 比對未登入時 yt-dlp 在 stderr 輸出的 WARNING（字幕不會被列出）。
var loginWarningLine = regexp.MustCompile(`(?i)Subtitles are only available when logged in`)

// classify 把 yt-dlp 的非 0 結束碼對應到錯誤類別（ADR-0009）：
// 影片不存在或地區限制 → permanent；要求登入 → needs_action（cookie:bilibili）；
// 412（風控）與其他 → retryable，有次數上限，用盡後步驟為 failed。
func classify(res toolexec.Result) error {
	e := &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ExitCode: res.ExitCode, Detail: tail(res.Stderr, 300)}
	switch {
	case goneLine.Match(res.Stderr):
		e.Class = errclass.ClassPermanent
	case loginLine.Match(res.Stderr):
		e.Class, e.Message, e.Actions, e.ResumeOn = errclass.ClassNeedsAction, msgLoginRequired, []string{ActionUploadCookie}, ResumeOnCookie
	}
	return e
}

// tail 回傳輸出最後 n 個位元組的單行摘要。
// 先遮蔽 yt-dlp 讀 cookie 檔的警告行：它們會夾帶 cookie 的值，不能進入錯誤訊息與 log（SEC-R4）。
func tail(b []byte, n int) string {
	b = bytes.TrimSpace(toolexec.RedactCookieLines(b))
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// Probe 以 yt-dlp -J --write-subs 取得分P 的中繼資料與可用的字幕軌，不下載任何內容。
// stderr 有未登入的 WARNING 時 Info.NeedsLogin 為 true。
func (c *Client) Probe(ctx context.Context, bvid string, part int) (*Info, error) {
	args := append(baseArgs(), "--no-playlist", "-J", "--write-subs")
	res, err := c.run(ctx, probeTimeout, args, VideoURL(bvid, part))
	if err != nil {
		return nil, err
	}
	info, err := parseInfo(ctx, res.Stdout, c.log())
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "無法解析 -J 的輸出", Err: err}
	}
	info.NeedsLogin = loginWarningLine.Match(res.Stderr)
	return info, nil
}

type rawFormat struct {
	Ext string `json:"ext"`
}

type rawInfo struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	Duration     *float64               `json:"duration"`
	Uploader     *string                `json:"uploader"`
	UploaderID   *string                `json:"uploader_id"`
	Timestamp    *int64                 `json:"timestamp"`
	Description  *string                `json:"description"`
	Availability *string                `json:"availability"`
	Subtitles    map[string][]rawFormat `json:"subtitles"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseInfo(ctx context.Context, b []byte, log *slog.Logger) (*Info, error) {
	var r rawInfo
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.ID == "" {
		return nil, errors.New("缺少 id")
	}
	info := &Info{
		ID: r.ID, Title: r.Title, Uploader: str(r.Uploader), UploaderID: str(r.UploaderID),
		Description: str(r.Description), Availability: str(r.Availability),
	}
	if r.Duration != nil && *r.Duration >= 0 {
		d := int(math.Round(*r.Duration))
		info.DurationS = &d
	}
	if r.Timestamp != nil {
		t := time.Unix(*r.Timestamp, 0).UTC()
		info.PublishedAt = &t
	}
	for key, fmts := range r.Subtitles {
		if len(fmts) == 0 {
			continue
		}
		// 鍵來自不可信的平台資料；下載時 FetchCaptions 一定會拒絕不合法的鍵，
		// 所以在這裡就丟掉，免得選軌選到下載不了的軌而白白失敗。
		if !usableKey(key) {
			log.WarnContext(ctx, "丟掉不合法的字幕軌鍵", "key", key)
			continue
		}
		info.Tracks = append(info.Tracks, key)
	}
	// map 的走訪順序不固定；排序讓結果可重現。
	sort.Strings(info.Tracks)
	return info, nil
}

// Parts 以 yt-dlp --flat-playlist -J 展開影片的分P，只取得各分P 的編號與標題。
// 只有一個分P 時 yt-dlp 回傳單一影片，結果是 [{1, 標題}]。
// 錯誤分類同 Probe：影片不存在 permanent、找不到 yt-dlp 與要求登入 needs_action、其他 retryable。
func (c *Client) Parts(ctx context.Context, bvid string) ([]PartEntry, error) {
	args := append(baseArgs(), "--flat-playlist", "-J")
	res, err := c.run(ctx, probeTimeout, args, bareURL(bvid))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Title   string `json:"title"`
		Entries []*struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(res.Stdout, &raw); err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "無法解析分P 的輸出", Err: err}
	}
	if raw.Entries == nil {
		return []PartEntry{{Part: 1, Title: raw.Title}}, nil
	}
	out := make([]PartEntry, 0, len(raw.Entries))
	for i, e := range raw.Entries {
		if e == nil {
			continue // 已刪除的項目可能是 null
		}
		n := partNumber(e.URL, e.ID)
		if n < 1 {
			n = i + 1
		}
		out = append(out, PartEntry{Part: n, Title: e.Title})
	}
	return out, nil
}

var idPart = regexp.MustCompile(`_p([0-9]+)$`)

// partNumber 從 entry 的網址（?p=<n>）或 id（<BV>_p<n>）取出分P 編號；都沒有時回傳 0。
func partNumber(rawURL, id string) int {
	if u, err := url.Parse(rawURL); err == nil {
		if n, err := strconv.Atoi(u.Query().Get("p")); err == nil && n >= 1 {
			return n
		}
	}
	if m := idPart.FindStringSubmatch(id); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 0
}

// 字幕軌的鍵來自不可信的平台資料，要放進命令列：只接受一般的字元，
// 而且不能以 - 開頭（yt-dlp 的 --sub-langs 以 - 表示排除）。
var safeKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// usableKey 回報字幕軌的鍵能不能交給 yt-dlp 下載。all 在 yt-dlp 代表全部字幕軌，不能當成單一軌。
func usableKey(key string) bool {
	return safeKey.MatchString(key) && !strings.EqualFold(key, "all")
}

// ErrNoCaptionFile 表示 yt-dlp 結束碼為 0，卻沒有寫出字幕檔（字幕軌消失或下載被略過）。
var ErrNoCaptionFile = errors.New("yt-dlp 沒有寫出字幕檔")

// FetchCaptions 下載分P 的一條字幕軌（SRT），key 是 Probe 回報的原始鍵（例如 zh-Hans、ai-zh）。
// 暫存檔寫在 dir，讀完後呼叫端清除 dir。
func (c *Client) FetchCaptions(ctx context.Context, bvid string, part int, key, dir string) ([]byte, error) {
	if !usableKey(key) {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: fmt.Sprintf("不合法的字幕軌鍵 %q", key)}
	}
	args := append(baseArgs(), "--no-playlist", "--skip-download", "--write-subs", "--sub-langs", key, "--sub-format", "srt",
		// -o 是 yt-dlp 的輸出模板，目錄中的 % 要跳脫成 %%；檔案實際寫在原本的 dir。
		"-o", filepath.Join(strings.ReplaceAll(dir, "%", "%%"), "sub.%(ext)s"))
	if _, err := c.run(ctx, captionTimeout, args, VideoURL(bvid, part)); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "sub."+key+".srt")
	fi, err := os.Stat(path)
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: ErrNoCaptionFile}
	}
	if fi.Size() > maxCaptionSize {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "字幕檔過大"}
	}
	return os.ReadFile(path)
}

// Version 回傳 yt-dlp 的版本，寫進來源紀錄（ADR-0009）。
func (c *Client) Version(ctx context.Context) (string, error) {
	bin, err := c.Tools.Resolve(toolexec.YtDlp)
	if err != nil {
		return "", &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: err, Message: msgNoYtDlp, Actions: []string{actionInstallYtDlp},
		}
	}
	res, err := c.Runner.Run(ctx, toolexec.Cmd{Path: bin, Args: []string{"--version"}, Timeout: probeTimeout})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: err}
	}
	if res.ExitCode != 0 {
		return "", classify(res)
	}
	v := strings.TrimSpace(string(res.Stdout))
	if v == "" {
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "--version 沒有輸出"}
	}
	return v, nil
}

// isPermanent 回報錯誤是不是 permanent（影片本身不存在、私人或地區限制）。
func isPermanent(err error) bool {
	c, ok := errclass.ClassOf(err)
	return ok && c == errclass.ClassPermanent
}
