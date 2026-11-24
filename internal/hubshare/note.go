package hubshare

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/cxyfer/notesteme/internal/bundle"
)

// 分享前的筆記清理與洩漏掃描（安全修補 B，HSHR 待決問題 S21）。

// localFrontmatterKey 回報 frontmatter 的鍵是不是只在本機有意義、不能送到 Hub 的：
// notesteme_*（本機影音 ID、處理狀態、引擎）與閱讀狀態（status、reading_status、read_at）。
func localFrontmatterKey(k string) bool {
	k = strings.ToLower(strings.TrimSpace(k))
	return strings.HasPrefix(k, "notesteme_") || k == "status" || k == "reading_status" || k == "read_at"
}

// StripNote 回傳送到 Hub 的筆記：frontmatter 去掉本機的欄位（含它們的續行），
// readButtons 中非空的字串（模板的 read_button，可能是內網網址或 obsidian://）一律刪除。
// 沒有 frontmatter 時只刪 read button。
func StripNote(md string, readButtons ...string) string {
	body := md
	var front []string
	nl := "\n"
	if strings.HasPrefix(md, "---\r\n") {
		nl = "\r\n"
	}
	if strings.HasPrefix(md, "---"+nl) {
		rest := md[len("---"+nl):]
		lines := strings.SplitAfter(rest, "\n")
		end := -1
		for i, l := range lines {
			if strings.TrimRight(l, "\r\n") == "---" {
				end = i
				break
			}
		}
		if end >= 0 {
			drop := false
			for _, l := range lines[:end] {
				t := strings.TrimRight(l, "\r\n")
				// 頂層的鍵（不是縮排、清單項目或註解）開始一個新欄位；其他行屬於上一個鍵。
				if t != "" && t[0] != ' ' && t[0] != '\t' && t[0] != '-' && t[0] != '#' {
					k, _, _ := strings.Cut(t, ":")
					drop = localFrontmatterKey(strings.Trim(k, `"'`))
				}
				if !drop {
					front = append(front, l)
				}
			}
			body = strings.Join(lines[end:], "")
			body = "---" + nl + strings.Join(front, "") + body
		}
	}
	for _, rb := range readButtons {
		if strings.TrimSpace(rb) != "" {
			body = strings.ReplaceAll(body, rb, "")
		}
	}
	return body
}

// maxScanEntry 是洩漏掃描讀取單一項目的上限（bundle 由 Client 自己產生，上限只是保險）。
const maxScanEntry = 256 << 20

// Leak 是洩漏掃描找到的問題：哪個項目含有哪一類不能送出的字串（不含字串本身）。
type Leak struct {
	Entry string
	What  string
}

// Needles 是不能出現在送往 Hub 的 bundle 中的字串。
type Needles struct {
	// Secrets 是 token、API key、cookie 等；任何項目都不能含有。
	Secrets []string
	// Local 是只在本機有意義的識別（本機影音 ID、模板的 read button）；任何項目都不能含有。
	Local []string
	// Paths 是本機路徑；只檢查 manifest.json（字幕與筆記的本文是使用者的內容）。
	Paths []string
	// ContainsPath 判斷 hay 是否含有完整的路徑 needle。
	ContainsPath func(hay []byte, needle string) bool
}

// Scan 解開 raw（Build 的輸出）逐項目掃描；找到時回傳第一個問題。
func Scan(raw []byte, n Needles) (*Leak, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		d, err := io.ReadAll(io.LimitReader(rc, maxScanEntry))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		for _, s := range n.Secrets {
			if s != "" && bytes.Contains(d, []byte(s)) {
				return &Leak{Entry: f.Name, What: "secret"}, nil
			}
		}
		for _, s := range n.Local {
			if s != "" && bytes.Contains(d, []byte(s)) {
				return &Leak{Entry: f.Name, What: "local"}, nil
			}
		}
		if f.Name == bundle.ManifestName && n.ContainsPath != nil {
			for _, p := range n.Paths {
				if n.ContainsPath(d, p) {
					return &Leak{Entry: f.Name, What: "path"}, nil
				}
			}
		}
	}
	return nil, nil
}
