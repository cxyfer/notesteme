// Package fakecompose 在驗收測試中模擬 `docker compose -f <file> [--profile p] config --format json`（OPS-R9）。
//
// 驗收測試不能依賴真正的 docker（沒有 daemon 的 CI、也不該連外網），但場景要驗證的是
// deploy/docker/compose.yaml「真正的內容」：所以這裡讀真的檔案，只模擬 compose 把它展開成 JSON 的過程，
// 而不是回傳寫死的答案。它只實作場景需要的子集，遇到沒有支援的指令、旗標或 compose 功能一律明確失敗
// （ErrUnsupported），不會默默回傳看似正確的結果：
//
//   - 指令：只有 `compose [-f <file>] [--profile <p>]... config --format json`；
//   - 變數代入：`$VAR`、`${VAR}`、`${VAR:-d}`、`${VAR-d}`、`${VAR:?msg}`、`${VAR?msg}`、`${VAR:+a}`、`${VAR+a}`、`$$`；
//   - profiles：沒有 profiles 的服務一律啟用，有的話要和 --profile 其中之一相符（`*` 啟用全部）；
//   - ports：short syntax 正規化成 long form（mode、host_ip、target、published、protocol）；
//   - environment：list 形式轉成 map。
//
// 不支援的 compose 功能（include、extends、env_file、多個 -f、port 範圍）會回 ErrUnsupported。
// 其他欄位（build、volumes、restart…）原樣輸出，不像真的 compose 那樣再正規化（例如不會把 build.context 轉成絕對路徑）。
package fakecompose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnsupported 表示指令或 compose 功能超出這個模擬器的範圍：這是 harness 的限制，不是被測行為的結果。
var ErrUnsupported = errors.New("fakecompose 不支援")

// ExitError 是 compose 自己會失敗的情況（例如必要的變數沒有設定）：對應 docker 的非零結束碼與 stderr。
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func unsupported(format string, a ...any) error {
	return fmt.Errorf("%w：%s", ErrUnsupported, fmt.Sprintf(format, a...))
}

// Run 模擬 `docker compose <args>`：args 是 `docker compose` 之後的參數。
//   - root 是相對路徑 -f 的基準（repo 根目錄），讀取的檔案必須在 root 之內；
//   - env 是變數代入用的環境變數，不讀真實的程序環境，結果才不受執行測試的機器影響。
//
// 成功時回傳 stdout 的內容。
func Run(root string, args []string, env map[string]string) (string, error) {
	opts, err := parseArgs(args)
	if err != nil {
		return "", err
	}
	path, err := resolve(root, opts.file)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// 檔案不存在時 compose 也會失敗；用 ExitError 讓場景看到結束碼，而不是 harness 的錯誤。
		if errors.Is(err, os.ErrNotExist) {
			return "", &ExitError{Code: 1, Msg: "open " + opts.file + ": no such file or directory"}
		}
		return "", err
	}
	out, err := render(raw, opts.profiles, env)
	if err != nil {
		return "", err
	}
	return out, nil
}

type options struct {
	file     string
	profiles []string
}

// parseArgs 只接受 `[-f file] [--profile p]... config --format json`（旗標順序不拘，--format 可寫成 --format=json）。
func parseArgs(args []string) (options, error) {
	var o options
	if len(args) == 0 || args[0] != "compose" {
		return o, unsupported("只模擬 `docker compose config`，收到 %q", strings.Join(args, " "))
	}
	args = args[1:]
	var sub string
	format := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", unsupported("旗標 %s 缺少值", a)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "-f" || a == "--file":
			v, err := next()
			if err != nil {
				return o, err
			}
			if o.file != "" {
				return o, unsupported("不支援多個 -f（不會合併 compose 檔）")
			}
			o.file = v
		case a == "--profile":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.profiles = append(o.profiles, v)
		case a == "--format":
			v, err := next()
			if err != nil {
				return o, err
			}
			format = v
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "-"):
			return o, unsupported("不認得的旗標 %q", a)
		case sub == "":
			sub = a
		default:
			return o, unsupported("多餘的參數 %q", a)
		}
	}
	if sub != "config" {
		return o, unsupported("只支援子命令 config，收到 %q", sub)
	}
	if format != "json" {
		return o, unsupported("config 只支援 --format json，收到 %q", format)
	}
	if o.file == "" {
		return o, unsupported("必須用 -f 指定 compose 檔（不做自動搜尋）")
	}
	return o, nil
}

// resolve 把 -f 的路徑解到 root 之內；超出 root 的路徑不讀。
func resolve(root, file string) (string, error) {
	p := file
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(filepath.Clean(root), p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", unsupported("compose 檔 %q 不在 repo 之內", file)
	}
	return p, nil
}

// render 把 compose.yaml 的內容代入變數、依 profile 篩選服務，輸出與 `config --format json` 相同形狀的 JSON。
func render(raw []byte, profiles []string, env map[string]string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", &ExitError{Code: 1, Msg: "yaml: " + err.Error()}
	}
	if doc == nil {
		return "", &ExitError{Code: 1, Msg: "empty compose file"}
	}
	if _, ok := doc["include"]; ok {
		return "", unsupported("不支援 include")
	}
	v, err := interpolateValue(doc, "", env)
	if err != nil {
		return "", err
	}
	doc = v.(map[string]any)

	services, _ := doc["services"].(map[string]any)
	enabled := map[string]any{}
	for name, svc := range services {
		m, ok := svc.(map[string]any)
		if !ok {
			return "", &ExitError{Code: 1, Msg: fmt.Sprintf("services.%s must be a mapping", name)}
		}
		if !profileEnabled(m, profiles) {
			continue
		}
		norm, err := normalizeService(name, m)
		if err != nil {
			return "", err
		}
		enabled[name] = norm
	}
	doc["services"] = enabled
	// x- 開頭的擴充欄位 compose 不會輸出。
	for k := range doc {
		if strings.HasPrefix(k, "x-") {
			delete(doc, k)
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// profileEnabled 回報服務是否啟用：沒有 profiles 的服務一律啟用；有的話要與 active 其中之一相符。
func profileEnabled(svc map[string]any, active []string) bool {
	list, _ := svc["profiles"].([]any)
	if len(list) == 0 {
		return true
	}
	for _, a := range active {
		if a == "*" {
			return true
		}
		for _, p := range list {
			if s, _ := p.(string); s == a {
				return true
			}
		}
	}
	return false
}

// normalizeService 正規化會影響場景判斷的欄位：ports 與 environment。
func normalizeService(name string, svc map[string]any) (map[string]any, error) {
	for _, k := range []string{"extends", "env_file"} {
		if _, ok := svc[k]; ok {
			return nil, unsupported("services.%s.%s", name, k)
		}
	}
	if ports, ok := svc["ports"]; ok {
		list, ok := ports.([]any)
		if !ok {
			return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("services.%s.ports must be a list", name)}
		}
		out := make([]any, 0, len(list))
		for i, p := range list {
			norm, err := normalizePort(p)
			if err != nil {
				return nil, fmt.Errorf("services.%s.ports[%d]：%w", name, i, err)
			}
			out = append(out, norm)
		}
		svc["ports"] = out
	}
	if envv, ok := svc["environment"]; ok {
		if list, isList := envv.([]any); isList {
			m := map[string]any{}
			for _, e := range list {
				s, _ := e.(string)
				k, v, has := strings.Cut(s, "=")
				if has {
					m[k] = v
				} else {
					m[k] = nil
				}
			}
			svc["environment"] = m
		}
	}
	return svc, nil
}
