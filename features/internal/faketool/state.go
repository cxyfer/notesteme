package faketool

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/features/internal/fakeframes"
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
	// Playlists 的 key 是 YouTube 播放清單 ID（例如 PL123），值是 FixturesDir/ytdlp/ 內的檔名：
	// `yt-dlp --flat-playlist -J` 對該清單輸出這個檔案的內容。沒有登記的清單回報「播放清單不存在」。
	Playlists map[string]string `json:"playlists,omitempty"`
	// Collections 的 key 是 Bilibili 合集的 "<mid>:<sid>"，ChannelLists 的 key 是 YouTube 頻道 ID，值同 Playlists：
	// `yt-dlp --flat-playlist -J` 對合集網址、頻道的 /videos 網址輸出這個 fixture（SUB，M4）。
	// 頻道沒有登記 fixture 時，/videos 依登記的影片（同一個 channel_id、不是 Shorts）產生清單。
	Collections  map[string]string `json:"collections,omitempty"`
	ChannelLists map[string]string `json:"channel_lists,omitempty"`
	// Channels 是 @handle（小寫，含 @）對應的頻道 ID；影片的頻道名稱轉小寫、去掉空白也會自動對應（SUB）。
	Channels map[string]string `json:"channels,omitempty"`
	// Gate 是 gate 檔的路徑；檔案存在時，處理網址的 yt-dlp 呼叫會阻塞，
	// 直到檔案被刪除。每個被擋住的程序各寫一個 <Gate>.waiting.<pid> 標記檔，
	// 並在 <root>/data/tmp 寫下部分檔（見 waitGate）。
	Gate string `json:"gate,omitempty"`
	// FFmpegNoLibass 為 true 時，假 ffmpeg 的 -buildconf 不含 --enable-libass。
	FFmpegNoLibass bool `json:"ffmpeg_no_libass,omitempty"`
	// CookieKinds 讓平台字幕軌需要登入才看得到：key 是字幕軌的種類（KindManual、KindAuto），
	// value 是需要登入的平台（目前只有 "bilibili"）。假 yt-dlp 沒有收到該平台的有效 cookie 檔時，
	// 不列出也不下載這個種類的字幕軌，並印出 login WARNING（見 bilibili.go）。
	CookieKinds map[string]string `json:"cookie_kinds,omitempty"`
	// RejectedCookies 是不再接受上傳的 cookie 的平台（目前只有 "bilibili"，SEC-R12）：
	// 即使 cookie 檔有 SESSDATA，假 yt-dlp 也當作沒有登入（例如 cookie 在瀏覽器中被登出）。
	RejectedCookies map[string]bool `json:"rejected_cookies,omitempty"`
	// Releases 是假 pip 可以安裝的 yt-dlp 版本，key 是版本。沒有登記的版本裝不起來（找不到 distribution）；
	// 沒有指定版本的安裝（-U）取最新的（版本字串最大的）一個。
	Releases map[string]*Release `json:"releases,omitempty"`
}

// Release 是一個可以用假 pip 安裝的 yt-dlp 版本。
type Release struct {
	// ProbeFailsFor 列出這個版本的 yt-dlp 在 -J 時會失敗的影片 ID：`--version` 照常成功，
	// 只有對這些影片的探測失敗，用來模擬「裝得起來但壞掉」的更新（OPS-R6 的自我檢查）。
	ProbeFailsFor []string `json:"probe_fails_for,omitempty"`
}

