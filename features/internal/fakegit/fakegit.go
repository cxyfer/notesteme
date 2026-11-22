// Package fakegit 是驗收測試用的假 `git` CLI 與假遠端（VAULT，M3）。
//
// 產品的 git adapter 經 toolexec 執行 `git`（ADR-0020 第 6 點）；驗收測試的 PATH 只有假工具，
// 由 faketool 依 argv[0] 分派到這裡的 Run（接進 faketool.Dispatch 由 M3 的實作切片負責）。
// 狀態放在場景目錄的檔案中（StateDir），和 faketool 一樣以假工具自己的位置找到場景，不依賴環境變數
// （產品會清掉 git 的環境變數，VAULT-R8）。
//
// 支援的子命令只有產品會用到的：clone、pull --ff-only、add --、commit、push、rev-parse HEAD。
// 每次呼叫的 argv 都記錄下來（Calls），讓步驟檢查 `-c core.hooksPath=/dev/null` 與 argv 中沒有憑證。
package fakegit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// 拒絕 push 的方式。
const (
	RejectNonFastForward = "non-fast-forward"
	RejectBadCredentials = "bad credentials"
)

// Commit 是假遠端或本機 clone 中的一個 commit。
type Commit struct {
	ID      string   `json:"id"`
	Message string   `json:"message"`
	Author  string   `json:"author,omitempty"`
	Files   []string `json:"files"` // 相對於 repo 根目錄、這個 commit 變動的檔案
}

// RemoteState 是假遠端的狀態（StateDir/remote.json）。
type RemoteState struct {
	URL          string   `json:"url"`
	Branch       string   `json:"branch"`
	Commits      []Commit `json:"commits"`
	Pushes       int      `json:"pushes"`
	Reject       string   `json:"reject,omitempty"`         // 一直拒絕
	FailNextPush bool     `json:"fail_next_push,omitempty"` // 下一次 push 網路錯誤
}

// cloneState 是本機 clone 的狀態（<clone>/.git/fakegit.json）。
type cloneState struct {
	Remote   string   `json:"remote"`
	Branch   string   `json:"branch"`
	Staged   []string `json:"staged"`
	Unpushed []Commit `json:"unpushed"`
	Head     string   `json:"head"`
}

// Call 是一次呼叫的紀錄。
type Call struct {
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"`
	Exit int      `json:"exit"`
}

// Fake 操作 StateDir 中的假遠端；StateDir 是場景目錄中的 fake/git。
type Fake struct {
	StateDir string
}

func (f Fake) remotePath() string { return filepath.Join(f.StateDir, "remote.json") }
func (f Fake) callsPath() string  { return filepath.Join(f.StateDir, "calls.jsonl") }

// Remote 讀取假遠端的狀態；還沒有建立時回傳空的狀態。
func (f Fake) Remote() (RemoteState, error) {
	var st RemoteState
	b, err := os.ReadFile(f.remotePath())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// SetRemote 寫入假遠端的狀態。
func (f Fake) SetRemote(st RemoteState) error {
	if err := os.MkdirAll(f.StateDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.remotePath(), b, 0o644)
}

// Calls 回傳記錄的呼叫。
func (f Fake) Calls() ([]Call, error) {
	b, err := os.ReadFile(f.callsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Call
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var c Call
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (f Fake) record(c Call) {
	b, _ := json.Marshal(c)
	_ = os.MkdirAll(f.StateDir, 0o755)
	fh, err := os.OpenFile(f.callsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(b, '\n'))
}

// Run 執行一次假 git：dir 是工作目錄，args 不含 argv[0]。回傳結束碼。
func (f Fake) Run(dir string, args []string, stdout, stderr io.Writer) int {
	code := f.run(dir, args, stdout, stderr)
	f.record(Call{Dir: dir, Argv: append([]string{"git"}, args...), Exit: code})
	return code
}

func (f Fake) run(dir string, args []string, stdout, stderr io.Writer) int {
	// 全域選項：-c k=v、-C dir。
	for len(args) > 0 {
		switch args[0] {
		case "-c":
			if len(args) < 2 {
				fmt.Fprintln(stderr, "error: option `-c' requires a value")
				return 129
			}
			args = args[2:]
			continue
		case "-C":
			if len(args) < 2 {
				fmt.Fprintln(stderr, "error: option `-C' requires a value")
				return 129
			}
			dir = resolve(dir, args[1])
			args = args[2:]
			continue
		}
		break
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: git <command>")
		return 1
	}
	switch args[0] {
	case "clone":
		return f.clone(dir, args[1:], stderr)
	case "pull":
		return f.pull(dir, args[1:], stderr)
	case "add":
		return f.add(dir, args[1:], stderr)
	case "commit":
		return f.commit(dir, args[1:], stdout, stderr)
	case "push":
		return f.push(dir, args[1:], stderr)
	case "rev-parse":
		cs, err := loadClone(dir)
		if err != nil {
			fmt.Fprintln(stderr, "fatal: not a git repository")
			return 128
		}
		fmt.Fprintln(stdout, cs.Head)
		return 0
	}
	fmt.Fprintf(stderr, "fake git: %q is not supported\n", args[0])
	return 1
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func clonePath(dir string) string { return filepath.Join(dir, ".git", "fakegit.json") }

func loadClone(dir string) (cloneState, error) {
	var cs cloneState
	b, err := os.ReadFile(clonePath(dir))
	if err != nil {
		return cs, err
	}
	return cs, json.Unmarshal(b, &cs)
}

func saveClone(dir string, cs cloneState) error {
	b, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	return os.WriteFile(clonePath(dir), b, 0o644)
}

func (f Fake) clone(dir string, args []string, stderr io.Writer) int {
	branch, rest := "main", []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--branch", "-b":
			if i+1 < len(args) {
				branch = args[i+1]
				i++
			}
		case "--single-branch", "--quiet", "-q":
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "usage: git clone [--branch <b>] <repo> <dir>")
		return 129
	}
	st, err := f.Remote()
	if err != nil || st.URL != rest[0] {
		fmt.Fprintf(stderr, "fatal: repository '%s' not found\n", rest[0])
		return 128
	}
	if st.Reject == RejectBadCredentials {
		fmt.Fprintf(stderr, "fatal: Authentication failed for '%s'\n", rest[0])
		return 128
	}
	dest := resolve(dir, rest[1])
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		fmt.Fprintf(stderr, "fatal: destination path '%s' already exists and is not an empty directory.\n", rest[1])
		return 128
	}
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 128
	}
	head := ""
	if n := len(st.Commits); n > 0 {
		head = st.Commits[n-1].ID
	}
	if err := saveClone(dest, cloneState{Remote: rest[0], Branch: branch, Head: head}); err != nil {
		fmt.Fprintln(stderr, err)
		return 128
	}
	return 0
}

