package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/features/internal/fakehub"
	"github.com/cxyfer/notesteme/features/internal/fakeoauth"
	"github.com/cxyfer/notesteme/internal/hub"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// M5 的測試替身（ADR-0030 第 3 點、steps.md 4.30～4.32、features/fixtures/README.md 的「M5 的假服務」）：
// 一個場景只會用到其中一種 Hub——真 Hub（StartHub，程序內的 internal/hub，SQLite 與 fs blob 放在場景目錄的 hub/）
// 或 fakehub（StartFakeHub，Client 的串接）。兩者都掛在 fakenet 的 hub.example.org，所以互斥。
// 主機名稱只在啟動時才登記到 fakenet：fakeoauth 用的是 github.com 這類真實名稱，太早登記會改變其他場景撞上 trap 的行為。

// HubHost 是場景中 Hub 的主機名稱（真 Hub 的 public_url 與 fakehub 共用）。
const HubHost = fakehub.Host

// HubPublicURL 是預設的 public_url（steps.md 4.30）。
const HubPublicURL = "https://" + HubHost

// TestHubToken 是 fakehub 接受的 Client token（Hub 的 PAT 格式：nsh_ 加 40 個 base62 字元，HACC-R9）。
const TestHubToken = "nsh_TestHubToken0123456789abcdefghijABCDEFGH"

// TestHubSessionKey 是真 Hub 的 session 金鑰（NOTESTEME_HUB_SESSION_KEY，HACC A10）；只經環境變數交給 Hub，也加進 log 的 secret 掃描。
// 固定值讓 Hub 依新設定重新啟動之後，等待中的登入仍然有效。
const TestHubSessionKey = "nst-test-hub-session-key-4b9e2f7a1c08d6e3"

// OAuth 供應商的測試憑證：client_id 寫在 Hub 的預設設定，secret 只經環境變數交給 Hub（HACC-R1）。
func testOAuthClientID(k fakeoauth.Kind) string { return "nst-test-" + string(k) + "-client" }

// TestOAuthClientSecret 回傳供應商的測試 client secret（也加進 log 的 secret 掃描）。
func TestOAuthClientSecret(k fakeoauth.Kind) string {
	return "nst-test-" + string(k) + "-secret-7d41c0"
}

// OAuthKinds 是三個假供應商，依 HACC-R1 的順序。
var OAuthKinds = []fakeoauth.Kind{fakeoauth.Google, fakeoauth.GitHub, fakeoauth.LinuxDo}

// HubKind 是場景啟動的 Hub 種類。
type HubKind int

const (
	HubNone HubKind = iota
	// HubReal 是程序內的真 Hub（`Given a Hub is running`）。
	HubReal
	// HubFake 是 fakehub（`Given the owner's client is connected to a Hub`）。
	HubFake
)

// ErrHubKindConflict 表示同一個場景想同時使用真 Hub 與 fakehub（ADR-0030 第 3 點：一個 .feature 只對一種 Hub）。
var ErrHubKindConflict = errors.New("同一個場景不能同時使用真 Hub 與 fakehub（一個 .feature 只對一種 Hub，ADR-0030 第 3 點）")

// hubState 是 World 中 M5 的狀態；零值代表還沒有啟動任何 Hub。
type hubState struct {
	kind HubKind
	// patches 是 `the Hub config contains:` 累積的內容，依序和預設設定合併。
	patches []string
	// dirty 代表 Hub 啟動之後設定又變了，下一個送到 Hub 的請求之前要重新啟動（資料保留）。
	dirty  bool
	server *hub.Server
	oauth  map[fakeoauth.Kind]*fakeoauth.Provider
	fake   *fakehub.Server
}

// HubKind 回傳這個場景啟動的 Hub 種類。
func (w *World) HubKind() HubKind { return w.hub.kind }

// HubDir 是真 Hub 的資料目錄（場景目錄的 hub/）。
func (w *World) HubDir() string { return filepath.Join(w.Root, "hub") }

// HubConfigFile 是真 Hub 的設定檔路徑（合併後的內容在啟動時寫入）。
func (w *World) HubConfigFile() string { return filepath.Join(w.ConfigDir(), "notesteme-hub.yaml") }

