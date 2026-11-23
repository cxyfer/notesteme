package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// 寫出 bundle v0（BND-R1）：manifest.json 是第一個項目，其後是 artifacts/<檔名>，一律 deflate、mode 0644 的一般檔案。
// 寫出的 bundle 必須能通過 Read 與 ValidateContents（單元測試守住）。

// OutManifest 是寫出用的 manifest（欄位順序即 JSON 的順序）；Files 由 Write 依內容計算，不必填。
type OutManifest struct {
	BundleVersion int           `json:"bundle_version"`
	Item          OutItem       `json:"item"`
	Artifacts     []OutArtifact `json:"artifacts"`
	Files         []OutFile     `json:"files"`
	CreatedBy     OutCreatedBy  `json:"created_by"`
	CreatedAt     string        `json:"created_at"`
	License       *string       `json:"license"`
}

// OutItem 是寫出用的影音 metadata；可以省略的欄位以 nil 表示（寫成 null）。
type OutItem struct {
	Platform     string      `json:"platform"`
	ExternalID   string      `json:"external_id"`
	Part         *int        `json:"part"`
	Title        string      `json:"title"`
	Channel      *OutChannel `json:"channel"`
	PublishedAt  *string     `json:"published_at"`
	DurationS    *float64    `json:"duration_s"`
	OriginalLang *string     `json:"original_lang"`
	Availability string      `json:"availability"`
	SourceName   *string     `json:"source_name"`
}

// OutChannel 是頻道。
type OutChannel struct {
	ExternalID string  `json:"external_id"`
	Title      *string `json:"title"`
}

// OutArtifact 是一個產出；Variant 只有 transcript 有（其他種類留空字串，不寫出）。
type OutArtifact struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Variant    string          `json:"variant,omitempty"`
	Lang       *string         `json:"lang"`
	Format     string          `json:"format"`
	Path       string          `json:"path"`
	Parents    []string        `json:"parents"`
	Provenance json.RawMessage `json:"provenance"`
}

// OutFile 是完整性清單的一項。
type OutFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// OutCreatedBy 是產生 bundle 的實例。
type OutCreatedBy struct {
	InstanceID string `json:"instance_id,omitempty"`
	AppVersion string `json:"app_version"`
}

// OutEntry 是 manifest 以外的一個檔案。
type OutEntry struct {
	Path string
	Data []byte
}

// zipEpoch 是寫進 zip 的固定修改時間：同樣的內容產生同樣的 bytes。
var zipEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// Write 產生 bundle 的 zip。m.Files 依 entries 重新計算（依路徑排序）；路徑必須是合法的 artifacts/<檔名> 且不重複。
func Write(m OutManifest, entries []OutEntry) ([]byte, error) {
	seen := map[string]bool{}
	m.Files = make([]OutFile, 0, len(entries))
	sorted := append([]OutEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, e := range sorted {
		if dir, ok := allowedName(e.Path); !ok || dir || e.Path == ManifestName {
			return nil, fmt.Errorf("bundle：不允許的項目名稱 %q", e.Path)
		}
		k := toLowerASCII(e.Path)
		if seen[k] {
			return nil, fmt.Errorf("bundle：重複的項目名稱 %q", e.Path)
		}
		seen[k] = true
		sum := sha256.Sum256(e.Data)
		m.Files = append(m.Files, OutFile{Path: e.Path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(e.Data))})
	}
	if m.Artifacts == nil {
		m.Artifacts = []OutArtifact{}
	}
	for i := range m.Artifacts {
		if m.Artifacts[i].Parents == nil {
			m.Artifacts[i].Parents = []string{}
		}
	}
	man, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := write(ManifestName, append(man, '\n')); err != nil {
		return nil, err
	}
	for _, e := range sorted {
		if err := write(e.Path, e.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// ValidLang 回報 s 是否符合 manifest 的 BCP 47 語法（BND-R6）。
func ValidLang(s string) bool { return langRe.MatchString(s) }
