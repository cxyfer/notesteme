package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// cuesFormat 是字幕產出的格式名稱（ADR-0005）。
const cuesFormat = "notesteme-cues+json"

const (
	artifactsDir = "artifacts"
	dirPerm      = 0o700
)

// writeArtifactFile 把產出寫到 <data>/artifacts/<item-id>/<artifact-id>.json：
// 先寫暫存檔並 fsync，再 rename 成正式檔名，最後 fsync 目錄，
// 所以中途中斷不會留下殘缺的正式檔（NFR-REL-2）。回傳相對於資料目錄的路徑、sha256 與大小。
func writeArtifactFile(dataDir, itemID, artifactID string, render func(io.Writer) error) (rel, sha string, size int64, err error) {
	dir := filepath.Join(dataDir, artifactsDir, itemID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", "", 0, err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+artifactID+"-*")
	if err != nil {
		return "", "", 0, err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(tmp, h)}
	if err = render(cw); err != nil {
		return "", "", 0, err
	}
	if err = tmp.Sync(); err != nil {
		return "", "", 0, err
	}
	if err = tmp.Close(); err != nil {
		return "", "", 0, err
	}
	name := artifactID + ".json"
	if err = os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return "", "", 0, err
	}
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync() // 目錄項目也要落地；不支援的檔案系統可以略過
		_ = d.Close()
	}
	return filepath.ToSlash(filepath.Join(artifactsDir, itemID, name)), hex.EncodeToString(h.Sum(nil)), cw.n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ErrCorruptArtifact 表示產出檔的內容和資料庫記錄的 sha256 不符。
var ErrCorruptArtifact = errors.New("產出檔的內容與記錄的 sha256 不符")

// readTranscript 讀取字幕產出，並驗證 sha256。
func readTranscript(dataDir, rel, wantSHA string) (subtitle.Transcript, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
	if err != nil {
		return subtitle.Transcript{}, err
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != wantSHA {
		return subtitle.Transcript{}, fmt.Errorf("%s：%w", rel, ErrCorruptArtifact)
	}
	return subtitle.DecodeCuesJSON(bytes.NewReader(b))
}

// provenance 組出來源紀錄（ADR-0005）。欄位由呼叫端填（executor 沒填時是 local），created_at 與 notesteme 的版本在這裡統一加上。
func (p *Pipeline) provenance(fields map[string]any, toolVersions map[string]string) (json.RawMessage, error) {
	if _, ok := fields["executor"]; !ok {
		fields["executor"] = "local"
	}
	fields["created_at"] = p.clock.Now().UTC().Format(time.RFC3339)
	tv := map[string]string{"notesteme": p.version}
	for k, v := range toolVersions {
		tv[k] = v
	}
	fields["tool_versions"] = tv
	return json.Marshal(fields)
}
