package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/auth"
)

// Scenario 持有一個場景的狀態；每個場景各自一份（ADR-0015）。
type Scenario struct {
	World *harness.World

	// ctx 是 server 的 root context，場景結束時取消。
	ctx    context.Context
	cancel context.CancelFunc

	// server 為 nil 代表目前沒有執行中的 server。
	server *app.Server
	// started 表示這個場景嘗試過啟動；startErr 是最近一次啟動的錯誤（成功時為 nil）。
	started  bool
	startErr error

	// client 只連到 server 的 loopback 位址，自己管理 cookie（不用 cookie jar）。
	client *http.Client
	// owner 是 owner 的 cookie（名稱對應值）；ownerLoggedIn 表示已經登入過。
	owner         map[string]string
	ownerLoggedIn bool

	// last 是最近一次請求的回應。
	last *response

	// givenSettings 是 `Given the global settings are:` 累積的全域設定（區段名稱對應內容）。
	// Given 步驟只累積設定，第一個送出請求的步驟才套用（ADR-0015）。
	givenSettings map[string]any
}

// response 是最近一次請求的回應。
type response struct {
	status int
	header http.Header
	body   []byte
	// preRoute403 表示這個請求缺少完整憑證，預期的 403 應該在路由之前就回應（見 pendingIfNotImplemented）。
	preRoute403 bool
}

// reset 以新的 World 重設場景狀態。
func (s *Scenario) reset(w *harness.World) {
	*s = Scenario{World: w, owner: map[string]string{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// 用自己的 Transport，不碰被 trap 的 http.DefaultTransport；
	// 只允許連到這個場景 server 的實際位址，其他一律拒絕。
	s.client = &http.Client{
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if !s.isServerAddr(addr) {
					return nil, fmt.Errorf("測試 client 只能連到 server 的 loopback 位址，收到 %q", addr)
				}
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		// 只是防止卡死的保險；正常情況下不會用到。
		Timeout: 30 * time.Second,
	}
}

// isServerAddr 回報 addr 是否為這個場景某次啟動時綁定的位址。
func (s *Scenario) isServerAddr(addr string) bool {
	for _, l := range s.World.Listens() {
		if l.Actual == addr {
			return true
		}
	}
	return false
}

// close 停止 server 並釋放 client 資源（After hook 使用）。
func (s *Scenario) close() error {
	var err error
	if s.server != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if e := s.server.Shutdown(shutCtx); e != nil {
			s.server.Kill()
			err = fmt.Errorf("關閉 server：%w", e)
		}
		cancel()
		s.server = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return err
}

// checkLogs 確認 server 的 log 沒有出現環境變數中的密碼或 master key（NFR-SEC-3）。
func (s *Scenario) checkLogs() error {
	logs := s.World.Log.String()
	secrets := map[string]bool{harness.TestPassword: true, harness.TestMasterKey: true}
	for _, k := range []string{"NOTESTEME_PASSWORD", "NOTESTEME_MASTER_KEY"} {
		if v := s.World.Env[k]; len(v) >= 4 {
			secrets[v] = true
		}
	}
	var errs []error
	for v := range secrets {
		if strings.Contains(logs, v) {
			errs = append(errs, fmt.Errorf("server 的 log 含有 secret %q（NFR-SEC-3）", v))
		}
	}
	return errors.Join(errs...)
}

// ---- server 的啟動與關閉 ----

// startServer 以場景目前的設定啟動 server。啟動失敗時錯誤記在 startErr 並回傳。
func (s *Scenario) startServer() error {
	s.started = true
	srv, err := app.StartServer(s.ctx, s.World.AppEnv())
	if err != nil {
		s.server, s.startErr = nil, err
		return err
	}
	s.server, s.startErr = srv, nil
	return nil
}

// ensureServer 在 server 尚未啟動時以當時的設定隱含啟動（ADR-0015）。
// 先前明確啟動失敗過的話不再重試，直接回報原因。
func (s *Scenario) ensureServer() error {
	if s.server != nil {
		return nil
	}
	if s.startErr != nil {
		return fmt.Errorf("server 沒有在執行（啟動失敗：%v）", s.startErr)
	}
	if err := s.startServer(); err != nil {
		return fmt.Errorf("隱含啟動 server 失敗：%w", err)
	}
	return nil
}

// ---- HTTP 請求 ----

// 請求的身分。
type identity int

const (
	asClient identity = iota // 不帶任何身分
	asOwner                  // 帶 owner 的 session cookie
)

// requestOpts 是送出請求的選項。
type requestOpts struct {
	who identity
	// body 非 nil 時以 JSON 送出。
	body []byte
	// noCSRF 為 true 時，owner 的非 GET 請求不帶 CSRF 標頭。
	noCSRF bool
	// headers 是額外的標頭。
	headers map[string]string
}

// parseRequest 把 "GET /api/items" 拆成方法與路徑。
func parseRequest(spec string) (method, path string, err error) {
	f := strings.Fields(spec)
	if len(f) != 2 || !strings.HasPrefix(f[1], "/") {
		return "", "", fmt.Errorf("請求格式應為 \"METHOD /path\"，收到 %q", spec)
	}
	return strings.ToUpper(f[0]), f[1], nil
}

// send 送出 spec（例如 "GET /api/items"）並記錄回應。
// server 尚未啟動時先隱含啟動；owner 的請求在尚未登入時先以目前的密碼登入。
func (s *Scenario) send(spec string, o requestOpts) error {
	method, path, err := parseRequest(spec)
	if err != nil {
		return err
	}
	if err := s.ensureServer(); err != nil {
		return err
	}
	if o.who == asOwner {
		if err := s.ensureOwner(); err != nil {
			return err
		}
	}
	// 影音代號要換成實際的 ID，查詢與 Given 累積的設定都需要 owner 的身分。
	if o.who == asOwner || refPattern.MatchString(path) {
		if err := s.prepareOwner(); err != nil {
			return err
		}
	}
	path, err = s.substRefs(s.World.Subst(path))
	if err != nil {
		return err
	}
	resp, err := s.do(method, path, o)
	if err != nil {
		return err
	}
	s.last = resp
	return nil
}

// ensureOwner 在第一次 owner 請求時登入並記下 cookie（Given the owner is signed in 只是這個意圖）。
// 登出後不會自動重新登入：舊的 cookie 繼續使用，才能驗證 session 已經失效。
func (s *Scenario) ensureOwner() error {
	if s.ownerLoggedIn {
		return nil
	}
	body := fmt.Appendf(nil, `{"password":%q}`, s.World.Env["NOTESTEME_PASSWORD"])
	resp, err := s.do(http.MethodPost, "/api/auth/login", requestOpts{who: asClient, body: body})
	if err != nil {
		return fmt.Errorf("owner 登入：%w", err)
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("owner 登入失敗：HTTP %d %s", resp.status, snippet(resp.body))
	}
	s.storeCookies(resp)
	s.ownerLoggedIn = true
	if s.owner[auth.SessionCookie] == "" {
		return errors.New("owner 登入後沒有收到 session cookie")
	}
	return nil
}

// storeCookies 依回應的 Set-Cookie 更新 owner 的 cookie。
// 伺服器的清除指令（MaxAge < 0 或空值）刻意忽略：這模擬攻擊者或另一個分頁仍持有舊 cookie，
// 登出後的請求照樣帶著它，才能驗證 session 在伺服器端真的失效，而不是只靠 client 把 cookie 丟掉。
func (s *Scenario) storeCookies(resp *response) {
	r := http.Response{Header: resp.header}
	for _, c := range r.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			continue
		}
		s.owner[c.Name] = c.Value
	}
}

