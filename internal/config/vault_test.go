package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func loadYAML(t *testing.T, content string) (*Config, error) {
	t.Helper()
	return Load(Sources{File: writeFile(t, content), Env: map[string]string{}})
}

func TestLoadVaultsAndHooks(t *testing.T) {
	cfg, err := loadYAML(t, `
vaults:
  main: { adapter: fs, path: /vault/ }
  remote: { adapter: git, remote: "https://git.example.com/me/notes.git", base: Notes }
  ssh-one: { adapter: git, remote: "git@github.com:me/notes.git", branch: notes/main, author: "Me <me@example.com>" }
hooks:
  notify: { argv: ["notesteme-test-hook", "{{note_path}}", "--title={{ title }}"], timeout: 2s, env_allowlist: [LANG] }
  plain: { argv: ["true"], max_output: 1KiB }
sponsorblock: { base_url: "https://sb.example.com/" }
llm:
  providers:
    gemini: { type: gemini }
`)
	if err != nil {
		t.Fatal(err)
	}
	if v := cfg.Vaults["main"]; v.Adapter != VaultAdapterFS || v.Path != "/vault" || v.Base != DefaultVaultBase {
		t.Errorf("main = %+v", v)
	}
	if v := cfg.Vaults["remote"]; v.Branch != DefaultVaultBranch || v.Author != DefaultVaultAuthor || v.Base != "Notes" {
		t.Errorf("remote = %+v", v)
	}
	if v := cfg.Vaults["ssh-one"]; v.Branch != "notes/main" || v.Author != "Me <me@example.com>" {
		t.Errorf("ssh-one = %+v", v)
	}
	if h := cfg.Hooks["notify"]; h.Timeout != 2*time.Second || h.MaxOutput != DefaultHookMaxOutput || len(h.EnvAllowlist) != 1 {
		t.Errorf("notify = %+v", h)
	}
	if h := cfg.Hooks["plain"]; h.Timeout != DefaultHookTimeout || h.MaxOutput != 1024 {
		t.Errorf("plain = %+v", h)
	}
	if cfg.SponsorBlockBaseURL != "https://sb.example.com" {
		t.Errorf("sponsorblock = %q", cfg.SponsorBlockBaseURL)
	}
	if cfg.LLMProviders["gemini"].Type != LLMTypeGemini {
		t.Errorf("gemini = %+v", cfg.LLMProviders["gemini"])
	}
}

