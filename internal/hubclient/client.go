// Package hubclient 是 Client 對 Hub 的連線（HSHR、ADR-0032）：驗證 token、上傳分享的 bundle、
// ASR 前查詢、取得版本資訊與原始內容。
//
// Hub 的回應是不可信的輸入：本體有大小上限、JSON 欄位逐一驗證、內容以版本的 sha256 核對，
// 錯誤依 ADR-0032 第 5 點對應成 Client 的錯誤（*Error），訊息不含 token 與 Hub 的原文（422 除外，會先清理）。
// 逾時以注入的 clock 計算（HSHR 待決問題 S7），不用 http.Client.Timeout。
package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// 逾時與大小上限（ADR-0032 第 4 點）。
const (
	// DefaultTimeout 是一般請求的逾時。
	DefaultTimeout = 30 * time.Second
	// LookupTimeout 是 ASR 前查詢的逾時（HSHR-R15）。
	LookupTimeout = 5 * time.Second
	// MaxContentBytes 是版本內容的上限（HSHR-R10）。
	MaxContentBytes = 64 << 20
	// maxJSONBytes 是一般 JSON 回應的上限。
	maxJSONBytes = 4 << 20
	// maxErrorBytes 是錯誤回應讀取的上限。
	maxErrorBytes = 64 << 10
	// maxLookupVideos、maxLookupVersions 是 lookup 回應處理的上限。
	maxLookupVideos   = 1000
	maxLookupVersions = 200
)

// TokenFunc 回傳目前的 Hub token；沒有設定時 ok 為 false。
type TokenFunc func(ctx context.Context) (token string, ok bool, err error)

// Client 連到一個 Hub。URL 為空時代表沒有設定 Hub。
type Client struct {
	url   string
	token TokenFunc
	http  *http.Client
	clock clock.Clock
}

// New 建立 Client。hc 的轉址設定會被取代成不跟隨（避免把 token 帶到別的主機）。
func New(hubURL string, token TokenFunc, hc *http.Client, clk clock.Clock) *Client {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Timeout = 0
	return &Client{url: strings.TrimRight(hubURL, "/"), token: token, http: &c, clock: clk}
}

// URL 回傳 Hub 的網址（沒有設定時為空字串）。
func (c *Client) URL() string {
	if c == nil {
		return ""
	}
	return c.url
}

// Configured 回報有沒有 hub.url 與 token。
func (c *Client) Configured(ctx context.Context) (bool, error) {
	if c == nil || c.url == "" {
		return false, nil
	}
	_, ok, err := c.token(ctx)
	return ok, err
}

type hubErrorBody struct {
	Code               string       `json:"code"`
	Message            string       `json:"message"`
	RequiredPermission string       `json:"required_permission"`
	RetryAfterS        *int         `json:"retry_after_s"`
	Errors             []FieldError `json:"errors"`
}

// do 送出請求並在逾時內讀完本體（上限 limit）。成功（2xx）時回傳本體，否則回傳 *Error。
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, timeout time.Duration, limit int64) ([]byte, http.Header, error) {
	return c.doRaw(ctx, method, path, query, body, contentType, timeout, limit, false)
}

// doRaw 同 do；verbatim 為 true 時回傳未改寫的本體（版本內容），含有 token 時視為內容驗證失敗。
// 其他回應中的 token 一律換成 redactedToken（Hub 是不可信的，HSHR 待決問題 S18）。
func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, timeout time.Duration, limit int64, verbatim bool) ([]byte, http.Header, error) {
	if c == nil || c.url == "" {
		return nil, nil, ErrNotConfigured
	}
	tok, ok, err := c.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !ok || tok == "" {
		return nil, nil, ErrNotConfigured
	}
	u := c.url + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	timer := c.clock.NewTimer(timeout)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-timer.C():
			timedOut.Store(true)
			cancel()
		case <-done:
			timer.Stop()
		}
	}()
	req, err := http.NewRequestWithContext(rctx, method, u, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, connError(ctx, err, timedOut.Load())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		raw = scrubToken(raw, tok)
		var eb hubErrorBody
		_ = json.Unmarshal(raw, &eb)
		return nil, nil, statusError(resp.StatusCode, resp.Header, eb)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, connError(ctx, err, timedOut.Load())
	}
	if int64(len(data)) > limit {
		return nil, nil, &Error{Kind: KindContentInvalid, Status: resp.StatusCode, Detail: fmt.Sprintf("回應超過 %d MiB 的上限", limit>>20)}
	}
	if verbatim {
		// 版本內容有 sha256，不能改寫：含有 token 時整份拒絕。
		if containsToken(data, tok) {
			return nil, nil, &Error{Kind: KindIntegrity, Status: resp.StatusCode}
		}
		return data, resp.Header, nil
	}
	return scrubToken(data, tok), resp.Header, nil
}

