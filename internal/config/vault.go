package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/vault"
	"gopkg.in/yaml.v3"
)

// M3 的設定檔區段（ADR-0020 第 6 點、VAULT-R1、R8、R10、R11、SPB-R2）：vault、hook 與 SponsorBlock 的
// base_url 只能寫在設定檔，因為它們等於寫檔權限、會執行程式或決定連線的主機；API 只能引用 id。
// M3-S0 只解析與驗證，寫入、git 與 hook 的執行由 VAULT 的切片實作。

// vault 的 adapter（VAULT-R1）。
const (
	VaultAdapterFS  = "fs"
	VaultAdapterGit = "git"
)

// vault 與 hook 的預設值（VAULT-R1、R11、SPB-R2）。
const (
	DefaultVaultBranch      = "main"
	DefaultVaultAuthor      = "Notesteme <notesteme@localhost>"
	DefaultVaultBase        = "Video Notes"
	DefaultHookTimeout      = 30 * time.Second
	MaxHookTimeout          = 5 * time.Minute
	DefaultHookMaxOutput    = 64 << 10
	MaxHookMaxOutput        = 16 << 20
	DefaultSponsorBlockBase = "https://sponsor.ajay.app"
)

// Vault 是設定檔 vaults.<id>（VAULT-R1）。
type Vault struct {
	ID      string
	Adapter string // fs 或 git
	Path    string // fs：絕對路徑
	Remote  string // git：https、ssh 或 user@host:path
	Branch  string // git：預設 main
	Author  string // git：預設 Notesteme <notesteme@localhost>
	Base    string // vault 內的根資料夾，預設 Video Notes
}

// Hook 是設定檔 hooks.<id>（VAULT-R10、R11）。
type Hook struct {
	ID           string
	Argv         []string
	Timeout      time.Duration
	MaxOutput    int
	EnvAllowlist []string
}

type vaultSchema struct {
	Adapter string `yaml:"adapter"`
	Path    string `yaml:"path"`
	Remote  string `yaml:"remote"`
	Branch  string `yaml:"branch"`
	Author  string `yaml:"author"`
	Base    string `yaml:"base"`
}

type hookSchema struct {
	Argv         []string  `yaml:"argv"`
	Timeout      string    `yaml:"timeout"`
	MaxOutput    yaml.Node `yaml:"max_output"`
	EnvAllowlist []string  `yaml:"env_allowlist"`
}

// sponsorBlockSchema 是設定檔 sponsorblock 區段（SPB-R2）。
type sponsorBlockSchema struct {
	BaseURL string `yaml:"base_url"`
}

