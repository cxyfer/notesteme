package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// OutEntry 是 manifest 以外的一個檔案。內容在 Data；File 不為空時內容是檔案 File（NFR-RES-3：不必整份讀進記憶體），
// 這時 SHA256（hex）與 Size 由呼叫端先算好，寫出時一邊複製一邊重算，不符時停止寫出（zip 不完整）。
type OutEntry struct {
	Path   string
	Data   []byte
	File   string
	SHA256 string
	Size   int64
}

// sum 回傳項目內容的 sha256 與大小。
func (e OutEntry) sum() (string, int64) {
	if e.File != "" {
		return e.SHA256, e.Size
	}
	s := sha256.Sum256(e.Data)
	return hex.EncodeToString(s[:]), int64(len(e.Data))
}

// zipEpoch 是寫進 zip 的固定修改時間：同樣的內容產生同樣的 bytes。
var zipEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// Write 產生 bundle 的 zip。m.Files 依 entries 重新計算（依路徑排序）；路徑必須是合法的 artifacts/<檔名> 且不重複。
func Write(m OutManifest, entries []OutEntry) ([]byte, error) {
	pl, err := Prepare(m, entries)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pl.Stream(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Plan 是驗證過、可以寫出的 bundle（Prepare 的結果）。
type Plan struct {
	// Manifest 是寫出的 manifest（含 Files）。
	Manifest OutManifest
	man      []byte
	sorted   []OutEntry
}

// Prepare 驗證項目名稱、算出 m.Files，但還不寫出：名稱的錯誤在開始輸出之前決定（Write 與串流的匯出共用）。
func Prepare(m OutManifest, entries []OutEntry) (*Plan, error) {
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
		sum, size := e.sum()
		m.Files = append(m.Files, OutFile{Path: e.Path, SHA256: sum, Size: size})
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
	return &Plan{Manifest: m, man: append(man, '\n'), sorted: sorted}, nil
}

// Stream 把 zip 寫到 w：manifest.json 在前，其後依路徑排序；File 項目逐段複製（不整份讀進記憶體）。
// 回傳錯誤時 zip 不完整（沒有寫出中央目錄），收到的一方無法讀取。
func (pl *Plan) Stream(w io.Writer) error {
	zw := zip.NewWriter(w)
	create := func(name string) (io.Writer, error) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
		h.SetMode(0o644)
		return zw.CreateHeader(h)
	}
	fw, err := create(ManifestName)
	if err != nil {
		return err
	}
	if _, err := fw.Write(pl.man); err != nil {
		return err
	}
	for _, e := range pl.sorted {
		fw, err := create(e.Path)
		if err != nil {
			return err
		}
		if e.File == "" {
			if _, err := fw.Write(e.Data); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(fw, e); err != nil {
			return err
		}
	}
	return zw.Close()
}

// copyFile 複製 File 項目，並確認內容與 Prepare 時的 sha256、大小相同（檔案在兩次讀取之間被換掉時不送出錯的內容）。
func copyFile(w io.Writer, e OutEntry) error {
	f, err := os.Open(e.File)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), f)
	if err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		return fmt.Errorf("bundle：%s 的內容在匯出途中改變了", e.Path)
	}
	return nil
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
