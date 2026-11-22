package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/configio"
)

func TestConfigUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":         {"config"},
		"旗標當子命令":        {"config", "--json"},
		"不認得的子命令":       {"config", "bogus"},
		"export 多餘位置參數": {"config", "export", "x.yaml"},
		"export 不認得的旗標": {"config", "export", "--nope"},
		"import 缺少檔案":   {"config", "import"},
		"import 空白檔名":   {"config", "import", "  "},
		"import 太多檔案":   {"config", "import", "a.yaml", "b.yaml"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2 且沒有模式（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const cliSettings = `default_profile: standard
profiles:
  fast:
    timing:
      split_timing: free
  standard:
    language:
      targets:
        - zh-Hant-TW
`

// 獨立模式走完整條路：匯入 → 匯出（stdout 與 --out、--output 內容相同）→ 往返 → 再匯入全部未變更。
func TestConfigImportExportStandalone(t *testing.T) {
	env, _ := testEnv(t)
	in := writeYAML(t, cliSettings)

	res, out, errOut := run(t, env, "config", "import", in)
	if res.Code != 0 || res.Mode != ModeStandalone {
		t.Fatalf("import：%+v，stderr：%s", res, errOut)
	}
	for _, want := range []string{"建立 2、更新 0、未變更 0", "建立：fast、standard", "預設處理方案：已設為 standard"} {
		if !strings.Contains(out, want) {
			t.Errorf("import 輸出沒有 %q：\n%s", want, out)
		}
	}

	res, exported, errOut := run(t, env, "config", "export")
	if res.Code != 0 || errOut != "" {
		t.Fatalf("export：%+v，stderr：%s", res, errOut)
	}
	// 輸出的是穩定的 YAML：鍵排序固定、可以直接放進版本控制。
	if exported != cliSettings {
		t.Errorf("export 內容：\n%s\n預期：\n%s", exported, cliSettings)
	}
	if _, errs := configio.Parse([]byte(exported)); errs != nil {
		t.Errorf("export 的輸出必須能被 import 讀回：%+v", errs)
	}

	// --out 與 --output 寫檔：內容與 stdout 相同，stdout 只有摘要。
	for _, flag := range []string{"--out", "--output"} {
		path := filepath.Join(t.TempDir(), "out.yaml")
		res, out, errOut = run(t, env, "config", "export", flag, path)
		if res.Code != 0 || !strings.Contains(out, "已匯出 2 個處理方案到 "+path) || strings.Contains(out, "profiles:") {
			t.Fatalf("%s：%+v，stdout：%s，stderr：%s", flag, res, out, errOut)
		}
		if got, _ := os.ReadFile(path); string(got) != exported {
			t.Errorf("%s 檔案內容與 stdout 不同：\n%s", flag, got)
		}
	}

	// 匯出的檔案原樣匯入：全部未變更。
	res, out, _ = run(t, env, "config", "import", writeYAML(t, exported))
	if res.Code != 0 || !strings.Contains(out, "建立 0、更新 0、未變更 2") || !strings.Contains(out, "（沒有變更）") {
		t.Errorf("往返匯入：%+v\n%s", res, out)
	}

	// --json 輸出和 API 的回應一致。
	res, out, _ = run(t, env, "config", "export", "--json")
	var body struct {
		Content        string   `json:"content"`
		Profiles       []string `json:"profiles"`
		DefaultProfile string   `json:"default_profile"`
	}
	if res.Code != 0 || json.Unmarshal([]byte(out), &body) != nil || body.Content != exported || len(body.Profiles) != 2 || body.DefaultProfile != "standard" {
		t.Errorf("export --json：%+v\n%s", res, out)
	}
	res, out, _ = run(t, env, "config", "import", in, "--json")
	var imp struct {
		Unchanged []string `json:"unchanged"`
	}
	if res.Code != 0 || json.Unmarshal([]byte(out), &imp) != nil || len(imp.Unchanged) != 2 {
		t.Errorf("import --json：%+v\n%s", res, out)
	}
}

// 內容有錯：結束碼是 1（不是 CLI-R6 對 422 的 2）、ErrorCode 是 config_invalid，stderr 列出每個欄位路徑，
// stdout 沒有東西，而且整份都沒有套用。
func TestConfigImportInvalidChangesNothing(t *testing.T) {
	env, _ := testEnv(t)
	if res, _, errOut := run(t, env, "config", "import", writeYAML(t, "profiles:\n  standard: { language: { targets: [zh-Hant-TW] } }\n")); res.Code != 0 {
		t.Fatalf("準備資料失敗：%+v %s", res, errOut)
	}
	_, before, _ := run(t, env, "config", "export")

	bad := writeYAML(t, `
extra: 1
profiles:
  standard: { language: { targets: [en] } }
  broken: { language: { targets: "not-a-list" } }
  default: {}
`)
	res, out, errOut := run(t, env, "config", "import", bad)
	if res.Code != 1 || res.ErrorCode != "config_invalid" || out != "" {
		t.Fatalf("結果 = %+v，stdout：%q，stderr：%s", res, out, errOut)
	}
	for _, want := range []string{"沒有套用任何設定", "extra：", "profiles.broken.language.targets：", "profiles.default：", "內建"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr 沒有 %q：\n%s", want, errOut)
		}
	}
	if _, after, _ := run(t, env, "config", "export"); after != before {
		t.Errorf("匯入失敗改變了設定：\n%s\n---\n%s", before, after)
	}
}

