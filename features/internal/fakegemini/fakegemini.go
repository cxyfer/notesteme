// Package fakegemini 是驗收測試用的假 Gemini（GEM，M3）。
//
// 只實作 GEM-R4 的端點：POST /v1beta/models/<model>:generateContent，以 `x-goog-api-key` 驗證。
//   - 請求的 `contents[].parts[].file_data.file_uri` 必須是 YouTube 影片網址；影片的片長與公開狀態由步驟登記（Video）。
//     片長超過 MaxDuration 時回 400 INVALID_ARGUMENT，不是公開影片時回 403 PERMISSION_DENIED（GEM-R7）。
//   - 回答用 features/fixtures/llm 的 fixture：`{"output": …}` 的 output 以 JSON 字串放進
//     `candidates[0].content.parts[0].text`，和真實的 Gemini structured output 相同。
//   - Fail 讓接下來的請求回應指定的 HTTP 狀態（once 時只有一次）。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 與接到 LLM 步驟由 features/steps 負責（M3 的實作切片）。
package fakegemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Host 是預設的主機名稱。
const Host = "generativelanguage.googleapis.com"

// Video 是假 Gemini 認得的一部影片。
type Video struct {
	Duration time.Duration
	Public   bool
}

// Server 是假 Gemini 的狀態。
type Server struct {
	fixtures string // features/fixtures/llm
	key      string // 空字串代表不檢查 key

	mu          sync.Mutex
	maxDuration time.Duration
	videos      map[string]Video // YouTube 影片 ID → 屬性
	answers     []string         // 依序回答的 fixture；之後一直回答最後一個
	fail        int
	failOnce    bool
	requests    [][]byte
}

// New 建立假 Gemini。fixtures 是 fixture 目錄，key 是要求的 API key（空字串不檢查）。
func New(fixtures, key string) *Server {
	return &Server{fixtures: fixtures, key: key, videos: map[string]Video{}}
}

// SetMaxDuration 設定可以讀取的片長上限（0 代表不限制）。
func (s *Server) SetMaxDuration(d time.Duration) { s.mu.Lock(); s.maxDuration = d; s.mu.Unlock() }

// SetVideo 登記影片的片長與公開狀態。
func (s *Server) SetVideo(id string, v Video) { s.mu.Lock(); s.videos[id] = v; s.mu.Unlock() }

// Answer 設定依序回答的 fixture。
func (s *Server) Answer(fixtures ...string) { s.mu.Lock(); s.answers = fixtures; s.mu.Unlock() }

// Fail 讓請求回應 status；once 為 true 時只有下一次。
func (s *Server) Fail(status int, once bool) {
	s.mu.Lock()
	s.fail, s.failOnce = status, once
	s.mu.Unlock()
}

// Requests 回傳收到的請求 body（依抵達順序，含失敗的請求）。
func (s *Server) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.requests...)
}

type request struct {
	Contents []struct {
		Parts []struct {
			Text     string `json:"text,omitempty"`
			FileData *struct {
				FileURI string `json:"file_uri"`
			} `json:"file_data,omitempty"`
		} `json:"parts"`
	} `json:"contents"`
	GenerationConfig struct {
		ResponseMimeType string          `json:"responseMimeType"`
		ResponseSchema   json.RawMessage `json:"responseSchema"`
	} `json:"generationConfig"`
}

func googleError(w http.ResponseWriter, code int, status, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "status": status, "message": msg}})
}

// youTubeID 取出 https://www.youtube.com/watch?v=<id> 的影片 ID。
func youTubeID(uri string) (string, bool) {
	rest, ok := strings.CutPrefix(uri, "https://www.youtube.com/watch?v=")
	if !ok || len(rest) != 11 {
		return "", false
	}
	return rest, true
}

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unreadable body")
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, body)
	fail := s.fail
	if s.failOnce {
		s.fail, s.failOnce = 0, false
	}
	s.mu.Unlock()

	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1beta/models/") || !strings.HasSuffix(r.URL.Path, ":generateContent") {
		googleError(w, http.StatusNotFound, "NOT_FOUND", "unknown endpoint")
		return
	}
	if s.key != "" && r.Header.Get("x-goog-api-key") != s.key {
		googleError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "API key not valid")
		return
	}
	if fail != 0 {
		googleError(w, fail, http.StatusText(fail), "scripted failure")
		return
	}
	var req request
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&req); err != nil {
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	if req.GenerationConfig.ResponseMimeType != "application/json" || len(req.GenerationConfig.ResponseSchema) == 0 {
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "structured output (responseMimeType + responseSchema) is required")
		return
	}
	var uri string
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p.FileData != nil && uri == "" {
				uri = p.FileData.FileURI
			}
		}
	}
	id, ok := youTubeID(uri)
	if !ok {
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "file_data.file_uri must be a YouTube video URL")
		return
	}
	s.mu.Lock()
	v, known := s.videos[id]
	maxD := s.maxDuration
	var fixture string
	if len(s.answers) > 0 {
		fixture = s.answers[0]
		if len(s.answers) > 1 {
			s.answers = s.answers[1:]
		}
	}
	s.mu.Unlock()
	switch {
	case !known || !v.Public:
		googleError(w, http.StatusForbidden, "PERMISSION_DENIED", "The video is not accessible.")
		return
	case maxD > 0 && v.Duration > maxD:
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "The video is too long.")
		return
	case fixture == "":
		googleError(w, http.StatusInternalServerError, "INTERNAL", "no scripted answer")
		return
	}
	text, err := s.loadOutput(fixture)
	if err != nil {
		googleError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 1000, "candidatesTokenCount": 200},
	})
}

// loadOutput 讀取 fixture 的 output，回傳 compact JSON 字串。
func (s *Server) loadOutput(name string) (string, error) {
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	if name != filepath.Base(name) {
		return "", fmt.Errorf("fixture 名稱 %q 不正確", name)
	}
	b, err := os.ReadFile(filepath.Join(s.fixtures, name))
	if err != nil {
		return "", err
	}
	var doc struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Output) == 0 {
		return "", fmt.Errorf("fixture %s 沒有 output：%v", name, err)
	}
	var out bytes.Buffer
	if err := json.Compact(&out, doc.Output); err != nil {
		return "", err
	}
	return out.String(), nil
}
