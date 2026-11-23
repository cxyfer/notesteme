// Package harness 提供驗收場景的 World：每個場景一份、互不共用狀態（ADR-0015）。
//
// World 持有場景的根目錄、環境變數 map、fake clock、fakenet、腳本化的字幕來源，
// 以及假工具的狀態。所有東西都只存在於這個 World 內：不呼叫 os.Setenv，
// 不使用全域的時間或網路（trap 的違規紀錄除外，見 Check）。
package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/features/internal/fakegemini"
	"github.com/cxyfer/notesteme/features/internal/fakegit"
	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/fakepodcast"
	"github.com/cxyfer/notesteme/features/internal/fakesource"
	"github.com/cxyfer/notesteme/features/internal/fakesponsorblock"
	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
)

// 測試用的預設 secret。這些值只用於測試；NFR-SEC-3 的掃描會在 log 與 calls.jsonl 中尋找它們。
const (
	TestPassword  = "nst-test-password-4f1b9c"
	TestMasterKey = "nst-test-master-key-7d2e8a0c5b31f964a8e1d3c7"
	// TestSupadataKey 是測試環境提供給 Supadata 的假 API key（fake origin 會檢查它）。
	TestSupadataKey = "nst-test-supadata-key-3a9c61e0f2"
	// TestGroqKey 是測試環境提供給 Groq 的假 API key（fake origin 要求 Authorization: Bearer 帶有它）。
	TestGroqKey = "nst-test-groq-key-8e4b17d92c"
	// TestGeminiKey 是測試環境提供給 Gemini 的假 API key（fakegemini 要求 x-goog-api-key 帶有它，GEM-R4）。
	TestGeminiKey = "nst-test-gemini-key-5c03e9a7d1"
)

// SupadataHost 是 Supadata API 的主機名稱；fakenet 把它導到腳本化的 fake origin。
const SupadataHost = "api.supadata.ai"

// GroqHost 是 Groq API 的主機名稱（預設 base_url 為 https://api.groq.com/openai/v1）；fakenet 把它導到
// 腳本化的 OpenAI 相容 ASR 端點。
const GroqHost = "api.groq.com"

// GroqAPIKeyEnv 是 Groq API key 的環境變數。
const GroqAPIKeyEnv = config.EnvGroqAPIKey

// LLM provider 的 API key 環境變數（LLM-execution 規格 L6：NOTESTEME_<ID 大寫，- 換成 _>_API_KEY）。
const (
	AnthropicAPIKeyEnv = "NOTESTEME_ANTHROPIC_API_KEY"
	OpenAIAPIKeyEnv    = "NOTESTEME_OPENAI_API_KEY"
	// GeminiAPIKeyEnv 是 provider gemini 的 API key（GEM-R7）。
	GeminiAPIKeyEnv = "NOTESTEME_GEMINI_API_KEY"
)

// DefaultNow 是新場景的 fake clock 起始時間。
var DefaultNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// 場景中使用的虛擬路徑。
const (
	VirtualData   = "/data"
	VirtualConfig = "/config"
	// VirtualMedia 是 GRD-R7 場景使用的媒體根目錄（media_roots: [/media]）。
	VirtualMedia = "/media"
	// VirtualBackups 是備份檔的放置處（OPS-R1），不加 /tmp 前綴：場景根目錄本身就在 /tmp 底下。
	VirtualBackups = "/backups"
	// VirtualVault 是 VAULT 場景的 fs vault（vaults.main.path: /vault，steps.md 4.16）。
	VirtualVault = "/vault"
)