// connError 是連線層的錯誤：呼叫端自己取消時原樣回傳 context 的錯誤，否則是 hub_unavailable。
func connError(ctx context.Context, err error, timedOut bool) error {
	if timedOut {
		return &Error{Kind: KindUnavailable, Reason: reasonTimeout, Timeout: true}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Kind: KindUnavailable, Reason: reasonTimeout, Timeout: true}
	}
	return &Error{Kind: KindUnavailable, Reason: reasonConnectFailed}
}

func decodeJSON(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return &Error{Kind: KindContentInvalid, Detail: "回應不是預期的 JSON"}
	}
	return nil
}

// Me 是 GET /api/v1/me 的結果（測試連線用）。
type Me struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Permissions []string `json:"permissions"`
}

// Me 驗證 token 並取得帳號與有效權限。
func (c *Client) Me(ctx context.Context) (Me, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, nil, "", DefaultTimeout, maxJSONBytes)
	if err != nil {
		return Me{}, err
	}
	var m Me
	if err := decodeJSON(data, &m); err != nil {
		return Me{}, err
	}
	m.ID, m.DisplayName = cleanText(m.ID, 64), cleanText(m.DisplayName, 100)
	perms := m.Permissions[:0]
	for _, p := range m.Permissions {
		if contains(permissions, p) {
			perms = append(perms, p)
		}
	}
	m.Permissions = perms
	return m, nil
}

// SharedVersion 是分享結果的一個版本。
type SharedVersion struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Variant      string `json:"variant"`
	Lang         string `json:"lang"`
	Deduplicated bool   `json:"deduplicated"`
}

// ShareResult 是 POST /api/v1/shares 的結果。
type ShareResult struct {
	ShareID  string          `json:"share_id"`
	Versions []SharedVersion `json:"versions"`
}

var (
	idRe   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	langRe = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)
)

