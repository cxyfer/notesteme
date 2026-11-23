package vault

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// git adapter（VAULT-R7～R9）：經 toolexec 執行 git CLI（ADR-0020 第 6 點）。每次呼叫都帶上關掉 hook、
// credential helper 與危險傳輸的設定；環境變數只有 git 需要的幾個；憑證只經 GIT_ASKPASS 的環境變數交給 git，
// 不出現在 argv、remote URL、log 與錯誤訊息中。

// SafeConfig 是每次執行 git 都帶上的 -c 設定（VAULT-R8）。
var SafeConfig = []string{
	"core.hooksPath=/dev/null",
	"credential.helper=",
	"protocol.allow=never",
	"protocol.https.allow=always",
	"protocol.ssh.allow=always",
	"core.fsmonitor=false",
}

// GitTimeout 是一次 git 呼叫的上限。
const GitTimeout = 5 * time.Minute

// tokenEnv 是交給 askpass 腳本的憑證變數（只在 git 的環境中）。
const tokenEnv = "NOTESTEME_GIT_TOKEN"

// askpassScript 回答 git 的帳號與密碼提示：帳號固定是 x-access-token（GitHub、GitLab、Gitea 的 token 都接受），
// 密碼是環境變數中的 token。腳本本身不含 token。
const askpassScript = "#!/bin/sh\ncase \"$1\" in\n  Username*) printf '%s\\n' x-access-token ;;\n  *) printf '%s\\n' \"$" + tokenEnv + "\" ;;\nesac\n"

// ErrorKind 是 git 失敗的分類（VAULT-R9）。
type ErrorKind int

const (
	// KindOther 是其他失敗（permanent）。
	KindOther ErrorKind = iota
	// KindNetwork 是連線失敗、逾時、遠端 5xx（retryable）。
	KindNetwork
	// KindRejected 是 non-fast-forward 或 pull --ff-only 無法快轉（needs_action）。
	KindRejected
	// KindAuth 是認證失敗（needs_action）。
	KindAuth
)

// GitError 是一次 git 呼叫的失敗；Detail 是 stderr 的摘要（不含憑證）。
type GitError struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *GitError) Error() string { return fmt.Sprintf("git %s 失敗：%s", e.Op, e.Detail) }

// classify 依 git 的 stderr 分類（VAULT-R9）。
func classify(stderr string) ErrorKind {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "authentication failed"), strings.Contains(s, "could not read username"),
		strings.Contains(s, "could not read password"), strings.Contains(s, "permission denied (publickey"),
		strings.Contains(s, "terminal prompts disabled"), strings.Contains(s, "returned error: 401"),
		strings.Contains(s, "returned error: 403"), strings.Contains(s, "invalid username or password"):
		return KindAuth
	case strings.Contains(s, "non-fast-forward"), strings.Contains(s, "not possible to fast-forward"),
		strings.Contains(s, "[rejected]"), strings.Contains(s, "fetch first"), strings.Contains(s, "diverging branches"),
		strings.Contains(s, "would be overwritten by merge"):
		return KindRejected
	case strings.Contains(s, "could not resolve host"), strings.Contains(s, "unable to access"),
		strings.Contains(s, "connection timed out"), strings.Contains(s, "connection refused"),
		strings.Contains(s, "connection reset"), strings.Contains(s, "operation timed out"),
		strings.Contains(s, "early eof"), strings.Contains(s, "the remote end hung up"),
		strings.Contains(s, "returned error: 5"), strings.Contains(s, "could not read from remote repository"),
		strings.Contains(s, "network is unreachable"):
		return KindNetwork
	}
	return KindOther
}

// Git 在一個 clone 中執行 git。
type Git struct {
	Runner toolexec.Runner
	// Path 是 git 的絕對路徑。
	Path string
	// Vars 是 notesteme 的環境變數（取 PATH、SSH_AUTH_SOCK）。
	Vars map[string]string
	// Home 是這次任務的暫存目錄（git 的 HOME 與 askpass 腳本的位置）。
	Home string
	// Token 是 https remote 的憑證；空字串代表沒有。
	Token string
}

// env 組出 git 的環境變數（VAULT-R8）。
func (g *Git) env() ([]string, error) {
	env := []string{
		"PATH=" + g.Vars["PATH"],
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"HOME=" + g.Home,
	}
	if sock := g.Vars["SSH_AUTH_SOCK"]; sock != "" {
		env = append(env, "SSH_AUTH_SOCK="+sock)
	}
	if g.Token != "" {
		script := filepath.Join(g.Home, "askpass.sh")
		if err := os.WriteFile(script, []byte(askpassScript), 0o700); err != nil {
			return nil, err
		}
		env = append(env, "GIT_ASKPASS="+script, tokenEnv+"="+g.Token)
	}
	return env, nil
}