// defaultHubConfig 是沒有 `the Hub config contains:` 時的 Hub 設定（steps.md 4.30）。
func defaultHubConfig() map[string]any {
	oauth := map[string]any{}
	for _, k := range OAuthKinds {
		oauth[string(k)] = map[string]any{"client_id": testOAuthClientID(k)}
	}
	return map[string]any{
		"public_url": HubPublicURL,
		"oauth":      oauth,
		"site":       map[string]any{"visibility": "authenticated"},
	}
}

// AddHubConfig 累積一段 Hub 設定（YAML）；Hub 已經啟動時標記為要重新啟動。內容不是 YAML 物件時回傳錯誤。
func (w *World) AddHubConfig(doc string) error {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(w.Expand(doc)), &m); err != nil {
		return fmt.Errorf("Hub 設定不是合法的 YAML 物件：%w", err)
	}
	w.hub.patches = append(w.hub.patches, w.Expand(doc))
	if w.hub.server != nil {
		w.hub.dirty = true
	}
	return nil
}

// HubConfigYAML 回傳預設設定與累積的設定合併後的 YAML（物件逐鍵合併，陣列與純量整個取代）。
func (w *World) HubConfigYAML() ([]byte, error) {
	merged := defaultHubConfig()
	for _, p := range w.hub.patches {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(p), &m); err != nil {
			return nil, err
		}
		mergeConfig(merged, m)
	}
	return yaml.Marshal(merged)
}

// mergeConfig 把 src 逐鍵合併進 dst：兩邊都是物件時遞迴，其他（陣列、純量、null）以 src 取代。
func mergeConfig(dst, src map[string]any) {
	for k, v := range src {
		sm, sok := v.(map[string]any)
		dm, dok := dst[k].(map[string]any)
		if sok && dok {
			mergeConfig(dm, sm)
			continue
		}
		dst[k] = v
	}
}

// hubEnv 是交給真 Hub 的環境變數：設定檔、資料目錄與 OAuth client secret（不放進 Client 的 Env）。
func (w *World) hubEnv() map[string]string {
	env := map[string]string{
		hubconfig.EnvConfig:     w.HubConfigFile(),
		hubconfig.EnvData:       w.HubDir(),
		hubconfig.EnvSessionKey: TestHubSessionKey,
	}
	for _, k := range OAuthKinds {
		env[hubconfig.OAuthSecretEnv(hubconfig.Provider(k))] = TestOAuthClientSecret(k)
	}
	return env
}

// OAuth 回傳假供應商；StartHub 之前是 nil。
func (w *World) OAuth(k fakeoauth.Kind) *fakeoauth.Provider { return w.hub.oauth[k] }

// FakeHub 回傳 fakehub；StartFakeHub 之前是 nil。
func (w *World) FakeHub() *fakehub.Server { return w.hub.fake }

// HubServer 回傳執行中的真 Hub；沒有啟動時是 nil。
func (w *World) HubServer() *hub.Server { return w.hub.server }

// HubStore 回傳真 Hub 的儲存，供步驟唯讀查詢（帳號數、版本狀態，steps.md 4.30、4.31）。
func (w *World) HubStore() (hubstore.Store, error) {
	if w.hub.server == nil {
		return nil, errors.New("真 Hub 沒有在執行（Background 要有 `Given a Hub is running`）")
	}
	return w.hub.server.Store(), nil
}

// StartHub 以目前累積的設定在程序內啟動真 Hub，並在 fakenet 登記 hub.example.org 與三個假供應商（steps.md 4.30 的閘門）。
// 已經啟動時不做任何事；場景已經啟動 fakehub 時回傳 ErrHubKindConflict。
func (w *World) StartHub(ctx context.Context) error {
	switch w.hub.kind {
	case HubReal:
		return nil
	case HubFake:
		return ErrHubKindConflict
	}
	w.hub.oauth = map[fakeoauth.Kind]*fakeoauth.Provider{}
	for _, k := range OAuthKinds {
		p, err := fakeoauth.New(k, testOAuthClientID(k), TestOAuthClientSecret(k), w.Clock.Now)
		if err != nil {
			return err
		}
		w.hub.oauth[k] = p
		for _, h := range fakeoauth.Hosts[k] {
			w.Net.Handle(h, p)
		}
	}
	w.hub.kind = HubReal
	return w.openHub(ctx)
}

