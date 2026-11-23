// Package sponsorblock 查詢 SponsorBlock 的片段（SPB-R2、R3、R5）。
//
// 只用 hash 前綴 API，不送出影片 ID：
//
//	GET <base_url>/api/skipSegments/<sha256(videoID) 的前 4 個十六進位字元>?categories=[...]&actionTypes=["skip"]
//
// 回應是不可信輸入：大小上限 1 MiB、不跟隨轉址、10 秒逾時；解析失敗一律視為查詢失敗，不讓步驟失敗。
package sponsorblock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
)

// 查詢的限制（SPB-R2）。
const (
	// MaxResponse 是回應大小的上限。
	MaxResponse int64 = 1 << 20
	// DefaultTimeout 是一次查詢的逾時。
	DefaultTimeout = 10 * time.Second
	// PrefixLen 是送出的 hash 前綴長度。
	PrefixLen = 4
	// MinSegmentMS 是合併後保留的最短片段（SPB-R3）。
	MinSegmentMS int64 = 1000
)

// 查詢結果（SPB-R6 的 sponsorblock.outcome）。
const (
	OutcomeOK       = "ok"
	OutcomeNotFound = "not_found"
	OutcomeError    = "error"
)

// Categories 是可以使用的類別（SPB-R3）。
var Categories = []string{"sponsor", "selfpromo", "interaction", "intro", "outro", "preview", "music_offtopic", "filler"}

// Segment 是一個片段；時間單位為毫秒。
type Segment struct {
	Category string `json:"category"`
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	UUID     string `json:"uuid,omitempty"`
}

// Result 是一次查詢的結果。Outcome 是 error 時 Segments 為空，Reason 是給使用者看的原因
// （「HTTP 503」、「連線失敗」…，放在提醒訊息的括號內）。
type Result struct {
	Outcome  string
	Segments []Segment
	Reason   string
}

// Client 查詢 SponsorBlock。HTTP 必須是 netguard 的 client（ADR-0014）；Lookup 會複製它並關掉轉址。
type Client struct {
	HTTP *http.Client
	// BaseURL 是設定檔的 sponsorblock.base_url，結尾不含 /。
	BaseURL string
	// Timeout 為 0 時用 DefaultTimeout。
	Timeout time.Duration
}

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidVideoID 回報是不是 YouTube 的 11 字元影片 ID。
func ValidVideoID(id string) bool { return videoIDPattern.MatchString(id) }

// HashPrefix 回傳 sha256(videoID) 的前 PrefixLen 個十六進位字元。
func HashPrefix(videoID string) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])[:PrefixLen]
}

// RequestURL 組出查詢網址；categories 依原順序送出。
func RequestURL(base, videoID string, categories []string) (string, error) {
	cats, err := json.Marshal(nonNil(categories))
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("categories", string(cats))
	q.Set("actionTypes", `["skip"]`)
	return base + "/api/skipSegments/" + HashPrefix(videoID) + "?" + q.Encode(), nil
}

// Lookup 查詢影片的片段，依 categories 過濾，並以 durationMS（0 代表不知道片長）整理（見 Clean）。
// 查詢失敗（連線、逾時、非 200／404、格式錯誤、超過大小上限）時回傳 Outcome error、不回傳錯誤（SPB-R5）；
// 只有 ctx 本身被取消（關閉或取消中）時回傳錯誤，呼叫端不能把它當成查詢失敗寫回。
func (c *Client) Lookup(ctx context.Context, videoID string, categories []string, durationMS int64) (Result, error) {
	if !ValidVideoID(videoID) {
		return Result{}, fmt.Errorf("sponsorblock: 影片 ID 格式不正確")
	}
	if c == nil || c.HTTP == nil {
		return Result{}, errors.New("sponsorblock: 沒有設定 HTTP client")
	}
	raw, err := RequestURL(c.BaseURL, videoID, categories)
	if err != nil {
		return Result{}, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hc := *c.HTTP
	// 不跟隨轉址：3xx 原樣回來，當成查詢失敗（SPB-R2）。
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, raw, nil)
	if err != nil {
		return failed("連線失敗"), nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return failed(transportReason(err)), nil
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// SponsorBlock 以 404 表示沒有片段（SPB-R4）。
		_, _ = io.Copy(io.Discard, netguard.LimitBody(resp, MaxResponse))
		return Result{Outcome: OutcomeNotFound, Segments: []Segment{}}, nil
	default:
		return failed(fmt.Sprintf("HTTP %d", resp.StatusCode)), nil
	}
	body, err := io.ReadAll(netguard.LimitBody(resp, MaxResponse))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return failed(transportReason(err)), nil
	}
	segs, found, err := Parse(body, videoID, categories)
	if err != nil {
		return failed("回應格式不正確"), nil
	}
	if !found {
		// 前綴相同的其他影片有片段、這部影片沒有：同 404（SPB-B7）。
		return Result{Outcome: OutcomeNotFound, Segments: []Segment{}}, nil
	}
	return Result{Outcome: OutcomeOK, Segments: Clean(segs, durationMS)}, nil
}

