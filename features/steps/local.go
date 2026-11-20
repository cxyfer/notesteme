package steps

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// 這個檔案放本機影音（ING-05）的步驟：inbox 的 fixture 檔案、音訊屬性、上傳，以及 local: 代號的處理。
// fixture 在 features/fixtures/local/（影音與字幕）與 features/fixtures/media/（音訊屬性），說明見 fixtures/README.md。

// localRefPath 回傳 local: 代號對應的實際路徑：相對路徑在 inbox 之下，絕對路徑（media_roots 等）換掉虛擬路徑。
func (s *Scenario) localRefPath(ref string) string {
	rel := strings.TrimPrefix(ref, "local:")
	if strings.HasPrefix(rel, "/") {
		return s.World.Expand(rel)
	}
	return filepath.Join(s.World.InboxDir(), filepath.FromSlash(rel))
}

// ingestBody 回傳 POST /api/items 的 body：本機影音（local:）送 path，其他代號與網址送 url。
// `local:<相對路徑>` 在 inbox 之下；`local:/abs` 的虛擬路徑換成實際路徑。回傳的 map 可以再加 until、overrides。
func (s *Scenario) ingestBody(ref string) map[string]any {
	if strings.HasPrefix(ref, "local:") {
		return map[string]any{"path": s.localRefPath(ref)}
	}
	return map[string]any{"url": refToURL(ref)}
}

// fixtureFile 讀取 features/fixtures/<dir>/<name>；name 不能跳出該目錄。
func (s *Scenario) fixtureFile(dir, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(s.World.FixturesDir, dir, filepath.Clean("/"+name)))
	if err != nil {
		return nil, fmt.Errorf("fixture %s/%s：%w（features/fixtures/%s/）", dir, name, err, dir)
	}
	return b, nil
}

// touchNow 把檔案的 mtime 設成 fake clock 的現在：inbox 的穩定判斷以 mtime 與時鐘比較，
// 不能用真實時間（NFR-TEST-2）。
func (s *Scenario) touchNow(path string) error {
	now := s.World.Clock.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		return fmt.Errorf("設定 %s 的修改時間：%w", path, err)
	}
	return nil
}

// localFileInInbox 把 features/fixtures/local/<name> 複製到 inbox（建立上層目錄），mtime 設為假時鐘的現在。
// fixture 不存在時失敗。
func (s *Scenario) localFileInInbox(name string) error {
	b, err := s.fixtureFile("local", name)
	if err != nil {
		return err
	}
	dst := filepath.Join(s.World.InboxDir(), filepath.Clean("/"+name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("建立 inbox 的目錄：%w", err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return fmt.Errorf("寫入 inbox 的 %s：%w", name, err)
	}
	return s.touchNow(dst)
}

// readFileWithMarker 讀出檔案的標記行（含換行）與標記之後的其餘內容（填充）。沒有換行時整個檔案都是標記。
func readFileWithMarker(path string) (marker []byte, rest []byte, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return b, nil, nil
	}
	return b[:i+1], b[i+1:], nil
}

// audioOfIs 實作 `the audio of … is …`：把 features/fixtures/media/<name> 的屬性（長度、壓縮後大小、靜音）
// 併進影音的假媒體標記。
//   - local:：改寫 inbox 檔的標記（item 設成代號；fixture 有 streams 才覆蓋串流），保留填充，mtime 設為假時鐘的現在。
//   - yt:／bili:：寫入假 yt-dlp 中這部影片的屬性，之後下載的媒體檔標記帶有這些屬性。
//
// 不依參數回 pending：fixture 或影音不存在時失敗。
func (s *Scenario) audioOfIs(ref, name string) error {
	raw, err := s.fixtureFile("media", name)
	if err != nil {
		return err
	}
	fx, ok := faketool.ParseMedia(raw)
	if !ok {
		return fmt.Errorf("fixture media/%s 不是假媒體標記", name)
	}
	if !strings.HasPrefix(ref, "local:") {
		return s.editVideo(ref, func(v *faketool.Video) error {
			v.Duration = int(fx.Duration)
			v.Compressed, v.Silences = fx.Compressed, fx.Silences
			return nil
		})
	}
	path := s.localRefPath(ref)
	head, rest, err := readFileWithMarker(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s 不在 inbox（先用 `a local file … in the inbox`）", ref)
	}
	if err != nil {
		return err
	}
	m, ok := faketool.ParseMedia(head)
	if !ok {
		return fmt.Errorf("%s 不是假媒體檔，無法套用音訊屬性", ref)
	}
	m.Item = ref
	m.Duration, m.Compressed, m.Silences = fx.Duration, fx.Compressed, fx.Silences
	if len(fx.Streams) > 0 {
		m.Streams = fx.Streams
	}
	if err := os.WriteFile(path, append([]byte(faketool.FormatMedia(m)), rest...), 0o644); err != nil {
		return err
	}
	return s.touchNow(path)
}

// ownerUploads 以 multipart/form-data（欄位 file）上傳檔案：名稱是 features/fixtures/local/ 的檔案；
// 以 / 開頭的路徑是場景中的檔案（/tmp 底下的對應見 filePath）。經 send 送出，回應中的代號與 ID 照常記下。
func (s *Scenario) ownerUploads(name string) error {
	var content []byte
	var err error
	if strings.HasPrefix(name, "/") {
		content, err = os.ReadFile(s.filePath(name))
		if err != nil {
			return fmt.Errorf("讀取要上傳的檔案 %s：%w", name, err)
		}
	} else if content, err = s.fixtureFile("local", name); err != nil {
		return err
	}
	body, contentType, err := multipartFile("file", filepath.Base(name), content)
	if err != nil {
		return err
	}
	return s.send("POST /api/items", requestOpts{who: asOwner, body: body, contentType: contentType})
}

// multipartFile 建立只有一個檔案欄位的 multipart body，回傳 body 與 Content-Type。
func multipartFile(field, filename string, content []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// sidecarLang 是 sidecar 字幕檔名中的語言碼（<stem>.<lang>.srt）：沒有語言碼的 <stem>.srt 不採用（SRC-R14）。
var sidecarLang = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// hasLocalSidecar 回報影音檔旁邊有沒有 <stem>.<lang>.srt 或 .vtt。
func hasLocalSidecar(path string) bool {
	dir, base := filepath.Split(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || (ext != ".srt" && ext != ".vtt") {
			continue
		}
		// 先去掉副檔名：<stem>.srt 去掉之後不再有 <stem>. 的前綴，沒有語言碼，不採用。
		lang, ok := strings.CutPrefix(strings.TrimSuffix(name, filepath.Ext(name)), stem+".")
		if ok && sidecarLang.MatchString(lang) {
			return true
		}
	}
	return false
}

// hasLocalTranscriptSource 回報本機影音有沒有字幕來源（steps.md 4.4）：
// inbox 有對應的 sidecar，或假媒體標記帶有文字字幕串流。
func (s *Scenario) hasLocalTranscriptSource(ref string) bool {
	path := s.localRefPath(ref)
	if hasLocalSidecar(path) {
		return true
	}
	head, _, err := readFileWithMarker(path)
	if err != nil {
		return false
	}
	m, ok := faketool.ParseMedia(head)
	return ok && m.HasTextSubtitle()
}
