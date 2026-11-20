package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 本機影音的錯誤訊息（給使用者看）。
const (
	msgLocalNotFound   = "找不到這個檔案。"
	msgLocalOutside    = "這個檔案不在允許的目錄內。"
	msgLocalNotRegular = "這不是一般檔案。"
	msgLocalNoFiles    = "這個伺服器沒有設定本機檔案的存取。"
	msgNoMediaTool     = "無法執行 ffprobe，請確認已安裝（可執行 notesteme doctor）。"
)

// maxSidecarBytes 是外掛字幕檔與抽出的內嵌字幕的大小上限。
const maxSidecarBytes = 20 << 20

// sidecarLang 是外掛字幕檔名中的語言代碼：<stem>.<lang>.srt 的 lang（SRC-R14）。
var sidecarLang = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// localAdapter 是本機影音的平台功能（ING-05）：探測容器與字幕軌，並取得外掛或內嵌的字幕。
// 檔案一律經 pathguard.Guard 開啟與解析，交給 ffprobe 與 ffmpeg 的路徑也在執行前重新解析（GRD-R7）。
type localAdapter struct {
	files   *pathguard.Guard
	media   *media.Tool
	dataDir string
}

// localError 建立 permanent 錯誤：重試也不會成功，msg 是給使用者看的訊息。
func localError(msg string, err error) error {
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Message: msg, Err: err}
}

// sourcePath 回傳影音檔的路徑：source_path 是相對資料目錄的 uploads/<id>/<name>（上傳）或絕對路徑；
// 沒有記錄時，external_id 是絕對路徑的影音用它。
func (a localAdapter) sourcePath(sourcePath, externalID string) (string, error) {
	path := sourcePath
	if path == "" && filepath.IsAbs(externalID) {
		path = externalID
	}
	if path == "" {
		return "", localError(msgLocalNotFound, errors.New("影音沒有檔案路徑"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.dataDir, filepath.FromSlash(path))
	}
	return path, nil
}

// openFile 以 Guard 開啟路徑，並把錯誤分類：不存在、不在根目錄內、不是一般檔案都是 permanent。
func (a localAdapter) openFile(path string) (*os.File, error) {
	if a.files == nil {
		return nil, localError(msgLocalNoFiles, nil)
	}
	f, _, err := a.files.Open(path)
	switch {
	case err == nil:
		return f, nil
	case errors.Is(err, pathguard.ErrOutsideRoots):
		return nil, localError(msgLocalOutside, err)
	case errors.Is(err, pathguard.ErrNotRegular):
		return nil, localError(msgLocalNotRegular, err)
	case errors.Is(err, fs.ErrNotExist):
		return nil, localError(msgLocalNotFound, err)
	}
	return nil, err
}

// open 開啟影音檔，並回傳要交給 ffprobe 與 ffmpeg 的、已解析的絕對路徑。呼叫端負責關閉檔案。
func (a localAdapter) open(sourcePath, externalID string) (*os.File, string, error) {
	path, err := a.sourcePath(sourcePath, externalID)
	if err != nil {
		return nil, "", err
	}
	f, err := a.openFile(path)
	if err != nil {
		return nil, "", err
	}
	abs, err := a.files.Resolve(path)
	if err != nil {
		_ = f.Close()
		return nil, "", localError(msgLocalOutside, err)
	}
	return f, abs, nil
}

func (a localAdapter) probe(ctx context.Context, it store.Item) (*probed, error) {
	f, abs, err := a.open(it.SourcePath, it.ExternalID)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if a.media == nil {
		return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "ffprobe", Message: msgNoMediaTool, Actions: []string{"安裝 ffprobe"}}
	}
	info, err := a.media.Probe(ctx, abs)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(abs)
	out := &probed{
		Title:     strings.TrimSuffix(base, filepath.Ext(base)),
		MediaKind: info.Kind(),
		Tracks:    append(listSidecars(filepath.Dir(abs), base), embeddedTracks(info)...),
	}
	if info.DurationS > 0 {
		d := int(math.Round(info.DurationS))
		out.DurationS = &d
	}
	// DEL-R11：永久刪除時 tombstone 要有 sha256，所以 metadata 步驟補算（path 新增時還沒有）。
	if out.ContentSHA256 = it.ContentSHA256; out.ContentSHA256 == "" {
		if out.ContentSHA256, err = hashFile(ctx, f); err != nil {
			return nil, err
		}
		out.ComputedSHA256 = out.ContentSHA256
	}
	return out, nil
}