func (f Fake) pull(dir string, args []string, stderr io.Writer) int {
	if !slices.Contains(args, "--ff-only") {
		fmt.Fprintln(stderr, "fake git: pull without --ff-only is not supported")
		return 1
	}
	if _, err := loadClone(dir); err != nil {
		fmt.Fprintln(stderr, "fatal: not a git repository")
		return 128
	}
	st, _ := f.Remote()
	switch st.Reject {
	case RejectNonFastForward:
		fmt.Fprintln(stderr, "fatal: Not possible to fast-forward, aborting.")
		return 128
	case RejectBadCredentials:
		fmt.Fprintf(stderr, "fatal: Authentication failed for '%s'\n", st.URL)
		return 128
	}
	return 0
}

func (f Fake) add(dir string, args []string, stderr io.Writer) int {
	cs, err := loadClone(dir)
	if err != nil {
		fmt.Fprintln(stderr, "fatal: not a git repository")
		return 128
	}
	i := slices.Index(args, "--")
	if i < 0 {
		fmt.Fprintln(stderr, "fake git: add requires -- before the paths")
		return 1
	}
	for _, p := range args[i+1:] {
		if filepath.IsAbs(p) || strings.HasPrefix(filepath.Clean(p), "..") {
			fmt.Fprintf(stderr, "fatal: %s: '%s' is outside repository\n", p, p)
			return 128
		}
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			fmt.Fprintf(stderr, "fatal: pathspec '%s' did not match any files\n", p)
			return 128
		}
		if !slices.Contains(cs.Staged, filepath.ToSlash(p)) {
			cs.Staged = append(cs.Staged, filepath.ToSlash(p))
		}
	}
	if err := saveClone(dir, cs); err != nil {
		fmt.Fprintln(stderr, err)
		return 128
	}
	return 0
}

func (f Fake) commit(dir string, args []string, stdout, stderr io.Writer) int {
	cs, err := loadClone(dir)
	if err != nil {
		fmt.Fprintln(stderr, "fatal: not a git repository")
		return 128
	}
	var msg, author string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-m" && i+1 < len(args):
			msg = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--author="):
			author = strings.TrimPrefix(args[i], "--author=")
		}
	}
	if len(cs.Staged) == 0 {
		fmt.Fprintln(stdout, "nothing to commit, working tree clean")
		return 1
	}
	st, _ := f.Remote()
	id := "c" + strconv.Itoa(len(st.Commits)+len(cs.Unpushed)+1)
	cs.Unpushed = append(cs.Unpushed, Commit{ID: id, Message: msg, Author: author, Files: cs.Staged})
	cs.Staged, cs.Head = nil, id
	if err := saveClone(dir, cs); err != nil {
		fmt.Fprintln(stderr, err)
		return 128
	}
	return 0
}

func (f Fake) push(dir string, args []string, stderr io.Writer) int {
	cs, err := loadClone(dir)
	if err != nil {
		fmt.Fprintln(stderr, "fatal: not a git repository")
		return 128
	}
	st, err := f.Remote()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 128
	}
	switch {
	case st.FailNextPush:
		st.FailNextPush = false
		_ = f.SetRemote(st)
		fmt.Fprintf(stderr, "fatal: unable to access '%s/': Could not resolve host: %s\n", st.URL, hostOf(st.URL))
		return 128
	case st.Reject == RejectBadCredentials:
		fmt.Fprintf(stderr, "fatal: Authentication failed for '%s'\n", st.URL)
		return 128
	case st.Reject == RejectNonFastForward:
		fmt.Fprintf(stderr, "To %s\n ! [rejected]        HEAD -> %s (non-fast-forward)\nerror: failed to push some refs to '%s'\n", st.URL, cs.Branch, st.URL)
		return 1
	}
	if len(cs.Unpushed) > 0 {
		st.Commits = append(st.Commits, cs.Unpushed...)
		st.Pushes++
		cs.Unpushed = nil
		if err := f.SetRemote(st); err != nil {
			fmt.Fprintln(stderr, err)
			return 128
		}
		if err := saveClone(dir, cs); err != nil {
			fmt.Fprintln(stderr, err)
			return 128
		}
	}
	return 0
}

func hostOf(url string) string {
	rest := url
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host, _, _ := strings.Cut(rest, "/")
	return host
}