// run 執行一次 git；extra 是額外的 -c 設定。非 0 結束碼回傳 *GitError。
func (g *Git) run(ctx context.Context, dir, op string, extra []string, args ...string) (string, error) {
	env, err := g.env()
	if err != nil {
		return "", err
	}
	argv := make([]string, 0, 2*len(SafeConfig)+2*len(extra)+len(args))
	for _, c := range append(append([]string{}, SafeConfig...), extra...) {
		argv = append(argv, "-c", c)
	}
	argv = append(argv, args...)
	res, err := g.Runner.Run(ctx, toolexec.Cmd{Path: g.Path, Args: argv, Dir: dir, Timeout: GitTimeout, Env: env})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", &GitError{Kind: KindNetwork, Op: op, Detail: "逾時"}
		}
		return "", fmt.Errorf("git %s：%w", op, err)
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(string(res.Stderr))
		if detail == "" {
			detail = strings.TrimSpace(string(res.Stdout))
		}
		if g.Token != "" {
			detail = strings.ReplaceAll(detail, g.Token, "***")
		}
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return "", &GitError{Kind: classify(detail), Op: op, Detail: detail}
	}
	return string(res.Stdout), nil
}

// Sync 準備 clone（VAULT-R7）：還沒有 clone 時 `git clone --branch <branch> --single-branch`；
// 已有時 `git pull --ff-only`。dir 存在但不是 git repo（而且不是空的）時回傳錯誤，不覆蓋。
func (g *Git) Sync(ctx context.Context, def Def, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		// 遠端分支還不存在（空的 repo，第一次 push 還沒成功）時沒有東西可以 pull。
		_, err := g.run(ctx, dir, "rev-parse", nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+def.Branch)
		var ge *GitError
		if errors.As(err, &ge) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = g.run(ctx, dir, "pull", nil, "pull", "--ff-only", "--quiet", "origin", def.Branch)
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s 已經存在而且不是 git repo", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := g.run(ctx, filepath.Dir(dir), "clone", nil, "clone", "--branch", def.Branch, "--single-branch", "--quiet", "--", def.Remote, filepath.Base(dir))
	return err
}

// Commit 只 add 指定的檔案（VAULT-R7，其他未提交的變更不加入），以 author 建立一個 commit，回傳 commit id。
// paths 是空的時不做任何事。
func (g *Git) Commit(ctx context.Context, dir string, paths []string, message, author string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	if _, err := g.run(ctx, dir, "add", nil, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", err
	}
	// committer 也用 author：GIT_CONFIG_GLOBAL=/dev/null 時沒有 user.name、user.email，git 會拒絕 commit。
	name, email := splitAuthor(author)
	ident := []string{"user.name=" + name, "user.email=" + email}
	if _, err := g.run(ctx, dir, "commit", ident, "commit", "--quiet", "--no-verify", "-m", oneLine(message), "--author="+author); err != nil {
		return "", err
	}
	return g.Head(ctx, dir)
}

// Head 回傳 HEAD 的 commit id；還沒有任何 commit 時回傳空字串。
func (g *Git) Head(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", nil, "rev-parse", "--verify", "--quiet", "HEAD")
	var ge *GitError
	if errors.As(err, &ge) {
		return "", nil
	}
	return strings.TrimSpace(out), err
}

// Ahead 回報本機的分支有沒有還沒推送的 commit（比較 HEAD 與 refs/remotes/origin/<branch>）。
// 遠端追蹤分支不存在（空的 repo）而本機有 commit 時也是 true。
func (g *Git) Ahead(ctx context.Context, dir, branch string) (bool, error) {
	head, err := g.Head(ctx, dir)
	if err != nil || head == "" {
		return false, err
	}
	out, err := g.run(ctx, dir, "rev-parse", nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	var ge *GitError
	if errors.As(err, &ge) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != head, nil
}

// Push 推送 HEAD 到遠端的分支（VAULT-R7）。
func (g *Git) Push(ctx context.Context, dir, branch string) error {
	_, err := g.run(ctx, dir, "push", nil, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
}

// splitAuthor 把「名稱 <email>」拆開（設定檔載入時已驗證格式）；解析不了時用預設的身分。
func splitAuthor(author string) (string, string) {
	a, err := mail.ParseAddress(author)
	if err != nil || a.Name == "" {
		return "Notesteme", "notesteme@localhost"
	}
	return a.Name, a.Address
}

// oneLine 把 commit 訊息壓成一行（標題是不可信的輸入），去掉控制字元。
func oneLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// TokenEnvVar 是 vault 憑證的環境變數名稱（VAULT 介面）：NOTESTEME_VAULT_<ID>_TOKEN，ID 大寫、- 換成 _。
func TokenEnvVar(id string) string {
	return "NOTESTEME_VAULT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_TOKEN"
}
