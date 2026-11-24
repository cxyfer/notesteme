package fakecompose

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// deploy/docker/compose.hub.yaml（ADR-0030 第 1 點）：沒有 docker 時以這個模擬器展開，檢查結構。
func TestHubComposeFile(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"NOTESTEME_HUB_MASTER_KEY":           "fake-master",
		"NOTESTEME_HUB_SESSION_KEY":          "fake-session",
		"NOTESTEME_HUB_POSTGRES_PASSWORD":    "fake-pg",
		"NOTESTEME_HUB_S3_ACCESS_KEY_ID":     "fake-ak",
		"NOTESTEME_HUB_S3_SECRET_ACCESS_KEY": "fake-sk",
	}
	file := "deploy/docker/compose.hub.yaml"
	// 沒有 --profile hub 時什麼都不啟動（不會和 Client 的 compose 混用）。
	if got := runJSON(t, root, configArgs(file), env)["services"]; got != nil && len(got.(map[string]any)) != 0 {
		t.Fatalf("沒有 profile 時的服務 = %v，預期沒有", got)
	}
	got := runJSON(t, root, configArgs(file, "hub"), env)
	if got["name"] != "notesteme-hub" {
		t.Errorf("name = %v", got["name"])
	}
	svcs := got["services"].(map[string]any)
	var names []string
	for n := range svcs {
		names = append(names, n)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "hub,hub-migrate,hub-postgres,hub-s3,hub-s3-init" {
		t.Fatalf("服務 = %v", names)
	}
	networks := func(name string) []string {
		var out []string
		for _, n := range svcs[name].(map[string]any)["networks"].([]any) {
			out = append(out, n.(string))
		}
		return out
	}
	// 資料庫與物件儲存只在 internal network、沒有映射 port。
	for _, n := range []string{"hub-postgres", "hub-s3", "hub-s3-init", "hub-migrate"} {
		if nets := networks(n); !slices.Equal(nets, []string{"hub-internal"}) {
			t.Errorf("%s 的 networks = %v，預期只有 hub-internal", n, nets)
		}
		if _, ok := svcs[n].(map[string]any)["ports"]; ok {
			t.Errorf("%s 不應該映射 port", n)
		}
	}
	if internal := got["networks"].(map[string]any)["hub-internal"].(map[string]any)["internal"]; internal != true {
		t.Errorf("hub-internal 的 internal = %v", internal)
	}
	hub := svcs["hub"].(map[string]any)
	if p := hub["ports"].([]any)[0].(map[string]any); p["host_ip"] != "127.0.0.1" || p["target"] != float64(8090) {
		t.Errorf("hub 的 port = %v，預期只綁 127.0.0.1", p)
	}
	if b := hub["build"].(map[string]any); b["target"] != "notesteme-hub" {
		t.Errorf("hub 的 build target = %v", b["target"])
	}
	e := hub["environment"].(map[string]any)
	if e["NOTESTEME_HUB_STORE_DSN"] != "postgres://notesteme_hub:fake-pg@hub-postgres:5432/notesteme_hub?sslmode=disable" ||
		e["NOTESTEME_HUB_MASTER_KEY"] != "fake-master" || e["NOTESTEME_HUB_S3_SECRET_ACCESS_KEY"] != "fake-sk" {
		t.Errorf("hub 的 environment = %v", e)
	}
	// 每個有 healthcheck 的服務都被等待；hub 等 migrate 成功結束。
	dep := hub["depends_on"].(map[string]any)["hub-migrate"].(map[string]any)
	if dep["condition"] != "service_completed_successfully" {
		t.Errorf("hub 的 depends_on = %v", dep)
	}
	for _, n := range []string{"hub-postgres", "hub-s3"} {
		if _, ok := svcs[n].(map[string]any)["healthcheck"]; !ok {
			t.Errorf("%s 沒有 healthcheck", n)
		}
	}
}

// 每一個密鑰都必須由 .env.hub 提供：沒有設定時 compose 失敗，而不是用寫死的預設值啟動。
func TestHubComposeRequiresSecrets(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"NOTESTEME_HUB_MASTER_KEY": "a", "NOTESTEME_HUB_SESSION_KEY": "b", "NOTESTEME_HUB_POSTGRES_PASSWORD": "c",
		"NOTESTEME_HUB_S3_ACCESS_KEY_ID": "d", "NOTESTEME_HUB_S3_SECRET_ACCESS_KEY": "e",
	}
	for k := range all {
		env := map[string]string{}
		for kk, v := range all {
			if kk != k {
				env[kk] = v
			}
		}
		_, err := Run(root, configArgs("deploy/docker/compose.hub.yaml", "hub"), env)
		var ee *ExitError
		if !errors.As(err, &ee) || !strings.Contains(ee.Msg, k) {
			t.Errorf("沒有 %s：err = %v", k, err)
		}
	}
	// 檔案中沒有寫死的密鑰值：.env.hub.example 的密鑰欄位都是空的。
	raw, err := os.ReadFile(filepath.Join(root, "deploy/docker/.env.hub.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && !strings.HasPrefix(k, "#") && (strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") || strings.Contains(k, "SECRET")) &&
			k != "NOTESTEME_HUB_S3_ACCESS_KEY_ID" && v != "" {
			t.Errorf(".env.hub.example 的 %s 有預設值", k)
		}
	}
}