// do 送出一個請求並讀完回應。owner 的回應會更新 owner 的 cookie。
func (s *Scenario) do(method, path string, o requestOpts) (*response, error) {
	var body io.Reader
	if o.body != nil {
		body = bytes.NewReader(o.body)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, "http://"+s.server.Addr()+path, body)
	if err != nil {
		return nil, err
	}
	if o.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}
	if o.who == asOwner {
		for name, v := range s.owner {
			req.AddCookie(&http.Cookie{Name: name, Value: v})
		}
		safe := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
		if !safe && !o.noCSRF {
			if csrf := s.owner[auth.CSRFCookie]; csrf != "" {
				req.Header.Set(auth.CSRFHeader, csrf)
			}
		}
	}
	httpResp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s：%w", method, path, err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("讀取回應：%w", err)
	}
	resp := &response{status: httpResp.StatusCode, header: httpResp.Header, body: raw}
	// 沒有完整的 owner 憑證（不帶身分，或非 GET 缺 CSRF）的請求，403 由路由之前的檢查決定。
	resp.preRoute403 = o.who == asClient || (o.noCSRF && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions)
	if o.who == asOwner {
		s.storeCookies(resp)
	}
	return resp, nil
}

// snippet 截短回應內容，放進錯誤訊息。
func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// prepareOwner 確保 server 在執行、owner 已登入，並套用 Given 累積的全域設定。
// 全域設定在 server 啟動之後才能透過 API 寫入，所以延到第一個送出請求的步驟。
func (s *Scenario) prepareOwner() error {
	if err := s.ensureServer(); err != nil {
		return err
	}
	if err := s.ensureOwner(); err != nil {
		return err
	}
	return s.flushSettings()
}
