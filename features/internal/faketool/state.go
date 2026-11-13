package faketool

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// 預設值。
const (
	// DefaultYtDlpVersion 是假 yt-dlp 回報的版本，與 SRC 場景的 provenance 一致。
	DefaultYtDlpVersion = "2026.11.01"
	// DefaultCues 是字幕軌沒有指定 fixture 或句數時產生的句數。
	DefaultCues = 120
)

// 字幕軌的種類。
const (
	KindManual             = "manual"
	KindAuto               = "auto"
	KindPlatformTranslated = "platform-translated"
)

// 可以指定失敗的操作（Failure.On）。
const (
	OpInfo  = "info"  // yt-dlp -J
	OpSubs  = "subs"  // 下載字幕
	OpMedia = "media" // 下載媒體
)

// State 是假工具讀取的場景狀態，harness 以 WriteState 寫入
// <root>/fake/state.json。
type State struct {
	// YtDlpVersion 為空時使用 DefaultYtDlpVersion。
	YtDlpVersion string `json:"ytdlp_version,omitempty"`
	// FixturesDir 是 features/fixtures 的絕對路徑，用來讀取 captions/ 內的檔案。
	FixturesDir string `json:"fixtures_dir,omitempty"`
	// Videos 的 key 是平台 ID，例如 abc123xxxxx、BV1xx411c7mD。
	Videos map[string]*Video `json:"videos,omitempty"`
	// Gate 是 gate 檔的路徑；檔案存在時，處理網址的 yt-dlp 呼叫會阻塞，
	// 直到檔案被刪除。每個被擋住的程序各寫一個 <Gate>.waiting.<pid> 標記檔，
	// 並在 <root>/data/tmp 寫下部分檔（見 waitGate）。
	Gate string `json:"gate,omitempty"`
}

// Video 是一部假的平台影片。
type Video struct {
	Title       string `json:"title,omitempty"`
	Duration    int    `json:"duration,omitempty"` // 秒
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
	// PublishedAt 是 RFC 3339 的發布時間；-J 輸出為 timestamp 與 upload_date。
	PublishedAt  string    `json:"published_at,omitempty"`
	Availability string    `json:"availability,omitempty"` // 預設 public
	LiveStatus   string    `json:"live_status,omitempty"`  // 預設 not_live
	ChannelID    string    `json:"channel_id,omitempty"`
	Channel      string    `json:"channel,omitempty"`
	Chapters     []Chapter `json:"chapters,omitempty"`
	// Parts 是 Bilibili 的分P數；0 視為 1。
	Parts  int     `json:"parts,omitempty"`
	Tracks []Track `json:"tracks,omitempty"`
	// AutoNotReady 為 true 時，自動字幕與平台翻譯字幕尚未生成：
	// 不會出現在 info JSON，也不會被下載。
	AutoNotReady bool     `json:"auto_not_ready,omitempty"`
	Fail         *Failure `json:"fail,omitempty"`
}

// Chapter 是章節。
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Track 是一條平台字幕軌。內容來自 Fixture（features/fixtures/captions/ 內的檔名）；
// Fixture 為空時依 Cues 產生，Cues 為 0 時使用 DefaultCues。
type Track struct {
	Kind    string `json:"kind"` // KindManual、KindAuto、KindPlatformTranslated
	Lang    string `json:"lang"`
	Fixture string `json:"fixture,omitempty"`
	Cues    int    `json:"cues,omitempty"`
}

// Failure 讓 yt-dlp 對這部影片失敗。
type Failure struct {
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code,omitempty"` // 0 視為 1
	// On 限定失敗的操作（OpInfo、OpSubs、OpMedia）；空代表全部。
	On []string `json:"on,omitempty"`
}

func (f *Failure) applies(op string) bool {
	if f == nil {
		return false
	}
	if len(f.On) == 0 {
		return true
	}
	for _, o := range f.On {
		if o == op {
			return true
		}
	}
	return false
}

// Call 是 calls.jsonl 的一行：一次假工具的呼叫。
type Call struct {
	Tool string   `json:"tool"`
	Argv []string `json:"argv"` // 含工具名稱（argv[0]）
	Cwd  string   `json:"cwd"`
	// CookiesFile 與 CookiesMode 在有 --cookies 時記錄檔案路徑與權限（例如 "0600"）；
	// 檔案不存在或無法讀取時 CookiesMode 為 "missing"。
	CookiesFile string `json:"cookies_file,omitempty"`
	CookiesMode string `json:"cookies_mode,omitempty"`
}

// FakeDir 回傳場景根目錄 root 下放狀態檔與呼叫紀錄的目錄。
func FakeDir(root string) string { return filepath.Join(root, "fake") }

func statePath(root string) string { return filepath.Join(FakeDir(root), "state.json") }
func callsPath(root string) string { return filepath.Join(FakeDir(root), "calls.jsonl") }

// WriteState 以原子方式（寫暫存檔再改名）寫入 root 場景的狀態檔。
func WriteState(root string, s State) error {
	dir := FakeDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), statePath(root))
}

// ReadState 讀取 root 場景的狀態檔；檔案不存在時回傳空的 State。
func ReadState(root string) (State, error) {
	var s State
	b, err := os.ReadFile(statePath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("faketool: parse state: %w", err)
	}
	return s, nil
}

// AppendCall 把一次呼叫附加到 root 場景的 calls.jsonl。
// 不建立 fake/ 目錄：場景由 WriteState 建立，目錄不存在時回傳錯誤。
func AppendCall(root string, c Call) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(callsPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// 單次 write，O_APPEND 下並行的呼叫不會交錯。
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadCalls 讀取 root 場景的全部呼叫紀錄；沒有紀錄時回傳 nil。
func ReadCalls(root string) ([]Call, error) {
	b, err := os.ReadFile(callsPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Call
	for _, line := range splitLines(b) {
		var c Call
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("faketool: parse calls.jsonl: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