// 檔案問題在送出之前就失敗（結束碼 1）：不存在、是資料夾、超過大小上限（--server 模式沒有任何請求）。
func TestConfigImportFileProblems(t *testing.T) {
	var big strings.Builder
	big.WriteString("profiles: {}\n")
	big.WriteString(strings.Repeat("#", configio.MaxBytes))
	cases := map[string]struct{ path, want string }{
		"不存在":  {filepath.Join(t.TempDir(), "nope.yaml"), "無法讀取"},
		"資料夾":  {t.TempDir(), "資料夾"},
		"超過上限": {writeYAML(t, big.String()), "太大"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{}`) })
			env := app.Env{Vars: map[string]string{}}
			res, _, errOut := run(t, env, "config", "import", c.path, "--server", url, "--token", "tk")
			if res.Code != 1 || !strings.Contains(errOut, c.want) {
				t.Errorf("結果 = %+v，stderr：%s", res, errOut)
			}
			if n := api.count(""); n != 0 {
				t.Errorf("檔案有問題時不該送出任何請求，送了 %d 個", n)
			}
		})
	}
}

// --server 模式：帶 bearer token、本體是 {"content": 檔案內容}；server 的各種回應對應到結束碼。
func TestConfigImportRemote(t *testing.T) {
	content := "profiles: {x: {}}\n"
	in := writeYAML(t, content)
	respond := func(status int, body string) (*fakeAPI, string) {
		return newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, status, body) })
	}

	api, url := respond(200, `{"created":["x"],"updated":[],"unchanged":[],"default_changed":false}`)
	res, out, errOut := run(t, app.Env{Vars: map[string]string{}}, "config", "import", in, "--server", url, "--token", "tk")
	if res.Code != 0 || res.Mode != ModeClient || !strings.Contains(out, "建立 1、更新 0、未變更 0") {
		t.Fatalf("成功：%+v\n%s\n%s", res, out, errOut)
	}
	if api.requests[0] != "POST /api/config/import" || api.auth[0] != "Bearer tk" {
		t.Errorf("請求 = %v，auth = %v", api.requests, api.auth)
	}
	var sent struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(api.bodies[0]), &sent); err != nil || sent.Content != content {
		t.Errorf("本體 = %q（%v）", api.bodies[0], err)
	}

	for name, c := range map[string]struct {
		status   int
		body     string
		wantCode int
		wantErr  string
		wantAPI  string
	}{
		"內容不正確": {422, `{"code":"config_invalid","message":"匯入內容有 1 個錯誤，沒有套用任何設定。","errors":[{"path":"profiles.x.timing","message":"必須是物件。"}]}`, 1, "profiles.x.timing：必須是物件。", "config_invalid"},
		"缺少權限":  {403, `{"code":"forbidden","message":"這個 token 缺少 admin 權限。","required_scope":"admin"}`, 1, "admin", "forbidden"},
		// 其他 422（不是 config_invalid）維持 CLI-R6：API 的 400、422 是 2。
		"其他 422": {422, `{"code":"invalid_request","message":"請求內容不正確。"}`, 2, "請求內容不正確", "invalid_request"},
		"伺服器錯誤":  {500, `{"code":"internal_error","message":"伺服器發生錯誤。"}`, 1, "伺服器發生錯誤", "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			_, url := respond(c.status, c.body)
			res, out, errOut := run(t, app.Env{Vars: map[string]string{}}, "config", "import", in, "--server", url, "--token", "tk")
			if res.Code != c.wantCode || res.ErrorCode != c.wantAPI || out != "" || !strings.Contains(errOut, c.wantErr) {
				t.Errorf("結果 = %+v，stdout：%q，stderr：%s", res, out, errOut)
			}
		})
	}
}

// --server 模式的 export：GET /api/config/export，stdout 只有 YAML。
func TestConfigExportRemote(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"content":"profiles: {}\n","profiles":[]}`)
	})
	res, out, errOut := run(t, app.Env{Vars: map[string]string{}}, "config", "export", "--server", url, "--token", "tk")
	if res.Code != 0 || out != "profiles: {}\n" || errOut != "" {
		t.Fatalf("結果 = %+v，stdout：%q，stderr：%s", res, out, errOut)
	}
	if api.requests[0] != "GET /api/config/export" || api.auth[0] != "Bearer tk" {
		t.Errorf("請求 = %v，auth = %v", api.requests, api.auth)
	}
	// 沒有權限：1。
	_, url = newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, `{"code":"forbidden","message":"缺少 read 權限。","required_scope":"read"}`)
	})
	if res, _, errOut := run(t, app.Env{Vars: map[string]string{}}, "config", "export", "--server", url, "--token", "tk"); res.Code != 1 || !strings.Contains(errOut, "read") {
		t.Errorf("403：%+v %s", res, errOut)
	}
}