// listSidecars 列出與影音同目錄的外掛字幕檔：<stem>.<lang>.srt 或 .vtt，lang 要像語言代碼（SRC-R14）。
// 沒有語言代碼的 <stem>.srt 不採用。
func listSidecars(dir, base string) []transcript.Track {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	var out []transcript.Track
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if e.IsDir() || (!strings.EqualFold(ext, ".srt") && !strings.EqualFold(ext, ".vtt")) {
			continue
		}
		lang, ok := strings.CutPrefix(strings.TrimSuffix(name, ext), stem+".")
		if !ok || !sidecarLang.MatchString(lang) {
			continue
		}
		out = append(out, transcript.Track{Lang: lang, Kind: transcript.KindSidecar, Handle: filepath.Join(dir, name)})
	}
	return out
}

// embeddedTracks 列出容器中的文字字幕串流；點陣字幕（PGS、DVD、DVB）無法轉成文字，略過。
func embeddedTracks(info *media.Info) []transcript.Track {
	var out []transcript.Track
	for _, s := range info.Streams {
		if s.Type == "subtitle" && media.IsTextSubtitle(s.Codec) {
			out = append(out, transcript.Track{
				Lang: s.Lang, Kind: transcript.KindEmbedded, Handle: strconv.Itoa(s.Index), Default: s.Default, Forced: s.Forced,
			})
		}
	}
	return out
}

func (a localAdapter) fetchCues(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]subtitle.Cue, error) {
	var data []byte
	var err error
	ext := ".srt"
	switch t.Kind {
	case transcript.KindSidecar:
		ext = strings.ToLower(filepath.Ext(t.Handle))
		data, err = a.readSidecar(t.Handle)
	case transcript.KindEmbedded:
		data, err = a.extractEmbedded(ctx, it, t, dir)
	default:
		err = errPermanent("不認識的字幕軌種類 " + t.Kind)
	}
	if err != nil {
		return nil, err
	}
	var cues []subtitle.Cue
	if ext == ".vtt" {
		cues, err = subtitle.ParseVTT(data)
	} else {
		cues, err = subtitle.ParseSRT(data)
	}
	if err != nil {
		return nil, errPermanent(fmt.Sprintf("字幕檔無法解析：%v", err))
	}
	return cues, nil
}

// readSidecar 以 Guard 讀取外掛字幕檔，上限 maxSidecarBytes。
func (a localAdapter) readSidecar(path string) ([]byte, error) {
	f, err := a.openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f)
}

// extractEmbedded 用 ffmpeg 把內嵌字幕抽成 SRT（暫存在 dir），讀回內容。
func (a localAdapter) extractEmbedded(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]byte, error) {
	idx, err := strconv.Atoi(t.Handle)
	if err != nil {
		return nil, errPermanent("內嵌字幕的串流 index 不正確：" + t.Handle)
	}
	f, abs, err := a.open(it.SourcePath, it.ExternalID)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if a.media == nil {
		return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "ffmpeg", Message: msgNoFFmpeg, Actions: []string{"安裝 ffmpeg"}}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	out := filepath.Join(absDir, "embedded.srt")
	if err := a.media.ExtractSubtitle(ctx, abs, idx, out); err != nil {
		return nil, err
	}
	of, err := os.Open(out)
	if err != nil {
		return nil, err
	}
	defer of.Close()
	return readLimited(of)
}

// readLimited 讀完 r，超過 maxSidecarBytes 是 permanent。
func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSidecarBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSidecarBytes {
		return nil, localError("字幕檔超過 20 MiB，無法處理。", nil)
	}
	return data, nil
}

func (a localAdapter) toolVersions(ctx context.Context) (map[string]string, error) {
	if a.media == nil {
		return map[string]string{}, nil
	}
	return a.media.Versions(ctx), nil
}

// hashFile 計算 f 的 sha256（小寫十六進位）；每次讀取都檢查 ctx，大檔案可以被取消。
func hashFile(ctx context.Context, f io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader 在 ctx 取消後讓讀取失敗。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// msgNoFFmpeg 是找不到 ffmpeg 時的訊息。
const msgNoFFmpeg = "無法執行 ffmpeg，請確認已安裝（可執行 notesteme doctor）。"
