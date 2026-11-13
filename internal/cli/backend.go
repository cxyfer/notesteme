package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/store"
)

// localBase 是獨立模式與本機 socket 使用的虛擬主機名稱；實際連線不看它。
const localBase = "http://notesteme.local"

// backend 是 CLI 與 API 之間的連線。三種模式（獨立、本機 client、--server）只差在 transport，
// 所有子命令都經由 HTTP 呼叫 API，輸出就是 API 的回應（CLI-R5）。
type backend struct {
	mode  Mode
	hc    *http.Client
	base  string
	token string
	// warn 是要在 stderr 印出的警告（目前只有明文 http 的 token）。
	warn string

	// standalone 只在獨立模式有值：用來執行任務與釋放資料目錄的鎖。
	standalone *app.Standalone
}

// close 釋放獨立模式持有的資料目錄。
func (b *backend) close() error {
	if b.standalone != nil {
		return b.standalone.Close()
	}
	b.hc.CloseIdleConnections()
	return nil
}

// openBackend 依 CLI-R2、R3 判斷模式並建立連線。
func openBackend(ctx context.Context, g *globals, env app.Env) (*backend, error) {
	if g.server != "" {
		return openRemote(g, env)
	}

	env = withData(env, g.data)
	sa, err := app.OpenStandalone(ctx, env)
	switch {
	case err == nil:
		return &backend{
			mode: ModeStandalone, standalone: sa, base: localBase,
			hc: &http.Client{Transport: handlerTransport{h: sa.Handler()}},
		}, nil
	case !errors.Is(err, store.ErrLocked):
		return nil, err
	}

	// 資料目錄已被鎖定：看持有者是誰。
	cfg, cerr := config.Load(config.Sources{Env: env.Vars})
	if cerr != nil {
		return nil, cerr
	}
	info, rerr := store.ReadLockInfo(cfg.Data)
	switch {
	case rerr != nil:
		return nil, rerr
	case info == nil || (info.Holder == "serve" && info.Socket == ""):
		// 鎖檔還是空的：serve 正在啟動，還沒寫入連線資訊。
		return nil, errors.New("資料目錄已被鎖定，但持有者還沒寫入連線資訊（notesteme serve 可能正在啟動），請稍後再試。")
	case info.Holder == "serve":
		return openSocket(info.Socket, env), nil
	case info.Holder == "cli":
		return nil, fmt.Errorf("資料目錄正被另一個 notesteme 指令使用中（%d），請等它結束。", info.PID)
	}
	return nil, fmt.Errorf("資料目錄已被鎖定，持有者不明（%q）。", info.Holder)
}

// withData 回傳 Vars 複本，--data 有指定時覆寫資料目錄。
func withData(env app.Env, data string) app.Env {
	vars := make(map[string]string, len(env.Vars)+1)
	for k, v := range env.Vars {
		vars[k] = v
	}
	if data != "" {
		vars[config.EnvData] = data
	}
	env.Vars = vars
	return env
}

// dialFunc 回傳連線用的函式：Env.Dial，沒有設定時用預設的 net.Dialer。
// 它不經過 netguard：目標是使用者自己指定的 server，不是不受信任的外部網址（見 app.Env.Dial）。
// timeout > 0 時限制連線所花的時間（Env.Dial 也一樣，用 ctx 的期限）。
func dialFunc(env app.Env, timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if env.Dial != nil {
		if timeout <= 0 {
			return env.Dial
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return env.Dial(ctx, network, addr)
		}
	}
	d := net.Dialer{Timeout: timeout}
	return d.DialContext
}

// openSocket 連到 serve 的本機 Unix socket（本機 client 模式，不需要 token）。
func openSocket(path string, env app.Env) *backend {
	dial := dialFunc(env, 0)
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, "unix", path)
		},
		DisableKeepAlives: true,
	}
	return &backend{mode: ModeClient, hc: &http.Client{Transport: tr}, base: localBase}
}

// --server 連線的逾時：連線與等待回應標頭有上限；回應 body 不設限（SSE 是長連線）。
// 做成變數只是為了讓測試縮短等待。
var (
	remoteDialTimeout   = 10 * time.Second
	remoteHeaderTimeout = 30 * time.Second
)

