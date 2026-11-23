// Package hubshare 產生送到 Hub 的 bundle（HSHR-R5、R6，ADR-0032 第 2 點）。
//
// 格式是 BND 的 bundle v0，另外套用白名單：created_by 只有 app_version、item 只有列出的欄位、
// 來源紀錄只保留白名單欄位並加上 parents_sha256（與衍生鏈上的 hub_origin）；產出 ID 每次重新產生，
// 不送本機的 artifact ID。產生之後以 internal/bundle 的驗證器自我檢查一次（Hub 會用同一個驗證器）。
package hubshare

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/bundle"
)

// Licenses 是 Client 接受的授權（HSHR-R3）。
var Licenses = []string{"CC-BY-4.0", "CC-BY-SA-4.0", "CC-BY-NC-4.0", "CC-BY-NC-SA-4.0", "CC0-1.0"}

// Item 是要分享的影音（ADR-0032 第 2 點的 item 白名單）。
type Item struct {
	Platform          string
	ExternalID        string
	Part              int
	Title             string
	ChannelExternalID string
	ChannelTitle      string
	PublishedAt       *time.Time
	DurationS         *int
	OriginalLang      string
	Availability      string
}

// HubOrigin 是從 Hub 取得的原始字幕（ADR-0031 第 3 點）。
type HubOrigin struct {
	VersionID string
	SHA256    string
}

// Image 是畫面產出附帶的一張圖片（JPEG）。
type Image struct {
	// N 是畫面編號（畫面 JSON 以它引用圖片）。
	N    int
	Data []byte
}

// Artifact 是要分享的一個產出。
type Artifact struct {
	Kind, Variant, Lang string
	// Data 是內容：字幕為 cues JSON、筆記為 Markdown、章節與畫面為 JSON。
	Data []byte
	// Provenance 是本機的來源紀錄（任意 JSON 物件，送出前依白名單過濾）。
	Provenance json.RawMessage
	// ParentsSHA256 是上游產出的內容雜湊（沒有上游時為空）。
	ParentsSHA256 []string
	// HubOrigin 非 nil 時寫進來源紀錄（衍生鏈上有 hub 來源，HSHR-R6）。
	HubOrigin *HubOrigin
	// Images 只用於 frames：圖片在 bundle 中名為 artifacts/<產出 ID>-<NNN>.jpg。
	Images []Image
}

// ImageName 是畫面圖片在 bundle 中的檔名。
func ImageName(artifactID string, n int) string {
	return fmt.Sprintf("artifacts/%s-%03d.jpg", artifactID, n)
}

// Format 是產出的格式字串（Hub 的 versions.format）。
func Format(kind string) string {
	switch kind {
	case "transcript":
		return bundle.CuesFormat
	case "note":
		return "markdown"
	}
	return "json"
}

func ext(kind string) string {
	if kind == "note" {
		return ".md"
	}
	return ".json"
}

const (
	maxField    = 512
	maxToolKeys = 32
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FilterProvenance 只保留 ADR-0032 第 2 點的白名單欄位；值的型別不對或太長時丟掉。
// parents_sha256 一律存在（沒有上游時是空陣列）。
func FilterProvenance(raw json.RawMessage, parents []string, origin *HubOrigin) map[string]any {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	out := map[string]any{}
	for _, k := range []string{"executor", "provider", "model", "model_reported", "params_sha256", "source", "timing", "created_at"} {
		if s, ok := short(in[k]); ok {
			out[k] = s
		}
	}
	for _, k := range []string{"model_verified", "cookie_required"} {
		if b, ok := in[k].(bool); ok {
			out[k] = b
		}
	}
	if m := pick(in["prompt"], "id", "version", "sha256"); m != nil {
		out["prompt"] = m
	}
	if m := pick(in["track"], "lang", "kind"); m != nil {
		out["track"] = m
	}
	if tv, ok := in["tool_versions"].(map[string]any); ok {
		m := map[string]any{}
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := short(tv[k]); ok && len(k) <= 64 && !looksLikePath(s) && len(m) < maxToolKeys {
				m[k] = s
			}
		}
		if len(m) > 0 {
			out["tool_versions"] = m
		}
	}
	ps := []string{}
	for _, p := range parents {
		if shaRe.MatchString(p) && !slices.Contains(ps, p) && len(ps) < 64 {
			ps = append(ps, p)
		}
	}
	out["parents_sha256"] = ps
	if origin != nil && shaRe.MatchString(origin.SHA256) && origin.VersionID != "" && len(origin.VersionID) <= 64 {
		out["hub_origin"] = map[string]any{"version_id": origin.VersionID, "sha256": origin.SHA256}
	}
	return out
}