// Video 是一部假的平台影片。
type Video struct {
	Title       string `json:"title,omitempty"`
	Duration    int    `json:"duration,omitempty"` // 秒
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
	// PublishedAt 是 RFC 3339 的發布時間；-J 輸出為 timestamp 與 upload_date。
	PublishedAt  string `json:"published_at,omitempty"`
	Availability string `json:"availability,omitempty"` // 預設 public
	LiveStatus   string `json:"live_status,omitempty"`  // 預設 not_live
	// Shorts 為 true 時，-J 的 webpage_url 是 /shorts/<ID>，也不出現在頻道 /videos 的清單（SUB 待決問題 S6）。
	Shorts    bool      `json:"shorts,omitempty"`
	ChannelID string    `json:"channel_id,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	Chapters  []Chapter `json:"chapters,omitempty"`
	// Parts 是 Bilibili 的分P數；0 視為 1。各分P 的標題、原文語言與字幕軌另放在
	// Videos["<bvid>/p<n>"]（PartKey），沒有登記的分P 沿用這部影片本身。
	Parts  int     `json:"parts,omitempty"`
	Tracks []Track `json:"tracks,omitempty"`
	// Later 是平台事後才補上的字幕軌（SRC-10，`the platform adds … captions in … to … at …`）：
	// 場景的 fake clock（NowFile）到了 Track.AvailableAt 才和 Tracks 一起列出。和 Tracks 分開存放，
	// `has no caption tracks` 不會清掉它。
	Later []Track `json:"later,omitempty"`
	// AutoNotReady 為 true 時，自動字幕與平台翻譯字幕尚未生成：
	// 不會出現在 info JSON，也不會被下載。
	AutoNotReady bool     `json:"auto_not_ready,omitempty"`
	Fail         *Failure `json:"fail,omitempty"`
	// Compressed 與 Silences 是下載的假媒體檔標記中的 compressed、silences（ASR 前處理後的大小與靜音，見 media.go）；
	// `Given the audio of … is …` 設定，0 與空代表不寫入。
	Compressed int64          `json:"compressed,omitempty"`
	Silences   []MediaSilence `json:"silences,omitempty"`
	// Scenes 是影片的畫面（FRM，`Given the video of … has the scenes:`）：有值時 -J 列出影像格式、
	// 下載的影片有影像串流，假 ffmpeg 的場景偵測與擷取依它回應（features/internal/fakeframes）。
	Scenes []fakeframes.Scene `json:"scenes,omitempty"`
	// BestFormat 是最高畫質的影像格式（`寬x高`，`Given the best video format of … is …`）；空字串代表 DefaultBestFormat。
	BestFormat string `json:"best_format,omitempty"`
}

// DefaultBestFormat 是有畫面的影片沒有指定最高畫質時的格式。
const DefaultBestFormat = "1920x1080"

// HasVideo 回報這部影片有沒有影像串流（登記了畫面或最高畫質）。
func (v *Video) HasVideo() bool { return len(v.Scenes) > 0 || v.BestFormat != "" }

// VideoFormats 回傳影像格式（寬、高），由高到低：最高畫質，再加上不超過它的 1920x1080、1280x720、854x480、640x360。
func (v *Video) VideoFormats() [][2]int {
	best := [2]int{1920, 1080}
	if v.BestFormat != "" {
		if w, h, ok := ParseSize(v.BestFormat); ok {
			best = [2]int{w, h}
		}
	}
	out := [][2]int{best}
	for _, f := range [][2]int{{1920, 1080}, {1280, 720}, {854, 480}, {640, 360}} {
		if f[0]*f[1] < best[0]*best[1] {
			out = append(out, f)
		}
	}
	return out
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
	// AvailableAt 只用於 Video.Later：RFC 3339 的補上時間。
	AvailableAt string `json:"available_at,omitempty"`
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
	// Pid 是這次呼叫的程序 ID，讓測試判斷呼叫是否仍在執行（見 PidAlive）。
	Pid int `json:"pid,omitempty"`
	// CookiesFile 與 CookiesMode 在有 --cookies 時記錄檔案路徑與權限（例如 "0600"）；
	// 檔案不存在或無法讀取時 CookiesMode 為 "missing"。
	CookiesFile string `json:"cookies_file,omitempty"`
	CookiesMode string `json:"cookies_mode,omitempty"`
}

// PartKey 是 Bilibili 分P 在 State.Videos 中的 key：<bvid>/p<n>。
func PartKey(bvid string, part int) string { return fmt.Sprintf("%s/p%d", bvid, part) }

// PidAlive 回報 pid 的程序是否仍存活（殭屍不算）。
func PidAlive(pid int) bool { return pidAlive(pid) }

// FakeDir 回傳場景根目錄 root 下放狀態檔與呼叫紀錄的目錄。
func FakeDir(root string) string { return filepath.Join(root, "fake") }

func statePath(root string) string { return filepath.Join(FakeDir(root), "state.json") }

// NowFile 是場景的 fake clock 現在的時間（RFC 3339），harness 在時鐘改變時寫入（見 WriteNow）；
// 假工具是另一個程序，讀不到 harness 的 clock。
func NowFile(root string) string { return filepath.Join(FakeDir(root), "now") }

// WriteNow 寫入 fake clock 現在的時間。
func WriteNow(root string, now time.Time) error {
	if err := os.MkdirAll(FakeDir(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(NowFile(root), []byte(now.UTC().Format(time.RFC3339Nano)), 0o644)
}

// readNow 讀取 fake clock 現在的時間；沒有寫入過或格式不對時是零值（事後補上的字幕軌都還沒出現）。
func readNow(root string) time.Time {
	b, err := os.ReadFile(NowFile(root))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// at 回傳 now 時看得到的影片：Later 中已經到了補上時間的字幕軌併進 Tracks（同種類、同語言的取代原本的）。
// 沒有任何字幕軌補上時回傳 v 本身。
func (v *Video) at(now time.Time) *Video {
	var add []Track
	for _, t := range v.Later {
		at, err := time.Parse(time.RFC3339, t.AvailableAt)
		if err != nil || now.IsZero() || now.Before(at) {
			continue
		}
		t.AvailableAt = ""
		add = append(add, t)
	}
	if len(add) == 0 {
		return v
	}
	cp := *v
	cp.Tracks = nil
	for _, t := range v.Tracks {
		if !slices.ContainsFunc(add, func(a Track) bool { return a.Kind == t.Kind && a.Lang == t.Lang }) {
			cp.Tracks = append(cp.Tracks, t)
		}
	}
	cp.Tracks = append(cp.Tracks, add...)
	return &cp
}
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
