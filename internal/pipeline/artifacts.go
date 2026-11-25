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
	"strings"
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
	tmp, err := os.CreateTemp(dir, artifactTmpPrefix+artifactID+"-*")
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

// artifactTmpPrefix 是 writeArtifactFile 暫存檔的檔名前綴；只有它用這個前綴（CleanArtifactTemps 依此清除）。
const artifactTmpPrefix = ".tmp-"

// CleanArtifactTemps 刪除 <data>/artifacts/<item>/ 底下 writeArtifactFile 留下的暫存檔（.tmp-*），回傳刪除的數量與失敗的原因。
//
// 暫存檔只在任務執行期間存在：rename 之前程序被 kill -9 或斷電才會留下，沒有任何 DB 記錄指向它（NFR-REL-2）。
// 必須在取得資料目錄的鎖之後、worker 開始執行任務之前呼叫（PIPE-R11 的重新啟動），否則會刪到正在寫的暫存檔。
// 只處理 artifacts/ 底下第一層目錄中的一般檔案，不跟隨 symlink。
// rename 之後、DB 記錄之前被殺而留下的正式檔不刪：它不影響正確性（沒有記錄指向它），刪除反而要判斷它是否真的沒有被引用。
func CleanArtifactTemps(dataDir string) (int, error) {
	root := filepath.Join(dataDir, artifactsDir)
	items, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	var first error
	for _, it := range items {
		if !it.IsDir() { // DirEntry 是 lstat 語意：symlink 不是目錄
			continue
		}
		dir := filepath.Join(root, it.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			first = errors.Join(first, err)
			continue
		}
		for _, f := range files {
			if !f.Type().IsRegular() || !strings.HasPrefix(f.Name(), artifactTmpPrefix) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, f.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				first = errors.Join(first, err)
				continue
			}
			n++
		}
	}
	return n, first
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