// RestartHubIfNeeded 在 Hub 啟動之後設定又改變時，以新設定重新啟動（資料目錄保留，steps.md 4.30 的 `the Hub config contains:`）。
// 送到真 Hub 的請求之前呼叫。
func (w *World) RestartHubIfNeeded(ctx context.Context) error {
	if w.hub.kind != HubReal || !w.hub.dirty {
		return nil
	}
	if err := w.hub.server.Close(); err != nil {
		return fmt.Errorf("關閉 Hub：%w", err)
	}
	w.hub.server = nil
	return w.openHub(ctx)
}

func (w *World) openHub(ctx context.Context) error {
	raw, err := w.HubConfigYAML()
	if err != nil {
		return err
	}
	if err := os.WriteFile(w.HubConfigFile(), raw, 0o600); err != nil {
		return err
	}
	cfg, err := hubconfig.Load(hubconfig.Sources{Env: w.hubEnv()})
	if err != nil {
		return fmt.Errorf("載入 Hub 設定：%w", err)
	}
	deps := w.Net.Deps()
	s, err := hub.Open(ctx, hub.Options{
		Config: cfg, Clock: w.Clock, Net: &deps, Version: "test",
		// Hub 的 log 和 Client 的寫在同一個 buffer，一起做 secret 掃描（NFR-SEC-3）。
		Log: slog.New(slog.NewJSONHandler(w.Log, nil)),
	})
	if err != nil {
		return fmt.Errorf("啟動 Hub：%w", err)
	}
	w.hub.server, w.hub.dirty = s, false
	w.Net.Handle(HubHost, s.Handler())
	return nil
}

// StartFakeHub 啟動 fakehub（接受 TestHubToken）並在 fakenet 登記 hub.example.org（steps.md 4.32 的閘門）。
// 已經啟動時不做任何事；場景已經啟動真 Hub 時回傳 ErrHubKindConflict。
func (w *World) StartFakeHub() error {
	switch w.hub.kind {
	case HubFake:
		return nil
	case HubReal:
		return ErrHubKindConflict
	}
	w.hub.fake = fakehub.New(TestHubToken)
	w.hub.kind = HubFake
	w.Net.Handle(HubHost, w.hub.fake)
	return nil
}

// HubHTTPClient 回傳送到 Hub 的請求用的 client：經 fakenet（信任測試 CA），不跟隨轉址（登入流程要看 302 與 Location）。
func (w *World) HubHTTPClient() *http.Client {
	return &http.Client{
		Transport:     netguard.Transport(w.Net.Deps()),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// HubSecrets 回傳 M5 的測試秘密（OAuth client secret 與 fakehub 的 token），加進 log 的 secret 掃描。
func HubSecrets() []string {
	out := []string{TestHubToken, TestHubSessionKey}
	for _, k := range OAuthKinds {
		out = append(out, TestOAuthClientSecret(k))
	}
	return out
}

// closeHub 關閉真 Hub（World.Close 呼叫）。
func (w *World) closeHub() error {
	if w.hub.server == nil {
		return nil
	}
	err := w.hub.server.Close()
	w.hub.server = nil
	return err
}

// hubRef 是 M5 的代入（steps.md 4.30）：{version:<名稱>}、{user:<名稱>}、{takedown:last}、{report:last}、{share:last}。
var hubRef = regexp.MustCompile(`\{(version|user|takedown|report|share):([^}]+)\}`)

// SubstRequest 是請求端的代入（請求路徑、請求 body、CLI 參數）：{token:…} 加上 M5 的參照，以 Refs["<種類>:<名稱>"] 取代；
// 沒有對應的項目時維持原樣。預期結果的 JSON 只用 Subst（steps.md 4.30：M5 的參照在預期 JSON 中不展開）。
func (w *World) SubstRequest(s string) string {
	s = w.Subst(s)
	return hubRef.ReplaceAllStringFunc(s, func(m string) string {
		sm := hubRef.FindStringSubmatch(m)
		if v, ok := w.Refs[sm[1]+":"+sm[2]]; ok {
			return v
		}
		return m
	})
}

// SetHubRef 記錄一個 M5 的參照（例如 SetHubRef("version", "raw", id)、SetHubRef("share", "last", id)）。
func (w *World) SetHubRef(kind, name, value string) {
	if w.Refs == nil {
		w.Refs = map[string]string{}
	}
	w.Refs[kind+":"+name] = value
}
