// Package fakesource 是腳本化的字幕來源（ASR、Supadata；ADR-0015）。
//
// Script 記錄場景的腳本（回傳哪個 fixture、沒有結果、要求 cookie、大小上限）與呼叫次數；
// OpenAIHandler、SupadataHandler 是可以用 fakenet.Handle 掛到主機上的 http.Handler。
//
// 注意：兩個 handler 的 API 形狀只是目前的假設，實作 SRC 時可能調整；
// 它們刻意保持簡單、容易替換，不要在其他地方依賴細節。
package fakesource

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// Script 是場景的來源腳本。零值不可用，請用 New。並行安全。
type Script struct {
	fixturesDir string // features/fixtures 的絕對路徑；回應內容讀自其下的 sources/

	mu      sync.Mutex
	sources map[string]*source
}

type source struct {
	returns  map[string]string // item key -> fixture 名稱
	nothing  map[string]bool
	cookie   string
	maxBytes int64 // 0 代表沒有上限
	calls    int
}

// New 建立腳本；fixturesDir 是 features/fixtures 的絕對路徑。
func New(fixturesDir string) *Script {
	return &Script{fixturesDir: fixturesDir, sources: map[string]*source{}}
}

func (s *Script) src(id string) *source {
	src := s.sources[id]
	if src == nil {
		src = &source{returns: map[string]string{}, nothing: map[string]bool{}}
		s.sources[id] = src
	}
	return src
}

// ItemKey 把場景中的影音代號轉成假媒體標記與網址中使用的 item：
// yt:abc123 -> abc123；bili:BV../p1 -> BV..；bili:BV../p2 -> BV../p2；其他代號維持原樣
// （例如 local:meetup.mp4）。
func ItemKey(ref string) string {
	if id, ok := strings.CutPrefix(ref, "yt:"); ok {
		return id
	}
	if rest, ok := strings.CutPrefix(ref, "bili:"); ok {
		return strings.TrimSuffix(rest, "/p1")
	}
	return ref
}

// Returns 讓來源對 ref 回傳 fixture。fixture 是 features/fixtures/sources/ 內的檔名；
// 形如 "http-503" 的名稱不讀檔，直接回應該 HTTP 狀態碼。
func (s *Script) Returns(sourceID, ref, fixture string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.src(sourceID)
	k := ItemKey(ref)
	src.returns[k] = fixture
	delete(src.nothing, k)
}

// Nothing 讓來源對 ref 沒有結果（Supadata 回 404、ASR 回空的轉錄）。
func (s *Script) Nothing(sourceID, ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.src(sourceID)
	k := ItemKey(ref)
	src.nothing[k] = true
	delete(src.returns, k)
}

// RequiresCookie 讓來源要求請求帶有名為 cookie 的 cookie，否則回 401。
func (s *Script) RequiresCookie(sourceID, cookie string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.src(sourceID).cookie = cookie
}

// RequiredCookie 回傳來源要求的 cookie 名稱（沒有要求時為空字串）。
func (s *Script) RequiredCookie(sourceID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src(sourceID).cookie
}

// AcceptsUpTo 設定 ASR 上傳檔案的大小上限（位元組）；超過時回 413。
func (s *Script) AcceptsUpTo(sourceID string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.src(sourceID).maxBytes = bytes
}

// Calls 回傳來源收到的請求次數（包含被拒絕的請求）。
func (s *Script) Calls(sourceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src(sourceID).calls
}

// begin 記錄一次呼叫並檢查 cookie；失敗時已寫入回應，回傳 false。
func (s *Script) begin(sourceID string, w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	src := s.src(sourceID)
	src.calls++
	cookie := src.cookie
	s.mu.Unlock()
	if cookie != "" {
		if _, err := r.Cookie(cookie); err != nil {
			writeJSON(w, http.StatusUnauthorized, `{"error":"cookie_required","cookie":`+strconv.Quote(cookie)+`}`)
			return false
		}
	}
	return true
}

type outcome struct {
	fixture string
	nothing bool
	found   bool
}