// World 是一個場景的全部狀態。
type World struct {
	// Root 是場景的根目錄（os.MkdirTemp("", "nst")，路徑很短，避免 unix socket 路徑過長）。
	Root string
	// Env 是交給程式的環境變數（app.Env.Vars），不會寫入真實的程序環境。
	Env map[string]string

	Clock   *clock.Fake
	Net     *fakenet.Net
	Sources *fakesource.Script
	// Podcasts 是假的 Podcast feed、音檔與字幕；handler 由 steps 登記到 Net。
	Podcasts *fakepodcast.Feeds
	// LLM 是腳本化的 LLM provider（anthropic、openai），掛在它們的正式主機名稱上。
	LLM *fakellm.Server
	// SponsorBlock 是假的 SponsorBlock（SPB-R2），掛在預設 base_url 的主機上；每個場景預設登記、沒有任何片段
	// （ADR-0020 第 2 點），YouTube 影音的 sponsorblock 步驟實作之後才不會撞上 fakenet 的 trap。
	SponsorBlock *fakesponsorblock.Server
	// Gemini 是假的 Gemini generateContent（GEM-R4），掛在正式主機名稱上，要求 TestGeminiKey。
	Gemini *fakegemini.Server
	// Tools 是假工具的狀態；修改後呼叫 SyncTools 寫入狀態檔。
	Tools faketool.State

	// Refs 是代入用的對照表，之後由 steps 填入：
	// 影音代號（例如 "yt:abc123xxxxx"）對應實際的影音 ID，
	// 以及 "token:<名稱>" 對應該 token 的實際內容。見 Subst。
	Refs map[string]string

	// FixturesDir 是 features/fixtures 的絕對路徑。
	FixturesDir string

	// Log 是 serve 的 log 輸出，見 AppEnv。
	Log *LogBuffer

	// ImplVersions 是步驟 impl_version 的覆寫（PIPE-R6），見 AppEnv。server 已啟動時也會寫入，重啟後覆寫仍然存在。
	ImplVersions map[string]int

	listenMu sync.Mutex
	listens  []ListenRecord

	// CurrentSchema 是 `the current schema version is N` 設定的、程式內建的最新 schema 版本；0 代表用內建的全部。
	CurrentSchema int
	// FailMigration 是 `migration N fails` 指定會失敗的 migration 版本；0 代表沒有。
	FailMigration int

	// hub 是 M5 的 Hub 狀態（真 Hub 或 fakehub，見 hub.go）。
	hub hubState

	closed bool
}

