// Package fakehub 是驗收測試用的假 Hub（HSHR，M5）。
//
// 只實作 Client 會用到的契約（ADR-0032 第 1 點，Routes）：驗證 token、上傳 bundle、
// 以 hash 前綴查詢、取得版本資訊與原始內容。狀態都在記憶體，並記錄收到的每個請求，
// 讓步驟斷言「沒有送出影片 ID」「bundle 不含本機路徑」。也支援故障注入：
// 固定的 HTTP 錯誤（Fail）、不回應（Hang）、內容與 sha256 不符（Tamper）。
//
// Hub 本身的行為（權限、去重、衍生關係…）由真 Hub 驗證（ADR-0030 第 3 點），這裡不實作。
// 這個套件只提供 handler 與狀態，登記到 fakenet（主機 Host）由 features/steps 負責（M5 的實作切片）。
package fakehub

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Host 是場景中 Client 的 hub.url 指到的主機名稱。
const Host = "hub.example.org"

// Routes 是 fakehub 實作的路由（ADR-0032 第 1 點的 Client 契約）。
// Hub 有 OpenAPI 快照之後，contract test 要確認每一條都存在於 Hub 的 OpenAPI。
var Routes = []string{
	"GET /api/v1/me",
	"POST /api/v1/shares",
	"GET /api/v1/lookup",
	"GET /api/v1/versions/{id}",
	"GET /api/v1/versions/{id}/content",
}

// 上限：bundle 與一般請求本體。
const (
	maxBundleBytes = 64 << 20
	maxBodyBytes   = 1 << 20
)

// Version 是 Hub 上的一個版本。
type Version struct {
	ID              string
	Platform        string // youtube、bilibili、podcast
	ExternalID      string
	Part            string // 沒有分P時為空字串
	Kind            string // transcript、chapters、note、frames
	Variant         string
	Lang            string
	Uploader        string
	TranscribeModel string
	CorrectModel    string
	TranslateModel  string
	ModelVerified   bool
	License         string
	Score           int
	// Content 是原始內容（字幕為 cues JSON）；SHA256 由 AddVersion 依它計算。
	Content []byte
	SHA256  string
}

// Request 是收到的一個請求的紀錄。分享的 bundle 不放在 Body，另外存在 Shares。
type Request struct {
	Method string
	Path   string
	Query  string
	// Header 是所有標頭以「名稱: 值」逐行連接（只用來檢查有沒有洩漏資料）。
	Header string
	Body   []byte
}

// Share 是收到的一次分享。
type Share struct {
	ID       string
	Manifest map[string]any
	// Files 是 bundle 中每個項目的內容（含 manifest.json），依名稱。
	Files map[string][]byte
	// Fields 是 multipart 的其他欄位（attest、visibility）。
	Fields map[string]string
}

// Server 是假 Hub 的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	mu       sync.Mutex
	token    string
	perms    map[string]bool
	versions map[string]*Version
	order    []string
	tampered map[string]bool
	fail     int
	hang     bool
	requests []Request
	shares   []Share
	seq      int
}

// DefaultPermissions 是 Client 的 token 預設擁有的權限（steps.md 4.32）。
var DefaultPermissions = []string{"read", "download", "regenerate", "upload"}

// New 建立接受 token、擁有 DefaultPermissions 的假 Hub。
func New(token string) *Server {
	s := &Server{token: token, versions: map[string]*Version{}, tampered: map[string]bool{}}
	s.SetPermissions(DefaultPermissions...)
	return s
}

// SetPermissions 取代 token 的權限。
func (s *Server) SetPermissions(perms ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.perms = map[string]bool{}
	for _, p := range perms {
		if p = strings.TrimSpace(p); p != "" {
			s.perms[p] = true
		}
	}
}

// AddVersion 登記一個版本並回傳它的 ID（v.ID 為空時自動產生）；sha256 依 Content 計算。
func (s *Server) AddVersion(v Version) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.ID == "" {
		s.seq++
		v.ID = fmt.Sprintf("0193a3b4-0000-7000-8000-%012d", s.seq)
	}
	sum := sha256.Sum256(v.Content)
	v.SHA256 = hex.EncodeToString(sum[:])
	if _, ok := s.versions[v.ID]; !ok {
		s.order = append(s.order, v.ID)
	}
	cp := v
	cp.Content = slices.Clone(v.Content)
	s.versions[v.ID] = &cp
	return v.ID
}

// Fail 讓所有請求回應 status（0 取消）。
func (s *Server) Fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = status
}

// Hang 讓所有請求不回應，直到請求的 context 結束（Client 逾時或斷線）。
func (s *Server) Hang() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hang = true
}

// Tamper 讓這個版本的 content 與 sha256 不符（仍是合法的 JSON）。
func (s *Server) Tamper(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tampered[id] = true
}

// Requests 回傳收到的全部請求。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Shares 回傳收到的全部分享。
func (s *Server) Shares() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.shares)
}