// envToken 是 --token 的環境變數版本；兩者都有時以 --token 為準。
const envToken = "NOTESTEME_TOKEN"

// isLoopbackHost 回傳 host（不含 port）是不是本機：localhost 或 loopback 位址。
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// openRemote 連到 --server 指定的位址，HTTP 帶 Authorization: Bearer（CLI-R3）。
// 不碰 --data，也不檢查本機的資料目錄。
//
// 安全：token 只會送給 --server 指定的主機。轉址一律不跟隨（連到另一個 port 時 net/http 也會帶著
// Authorization），收到轉址當成錯誤；http:// 而且不是本機時，警告 token 會以明文傳送。
func openRemote(g *globals, env app.Env) (*backend, error) {
	token := g.token
	if token == "" {
		token = env.Vars[envToken]
	}
	if token == "" {
		return nil, usagef("--server 需要同時指定 --token（或設定環境變數 %s）。", envToken)
	}
	u, err := url.Parse(g.server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, usagef("--server 必須是 http:// 或 https:// 開頭的位址，收到 %q。", g.server)
	}
	tr := &http.Transport{
		// 不讀環境變數的 proxy 設定：連線完全由 Env.Dial 決定。
		DialContext:           dialFunc(env, remoteDialTimeout),
		ResponseHeaderTimeout: remoteHeaderTimeout,
		DisableKeepAlives:     true,
	}
	b := &backend{
		mode: ModeClient, token: token, base: strings.TrimRight(u.String(), "/"),
		hc: &http.Client{
			Transport: tr,
			// 不設 Client.Timeout：它連 body 一起限制，會截斷 SSE。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		b.warn = fmt.Sprintf("--server %s 使用 http://，token 會以明文傳送；請改用 https://，或只連到本機。", g.server)
	}
	return b, nil
}

// open 送出請求並回傳回應（不論狀態碼）；呼叫端負責關閉 Body。body 非 nil 時以 JSON 送出。
func (b *backend) open(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("連線失敗：%w", err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		// 只有 --server 會走到這裡（它不跟隨轉址）。Authorization 不能跟著轉址送到別處。
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		return nil, fmt.Errorf("server 回了轉址（HTTP %d，Location：%q），CLI 不會跟隨；請確認 --server 的位址（例如應該用 https://）。", resp.StatusCode, loc)
	}
	return resp, nil
}

// call 送出請求並讀完回應；非 2xx 回傳 *apiError。
func (b *backend) call(ctx context.Context, method, path string, body any) (raw []byte, status int, err error) {
	resp, err := b.open(ctx, method, path, body)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("讀取回應失敗：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return raw, resp.StatusCode, errorFromResponse(resp.StatusCode, raw)
	}
	return raw, resp.StatusCode, nil
}

// getJSON 送出 GET 並把回應解碼到 out。
func (b *backend) getJSON(ctx context.Context, path string, out any) error {
	raw, _, err := b.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	return nil
}

// handlerTransport 讓獨立模式在程序內呼叫 API handler（不經過任何 listener）。
// handler 是 api.NewLocal，以 owner 身分執行，所以只能在程序內使用。
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &memWriter{header: http.Header{}, status: http.StatusOK}
	r := req.Clone(req.Context())
	r.RemoteAddr = "127.0.0.1:0"
	r.RequestURI = req.URL.RequestURI()
	if r.Body == nil {
		r.Body = http.NoBody
	}
	t.h.ServeHTTP(rec, r)
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", rec.status, http.StatusText(rec.status)),
		StatusCode:    rec.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header,
		Body:          io.NopCloser(&rec.buf),
		ContentLength: int64(rec.buf.Len()),
		Request:       req,
	}, nil
}

// memWriter 是記憶體中的 http.ResponseWriter；回應完整收完才交給 client（獨立模式不使用串流）。
type memWriter struct {
	header http.Header
	status int
	wrote  bool
	buf    bytes.Buffer
}

func (w *memWriter) Header() http.Header { return w.header }

func (w *memWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status, w.wrote = status, true
	}
}

func (w *memWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.buf.Write(p)
}

// Flush 讓 handler 的 http.ResponseController 不會因為不支援而失敗。
func (w *memWriter) Flush() {}