func TestLoadM3Defaults(t *testing.T) {
	cfg, err := loadYAML(t, "listen: 127.0.0.1:1\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SponsorBlockBaseURL != DefaultSponsorBlockBase || cfg.Vaults != nil || cfg.Hooks != nil {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadRejectsBadM3Config(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"ext:: remote", `vaults: { remote: { adapter: git, remote: "ext::sh -c touch% /tmp/pwned" } }`, "vaults.remote.remote 只接受 https 或 ssh 網址。"},
		{"file remote", `vaults: { remote: { adapter: git, remote: "file:///data/config" } }`, "vaults.remote.remote 只接受 https 或 ssh 網址。"},
		{"本機路徑", `vaults: { remote: { adapter: git, remote: /srv/git/vault.git } }`, "vaults.remote.remote 只接受 https 或 ssh 網址。"},
		{"fd remote", `vaults: { r: { adapter: git, remote: "fd::3" } }`, "vaults.r.remote 只接受 https 或 ssh 網址。"},
		{"host::helper", `vaults: { r: { adapter: git, remote: "u@host::x" } }`, "vaults.r.remote 只接受 https 或 ssh 網址。"},
		{"帶密碼", `vaults: { r: { adapter: git, remote: "https://me:secret@git.example.com/x.git" } }`, "vaults.r.remote 只接受 https 或 ssh 網址。"},
		{"選項注入", `vaults: { r: { adapter: git, remote: "--upload-pack=touch" } }`, "vaults.r.remote 只接受 https 或 ssh 網址。"},
		{"沒有 remote", `vaults: { r: { adapter: git } }`, "vaults.r.remote 只接受 https 或 ssh 網址。"},
		{"分支以 - 開頭", `vaults: { r: { adapter: git, remote: "https://g.example/x.git", branch: "-x" } }`, "vaults.r.branch 不是有效的分支名稱"},
		{"作者格式", `vaults: { r: { adapter: git, remote: "https://g.example/x.git", author: "me" } }`, "vaults.r.author 必須是"},
		{"git 不能有 path", `vaults: { r: { adapter: git, remote: "https://g.example/x.git", path: /x } }`, "vaults.r.path 只用於 adapter: fs"},
		{"fs 相對路徑", `vaults: { main: { adapter: fs, path: vault } }`, "vaults.main.path 必須是絕對路徑。"},
		{"fs 不能有 remote", `vaults: { main: { adapter: fs, path: /v, remote: "https://g.example/x.git" } }`, "vaults.main.remote 只用於 adapter: git。"},
		{"沒有 adapter", `vaults: { main: { path: /v } }`, "vaults.main.adapter 必須指定"},
		{"adapter 不認得", `vaults: { main: { adapter: s3 } }`, "vaults.main.adapter 不認得"},
		{"未知欄位", `vaults: { main: { adapter: fs, path: /v, token: x } }`, "vaults.main 格式不正確"},
		{"base 含 ..", `vaults: { main: { adapter: fs, path: /v, base: "../x" } }`, "vaults.main.base 必須是 vault 內的相對資料夾"},
		{"vault id 不合法", `vaults: { Main: { adapter: fs, path: /v } }`, "vaults 有不合法的 id"},
		{"vault id 撞名", `vaults: { a-b: { adapter: fs, path: /v }, a_b: { adapter: fs, path: /w } }`, "NOTESTEME_VAULT_A_B_TOKEN"},
		{"master key", `hooks: { leak: { argv: [x], env_allowlist: [NOTESTEME_MASTER_KEY] } }`, "hooks.leak.env_allowlist 不能包含 NOTESTEME_MASTER_KEY。"},
		{"password", `hooks: { leak: { argv: [x], env_allowlist: [NOTESTEME_PASSWORD] } }`, "hooks.leak.env_allowlist 不能包含 NOTESTEME_PASSWORD。"},
		{"api key", `hooks: { leak: { argv: [x], env_allowlist: [NOTESTEME_GROQ_API_KEY] } }`, "hooks.leak.env_allowlist 不能包含 NOTESTEME_GROQ_API_KEY。"},
		{"token", `hooks: { leak: { argv: [x], env_allowlist: [GITHUB_TOKEN] } }`, "hooks.leak.env_allowlist 不能包含 GITHUB_TOKEN。"},
		{"變數名稱", `hooks: { h: { argv: [x], env_allowlist: ["A-B"] } }`, "不是有效的環境變數名稱"},
		{"argv 空", `hooks: { h: { argv: [] } }`, "hooks.h.argv 必須是字串陣列"},
		{"argv 是字串", `hooks: { h: { argv: "sh -c id" } }`, "hooks.h 格式不正確"},
		{"不認識的變數", `hooks: { h: { argv: [x, "{{secret}}"] } }`, "hooks.h.argv[1] 使用了不認識的變數 {{secret}}"},
		{"程式不能是變數", `hooks: { h: { argv: ["{{note_path}}"] } }`, "hooks.h.argv[0] 是要執行的程式"},
		{"逾時太長", `hooks: { h: { argv: [x], timeout: 6m } }`, "hooks.h.timeout 必須是大於 0、不超過 5m0s 的期間"},
		{"逾時格式", `hooks: { h: { argv: [x], timeout: 2d } }`, "hooks.h.timeout"},
		{"max_output", `hooks: { h: { argv: [x], max_output: 1GB } }`, "hooks.h.max_output"},
		{"hook 未知欄位", `hooks: { h: { argv: [x], shell: true } }`, "hooks.h 格式不正確"},
		{"sponsorblock 網址", `sponsorblock: { base_url: "ftp://x" }`, "sponsorblock.base_url 必須是 http 或 https"},
		{"sponsorblock 未知欄位", `sponsorblock: { categories: [sponsor] }`, "格式不正確"},
		{"llm type", "llm:\n  providers:\n    g: { type: vertex }\n", "可用 anthropic、openai、gemini"},
	}
	for _, c := range cases {
		_, err := loadYAML(t, c.yaml)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：err = %v，預期含 %q", c.name, err, c.want)
		}
	}
}

