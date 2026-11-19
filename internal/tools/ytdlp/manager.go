package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// versionRe 是可以安裝或固定的版本格式。pip 的參數由它擋住：不符合的字串（含 `;`、空白、`-` 開頭的旗標）一律不進入命令列。
var versionRe = regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}(\.\d+)?$`)

// ValidVersion 回報 v 是不是合法的 yt-dlp 版本字串（OPS-R6 的安全限制）。
func ValidVersion(v string) bool { return versionRe.MatchString(v) }

// 各個外部指令的逾時。pip 要從 PyPI 下載，給得比較寬。
const (
	venvTimeout    = 2 * time.Minute
	pipTimeout     = 10 * time.Minute
	versionTimeout = 30 * time.Second
)

// 錯誤；API 層依它們對應 HTTP 狀態碼。
var (
	// ErrInvalidVersion 表示版本字串不符合格式。
	ErrInvalidVersion = errors.New("版本必須是 YYYY.MM.DD 或 YYYY.MM.DD.N 的格式。")
	// ErrInvalidRequest 表示請求本身互相矛盾。
	ErrInvalidRequest = errors.New("更新請求不正確")
	// ErrExternallyManaged 表示 yt-dlp 的路徑由設定檔或環境變數指定，Notesteme 不會更新它。
	ErrExternallyManaged = errors.New("yt-dlp 的路徑是自行指定的，Notesteme 不會更新它")
	// ErrBusy 表示已經有另一個更新或回退正在進行。
	ErrBusy = errors.New("已經有另一個 yt-dlp 更新正在進行，請稍後再試。")
	// ErrInstall 表示安裝沒有完成（建立 venv 或 pip 失敗）；沒有任何東西被切換。
	ErrInstall = errors.New("安裝 yt-dlp 失敗")
	// ErrNoPrevious 表示沒有可以回到的上一版。
	ErrNoPrevious = errors.New("沒有可以回到的上一個 yt-dlp 版本。")
	// ErrUnsupportedPlatform 表示這個平台不支援受管理的 yt-dlp（Windows 的 venv 布局與 symlink 都不同）。
	// 環境變數名稱取自 toolexec，與實際讀取的一致。
	ErrUnsupportedPlatform = errors.New("受管理的 yt-dlp 在 Windows 上不支援，請用設定檔或 " + toolexec.YtDlp.EnvVar + " 指定路徑。")
)

// UpdateRequest 是更新的參數。兩者都沒有指定時，更新到最新版。
type UpdateRequest struct {
	// Pin 非空時，安裝並固定在這個版本（目前已經是這個版本就只固定）。
	Pin string
	// Unpin 解除固定；不更新。
	Unpin bool
}

// Result 是更新或回退的結果。
type Result struct {
	// Version 是現在使用的版本；沒有任何可用的 yt-dlp 時為空字串。
	Version string `json:"version"`
	// RolledBack 為 true 表示新版本沒有通過自我檢查，已回到原本的版本。
	RolledBack bool `json:"rolled_back"`
	// Skipped 為 true 表示因為固定了版本而略過更新。
	Skipped bool `json:"skipped"`
	// Pinned 是固定的版本；沒有固定時為空字串。
	Pinned string `json:"pinned"`
	// Previous 是上一版（供 rollback）；沒有時為空字串。
	Previous string `json:"previous"`
	// Message 是給人看的說明（繁體中文）。
	Message string `json:"message"`
	// Reason 是回退（RolledBack）的簡短原因：單行、已遮蔽 cookie 內容；其他情況省略。
	// 原因放在這裡而不是附在 Message 後面，Message 維持規格的固定文字。
	Reason string `json:"reason,omitempty"`
}

// Manager 安裝、更新與回退受管理的 yt-dlp。所有外部指令都經過 Runner，也就是和其他工具相同的子程序限制。
// 同一時間只做一件事：進行中再呼叫會得到 ErrBusy。
type Manager struct {
	// Tools 是 <data>/tools。
	Tools string
	// Runner 執行 python、pip 與 yt-dlp。
	Runner toolexec.Runner
	// Resolver 尋找目前的 yt-dlp 與 PATH 上的 python3；它的 ManagedDir 必須是 Tools。
	Resolver toolexec.Resolver
	Clock    clock.Clock
	Log      *slog.Logger
	// GOOS 是執行的平台；空字串用 runtime.GOOS。測試可以注入。
	GOOS string

	mu sync.Mutex
}

// New 建立 Manager；resolver 的 ManagedDir 會設成 tools。
func New(tools string, runner toolexec.Runner, resolver toolexec.Resolver, clk clock.Clock, log *slog.Logger) *Manager {
	resolver.ManagedDir = tools
	return &Manager{Tools: tools, Runner: runner, Resolver: resolver, Clock: clk, Log: log}
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.New(slog.DiscardHandler)
}

// goos 回傳要判斷的平台。
func (m *Manager) goos() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return runtime.GOOS
}

// checkSupported 在平台不支援受管理的 yt-dlp 時回傳 ErrUnsupportedPlatform，不必等到安裝失敗才發現。
func (m *Manager) checkSupported() error {
	if m.goos() == "windows" {
		return ErrUnsupportedPlatform
	}
	return nil
}

// State 回傳固定的版本與上一版。
func (m *Manager) State() (State, error) { return LoadState(m.Tools) }

// PipArgs 是安裝 yt-dlp 時交給 venv 內 python 的參數。固定的白名單：只有這幾個旗標，套件只有 yt-dlp[default]，
// 不接受自訂的 package index 或其他 pip 參數；version 已經過 ValidVersion（空字串代表最新版）。
func PipArgs(version string) []string {
	// --isolated 忽略使用者與系統的 pip.conf 和 PIP_ 開頭的環境變數，這樣設定檔或環境也無法塞進自訂的 index。
	args := []string{"-m", "pip", "install", "--isolated", "--disable-pip-version-check", "--no-input"}
	if version == "" {
		return append(args, "-U", "yt-dlp[default]")
	}
	return append(args, "yt-dlp[default]=="+version)
}

// Update 依 req 更新 yt-dlp（OPS-R6、R7）：
//  1. 路徑由設定檔或環境變數指定時拒絕（ErrExternallyManaged）。
//  2. 已經固定版本、而且不是要改固定時略過，不做任何事。
//  3. 在新的 venv 安裝，通過自我檢查才切換 symlink；沒通過就刪除新 venv，維持原本的版本（RolledBack）。
func (m *Manager) Update(ctx context.Context, req UpdateRequest) (*Result, error) {
	if req.Pin != "" && req.Unpin {
		return nil, fmt.Errorf("%w：不能同時指定 pin 與 unpin。", ErrInvalidRequest)
	}
	if req.Pin != "" && !ValidVersion(req.Pin) {
		return nil, ErrInvalidVersion
	}
	if !m.mu.TryLock() {
		return nil, ErrBusy
	}
	defer m.mu.Unlock()

	loc := m.Resolver.Locate(toolexec.YtDlp)
	if err := externallyManaged(loc); err != nil {
		return nil, err
	}
	if err := m.checkSupported(); err != nil {
		return nil, err
	}
	st, err := LoadState(m.Tools)
	if err != nil {
		return nil, err
	}
	cur := m.version(ctx, loc)

	switch {
	case req.Unpin:
		st.Pinned = ""
		if err := saveState(m.Tools, st); err != nil {
			return nil, err
		}
		return &Result{Version: cur, Previous: st.Previous, Message: "yt-dlp 已解除固定。"}, nil
	case req.Pin == "" && st.Pinned != "":
		return &Result{Version: cur, Skipped: true, Pinned: st.Pinned, Previous: st.Previous,
			Message: fmt.Sprintf("yt-dlp 已固定在 %s，略過更新。", st.Pinned)}, nil
	case req.Pin != "" && req.Pin == cur:
		st.Pinned = req.Pin
		if err := saveState(m.Tools, st); err != nil {
			return nil, err
		}
		return &Result{Version: cur, Pinned: st.Pinned, Previous: st.Previous,
			Message: fmt.Sprintf("yt-dlp 已固定在 %s。", cur)}, nil
	}
	return m.install(ctx, req.Pin, cur, st)
}

// install 在新的 venv 安裝 target（空字串代表最新版），自我檢查通過才切換；cur 是目前的版本。
func (m *Manager) install(ctx context.Context, target, cur string, st State) (*Result, error) {
	if err := os.MkdirAll(VersionsDir(m.Tools), 0o700); err != nil {
		return nil, err
	}
	python, err := toolexec.LookPath("python3", m.Resolver.Vars["PATH"])
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInstall, err)
	}
	id := m.newID(target)
	dir := filepath.Join(VersionsDir(m.Tools), id)
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(dir) // 失敗、被取消或不需要的新 venv 都清掉，不留下半成品
		}
	}()

	if err := m.run(ctx, venvTimeout, python, "-m", "venv", dir); err != nil {
		return nil, fmt.Errorf("%w：建立 venv：%v", ErrInstall, err)
	}
	if err := m.run(ctx, pipTimeout, filepath.Join(dir, "bin", "python"), PipArgs(target)...); err != nil {
		return nil, fmt.Errorf("%w：pip：%v", ErrInstall, err)
	}

	got, checkErr := m.selfCheck(ctx, dir, target)
	if checkErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		m.log().WarnContext(ctx, "yt-dlp 更新後自我檢查失敗，已回退", "error", checkErr, "version", got, "current", cur)
		msg := fmt.Sprintf("yt-dlp 更新後自我檢查失敗，已回到 %s。", cur)
		if cur == "" {
			msg = "yt-dlp 更新後自我檢查失敗，沒有可以回到的版本。"
		}
		return &Result{Version: cur, RolledBack: true, Pinned: st.Pinned, Previous: st.Previous, Message: msg, Reason: shortReason(checkErr)}, nil
	}
	if got == cur && target == "" {
		// 最新版就是現在的版本：不需要切換，新 venv 由 defer 清掉。
		return &Result{Version: cur, Pinned: st.Pinned, Previous: st.Previous,
			Message: fmt.Sprintf("yt-dlp 已經是最新版本（%s）。", cur)}, nil
	}

	// 通過了：先記下上一版再切換 symlink（原子地換掉）。
	oldID := m.currentID()
	if err := switchLink(m.Tools, id); err != nil {
		return nil, err
	}
	next := State{Pinned: st.Pinned, Previous: cur, PreviousID: oldID}
	if target != "" {
		next.Pinned = target
	}
	if err := saveState(m.Tools, next); err != nil {
		m.restoreLink(oldID)
		return nil, err
	}
	done = true
	m.prune(id, oldID)

	msg := fmt.Sprintf("yt-dlp 已更新到 %s。", got)
	if target != "" {
		msg = fmt.Sprintf("yt-dlp 已更新到 %s，並固定在這個版本。", got)
	}
	return &Result{Version: got, Pinned: next.Pinned, Previous: next.Previous, Message: msg}, nil
}

// Rollback 回到上一版（OPS-R6）：上一版是受管理的 venv 就切換 symlink；不是（原本用映像內建的）就移除 symlink，
// 讓尋找順序退回 PATH。原本固定的版本與回到的版本不同時，一併解除固定。
func (m *Manager) Rollback(ctx context.Context) (*Result, error) {
	if !m.mu.TryLock() {
		return nil, ErrBusy
	}
	defer m.mu.Unlock()

	loc := m.Resolver.Locate(toolexec.YtDlp)
	if err := externallyManaged(loc); err != nil {
		return nil, err
	}
	if err := m.checkSupported(); err != nil {
		return nil, err
	}
	st, err := LoadState(m.Tools)
	if err != nil {
		return nil, err
	}
	if st.Previous == "" && st.PreviousID == "" {
		return nil, ErrNoPrevious
	}
	curID, cur := m.currentID(), m.version(ctx, loc)

	if st.PreviousID != "" {
		if fi, err := os.Stat(filepath.Join(VersionsDir(m.Tools), st.PreviousID)); err != nil || !fi.IsDir() {
			return nil, ErrNoPrevious
		}
		if err := switchLink(m.Tools, st.PreviousID); err != nil {
			return nil, err
		}
	} else if err := os.Remove(LinkPath(m.Tools)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	now := m.version(ctx, m.Resolver.Locate(toolexec.YtDlp))
	if now == "" {
		// 目標版本無法執行：把 symlink 換回原本的版本並保持 state.json 不變。
		m.restoreLink(curID)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w：上一版 yt-dlp 無法執行。", ErrNoPrevious)
	}

	next := State{Pinned: st.Pinned, Previous: cur, PreviousID: curID}
	if next.Pinned != "" && next.Pinned != now {
		next.Pinned = ""
	}
	if err := saveState(m.Tools, next); err != nil {
		// 狀態沒寫進去，symlink 也要換回去，不能留下「symlink 與狀態不一致」。
		m.restoreLink(curID)
		return nil, err
	}
	m.prune(st.PreviousID, curID)
	return &Result{Version: now, Pinned: next.Pinned, Previous: next.Previous,
		Message: fmt.Sprintf("yt-dlp 已回到 %s。", now)}, nil
}

// shortReason 把 err 整理成單行的簡短原因：先遮蔽 cookie 內容，空白收斂成單一空格，最多 200 個字元。
func shortReason(err error) string {
	s := strings.Join(strings.Fields(string(toolexec.RedactCookieLines([]byte(err.Error())))), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// externallyManaged 在路徑由設定檔或環境變數指定時回傳 ErrExternallyManaged（加上是哪一個來源）。
func externallyManaged(loc toolexec.Location) error {
	switch loc.Source {
	case toolexec.SourceConfigFile:
		return fmt.Errorf("%w：設定檔的 tools.%s 指定了路徑。", ErrExternallyManaged, toolexec.YtDlp.ConfigKey)
	case toolexec.SourceEnv:
		return fmt.Errorf("%w：環境變數 %s 指定了路徑。", ErrExternallyManaged, toolexec.YtDlp.EnvVar)
	}
	return nil
}

// selfCheck 對剛裝好的 yt-dlp 做自我檢查（OPS-R6）：`--version`，以及對內建的檢查影片跑一次 Probe。
// want 非空時，回報的版本必須與要求的相同。回傳 yt-dlp 回報的版本。
func (m *Manager) selfCheck(ctx context.Context, dir, want string) (string, error) {
	bin := filepath.Join(dir, "bin", toolexec.YtDlp.Name)
	yt := &youtube.Client{
		Runner: m.Runner,
		Tools:  toolexec.Resolver{Config: map[string]string{toolexec.YtDlp.ConfigKey: bin}},
	}
	v, err := yt.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("--version 失敗：%w", err)
	}
	if want != "" && v != want {
		return v, fmt.Errorf("安裝的版本是 %s，不是要求的 %s", v, want)
	}
	if _, err := yt.Probe(ctx, CheckVideoID); err != nil {
		return v, fmt.Errorf("探測檢查影片失敗：%w", err)
	}
	return v, nil
}

// version 回傳 loc 這個 yt-dlp 回報的版本；不能執行或沒有輸出時回傳空字串。
func (m *Manager) version(ctx context.Context, loc toolexec.Location) string {
	if !loc.Available() {
		return ""
	}
	res, err := m.Runner.Run(ctx, toolexec.Cmd{Path: loc.Path, Args: []string{"--version"}, Timeout: versionTimeout})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

// run 執行一個指令；非 0 結束碼視為錯誤，訊息只附 stderr 的最後一段。
func (m *Manager) run(ctx context.Context, timeout time.Duration, path string, args ...string) error {
	res, err := m.Runner.Run(ctx, toolexec.Cmd{Path: path, Args: args, Timeout: timeout})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// 先遮蔽含 cookie 內容的行，再收斂成單行（收斂之後就無法逐行遮蔽了）。
		tail := strings.Join(strings.Fields(string(toolexec.RedactCookieLines(res.Stderr))), " ")
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return fmt.Errorf("結束碼 %d：%s", res.ExitCode, tail)
	}
	return nil
}

// newID 決定新 venv 的目錄名稱。venv 裝好之後不能搬移，所以名稱要在安裝前決定：
// 指定版本時用版本，否則用時間；已經存在就加上序號。
func (m *Manager) newID(target string) string {
	base := target
	if base == "" {
		base = "latest-" + m.Clock.Now().UTC().Format("20060102T150405Z")
	}
	id := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(VersionsDir(m.Tools), id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// currentID 回傳 symlink 目前指向的 venv 目錄名稱；沒有 symlink（沒有受管理的版本）時為空字串。
func (m *Manager) currentID() string {
	target, err := os.Readlink(LinkPath(m.Tools))
	if err != nil {
		return ""
	}
	// 名稱之後會寫進 state.json 當 previous_id；不合法的（例如 symlink 被改成指向 ..）一律視為沒有。
	if id := filepath.Base(target); ValidID(id) {
		return id
	}
	return ""
}

// switchLink 讓 yt-dlp-venv 指向 yt-dlp/<id>：先在旁邊建立新的 symlink，再 rename 覆蓋，切換是原子的。
func switchLink(tools, id string) error {
	link := LinkPath(tools)
	tmp := link + ".new"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(filepath.Join(VersionsDirName, id), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// restoreLink 在狀態檔寫入失敗時把 symlink 換回去（盡力而為）。
func (m *Manager) restoreLink(oldID string) {
	if oldID != "" {
		_ = switchLink(m.Tools, oldID)
		return
	}
	_ = os.Remove(LinkPath(m.Tools))
}

// prune 刪除 yt-dlp/ 底下除了 keep 以外的 venv 目錄（目前版本與上一版）；一般檔案（state.json）不動。
func (m *Manager) prune(keep ...string) {
	entries, err := os.ReadDir(VersionsDir(m.Tools))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		keepIt := false
		for _, k := range keep {
			keepIt = keepIt || e.Name() == k
		}
		if !keepIt {
			_ = os.RemoveAll(filepath.Join(VersionsDir(m.Tools), e.Name()))
		}
	}
}