// Share 上傳 bundle（multipart：bundle、attest、visibility）。
func (c *Client) Share(ctx context.Context, bundle []byte, visibility string) (ShareResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("bundle", "share.notesteme.zip")
	if err != nil {
		return ShareResult{}, err
	}
	if _, err := fw.Write(bundle); err != nil {
		return ShareResult{}, err
	}
	if err := mw.WriteField("attest", "true"); err != nil {
		return ShareResult{}, err
	}
	if visibility != "" {
		if err := mw.WriteField("visibility", visibility); err != nil {
			return ShareResult{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return ShareResult{}, err
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/shares", nil, &buf, mw.FormDataContentType(), DefaultTimeout, maxJSONBytes)
	if err != nil {
		return ShareResult{}, err
	}
	var r ShareResult
	if err := decodeJSON(data, &r); err != nil {
		return ShareResult{}, err
	}
	if !idRe.MatchString(r.ShareID) {
		return ShareResult{}, &Error{Kind: KindContentInvalid, Detail: "分享結果缺少合法的 share_id"}
	}
	out := r.Versions[:0]
	for _, v := range r.Versions {
		if !idRe.MatchString(v.ID) || len(out) >= 500 {
			continue
		}
		v.Kind, v.Variant, v.Lang = cleanText(v.Kind, 32), cleanText(v.Variant, 32), cleanText(v.Lang, 64)
		out = append(out, v)
	}
	r.Versions = out
	return r, nil
}

// LookupVersion 是查詢結果中的一個原始字幕版本。
type LookupVersion struct {
	ID            string `json:"id"`
	Variant       string `json:"variant"`
	Lang          string `json:"lang"`
	SHA256        string `json:"sha256"`
	Score         int    `json:"score"`
	ModelVerified bool   `json:"model_verified"`
}

// LookupVideo 是查詢結果中的一部影片。
type LookupVideo struct {
	VideoKeySHA256 string          `json:"video_key_sha256"`
	Versions       []LookupVersion `json:"versions"`
}

// Lookup 以 hash 前綴查詢（HSHR-R13）；逾時為 LookupTimeout。請求只含平台與前綴。
func (c *Client) Lookup(ctx context.Context, platform, prefix string) ([]LookupVideo, error) {
	q := url.Values{"platform": {platform}, "prefix": {prefix}}
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/lookup", q, nil, "", LookupTimeout, maxJSONBytes)
	if err != nil {
		return nil, err
	}
	var r struct {
		Videos []LookupVideo `json:"videos"`
	}
	if err := decodeJSON(data, &r); err != nil {
		return nil, err
	}
	var out []LookupVideo
	for _, v := range r.Videos {
		if len(out) >= maxLookupVideos {
			break
		}
		if !shaRe.MatchString(v.VideoKeySHA256) || !strings.HasPrefix(v.VideoKeySHA256, prefix) {
			continue
		}
		var vs []LookupVersion
		for _, x := range v.Versions {
			if len(vs) >= maxLookupVersions {
				break
			}
			if !idRe.MatchString(x.ID) || !shaRe.MatchString(x.SHA256) || !langRe.MatchString(x.Lang) ||
				(x.Variant != "source" && x.Variant != "normalized") {
				continue
			}
			vs = append(vs, x)
		}
		out = append(out, LookupVideo{VideoKeySHA256: v.VideoKeySHA256, Versions: vs})
	}
	return out, nil
}

// VideoRef 是版本所屬的影片。
type VideoRef struct {
	Platform   string `json:"platform"`
	ExternalID string `json:"external_id"`
	Part       *int   `json:"part"`
}

// Version 是 GET /api/v1/versions/{id} 中 Client 會用到的欄位（已驗證）。
type Version struct {
	ID              string
	Video           VideoRef
	Kind            string
	Variant         string
	Lang            string
	SHA256          string
	TranscribeModel string
	CorrectModel    string
	TranslateModel  string
	ModelVerified   bool
	Uploader        string
}

type versionBody struct {
	ID              string   `json:"id"`
	Video           VideoRef `json:"video"`
	Kind            string   `json:"kind"`
	Variant         string   `json:"variant"`
	Lang            string   `json:"lang"`
	SHA256          string   `json:"sha256"`
	TranscribeModel string   `json:"transcribe_model"`
	CorrectModel    string   `json:"correct_model"`
	TranslateModel  string   `json:"translate_model"`
	ModelVerified   bool     `json:"model_verified"`
	Uploaders       []struct {
		DisplayName string `json:"display_name"`
	} `json:"uploaders"`
}

// 平台的 external_id 格式：通過才會拿來組網址（不讓 Hub 的字串變成要抓取的 URL）。
var externalIDRe = map[string]*regexp.Regexp{
	"youtube":  regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`),
	"bilibili": regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`),
	// Podcast 的 external_id 是 <feed key>/<guid>，沒辦法從它組出網址；只用來比對已經存在的影音。
	"podcast": regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,512}$`),
}

// ValidVersionID 回報 id 是否是合法的版本 ID（送出前檢查，避免路徑注入）。
func ValidVersionID(id string) bool { return idRe.MatchString(id) }

// Version 取得版本資訊並驗證欄位。
func (c *Client) Version(ctx context.Context, id string) (Version, error) {
	if !idRe.MatchString(id) {
		return Version{}, &Error{Kind: KindNotFound, Status: http.StatusNotFound}
	}
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/versions/"+url.PathEscape(id), nil, nil, "", DefaultTimeout, maxJSONBytes)
	if err != nil {
		return Version{}, err
	}
	var b versionBody
	if err := decodeJSON(data, &b); err != nil {
		return Version{}, err
	}
	return checkVersion(id, b)
}

func checkVersion(id string, b versionBody) (Version, error) {
	bad := func(what string) (Version, error) {
		return Version{}, &Error{Kind: KindContentInvalid, Detail: "版本資訊的 " + what + " 不正確"}
	}
	if b.ID != id {
		return bad("id")
	}
	re, ok := externalIDRe[b.Video.Platform]
	if !ok {
		return bad("video.platform")
	}
	if !re.MatchString(b.Video.ExternalID) {
		return bad("video.external_id")
	}
	if b.Video.Part != nil && (*b.Video.Part < 1 || *b.Video.Part > 10000 || b.Video.Platform != "bilibili") {
		return bad("video.part")
	}
	if !shaRe.MatchString(b.SHA256) {
		return bad("sha256")
	}
	if b.Kind == "" || len(b.Kind) > 32 || (b.Lang != "" && !langRe.MatchString(b.Lang)) {
		return bad("kind／lang")
	}
	v := Version{
		ID: b.ID, Video: b.Video, Kind: b.Kind, Variant: cleanText(b.Variant, 32), Lang: b.Lang, SHA256: b.SHA256,
		TranscribeModel: cleanText(b.TranscribeModel, 200), CorrectModel: cleanText(b.CorrectModel, 200),
		TranslateModel: cleanText(b.TranslateModel, 200), ModelVerified: b.ModelVerified,
	}
	if len(b.Uploaders) > 0 {
		v.Uploader = cleanText(b.Uploaders[0].DisplayName, 100)
	}
	return v, nil
}

// Content 取得版本的原始內容，確認 sha256 等於 wantSHA（HSHR-R10）。
func (c *Client) Content(ctx context.Context, id, wantSHA string, timeout time.Duration) ([]byte, error) {
	if !idRe.MatchString(id) {
		return nil, &Error{Kind: KindNotFound, Status: http.StatusNotFound}
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	data, _, err := c.doRaw(ctx, http.MethodGet, "/api/v1/versions/"+url.PathEscape(id)+"/content", nil, nil, "", timeout, MaxContentBytes, true)
	if err != nil {
		return nil, err
	}
	if SHA256Hex(data) != wantSHA {
		return nil, &Error{Kind: KindIntegrity, Status: http.StatusOK}
	}
	return data, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
