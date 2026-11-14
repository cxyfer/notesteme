package fakecompose

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeCompose 在暫存目錄寫一份 compose.yaml，回傳 root 與相對路徑。
func writeCompose(t *testing.T, content string) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	rel = "deploy/compose.yaml"
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, rel
}

func runJSON(t *testing.T, root string, args []string, env map[string]string) map[string]any {
	t.Helper()
	out, err := Run(root, args, env)
	if err != nil {
		t.Fatalf("Run：%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("輸出不是 JSON：%v\n%s", err, out)
	}
	return got
}

func configArgs(file string, profiles ...string) []string {
	a := []string{"compose", "-f", file}
	for _, p := range profiles {
		a = append(a, "--profile", p)
	}
	return append(a, "config", "--format", "json")
}

// 真正的 deploy/docker/compose.yaml：ports 正規化成 long form，必要的變數用假的值代入。
func TestRealComposeFile(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOTESTEME_MASTER_KEY": "fake-key"}
	got := runJSON(t, root, configArgs("deploy/docker/compose.yaml", "wsl"), env)
	svc := got["services"].(map[string]any)["notesteme"].(map[string]any)
	want := []any{map[string]any{
		"mode": "ingress", "host_ip": "127.0.0.1", "target": float64(8080), "published": "8080", "protocol": "tcp",
	}}
	if !reflect.DeepEqual(svc["ports"], want) {
		t.Errorf("ports = %v，預期 %v", svc["ports"], want)
	}
	e := svc["environment"].(map[string]any)
	if e["NOTESTEME_MASTER_KEY"] != "fake-key" || e["NOTESTEME_PASSWORD"] != "" {
		t.Errorf("environment = %v", e)
	}
	if got["name"] != "notesteme" {
		t.Errorf("name = %v", got["name"])
	}

	// 改 NOTESTEME_PORT 會反映在 published（預設值只在沒設定時使用）。
	env["NOTESTEME_PORT"] = "9090"
	got = runJSON(t, root, configArgs("deploy/docker/compose.yaml"), env)
	p := got["services"].(map[string]any)["notesteme"].(map[string]any)["ports"].([]any)[0].(map[string]any)
	if p["published"] != "9090" || p["host_ip"] != "127.0.0.1" {
		t.Errorf("port = %v", p)
	}
}

// 沒有設定必要的變數時像 compose 一樣失敗（非零結束碼），訊息指出變數與說明。
func TestRealComposeFileRequiresMasterKey(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{nil, {"NOTESTEME_MASTER_KEY": ""}} {
		_, err := Run(root, configArgs("deploy/docker/compose.yaml"), env)
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code != 1 {
			t.Fatalf("err = %v，預期 ExitError(1)", err)
		}
		if !strings.Contains(ee.Msg, "required variable NOTESTEME_MASTER_KEY is missing a value") {
			t.Errorf("訊息 = %q", ee.Msg)
		}
	}
}

func TestProfileFiltering(t *testing.T) {
	root, rel := writeCompose(t, `
services:
  app: { image: a }
  proxy: { image: p, profiles: [vps] }
  tool: { image: t, profiles: [wsl, debug] }
`)
	names := func(profiles ...string) []string {
		var out []string
		for n := range runJSON(t, root, configArgs(rel, profiles...), nil)["services"].(map[string]any) {
			out = append(out, n)
		}
		return out
	}
	has := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		m := map[string]bool{}
		for _, g := range got {
			m[g] = true
		}
		for _, w := range want {
			if !m[w] {
				return false
			}
		}
		return true
	}
	if g := names(); !has(g, "app") {
		t.Errorf("沒有 profile：%v，預期只有 app", g)
	}
	if g := names("wsl"); !has(g, "app", "tool") {
		t.Errorf("wsl：%v", g)
	}
	if g := names("vps", "debug"); !has(g, "app", "proxy", "tool") {
		t.Errorf("vps+debug：%v", g)
	}
	if g := names("*"); !has(g, "app", "proxy", "tool") {
		t.Errorf("*：%v", g)
	}
	if g := names("nonexistent"); !has(g, "app") {
		t.Errorf("沒有服務宣告的 profile：%v，預期 compose 不報錯、只有 app", g)
	}
}