// short 接受字串、數字（轉成字串）；拒絕太長、含控制字元或像本機路徑的值。
func short(v any) (string, bool) {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case float64:
		s = fmt.Sprint(t)
	default:
		return "", false
	}
	if s == "" || len(s) > maxField || !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	if looksLikePath(s) {
		return "", false
	}
	return s, true
}

// looksLikePath 回報字串是否像本機的絕對路徑（/data、/media、C:\ 這類）。
func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.Contains(s, `:\`) || strings.HasPrefix(s, `\\`)
}

func pick(v any, keys ...string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range keys {
		switch x := m[k].(type) {
		case string:
			if s, ok := short(x); ok {
				out[k] = s
			}
		case float64:
			out[k] = x
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Options 是產生 bundle 的其他參數。
type Options struct {
	License    string
	AppVersion string
	Now        time.Time
	// NewID 產生產出 ID（測試用；nil 時用 UUIDv4）。
	NewID func() string
}

// ErrNoArtifacts 表示沒有任何要分享的產出。
var ErrNoArtifacts = errors.New("沒有可以分享的產出。")

// Build 產生 bundle（zip，項目以 deflate 壓縮），並以 internal/bundle 驗證一次。
func Build(it Item, arts []Artifact, o Options) ([]byte, error) {
	if len(arts) == 0 {
		return nil, ErrNoArtifacts
	}
	newID := o.NewID
	if newID == nil {
		newID = func() string { return uuid.NewString() }
	}
	type entry struct {
		name string
		data []byte
	}
	var entries []entry
	var manifestArts []map[string]any
	for _, a := range arts {
		id := newID()
		path := "artifacts/" + id + ext(a.Kind)
		entries = append(entries, entry{path, a.Data})
		ma := map[string]any{
			"id": id, "kind": a.Kind, "format": Format(a.Kind), "path": path,
			"provenance": FilterProvenance(a.Provenance, a.ParentsSHA256, a.HubOrigin),
		}
		if a.Kind == "transcript" {
			ma["variant"] = a.Variant
		}
		if a.Lang != "" {
			ma["lang"] = a.Lang
		}
		manifestArts = append(manifestArts, ma)
		for _, im := range a.Images {
			entries = append(entries, entry{ImageName(id, im.N), im.Data})
		}
	}
	files := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		files = append(files, map[string]any{"path": e.name, "sha256": sha(e.data), "size": len(e.data)})
	}
	item := map[string]any{
		"platform": it.Platform, "external_id": it.ExternalID, "title": it.Title, "availability": it.Availability,
	}
	if it.Part > 0 {
		item["part"] = it.Part
	}
	if it.ChannelExternalID != "" {
		ch := map[string]any{"external_id": it.ChannelExternalID}
		if it.ChannelTitle != "" {
			ch["title"] = it.ChannelTitle
		}
		item["channel"] = ch
	}
	if it.PublishedAt != nil {
		item["published_at"] = it.PublishedAt.UTC().Format(time.RFC3339)
	}
	if it.DurationS != nil {
		item["duration_s"] = *it.DurationS
	}
	if it.OriginalLang != "" {
		item["original_lang"] = it.OriginalLang
	}
	manifest := map[string]any{
		"bundle_version": 0, "item": item, "artifacts": manifestArts, "files": files,
		"created_by": map[string]any{"app_version": o.AppVersion},
		"created_at": o.Now.UTC().Format(time.RFC3339), "license": o.License,
	}
	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: o.Now.UTC()})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := write(bundle.ManifestName, mb); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := write(e.name, e.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// 自我檢查：Hub 用同一個驗證器（BND-R4～R7，HREP-R1），通不過就是 Client 的錯誤，不送出。
	b, err := bundle.Read(out, bundle.Options{InstanceIDOptional: true})
	if err != nil {
		return nil, fmt.Errorf("產生的 bundle 沒有通過驗證：%w", err)
	}
	if err := b.ValidateContents(); err != nil {
		return nil, fmt.Errorf("產生的 bundle 內容沒有通過驗證：%w", err)
	}
	return out, nil
}

func sha(d []byte) string {
	return fmt.Sprintf("%x", sha256Sum(d))
}

func sha256Sum(d []byte) [32]byte { return sha256.Sum256(d) }