var (
	// configID 是 vault、hook id 允許的字元：vault id 會變成 NOTESTEME_VAULT_<ID>_TOKEN 的一部分（VAULT-R8）。
	configID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// scpRemote 是 git 的 scp 形式 user@host:path（不能是 host::helper 這類 remote helper）。
	scpRemote = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^:]`)
	// gitBranch 是保守的分支名稱：不能以 - 開頭（會被當成選項），不含空白、控制字元與 ..。
	gitBranch = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)
	// gitAuthor 是 git 的 "名稱 <email>"。
	gitAuthor = regexp.MustCompile(`^[^<>\n\r\x00]+ <[^<>\s]+>$`)
	// envName 是環境變數名稱。
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// sizeValue 是 max_output 的大小字串（例如 64KiB、1MiB、512）。
	sizeValue = regexp.MustCompile(`^([0-9]+)\s*(B|KiB|MiB)?$`)
)

// HookVariables 是 hook argv 可以使用的變數（VAULT-R10）。
var HookVariables = []string{"note_path", "vault_root", "item_id", "title", "commit"}

// sortedKeys 依字母順序回傳 map 的 key（錯誤訊息要穩定）。
func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseVaults 解析設定檔的 vaults 區段（VAULT-R1、R8）。
func parseVaults(node *yaml.Node, file string) (map[string]Vault, error) {
	if node.IsZero() {
		return nil, nil
	}
	var raw map[string]yaml.Node
	if err := node.Decode(&raw); err != nil {
		return nil, fmt.Errorf("設定檔 %s 的欄位 vaults 格式不正確：%w", file, err)
	}
	out := make(map[string]Vault, len(raw))
	byEnvKey := map[string]string{}
	for _, id := range sortedKeys(raw) {
		field := "vaults." + id
		bad := func(format string, a ...any) error {
			return fmt.Errorf("設定檔 %s：%s", file, fmt.Sprintf(format, a...))
		}
		if !configID.MatchString(id) {
			return nil, bad("vaults 有不合法的 id %q：只能用小寫英數字、底線與連字號，並以英數字開頭。", id)
		}
		envKey := strings.ReplaceAll(id, "-", "_")
		if other, ok := byEnvKey[envKey]; ok {
			return nil, bad("vaults 的 id %q 與 %q 會使用同一個憑證環境變數 NOTESTEME_VAULT_%s_TOKEN，請改用不同的 id。", other, id, strings.ToUpper(envKey))
		}
		byEnvKey[envKey] = id
		n := raw[id]
		var s vaultSchema
		if err := decodeKnownYAML(&n, &s); err != nil {
			return nil, bad("%s 格式不正確：%v", field, err)
		}
		v := Vault{ID: id, Adapter: strings.TrimSpace(s.Adapter), Path: s.Path, Remote: strings.TrimSpace(s.Remote),
			Branch: s.Branch, Author: s.Author, Base: s.Base}
		switch v.Adapter {
		case VaultAdapterFS:
			if v.Path == "" || !path.IsAbs(v.Path) {
				return nil, bad("%s.path 必須是絕對路徑。", field)
			}
			v.Path = path.Clean(v.Path)
			for _, f := range []struct{ name, val string }{{"remote", s.Remote}, {"branch", s.Branch}, {"author", s.Author}} {
				if f.val != "" {
					return nil, bad("%s.%s 只用於 adapter: git。", field, f.name)
				}
			}
		case VaultAdapterGit:
			if v.Path != "" {
				return nil, bad("%s.path 只用於 adapter: fs；git 的 clone 放在資料目錄的 vault-repo/%s。", field, id)
			}
			if !validGitRemote(v.Remote) {
				return nil, bad("%s.remote 只接受 https 或 ssh 網址。", field)
			}
			if v.Branch == "" {
				v.Branch = DefaultVaultBranch
			}
			if !gitBranch.MatchString(v.Branch) || strings.Contains(v.Branch, "..") || strings.HasSuffix(v.Branch, ".lock") {
				return nil, bad("%s.branch 不是有效的分支名稱：%q", field, v.Branch)
			}
			if v.Author == "" {
				v.Author = DefaultVaultAuthor
			}
			if !gitAuthor.MatchString(v.Author) {
				return nil, bad("%s.author 必須是「名稱 <email>」的形式：%q", field, v.Author)
			}
		case "":
			return nil, bad("%s.adapter 必須指定（%s 或 %s）。", field, VaultAdapterFS, VaultAdapterGit)
		default:
			return nil, bad("%s.adapter 不認得：%q（可用 %s、%s）。", field, v.Adapter, VaultAdapterFS, VaultAdapterGit)
		}
		if v.Base == "" {
			v.Base = DefaultVaultBase
		}
		if !validRelativeFolder(v.Base) {
			return nil, bad("%s.base 必須是 vault 內的相對資料夾，不能是絕對路徑或包含 ..。", field)
		}
		out[id] = v
	}
	return out, nil
}

// validGitRemote 只接受 https://、ssh:// 與 user@host:path（VAULT-R8）。ext::、file://、fd::、本機路徑、
// 帶密碼的網址（憑證只能經 GIT_ASKPASS 交給 git）與以 - 開頭的值（會被當成選項）一律拒絕。
func validGitRemote(remote string) bool {
	if remote == "" || strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, " \t\r\n\x00") {
		return false
	}
	if strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" || strings.HasPrefix(u.Host, "-") {
			return false
		}
		if _, hasPassword := u.User.Password(); hasPassword || (u.Scheme == "https" && u.User != nil) {
			return false
		}
		return true
	}
	return scpRemote.MatchString(remote) && !strings.Contains(remote, "::")
}

// validRelativeFolder 回報 s 是不是不含 .. 的相對資料夾。
func validRelativeFolder(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || (len(s) >= 2 && s[1] == ':') {
		return false
	}
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return false
		}
	}
	return true
}

// parseHooks 解析設定檔的 hooks 區段（VAULT-R10、R11）。
func parseHooks(node *yaml.Node, file string) (map[string]Hook, error) {
	if node.IsZero() {
		return nil, nil
	}
	var raw map[string]yaml.Node
	if err := node.Decode(&raw); err != nil {
		return nil, fmt.Errorf("設定檔 %s 的欄位 hooks 格式不正確：%w", file, err)
	}
	out := make(map[string]Hook, len(raw))
	for _, id := range sortedKeys(raw) {
		field := "hooks." + id
		bad := func(format string, a ...any) error {
			return fmt.Errorf("設定檔 %s：%s", file, fmt.Sprintf(format, a...))
		}
		if !configID.MatchString(id) {
			return nil, bad("hooks 有不合法的 id %q：只能用小寫英數字、底線與連字號，並以英數字開頭。", id)
		}
		n := raw[id]
		var s hookSchema
		if err := decodeKnownYAML(&n, &s); err != nil {
			return nil, bad("%s 格式不正確：%v", field, err)
		}
		h := Hook{ID: id, Argv: s.Argv, Timeout: DefaultHookTimeout, MaxOutput: DefaultHookMaxOutput, EnvAllowlist: s.EnvAllowlist}
		if len(h.Argv) == 0 || strings.TrimSpace(h.Argv[0]) == "" {
			return nil, bad("%s.argv 必須是字串陣列，第一個元素是要執行的程式。", field)
		}
		for i, a := range h.Argv {
			for _, m := range vault.HookVar.FindAllStringSubmatch(a, -1) {
				name := m[1]
				if !containsString(HookVariables, name) {
					return nil, bad("%s.argv[%d] 使用了不認識的變數 {{%s}}（可用 %s）。", field, i, name, strings.Join(HookVariables, "、"))
				}
			}
			if i == 0 && vault.HookVar.MatchString(a) {
				return nil, bad("%s.argv[0] 是要執行的程式，不能使用變數。", field)
			}
		}
		if s.Timeout != "" {
			d, err := time.ParseDuration(s.Timeout)
			if err != nil || d <= 0 || d > MaxHookTimeout {
				return nil, bad("%s.timeout 必須是大於 0、不超過 %s 的期間（例如 30s）：%q", field, MaxHookTimeout, s.Timeout)
			}
			h.Timeout = d
		}
		if s.MaxOutput.Kind != 0 {
			size, ok := parseSize(s.MaxOutput.Value)
			if !ok || s.MaxOutput.Kind != yaml.ScalarNode || size <= 0 || size > MaxHookMaxOutput {
				return nil, bad("%s.max_output 必須是 1 到 %d 位元組的大小（例如 64KiB）。", field, MaxHookMaxOutput)
			}
			h.MaxOutput = size
		}
		for _, name := range h.EnvAllowlist {
			if !envName.MatchString(name) {
				return nil, bad("%s.env_allowlist 的 %q 不是有效的環境變數名稱。", field, name)
			}
			if vault.ForbiddenHookEnv(name) {
				return nil, bad("%s.env_allowlist 不能包含 %s。", field, name)
			}
		}
		out[id] = h
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parseSize 解析 512、64KiB、1MiB。
func parseSize(s string) (int, bool) {
	m := sizeValue.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	switch m[2] {
	case "KiB":
		n <<= 10
	case "MiB":
		n <<= 20
	}
	return n, n > 0
}

// parseSponsorBlock 解析設定檔的 sponsorblock 區段（SPB-R2）；沒有設定時用公開伺服器。
func parseSponsorBlock(s sponsorBlockSchema, file string) (string, error) {
	base := strings.TrimSpace(s.BaseURL)
	if base == "" {
		return DefaultSponsorBlockBase, nil
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("設定檔 %s 的欄位 sponsorblock.base_url 必須是 http 或 https 的網址（不含帳號密碼與查詢字串）：%q", file, base)
	}
	return strings.TrimRight(base, "/"), nil
}