// Lookups 回傳 GET /api/v1/lookup 的次數。
func (s *Server) Lookups() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == http.MethodGet && r.Path == "/api/v1/lookup" {
			n++
		}
	}
	return n
}

// SawString 回報收到的請求（路徑、query、標頭、body，不含分享的 bundle）有沒有任何一個含有 text。
func (s *Server) SawString(text string) bool {
	for _, r := range s.Requests() {
		if strings.Contains(r.Path, text) || strings.Contains(r.Query, text) ||
			strings.Contains(r.Header, text) || bytes.Contains(r.Body, []byte(text)) {
			return true
		}
	}
	return false
}

// VideoKey 回傳 ASR 前查詢用的鍵 "<platform>:<external_id>:<part>"（ADR-0032 第 3 點）。
func VideoKey(platform, externalID, part string) string {
	return platform + ":" + externalID + ":" + part
}

// VideoKeySHA256 回傳 VideoKey 的 sha256（小寫十六進位）。
func VideoKeySHA256(platform, externalID, part string) string {
	sum := sha256.Sum256([]byte(VideoKey(platform, externalID, part)))
	return hex.EncodeToString(sum[:])
}

type apiError struct {
	Code               string `json:"code"`
	Message            string `json:"message"`
	RequiredPermission string `json:"required_permission,omitempty"`
	RetryAfterS        *int   `json:"retry_after_s,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, e apiError) { writeJSON(w, status, e) }

var versionPath = regexp.MustCompile(`^/api/v1/versions/([^/]+)(/content)?$`)

var prefixPattern = regexp.MustCompile(`^[0-9a-f]{4}$`)

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isShare := r.Method == http.MethodPost && r.URL.Path == "/api/v1/shares"
	limit := int64(maxBodyBytes)
	if isShare {
		limit = maxBundleBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		writeError(w, http.StatusRequestEntityTooLarge, apiError{Code: "too_large", Message: "請求太大。"})
		return
	}
	rec := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: headerText(r.Header)}
	if !isShare {
		rec.Body = body
	}

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	fail, hang := s.fail, s.hang
	s.mu.Unlock()

	if hang {
		<-r.Context().Done()
		return
	}
	if fail != 0 {
		e := apiError{Code: "fake_failure", Message: fmt.Sprintf("HTTP %d", fail)}
		if fail == http.StatusTooManyRequests {
			retry := 30
			e = apiError{Code: "rate_limited", Message: "請求太頻繁，請在 30 秒後再試。", RetryAfterS: &retry}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
		}
		writeError(w, fail, e)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.token || s.token == "" {
		writeError(w, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "請先登入 Hub。"})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
		s.serveMe(w)
	case isShare:
		if s.require(w, "upload") {
			s.serveShare(w, r, body)
		}
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/lookup":
		if s.require(w, "regenerate") {
			s.serveLookup(w, r)
		}
	case r.Method == http.MethodGet && versionPath.MatchString(r.URL.Path):
		m := versionPath.FindStringSubmatch(r.URL.Path)
		if m[2] == "" {
			if s.require(w, "read") {
				s.serveVersion(w, m[1])
			}
		} else if s.require(w, "regenerate") {
			s.serveContent(w, m[1])
		}
	default:
		writeError(w, http.StatusNotFound, apiError{Code: "not_found", Message: "找不到這個路由。"})
	}
}

func headerText(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	return b.String()
}

// require 檢查權限；沒有時寫出 403 並回傳 false。
func (s *Server) require(w http.ResponseWriter, perm string) bool {
	s.mu.Lock()
	ok := s.perms[perm]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusForbidden, apiError{Code: "forbidden", Message: "權限不足。", RequiredPermission: perm})
	}
	return ok
}

func (s *Server) serveMe(w http.ResponseWriter) {
	s.mu.Lock()
	perms := make([]string, 0, len(s.perms))
	for p := range s.perms {
		perms = append(perms, p)
	}
	s.mu.Unlock()
	sort.Strings(perms)
	writeJSON(w, http.StatusOK, map[string]any{"id": "fake-user", "display_name": "owner", "permissions": perms})
}

func (s *Server) serveShare(w http.ResponseWriter, r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err := r.ParseMultipartForm(maxBundleBytes); err != nil {
		writeError(w, http.StatusBadRequest, apiError{Code: "invalid_request", Message: "不是 multipart 請求。"})
		return
	}
	f, _, err := r.FormFile("bundle")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, apiError{Code: "bundle_invalid", Message: "缺少 bundle 欄位。"})
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxBundleBytes+1))
	if err != nil || len(raw) > maxBundleBytes {
		writeError(w, http.StatusRequestEntityTooLarge, apiError{Code: "bundle_too_large", Message: "bundle 太大。"})
		return
	}
	files, manifest, err := readBundle(raw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, apiError{Code: "bundle_invalid", Message: err.Error()})
		return
	}
	fields := map[string]string{}
	for k, v := range r.MultipartForm.Value {
		if len(v) > 0 {
			fields[k] = v[0]
		}
	}
	if fields["attest"] != "true" {
		writeError(w, http.StatusUnprocessableEntity, apiError{Code: "attest_required", Message: "分享前要確認你有權分享這些內容。"})
		return
	}

	s.mu.Lock()
	s.seq++
	share := Share{ID: fmt.Sprintf("share-%d", s.seq), Manifest: manifest, Files: files, Fields: fields}
	s.shares = append(s.shares, share)
	known := map[string]bool{}
	for _, v := range s.versions {
		known[v.SHA256] = true
	}
	s.mu.Unlock()

	var out []map[string]any
	arts, _ := manifest["artifacts"].([]any)
	for i, a := range arts {
		m, _ := a.(map[string]any)
		sha, _ := m["sha256"].(string)
		out = append(out, map[string]any{
			"kind": m["kind"], "variant": m["variant"], "lang": m["lang"],
			"version_id": fmt.Sprintf("%s-v%d", share.ID, i+1), "deduplicated": known[sha],
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"share_id": share.ID, "versions": out})
}

// readBundle 解開 bundle 並讀出 manifest；只做 fakehub 需要的最小檢查（真正的驗證在 Hub 與 BND）。
func readBundle(raw []byte) (map[string][]byte, map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, nil, fmt.Errorf("這不是 Notesteme bundle（無法以 zip 讀取）。")
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("無法讀取 bundle 項目 %q", f.Name)
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxBundleBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("無法讀取 bundle 項目 %q", f.Name)
		}
		total += int64(len(b))
		if total > maxBundleBytes {
			return nil, nil, fmt.Errorf("bundle 解壓後太大")
		}
		files[f.Name] = b
	}
	mb, ok := files["manifest.json"]
	if !ok {
		return nil, nil, fmt.Errorf("bundle 沒有 manifest.json")
	}
	var manifest map[string]any
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return nil, nil, fmt.Errorf("manifest.json 不是合法的 JSON")
	}
	return files, manifest, nil
}

func (s *Server) serveLookup(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	prefix := r.URL.Query().Get("prefix")
	if !prefixPattern.MatchString(prefix) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"code": "invalid_request", "message": "prefix 必須是 4 個十六進位字元。",
			"errors": []map[string]string{{"path": "query.prefix", "message": "必須是 4 個小寫十六進位字元。"}},
		})
		return
	}
	s.mu.Lock()
	groups := map[string][]map[string]any{}
	var keys []string
	for _, id := range s.order {
		v := s.versions[id]
		if v.Platform != platform || v.Kind != "transcript" || (v.Variant != "source" && v.Variant != "normalized") {
			continue
		}
		key := VideoKeySHA256(v.Platform, v.ExternalID, v.Part)
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], map[string]any{
			"id": v.ID, "variant": v.Variant, "lang": v.Lang, "sha256": v.SHA256,
			"score": v.Score, "model_verified": v.ModelVerified,
		})
	}
	s.mu.Unlock()
	videos := []map[string]any{}
	for _, k := range keys {
		videos = append(videos, map[string]any{"video_key_sha256": k, "versions": groups[k]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"videos": videos})
}

func (s *Server) version(id string) (Version, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[id]
	if !ok {
		return Version{}, false
	}
	cp := *v
	cp.Content = slices.Clone(v.Content)
	return cp, true
}

func (s *Server) serveVersion(w http.ResponseWriter, id string) {
	v, ok := s.version(id)
	if !ok {
		writeError(w, http.StatusNotFound, apiError{Code: "not_found", Message: "找不到這個版本。"})
		return
	}
	video := map[string]any{"platform": v.Platform, "external_id": v.ExternalID}
	if v.Part != "" {
		video["part"] = v.Part
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": v.ID, "video": video, "kind": v.Kind, "variant": v.Variant, "lang": v.Lang,
		"sha256": v.SHA256, "transcribe_model": v.TranscribeModel, "correct_model": v.CorrectModel,
		"translate_model": v.TranslateModel, "model_verified": v.ModelVerified, "license": v.License,
		"score": v.Score, "uploaders": []map[string]string{{"display_name": v.Uploader}}, "state": "visible",
	})
}

func (s *Server) serveContent(w http.ResponseWriter, id string) {
	v, ok := s.version(id)
	if !ok {
		writeError(w, http.StatusNotFound, apiError{Code: "not_found", Message: "找不到這個版本。"})
		return
	}
	s.mu.Lock()
	tampered := s.tampered[id]
	s.mu.Unlock()
	content := v.Content
	if tampered {
		content = tamper(content)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Notesteme-SHA256", v.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// tamper 在 JSON 物件的最後加一個欄位，內容仍是合法的 JSON，但 sha256 改變。
func tamper(content []byte) []byte {
	trimmed := bytes.TrimRight(content, " \n\r\t")
	if len(trimmed) > 1 && trimmed[len(trimmed)-1] == '}' {
		out := slices.Clone(trimmed[:len(trimmed)-1])
		if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
			out = append(out, ',')
		}
		return append(out, []byte(`"tampered":true}`)...)
	}
	return append(slices.Clone(content), ' ')
}
