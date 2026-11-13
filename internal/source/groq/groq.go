// Package groq 是 Groq 的語音轉錄（ASR）來源（SRC-R3、ADR-0006）：
// 把已下載的音檔送到 OpenAI 相容的 POST /audio/transcriptions，取得 segment 級的字幕。
//
// 連線一律由呼叫端提供的 http.Client 發出（netguard 的 Transport，GRD-R1）；
// API key 只放在 Authorization 標頭，不寫進網址、錯誤訊息或 log（NFR-SEC-3）。
package groq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const (
	// ID 是來源 ID（steps.md 第 3 節）。
	ID = "groq"
	// DefaultBaseURL 是 Groq 的 OpenAI 相容 API 位址。
	DefaultBaseURL = "https://api.groq.com/openai/v1"
	// DefaultModel 是預設的轉錄模型。
	DefaultModel = "whisper-large-v3-turbo"
	// Timing 是 M1 宣告的時間粒度（ADR-0006 的 Caps.Timing）：
	// 只取 segment 級時間戳，word 級是否可用待 M0 確認（SRC 規格的待決問題）。
	Timing = "segment"
)

// requestTimeout 是整個請求（含上傳音檔）的逾時；回應大小上限用 netguard.LimitFeed（GRD-R5）。
const requestTimeout = 10 * time.Minute

// ErrNoTranscript 表示 Groq 回傳的 segments 為空（例如靜音或沒有語音）；呼叫端改用下一個來源。
var ErrNoTranscript = errors.New("groq：沒有轉錄結果")

// 401、403 都是 needs_action；ErrNoKey 與 ErrKeyRejected 讓呼叫端分辨「沒設定」與「被拒絕」。
var (
	// ErrNoKey 表示沒有設定 API key（SRC-R10）；不會發出請求。
	ErrNoKey = errors.New("沒有設定 API key")
	// ErrKeyRejected 表示 API key 無效或沒有權限（401、403）。
	ErrKeyRejected = errors.New("API key 無效或沒有權限")
)

// Client 是 Groq 的 HTTP client。
type Client struct {
	// HTTP 是對外 HTTP 的 client（netguard 的 Transport）。
	HTTP *http.Client
	// BaseURL 為空時使用 DefaultBaseURL。
	BaseURL string
	// Model 為空時使用 DefaultModel。
	Model string
	// APIKey 是 Groq 的 API key。
	APIKey string
}

// Result 是轉錄結果。
type Result struct {
	// Lang 是 BCP-47 語言代碼，例如 en；Whisper 回報的語言不在對照表內時為空。
	Lang string
	// Cues 的時間為 segment 級，Timing 為 subtitle.TimingSource。
	Cues []subtitle.Cue
	// DurationS 是 Groq 回報的音檔長度（秒）；沒有回報時取最後一個 cue 的結束時間。
	DurationS float64
}

// transcriptionResponse 是 response_format=verbose_json 的回應。
type transcriptionResponse struct {
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// Transcribe 轉錄 audioPath 的音檔；langHint 是 ISO-639-1 語言提示，空字串表示讓 Whisper 自行偵測。
//
// 錯誤分類：
//   - 沒有 API key、401、403：needs_action，分別帶 ErrNoKey、ErrKeyRejected。
//   - 413（檔案過大）與其他 4xx、3xx（不跟隨轉址）、回應超過大小上限、無法讀取音檔：permanent。
//   - 429、5xx、網路錯誤、無法解析的回應：retryable。
//   - segments 為空：ErrNoTranscript，這個來源沒有結果。
//   - ctx 被取消時回傳 ctx.Err()，不是來源的錯。
func (c *Client) Transcribe(ctx context.Context, audioPath, langHint string) (*Result, error) {
	if c.APIKey == "" {
		return nil, fail(errclass.ClassNeedsAction, "", ErrNoKey)
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fail(errclass.ClassPermanent, "無法讀取音檔", err)
	}
	defer f.Close()

	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}

	// 以 pipe 串流上傳，不把整個音檔讀進記憶體。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeForm(mw, f, filepath.Base(audioPath), model, langHint))
	}()

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(base, "/")+"/audio/transcriptions", pr)
	if err != nil {
		pr.Close()
		return nil, fail(errclass.ClassPermanent, "", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	// 標頭帶有 API key，所以不跟隨轉址，3xx 當成 permanent。複製 client，不改動呼叫端提供的那一個。
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		pr.Close() // 讓上傳的 goroutine 結束
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// url.Error 只含網址（沒有標頭），不會帶出 API key。
		return nil, fail(errclass.ClassRetryable, "", err)
	}
	defer resp.Body.Close()
	defer pr.Close()

	// LimitBody 多讀 1 個 byte 才能分辨「剛好到上限」與「超過上限」，超過時不截斷。
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		if errors.Is(err, netguard.ErrTooLarge) {
			return nil, fail(errclass.ClassPermanent, "回應超過大小上限", err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail(errclass.ClassRetryable, "讀取回應失敗", err)
	}

	// 錯誤訊息只帶狀態碼，不帶回應內文。
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return nil, fail(errclass.ClassNeedsAction, fmt.Sprintf("HTTP %d", code), ErrKeyRejected)
	case code == http.StatusTooManyRequests || code >= 500:
		return nil, fail(errclass.ClassRetryable, fmt.Sprintf("HTTP %d", code), nil)
	case code == http.StatusRequestEntityTooLarge:
		return nil, fail(errclass.ClassPermanent, "HTTP 413（音檔超過 provider 的單檔上限）", nil)
	case code >= 300 && code < 400:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d（不跟隨轉址）", code), nil)
	case code != http.StatusOK:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d", code), nil)
	}

	var out transcriptionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fail(errclass.ClassRetryable, "回應不是預期的 JSON", err)
	}
	res := &Result{Lang: BCP47(out.Language), DurationS: out.Duration}
	for _, seg := range out.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		start, end := ms(seg.Start), ms(seg.End)
		end = max(end, start)
		res.Cues = append(res.Cues, subtitle.Cue{
			Index: len(res.Cues) + 1, StartMS: start, EndMS: end, Text: text, Timing: subtitle.TimingSource,
		})
	}
	if len(res.Cues) == 0 {
		return nil, ErrNoTranscript
	}
	if last := float64(res.Cues[len(res.Cues)-1].EndMS) / 1000; res.DurationS < last {
		res.DurationS = last
	}
	return res, nil
}

// writeForm 寫出 multipart 欄位；file 放最後，避免 provider 在讀完設定前就開始處理。
func writeForm(mw *multipart.Writer, file io.Reader, name, model, langHint string) error {
	if err := mw.WriteField("model", model); err != nil {
		return err
	}
	if err := mw.WriteField("response_format", "verbose_json"); err != nil {
		return err
	}
	if langHint != "" {
		if err := mw.WriteField("language", langHint); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return mw.Close()
}

// ms 把秒轉成毫秒；負數、NaN 與無限大視為 0。
func ms(sec float64) int64 {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
		return 0
	}
	return int64(math.Round(sec * 1000))
}

func fail(class errclass.Class, detail string, err error) error {
	return &errclass.Error{Class: class, Source: ID, Detail: detail, Err: err}
}