func failed(reason string) Result {
	return Result{Outcome: OutcomeError, Segments: []Segment{}, Reason: reason}
}

// transportReason 是沒有 HTTP 狀態碼的失敗在提醒訊息中的原因（SPB-B8）。
func transportReason(err error) string {
	if errors.Is(err, netguard.ErrTooLarge) {
		return "回應超過大小上限"
	}
	return "連線失敗"
}

type apiSegment struct {
	Category   string    `json:"category"`
	ActionType string    `json:"actionType"`
	Segment    []float64 `json:"segment"`
	UUID       string    `json:"UUID"`
}

type apiVideo struct {
	VideoID  string       `json:"videoID"`
	Segments []apiSegment `json:"segments"`
}

var uuidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Parse 解析 hash 前綴 API 的回應，挑出 videoID 相同的項目，只留 actionType 為 skip、類別在 categories 中的片段
// （沒有整理，見 Clean）。found 回報回應中有沒有這部影片。時間不是有限的非負數、或結束不晚於開始的片段捨棄；
// 不認得的 UUID 格式改為空字串（不可信輸入，不原樣保存）。
func Parse(body []byte, videoID string, categories []string) (segs []Segment, found bool, err error) {
	var videos []apiVideo
	if err := json.Unmarshal(body, &videos); err != nil {
		return nil, false, err
	}
	segs = []Segment{}
	for _, v := range videos {
		if v.VideoID != videoID {
			continue
		}
		found = true
		for _, s := range v.Segments {
			if s.ActionType != "skip" || !slices.Contains(categories, s.Category) || len(s.Segment) != 2 {
				continue
			}
			start, end, ok := toMS(s.Segment[0], s.Segment[1])
			if !ok {
				continue
			}
			uuid := s.UUID
			if !uuidPattern.MatchString(uuid) {
				uuid = ""
			}
			segs = append(segs, Segment{Category: s.Category, StartMS: start, EndMS: end, UUID: uuid})
		}
	}
	return segs, found, nil
}

// maxSeconds 是接受的最大時間（約 1000 小時），避免換算毫秒時溢位。
const maxSeconds = 3.6e6

func toMS(start, end float64) (int64, int64, bool) {
	if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) {
		return 0, 0, false
	}
	start = max(start, 0)
	if end > maxSeconds {
		end = maxSeconds
	}
	s, e := int64(math.Round(start*1000)), int64(math.Round(end*1000))
	if e <= s {
		return 0, 0, false
	}
	return s, e, true
}

// Clean 整理片段（SPB-R3）：依開始時間排序，重疊或相接的合併（類別取最早開始的那一段，SPB-B9），
// 超出片長（durationMS > 0 時）的部分截掉，合併後短於 MinSegmentMS 的捨棄。回傳新的切片，依開始時間排序。
func Clean(segs []Segment, durationMS int64) []Segment {
	in := slices.Clone(segs)
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].StartMS != in[j].StartMS {
			return in[i].StartMS < in[j].StartMS
		}
		return in[i].EndMS > in[j].EndMS
	})
	var merged []Segment
	for _, s := range in {
		if n := len(merged); n > 0 && s.StartMS <= merged[n-1].EndMS {
			merged[n-1].EndMS = max(merged[n-1].EndMS, s.EndMS)
			continue
		}
		merged = append(merged, s)
	}
	out := []Segment{}
	for _, s := range merged {
		if durationMS > 0 {
			if s.StartMS >= durationMS {
				continue
			}
			s.EndMS = min(s.EndMS, durationMS)
		}
		if s.EndMS-s.StartMS < MinSegmentMS {
			continue
		}
		out = append(out, s)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