func (s *Script) lookup(sourceID, item string) outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.src(sourceID)
	if src.nothing[item] {
		return outcome{nothing: true, found: true}
	}
	if f, ok := src.returns[item]; ok {
		return outcome{fixture: f, found: true}
	}
	return outcome{}
}

func (s *Script) maxBytes(sourceID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src(sourceID).maxBytes
}

// respond 依腳本結果寫出回應。notFound 是「沒有結果」時的回應。
func (s *Script) respond(w http.ResponseWriter, o outcome, item string, notFound func()) {
	switch {
	case o.nothing:
		notFound()
	case !o.found:
		// 沒有腳本的 ref：視同沒有結果，並在回應中說明，方便診斷。
		writeJSON(w, http.StatusNotFound, `{"error":"fakesource: no script for item `+strconv.Quote(item)+`"}`)
	case strings.HasPrefix(o.fixture, "http-"):
		code, err := strconv.Atoi(strings.TrimPrefix(o.fixture, "http-"))
		if err != nil || code < 400 || code > 599 {
			writeJSON(w, http.StatusInternalServerError, `{"error":"fakesource: bad status fixture"}`)
			return
		}
		writeJSON(w, code, `{"error":"scripted http `+strconv.Itoa(code)+`"}`)
	default:
		b, err := os.ReadFile(filepath.Join(s.fixturesDir, "sources", filepath.Clean("/"+o.fixture)))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, `{"error":`+strconv.Quote(fmt.Sprintf("fakesource: fixture %q: %v", o.fixture, err))+`}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

// OpenAIHandler 回傳 OpenAI 相容的 ASR 端點：POST /openai/v1/audio/transcriptions
// （multipart；file 欄位為假媒體檔，用其中的標記決定是哪個 item）。
// sourceID 是 provider 的 ID（groq、whisper-1、voxtral）。
// 沒有結果時回 200 與空的轉錄；上傳超過 AcceptsUpTo 時回 413。
func (s *Script) OpenAIHandler(sourceID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.begin(sourceID, w, r) {
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, `{"error":"expected multipart/form-data"}`)
			return
		}
		var head []byte
		var size int64
		var haveFile bool
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, `{"error":"bad multipart body"}`)
				return
			}
			if part.FormName() != "file" {
				_, _ = io.Copy(io.Discard, part)
				continue
			}
			haveFile = true
			head = make([]byte, 512)
			n, _ := io.ReadFull(part, head)
			head = head[:n]
			rest, _ := io.Copy(io.Discard, part)
			size = int64(n) + rest
		}
		if !haveFile {
			writeJSON(w, http.StatusBadRequest, `{"error":"missing file"}`)
			return
		}
		if limit := s.maxBytes(sourceID); limit > 0 && size > limit {
			writeJSON(w, http.StatusRequestEntityTooLarge, `{"error":"file too large"}`)
			return
		}
		item, _, ok := faketool.ParseMediaMarker(head)
		if !ok {
			writeJSON(w, http.StatusBadRequest, `{"error":"fakesource: upload is not a fake media file"}`)
			return
		}
		s.respond(w, s.lookup(sourceID, item), item, func() {
			writeJSON(w, http.StatusOK, `{"text":"","segments":[]}`)
		})
	})
}

// SupadataHandler 回傳 Supadata 風格的端點：GET /v1/transcript?url=<影片網址>，
// 依網址中的影片 ID 決定 item。來源 ID 固定為 "supadata"。沒有結果時回 404。
func (s *Script) SupadataHandler() http.Handler {
	const sourceID = "supadata"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/transcript" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.begin(sourceID, w, r) {
			return
		}
		item, err := faketool.ItemKeyFromURL(r.URL.Query().Get("url"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, `{"error":"invalid url"}`)
			return
		}
		s.respond(w, s.lookup(sourceID, item), item, func() {
			writeJSON(w, http.StatusNotFound, `{"error":"transcript-unavailable"}`)
		})
	})
}