// 檔案中的鍵是不可信的輸入：錯誤訊息到終端機之前要跳脫控制字元，不能帶出 ESC、BEL 或換行。
func TestConfigImportEscapesControlCharsInErrors(t *testing.T) {
	env, _ := testEnv(t)
	bad := writeYAML(t, "\"\\e]52;c;evil\\a\": 1\n"+
		"\"fake\\nprofiles.injected\": 2\n"+
		"profiles:\n  \"a\\e[2J\\u202e\": 5\n")
	res, out, errOut := run(t, env, "config", "import", bad)
	if res.Code != 1 || out != "" {
		t.Fatalf("結果 = %+v，stdout：%q，stderr：%q", res, out, errOut)
	}
	for _, r := range errOut {
		if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x202e {
			t.Errorf("stderr 含有未跳脫的控制字元 %U：%q", r, errOut)
		}
	}
	// 每個錯誤各佔一行：含換行的鍵不能偽造出額外的一行。
	for _, line := range strings.Split(strings.TrimSpace(errOut), "\n")[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("出現不是錯誤項目的行：%q（整段：%q）", line, errOut)
		}
	}
	for _, want := range []string{`\x1b]52;c;evil\x07`, `fake\x0aprofiles.injected`, `a\x1b[2J\u202e`} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr 沒有跳脫後的 %q：%q", want, errOut)
		}
	}
}

func TestEscapeControl(t *testing.T) {
	cases := map[string]string{
		"plain 中文 path.0": "plain 中文 path.0",
		"a\x1bb":          `a\x1bb`,
		"a\nb\tc":         `a\x0ab\x09c`,
		"\x7f\u009b":      `\x7f\x9b`,
		"x\u200by":        `x\u200by`,
	}
	for in, want := range cases {
		if got := escapeControl(in); got != want {
			t.Errorf("escapeControl(%q) = %q，預期 %q", in, got, want)
		}
	}
}