// steps.md 4.13：@M3 場景在閘門句型實作之前一律 pending，驗證不到設定檔。
// 這裡把 M3 .feature 中每個 `the config file contains:` 的內容載入一次：預期 server 啟動失敗的要失敗且訊息相符，其他要成功。
func TestLoadM3FeatureConfigs(t *testing.T) {
	files, err := filepath.Glob("../../features/*.feature")
	if err != nil {
		t.Fatal(err)
	}
	placeholder := regexp.MustCompile(`<([a-z_]+)>`)
	checked := 0
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.HasPrefix(base, "VAULT-") && !strings.HasPrefix(base, "SPB-") && !strings.HasPrefix(base, "GEM-") &&
			!strings.HasPrefix(base, "NOTE-") && !strings.HasPrefix(base, "FRM-") && !strings.HasPrefix(base, "CHAP-") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		examples := outlineExamples(lines)
		for i := 0; i+1 < len(lines); i++ {
			if !strings.HasSuffix(strings.TrimSpace(lines[i]), "the config file contains:") {
				continue
			}
			indent := strings.Index(lines[i+1], `"""`)
			var body []string
			j := i + 2
			for ; j < len(lines) && strings.TrimSpace(lines[j]) != `"""`; j++ {
				l := lines[j]
				if len(l) >= indent {
					l = l[indent:]
				}
				body = append(body, l)
			}
			var want string
			for k := j + 1; k < len(lines) && k < j+4; k++ {
				if m := regexp.MustCompile(`the server fails to start with "(.*)"$`).FindStringSubmatch(strings.TrimSpace(lines[k])); m != nil {
					want = m[1]
				}
			}
			doc := strings.Join(body, "\n")
			// Scenario Outline：每一列 Examples 各代入一次；沒有佔位符時只跑一次。
			rows := []map[string]string{nil}
			if placeholder.MatchString(doc) {
				rows = examples[i]
			}
			for _, row := range rows {
				d := placeholder.ReplaceAllStringFunc(doc, func(m string) string { return row[m[1:len(m)-1]] })
				// 場景的虛擬路徑在 harness 中會換成場景目錄下的絕對路徑；這裡維持原樣（已經是絕對路徑）。
				_, err := loadYAML(t, d)
				checked++
				switch {
				case want == "" && err != nil:
					t.Errorf("%s 第 %d 行：設定檔應該可以載入：%v\n%s", base, i+1, err, d)
				case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
					t.Errorf("%s 第 %d 行：預期失敗並含 %q，實際 %v\n%s", base, i+1, want, err, d)
				}
			}
		}
	}
	if checked < 5 {
		t.Errorf("只檢查了 %d 份設定檔，解析可能有誤", checked)
	}
	t.Logf("檢查了 %d 份 M3 的設定檔", checked)
}

// outlineExamples 把每一行（步驟所在的行號）對到它所屬 Scenario Outline 的 Examples 列。
func outlineExamples(lines []string) map[int][]map[string]string {
	out := map[int][]map[string]string{}
	start := -1
	for i := 0; i < len(lines); i++ {
		tl := strings.TrimSpace(lines[i])
		if strings.HasPrefix(tl, "Scenario") || strings.HasPrefix(tl, "Background") {
			start = i
		}
		if !strings.HasPrefix(tl, "Examples:") || start < 0 {
			continue
		}
		var header []string
		var rows []map[string]string
		for j := i + 1; j < len(lines); j++ {
			r := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(r, "|") {
				if r == "" && header == nil {
					continue
				}
				break
			}
			cells := strings.Split(strings.Trim(r, "|"), "|")
			for k := range cells {
				cells[k] = strings.TrimSpace(cells[k])
			}
			if header == nil {
				header = cells
				continue
			}
			row := map[string]string{}
			for k, h := range header {
				if k < len(cells) {
					row[h] = cells[k]
				}
			}
			rows = append(rows, row)
		}
		for k := start; k < i; k++ {
			out[k] = rows
		}
	}
	return out
}
