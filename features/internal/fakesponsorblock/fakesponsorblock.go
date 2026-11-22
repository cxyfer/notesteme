// Package fakesponsorblock 是驗收測試用的假 SponsorBlock（SPB，M3）。
//
// 只實作產品會用到的 hash 前綴 API（SPB-R2）：
//
//	GET /api/skipSegments/<sha256(videoID) 的前 4～32 個十六進位字元>?categories=[...]&actionTypes=[...]
//
// 回應是 SponsorBlock 的格式：[{videoID, hash, segments: [{category, actionType, segment: [start, end], UUID}]}]，
// 前綴相同的所有影片都會出現在回應中，由產品挑出相符的 videoID；沒有任何片段時回 404。
// 舊的 `GET /api/skipSegments?videoID=` 也會回應，但會被記錄成「收到了影片 ID」（SawVideoID），讓步驟斷言產品沒有用它。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 由 features/steps 負責（M3 的實作切片）。
package fakesponsorblock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Host 是預設的主機名稱（設定檔 sponsorblock.base_url 的預設值 https://sponsor.ajay.app）。
const Host = "sponsor.ajay.app"

// Segment 是一個片段；時間單位為秒。
type Segment struct {
	Category string
	Action   string // 空字串視為 skip
	Start    float64
	End      float64
}

// Request 是收到的一個請求的紀錄。
type Request struct {
	Path  string
	Query string
	// Header 是所有標頭值以換行連接（只用來檢查有沒有洩漏影片 ID）。
	Header string
}

// Server 是假 SponsorBlock 的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	mu       sync.Mutex
	segments map[string][]Segment // videoID → 片段
	fail     map[string]int       // videoID → HTTP 狀態碼
	requests []Request
}

// New 建立沒有任何片段的假 SponsorBlock。
func New() *Server {
	return &Server{segments: map[string][]Segment{}, fail: map[string]int{}}
}

// HashPrefix 回傳 sha256(videoID) 的前 n 個十六進位字元。
func HashPrefix(videoID string, n int) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])[:n]
}

// SetSegments 登記影片的片段（取代先前的登記）。
func (s *Server) SetSegments(videoID string, segs []Segment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segments[videoID] = slices.Clone(segs)
}

// Fail 讓查詢這部影片（含相同 hash 前綴的查詢）時回應 status。
func (s *Server) Fail(videoID string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[videoID] = status
}

// Requests 回傳收到的全部請求。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// RequestsFor 回傳 hash 前綴與這部影片相同（或直接帶了這個影片 ID）的請求數。
func (s *Server) RequestsFor(videoID string) int {
	full := HashPrefix(videoID, 64)
	n := 0
	for _, r := range s.Requests() {
		prefix, ok := strings.CutPrefix(r.Path, "/api/skipSegments/")
		switch {
		case ok && prefix != "" && strings.HasPrefix(full, strings.ToLower(prefix)):
			n++
		case strings.Contains(r.Query, videoID):
			n++
		}
	}
	return n
}

// SawVideoID 回報收到的請求中，有沒有任何一個的路徑、query 或標頭含有這個影片 ID。
func (s *Server) SawVideoID(videoID string) bool {
	for _, r := range s.Requests() {
		if strings.Contains(r.Path, videoID) || strings.Contains(r.Query, videoID) || strings.Contains(r.Header, videoID) {
			return true
		}
	}
	return false
}

type apiSegment struct {
	Category   string     `json:"category"`
	ActionType string     `json:"actionType"`
	Segment    [2]float64 `json:"segment"`
	UUID       string     `json:"UUID"`
}

type apiVideo struct {
	VideoID  string       `json:"videoID"`
	Hash     string       `json:"hash"`
	Segments []apiSegment `json:"segments"`
}

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var hdr []string
	for k, vs := range r.Header {
		hdr = append(hdr, k+": "+strings.Join(vs, ","))
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Path: r.URL.Path, Query: r.URL.RawQuery, Header: strings.Join(hdr, "\n")})
	s.mu.Unlock()

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var match func(videoID string) bool
	if prefix, ok := strings.CutPrefix(r.URL.Path, "/api/skipSegments/"); ok && prefix != "" {
		prefix = strings.ToLower(prefix)
		if len(prefix) < 4 || len(prefix) > 32 || strings.Trim(prefix, "0123456789abcdef") != "" {
			http.Error(w, "bad hash prefix", http.StatusBadRequest)
			return
		}
		match = func(id string) bool { return strings.HasPrefix(HashPrefix(id, 64), prefix) }
	} else if r.URL.Path == "/api/skipSegments" && r.URL.Query().Get("videoID") != "" {
		want := r.URL.Query().Get("videoID")
		match = func(id string) bool { return id == want }
	} else {
		http.NotFound(w, r)
		return
	}
	categories := parseList(r.URL.Query().Get("categories"), []string{"sponsor"})
	actions := parseList(r.URL.Query().Get("actionTypes"), []string{"skip"})

	s.mu.Lock()
	var out []apiVideo
	status := 0
	ids := make([]string, 0, len(s.segments)+len(s.fail))
	for id := range s.segments {
		ids = append(ids, id)
	}
	for id := range s.fail {
		if _, ok := s.segments[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !match(id) {
			continue
		}
		if code, ok := s.fail[id]; ok {
			status = code
		}
		v := apiVideo{VideoID: id, Hash: HashPrefix(id, 64)}
		for i, seg := range s.segments[id] {
			action := seg.Action
			if action == "" {
				action = "skip"
			}
			if !slices.Contains(categories, seg.Category) || !slices.Contains(actions, action) {
				continue
			}
			v.Segments = append(v.Segments, apiSegment{Category: seg.Category, ActionType: action, Segment: [2]float64{seg.Start, seg.End}, UUID: HashPrefix(id+string(rune('a'+i)), 64)})
		}
		if len(v.Segments) > 0 {
			out = append(out, v)
		}
	}
	s.mu.Unlock()

	switch {
	case status != 0:
		http.Error(w, http.StatusText(status), status)
	case len(out) == 0:
		http.Error(w, "Not Found", http.StatusNotFound)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// parseList 解析 SponsorBlock 的 JSON 陣列參數（例如 `["sponsor","intro"]`）；空字串回傳 def。
func parseList(raw string, def []string) []string {
	if raw == "" {
		return def
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
