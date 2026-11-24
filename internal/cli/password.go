package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/cxyfer/notesteme/internal/app"
)

// parsePassword 解析 `notesteme password set`（AUTH-R2）。
func parsePassword(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("password 需要子命令：set。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "set":
		return parsePasswordSet(rest)
	default:
		return nil, usagef("不認得的子命令 password %s。", sub)
	}
}

// parsePasswordSet 重設登入密碼：和 secrets 一樣直接操作資料目錄（取得檔案鎖，不經由 API），所以 noBackend 為 true，
// 不能和 --server 一起使用，serve 執行中時結束碼為 1。不需要目前的密碼（有主機的存取權就能重設，忘記密碼時唯一的路）；
// 成功後撤銷所有 session。新密碼不接受命令列參數（會出現在 process list 與 shell history），
// 只能來自環境變數（--password-env）、stdin 的一行（--password-stdin）或終端機提示（輸入兩次、不回顯）。
func parsePasswordSet(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("password set", g)
	envName := fs.String("password-env", "", "存放新密碼的環境變數名稱")
	fromStdin := fs.Bool("password-stdin", false, "從 stdin 讀取新密碼（一行）")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("password set 不接受位置參數：%q（新密碼不能放在命令列）。", pos[0])
	}
	if *envName != "" && *fromStdin {
		return nil, usagef("--password-env 與 --password-stdin 只能擇一。")
	}
	name, stdinFlag := strings.TrimSpace(*envName), *fromStdin
	if fs.Changed("password-env") && name == "" {
		return nil, usagef("--password-env 需要環境變數名稱。")
	}
	return &plan{g: g, noBackend: true, run: func(ctx context.Context, s *session) (int, error) {
		if g.server != "" {
			return 0, usagef("password set 直接改資料目錄，不能和 --server 一起使用。")
		}
		pw, err := s.newPassword(name, stdinFlag)
		if err != nil {
			return 0, err
		}
		revoked, err := app.SetPassword(ctx, withData(s.env, g.data), pw)
		if err != nil {
			return 0, err
		}
		if g.json {
			return exitOK, s.printJSON(map[string]any{"changed": true, "revoked_sessions": revoked})
		}
		fmt.Fprintf(s.stdout, "已更新登入密碼；%d 個 session 已撤銷。\n", revoked)
		return exitOK, nil
	}}, nil
}

// readSecret 在終端機上讀一行不回顯的輸入；做成變數，測試才能模擬終端機。
var readSecret = func(r io.Reader) (string, error) {
	f, ok := r.(*os.File)
	if !ok || f == nil {
		return "", errors.New("stdin 不是終端機")
	}
	b, err := term.ReadPassword(int(f.Fd()))
	return string(b), err
}

// newPassword 取得新密碼：環境變數、stdin 的一行，或終端機上輸入兩次。密碼不會寫進任何輸出。
func (s *session) newPassword(envName string, fromStdin bool) (string, error) {
	switch {
	case envName != "":
		pw := s.env.Vars[envName]
		if pw == "" {
			return "", usagef("環境變數 %s 未設定或為空。", envName)
		}
		return pw, nil
	case fromStdin:
		if s.stdin == nil {
			return "", usagef("--password-stdin 需要從 stdin 讀取新密碼。")
		}
		line, err := bufio.NewReader(s.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("讀取 stdin 失敗：%w", err)
		}
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			return "", usagef("stdin 沒有新密碼（第一行是空的）。")
		}
		return pw, nil
	case s.stdin != nil && isTerminal(s.stdin):
		fmt.Fprint(s.stderr, "新密碼：")
		first, err := readSecret(s.stdin)
		fmt.Fprintln(s.stderr)
		if err != nil {
			return "", fmt.Errorf("讀取新密碼失敗：%w", err)
		}
		if first == "" {
			return "", usagef("新密碼不能是空的。")
		}
		fmt.Fprint(s.stderr, "再輸入一次：")
		second, err := readSecret(s.stdin)
		fmt.Fprintln(s.stderr)
		if err != nil {
			return "", fmt.Errorf("讀取新密碼失敗：%w", err)
		}
		if first != second {
			return "", usagef("兩次輸入的密碼不一致，沒有變更。")
		}
		return first, nil
	}
	return "", usagef("password set 需要 --password-env <變數名稱>、--password-stdin，或在終端機中輸入新密碼。")
}