func TestPortNormalization(t *testing.T) {
	root, rel := writeCompose(t, `
services:
  s:
    image: x
    ports:
      - "8080"
      - "9000:8080"
      - "127.0.0.1:9001:8080/udp"
      - "[::1]:9002:8080"
      - 3000
      - { target: 80, published: 8081, host_ip: 127.0.0.1 }
`)
	got := runJSON(t, root, configArgs(rel), nil)["services"].(map[string]any)["s"].(map[string]any)["ports"].([]any)
	m := func(hostIP, published string, target int, proto string) any {
		o := map[string]any{"mode": "ingress", "target": float64(target), "protocol": proto}
		if hostIP != "" {
			o["host_ip"] = hostIP
		}
		if published != "" {
			o["published"] = published
		}
		return o
	}
	want := []any{
		m("", "", 8080, "tcp"),
		m("", "9000", 8080, "tcp"),
		m("127.0.0.1", "9001", 8080, "udp"),
		m("::1", "9002", 8080, "tcp"),
		m("", "", 3000, "tcp"),
		m("127.0.0.1", "8081", 80, "tcp"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ports =\n%v\n預期\n%v", got, want)
	}
}

func TestPortErrors(t *testing.T) {
	for _, c := range []struct {
		port        string
		unsupported bool
	}{
		{"8080-8090:8080-8090", true},
		{"a:b:c:d", false},
		{"abc", false},
		{"0", false},
		{"8080/ftp", false},
		{"[::1:8080", false},
	} {
		root, rel := writeCompose(t, "services:\n  s:\n    image: x\n    ports: [\""+c.port+"\"]\n")
		_, err := Run(root, configArgs(rel), nil)
		if err == nil {
			t.Errorf("%q：應該失敗", c.port)
			continue
		}
		var ee *ExitError
		if c.unsupported != errors.Is(err, ErrUnsupported) || (!c.unsupported && !errors.As(err, &ee)) {
			t.Errorf("%q：err = %v（預期 unsupported=%v）", c.port, err, c.unsupported)
		}
	}
}

func TestInterpolate(t *testing.T) {
	env := map[string]string{"A": "a", "EMPTY": "", "PORT": "8080"}
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"$A", "a"},
		{"x$A-y", "xa-y"},
		{"${A}", "a"},
		{"$$A", "$A"},
		{"${UNSET}", ""},
		{"${UNSET:-d}", "d"},
		{"${EMPTY:-d}", "d"},
		{"${EMPTY-d}", ""},
		{"${A:-d}", "a"},
		{"${UNSET:-${A}}", "a"},
		{"${UNSET:-${UNSET2:-deep}}", "deep"},
		{"${A:+alt}", "alt"},
		{"${EMPTY:+alt}", ""},
		{"${EMPTY+alt}", "alt"},
		{"${UNSET:+alt}", ""},
		{"127.0.0.1:${PORT:-1}:8080", "127.0.0.1:8080:8080"},
		{"${A:?needed}", "a"},
	} {
		got, err := interpolate(c.in, env)
		if err != nil || got != c.want {
			t.Errorf("interpolate(%q) = %q, %v；預期 %q", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ in, wantErr string }{
		{"${UNSET:?請設定}", "required variable UNSET is missing a value: 請設定"},
		{"${EMPTY:?e}", "required variable EMPTY is missing a value: e"},
		{"${UNSET?e}", "required variable UNSET is missing a value: e"},
		{"${A", "缺少 }"},
		{"${}", "缺少變數名稱"},
		{"${A:=x}", "invalid interpolation"},
		{"cost $5", "invalid interpolation"},
		{"end$", "invalid interpolation"},
	} {
		if _, err := interpolate(c.in, env); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("interpolate(%q) err = %v；預期含 %q", c.in, err, c.wantErr)
		}
	}
	// 沒有冒號的 `?` 只在變數沒有設定時才失敗；EMPTY 有設定（空字串），所以不失敗。
	if got, err := interpolate("${EMPTY?e}", env); err != nil || got != "" {
		t.Errorf("${EMPTY?e} = %q, %v；預期空字串", got, err)
	}
}

func TestEnvironmentListBecomesMap(t *testing.T) {
	root, rel := writeCompose(t, "services:\n  s:\n    image: x\n    environment:\n      - A=1\n      - B\n")
	e := runJSON(t, root, configArgs(rel), nil)["services"].(map[string]any)["s"].(map[string]any)["environment"].(map[string]any)
	if e["A"] != "1" {
		t.Errorf("environment = %v", e)
	}
	if v, ok := e["B"]; !ok || v != nil {
		t.Errorf("沒有值的項目應是 null：%v", e)
	}
}

// 不支援的指令、旗標與 compose 功能一律明確失敗，而且不是 compose 的結束碼（那會被場景誤認為被測行為）。
func TestUnsupportedFailsLoudly(t *testing.T) {
	root, rel := writeCompose(t, "services:\n  s: { image: x }\n")
	for _, c := range []struct {
		name string
		args []string
	}{
		{"沒有 compose", []string{"ps"}},
		{"空參數", nil},
		{"其他子命令", []string{"compose", "-f", rel, "up", "-d"}},
		{"沒有子命令", []string{"compose", "-f", rel}},
		{"不是 json", []string{"compose", "-f", rel, "config"}},
		{"yaml 格式", []string{"compose", "-f", rel, "config", "--format", "yaml"}},
		{"不認得的旗標", []string{"compose", "-f", rel, "--ansi", "never", "config", "--format", "json"}},
		{"多個 -f", []string{"compose", "-f", rel, "-f", rel, "config", "--format", "json"}},
		{"沒有 -f", []string{"compose", "config", "--format", "json"}},
		{"旗標缺值", []string{"compose", "-f"}},
		{"多餘參數", []string{"compose", "-f", rel, "config", "svc", "--format", "json"}},
		{"超出 repo", configArgs("../outside.yaml")},
	} {
		_, err := Run(root, c.args, nil)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s：err = %v，預期 ErrUnsupported", c.name, err)
		}
	}

	for name, content := range map[string]string{
		"include":  "include: [other.yaml]\nservices:\n  s: { image: x }\n",
		"extends":  "services:\n  s: { extends: { service: t } }\n",
		"env_file": "services:\n  s: { image: x, env_file: .env }\n",
	} {
		root, rel := writeCompose(t, content)
		if _, err := Run(root, configArgs(rel), nil); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s：err = %v，預期 ErrUnsupported", name, err)
		}
	}
}

func TestMissingFileAndBadYAMLAreComposeFailures(t *testing.T) {
	root, _ := writeCompose(t, "services: [")
	for _, rel := range []string{"deploy/compose.yaml", "deploy/missing.yaml"} {
		_, err := Run(root, configArgs(rel), nil)
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code != 1 {
			t.Errorf("%s：err = %v，預期 ExitError(1)", rel, err)
		}
	}
}

func TestFindRoot(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("根目錄 %s 沒有 go.mod：%v", root, err)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		// 暫存目錄的上層若剛好有 go.mod 才會到這裡，極不可能；不當成失敗。
		t.Log("暫存目錄之上找到了 go.mod")
	}
}