// New 建立場景的根目錄與子目錄（data/、data/inbox/、config/、bin/、fake/、home/、media/talks/），
// 放入假工具的 symlink、初始狀態檔，以及 media/link-to-root（指向 / 的 symlink，GRD-R7 用）。失敗時已建立的目錄會被清除。
func New() (*World, error) {
	root, err := os.MkdirTemp("", "nst")
	if err != nil {
		return nil, err
	}
	fx := FixturesDir()
	clk := clock.NewFake(DefaultNow)
	w := &World{
		Root: root,
		Env: map[string]string{
			"NOTESTEME_CONFIG":     filepath.Join(root, "config", "notesteme.yaml"),
			"NOTESTEME_DATA":       filepath.Join(root, "data"),
			"NOTESTEME_PASSWORD":   TestPassword,
			"NOTESTEME_MASTER_KEY": TestMasterKey,
			// 測試環境預設有 Supadata 的 API key（steps.md 第 4.1 節：fake provider 預設有 key）。
			config.EnvSupadataAPIKey: TestSupadataKey,
			GroqAPIKeyEnv:            TestGroqKey,
			// LLM provider 同理：fake 預設有 key，fakellm 會檢查它。
			AnthropicAPIKeyEnv: fakellm.TestAnthropicKey,
			OpenAIAPIKeyEnv:    fakellm.TestOpenAIKey,
			GeminiAPIKeyEnv:    TestGeminiKey,
			"PATH":             filepath.Join(root, "bin"),
			"HOME":             filepath.Join(root, "home"),
		},
		Clock:    clk,
		Net:      fakenet.New(),
		Sources:  fakesource.New(fx),
		Podcasts: fakepodcast.New(),
		LLM:      fakellm.New(fx, clk),
		// M3 的假服務（features/fixtures/README.md 的「M3 的假服務」）。
		SponsorBlock: fakesponsorblock.New(),
		Gemini:       fakegemini.New(filepath.Join(fx, "llm"), TestGeminiKey),
		Tools:        faketool.State{FixturesDir: fx, Videos: map[string]*faketool.Video{}},
		Refs:         map[string]string{},
		FixturesDir:  fx,
		Log:          &LogBuffer{},
	}
	// 腳本化的 Supadata 掛在它的正式主機名稱上，被測程式照正常路徑連線（經 netguard 與 fakenet）。
	w.Sources.RequiresAPIKey("supadata", TestSupadataKey)
	w.Net.Handle(SupadataHost, w.Sources.SupadataHandler())
	// Groq 同理：OpenAI 相容的 ASR 端點掛在正式主機名稱上，要求 Authorization: Bearer。
	w.Sources.RequiresAPIKey("groq", TestGroqKey)
	w.Net.Handle(GroqHost, w.Sources.OpenAIHandler("groq"))
	// LLM provider：anthropic、openai 掛在正式主機名稱上，要求各自的憑證（fakellm 的 wire 契約見其套件說明）。
	w.LLM.RequireKey("anthropic", fakellm.TestAnthropicKey)
	w.LLM.RequireKey("openai", fakellm.TestOpenAIKey)
	w.Net.Handle(fakellm.AnthropicHost, w.LLM.Handler("anthropic"))
	w.Net.Handle(fakellm.OpenAIHost, w.LLM.Handler("openai"))
	w.Net.Handle(fakesponsorblock.Host, w.SponsorBlock)
	w.Net.Handle(fakegemini.Host, w.Gemini)
	for _, d := range []string{w.DataDir(), w.InboxDir(), w.ConfigDir(), w.BinDir(), w.FakeDir(), w.HomeDir(), w.VaultDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	if err := w.seedMedia(); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := faketool.Install(w.BinDir()); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.SyncTools(); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

// NewT 建立 World，失敗時讓測試失敗，並在測試結束時自動 Close。
func NewT(t interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}) *World {
	t.Helper()
	w, err := New()
	if err != nil {
		t.Fatalf("harness.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// 各子目錄的實際路徑。
func (w *World) DataDir() string   { return filepath.Join(w.Root, "data") }
func (w *World) InboxDir() string  { return filepath.Join(w.Root, "data", "inbox") }
func (w *World) ConfigDir() string { return filepath.Join(w.Root, "config") }
func (w *World) BinDir() string    { return filepath.Join(w.Root, "bin") }
func (w *World) FakeDir() string   { return faketool.FakeDir(w.Root) }
func (w *World) HomeDir() string   { return filepath.Join(w.Root, "home") }
func (w *World) MediaDir() string  { return filepath.Join(w.Root, "media") }

// VaultDir 是 /vault 的實際路徑（fs vault 的根目錄，steps.md 4.16）。
func (w *World) VaultDir() string { return filepath.Join(w.Root, "vault") }

// FakeGit 是這個場景的假 git 遠端與呼叫紀錄（features/internal/fakegit）。
func (w *World) FakeGit() fakegit.Fake { return fakegit.Fake{StateDir: faketool.GitStateDir(w.Root)} }

// HookRecord 是假 hook 的執行紀錄檔（fakehook.Runs 讀取）。
func (w *World) HookRecord() string { return faketool.HookRecord(w.Root) }

// BackupsDir 是 /backups 的實際路徑（不預先建立，由 CLI 或步驟需要時建立）。
func (w *World) BackupsDir() string { return filepath.Join(w.Root, "backups") }

// mediaLinkName 是 media/ 下指向 / 的 symlink 名稱：驗證路徑檢查會解析 symlink（GRD-R7）。
const mediaLinkName = "link-to-root"

// seedMedia 建立 media/talks/（放一個小檔案，供 GRD-R7 預期通過的例子使用）
// 與 media/link-to-root -> /。symlink 只用來讓程式的路徑檢查解析；harness 自己絕不透過它讀寫。
func (w *World) seedMedia() error {
	talks := filepath.Join(w.MediaDir(), "talks")
	if err := os.MkdirAll(talks, 0o700); err != nil {
		return err
	}
	marker := faketool.MediaMarker("local:keynote.mp4", 60)
	if err := os.WriteFile(filepath.Join(talks, "keynote.mp4"), []byte(marker), 0o600); err != nil {
		return err
	}
	return os.Symlink("/", filepath.Join(w.MediaDir(), mediaLinkName))
}

// removeRoot 清除場景根目錄。場景內含指向 / 的 symlink，所以先以 os.Remove 移除該 symlink 本身
// （unlink，不跟隨），再 RemoveAll；RemoveAll 本身也不跟隨 symlink，這裡是雙重保險。
func removeRoot(root string) error {
	link := filepath.Join(root, "media", mediaLinkName)
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.RemoveAll(root)
}

// SyncTools 把 Tools 寫入假工具的狀態檔。
func (w *World) SyncTools() error {
	if w.Tools.FixturesDir == "" {
		w.Tools.FixturesDir = w.FixturesDir
	}
	return faketool.WriteState(w.Root, w.Tools)
}

// Video 回傳（必要時建立）假工具狀態中指定平台 ID 的影片。
func (w *World) Video(id string) *faketool.Video {
	if w.Tools.Videos == nil {
		w.Tools.Videos = map[string]*faketool.Video{}
	}
	v := w.Tools.Videos[id]
	if v == nil {
		v = &faketool.Video{}
		w.Tools.Videos[id] = v
	}
	return v
}

// RemoveAPIKey 移除字幕來源（ASR provider、Supadata）或 LLM provider（anthropic、openai）的 API key：
// 環境變數不再帶入，fake origin 也不再要求 key。
// 兩邊都移除，被測程式若沒檢查 key 就直接呼叫，會拿到轉錄結果或正常的回答而不是 blocked，場景因此失敗。
// 不是已知的 ID 時回傳錯誤。
func (w *World) RemoveAPIKey(sourceID string) error {
	var env string
	switch sourceID {
	case "supadata":
		env = config.EnvSupadataAPIKey
	case "groq":
		env = GroqAPIKeyEnv
	case "anthropic":
		env = AnthropicAPIKeyEnv
	case "openai":
		env = OpenAIAPIKeyEnv
	default:
		return fmt.Errorf("harness 不知道 %q 的 API key 要怎麼移除（只有 supadata、groq、anthropic、openai）", sourceID)
	}
	delete(w.Env, env)
	if fakellm.IsProvider(sourceID) {
		w.LLM.RequireKey(sourceID, "")
	} else {
		w.Sources.RequiresAPIKey(sourceID, "")
	}
	return nil
}

// GatePath 回傳假 yt-dlp 的 gate 檔路徑。
func (w *World) GatePath() string { return filepath.Join(w.FakeDir(), "gate") }

// ArmGate 啟用 gate：之後處理網址的假 yt-dlp 呼叫會阻塞（並留下標記檔與部分檔，見 faketool.State.Gate），
// 直到 OpenGate。已經啟用時不動作，不會清掉正停在 gate 前的程序的標記。
// 任務停在 running 的場景用它代替時序：被擋住的任務不會自己結束。
func (w *World) ArmGate() error {
	gate := w.GatePath()
	if _, err := os.Stat(gate); err == nil {
		return nil
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		return err
	}
	w.Tools.Gate = gate
	return w.SyncTools()
}

// OpenGate 解除 gate，讓被擋住的程序繼續，並清掉被 kill 的程序來不及移除的標記檔。
// gate 沒有啟用時不動作。場景結束或模擬重啟前呼叫，才不會讓程序卡住。
func (w *World) OpenGate() error {
	if w.Tools.Gate == "" {
		return nil
	}
	if err := os.Remove(w.Tools.Gate); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	marks, err := faketool.GateWaiting(w.Tools.Gate)
	if err != nil {
		return err
	}
	for _, m := range marks {
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// GateWaiting 回傳目前停在 gate 前且仍存活的程序數。
// 被 kill 的程序不會移除自己的標記檔，所以只數存活的：已結束的程序不能算成「還在執行」，
// 也不會讓 WaitGateWaiting 的 before+n 偏掉。
func (w *World) GateWaiting() (int, error) {
	if w.Tools.Gate == "" {
		return 0, nil
	}
	return faketool.GateAlive(w.Tools.Gate)
}

// WaitGateWaiting 等到至少 n 個存活的程序停在 gate 前。只以真實時間 limit 防止卡死；
// 結果由標記檔與程序是否存活決定，不靠睡一段固定時間。
func (w *World) WaitGateWaiting(n int, limit time.Duration) error {
	return w.waitGate(limit, func(got int) bool { return got >= n }, fmt.Sprintf("至少 %d", n))
}

// WaitGateCount 等到恰好 n 個存活的程序停在 gate 前，用來確認工作確實被停止（或確實在執行）。
// 程序結束可能有短暫延遲，所以輪詢；limit 只防止卡死。
func (w *World) WaitGateCount(n int, limit time.Duration) error {
	return w.waitGate(limit, func(got int) bool { return got == n }, fmt.Sprintf("恰好 %d", n))
}

func (w *World) waitGate(limit time.Duration, ok func(int) bool, want string) error {
	deadline := time.Now().Add(limit)
	for {
		got, err := w.GateWaiting()
		if err != nil {
			return err
		}
		if ok(got) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待%s 個存活的程序停在 gate 前，%v 內變成 %d 個", want, limit, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ToolCalls 讀取假工具到目前為止的呼叫紀錄。
func (w *World) ToolCalls() ([]faketool.Call, error) { return faketool.ReadCalls(w.Root) }

// Check 彙整這個場景的違規：fakenet 沒有登記的連線、全域 trap 記錄的繞過 netguard 的連線，
// 以及 fakellm 收到的不符 wire 契約或沒有腳本的請求。
// 有違規時回傳錯誤，供 After hook 讓場景失敗。
//
// trap 的違規紀錄是全域的，Check 會取出並清空；場景並行執行時無法分辨違規屬於哪個場景，
// 所以並行度大於 1 時，違規可能被其他場景的 Check 取走（仍然會讓某個場景失敗）。
func (w *World) Check() error {
	var errs []error
	for _, v := range w.Net.Violations() {
		errs = append(errs, errors.New("fakenet: "+v))
	}
	for _, v := range fakenet.DrainTrapViolations() {
		errs = append(errs, errors.New("trap: "+v))
	}
	for _, v := range w.LLM.Violations() {
		errs = append(errs, errors.New("fakellm: "+v))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("outbound connection violations (NFR-TEST-1):\n%w", errors.Join(errs...))
}

// Close 關閉 fake origin 並清除場景根目錄。可重複呼叫。
func (w *World) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	hubErr := w.closeHub()
	if w.Net != nil {
		w.Net.Close()
	}
	if w.Root == "" {
		return hubErr
	}
	return errors.Join(hubErr, removeRoot(w.Root))
}

// virtuals 回傳虛擬路徑與實際路徑的對照。
func (w *World) virtuals() [][2]string {
	return [][2]string{
		{VirtualData, w.DataDir()},
		{VirtualConfig, w.ConfigDir()},
		{VirtualMedia, w.MediaDir()},
		{VirtualBackups, w.BackupsDir()},
		{VirtualVault, w.VaultDir()},
	}
}

// Expand 把字串中的虛擬路徑（/data、/config、/media、/backups、/vault）改寫成場景根目錄下的實際路徑。
// 只在路徑段的邊界比對：/data/x 與 "/data" 會被改寫，/database 與 /home/u/data 不會。
// 用於設定檔內容、CLI 參數、請求 body、檔案路徑。
func (w *World) Expand(s string) string {
	for _, v := range w.virtuals() {
		s = replacePath(s, v[0], v[1])
	}
	return s
}

// Unexpand 是 Expand 的反向改寫：把實際路徑換回虛擬路徑，用於比對輸出前。
func (w *World) Unexpand(s string) string {
	for _, v := range w.virtuals() {
		s = replacePath(s, v[1], v[0])
	}
	return s
}

// isNameByte 回報 b 是否屬於路徑段中的名稱字元。
func isNameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || b == '~' || b == '+' || b == '@' ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

// replacePath 把 s 中完整的路徑前綴 from 換成 to。
// 前綴之前不能是名稱字元或 '/'（否則 from 只是更長路徑的一部分）；
// 之後必須是結尾、'/' 或非名稱字元（否則是 /database 這類不同名稱）。
func replacePath(s, from, to string) string {
	if from == "" || !strings.Contains(s, from) {
		return s
	}
	var sb strings.Builder
	i := 0
	for {
		j := strings.Index(s[i:], from)
		if j < 0 {
			sb.WriteString(s[i:])
			break
		}
		j += i
		end := j + len(from)
		okBefore := j == 0 || (!isNameByte(s[j-1]) && s[j-1] != '/')
		okAfter := end == len(s) || s[end] == '/' || !isNameByte(s[end])
		sb.WriteString(s[i:j])
		if okBefore && okAfter {
			sb.WriteString(to)
		} else {
			sb.WriteString(from)
		}
		i = end
	}
	return sb.String()
}

var tokenRef = regexp.MustCompile(`\{token:([^}]+)\}`)

// Subst 代入 {token:<名稱>}：以 Refs["token:<名稱>"] 取代；Refs 沒有該項目時維持原樣。
// 影音代號的代入（steps.md 第 2 節）之後接在這裡。
func (w *World) Subst(s string) string {
	return tokenRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := w.Refs["token:"+tokenRef.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
}

// FixturesDir 回傳 features/fixtures 的絕對路徑。
// 優先用原始碼位置推算；以 -trimpath 建置而找不到時，從目前目錄往上尋找 features/fixtures。
func FixturesDir() string {
	var guess string
	if _, file, _, ok := runtime.Caller(0); ok {
		guess = filepath.Join(filepath.Dir(file), "..", "..", "fixtures")
		if fi, err := os.Stat(guess); err == nil && fi.IsDir() {
			return guess
		}
	}
	if dir, err := os.Getwd(); err == nil {
		for {
			for _, rel := range []string{filepath.Join("features", "fixtures"), "fixtures"} {
				p := filepath.Join(dir, rel)
				if fi, err := os.Stat(p); err == nil && fi.IsDir() {
					return p
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return guess
}
