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
}

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
	Description string
	Chapters    []Chapter
	Tracks      []Track
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

func (c *Client) run(ctx context.Context, timeout time.Duration, args []string) (toolexec.Result, error) {
	bin, err := c.Tools.Resolve(toolexec.YtDlp)
	if err != nil {
		// 找不到工具要使用者先處理，重試也沒有用。
		return toolexec.Result{}, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: err}
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

// classify 把 yt-dlp 的非 0 結束碼對應到錯誤類別（ADR-0009）。
//
// TODO(SRC-R9)：HTTP 403 → needs_action，訊息建議先更新 yt-dlp；
// TODO(ADR-0009)：影片已刪除、私人、地區封鎖 → permanent；429、5xx → retryable 並依 Retry-After 退避。
// 目前所有非 0 結束碼一律視為 retryable：有次數上限，用盡後步驟為 failed。
func classify(res toolexec.Result) error {
	return &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ExitCode: res.ExitCode, Detail: tail(res.Stderr, 300)}
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
	res, err := c.run(ctx, probeTimeout, []string{"--version"})
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
			info.PublishedAt = &t
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
		"-o", filepath.Join(dir, "sub.%(ext)s"), "--", WatchURL(videoID))
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
