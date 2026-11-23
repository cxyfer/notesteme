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
	"sync"
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

// 資料目錄的鎖剛好在轉換（serve 啟動中、別的 CLI 正要結束）時，CLI 短暫重試而不是立刻失敗。
// 時間用注入的 clock，測試不需要真的等待；做成變數只是為了讓測試縮短等待。
var (
	lockRetryWindow = 2 * time.Second
	lockRetryStep   = 100 * time.Millisecond
	// socketProbeTimeout 是探測本機 socket 時，單次撥號的真實時間上限（防止卡死）。
	socketProbeTimeout = 2 * time.Second
)

// openBackend 依 CLI-R2、R3 判斷模式並建立連線。
//
// 資料目錄被鎖定時看持有者是誰：serve 就連它的本機 socket；其他情況（鎖檔還是空的、serve 還沒寫入連線資訊、
// 持有者是另一個 CLI、socket 還連不上）會在 lockRetryWindow 內每隔 lockRetryStep 重試，
// 超過仍然不行才回報錯誤。serve 沒有提供 socket（路徑太長）不會因為等待而改變，直接回報。
func openBackend(ctx context.Context, g *globals, env app.Env) (*backend, error) {
	if g.server != "" {
		return openRemote(g, env)
	}

	env = withData(env, g.data)
	for waited := time.Duration(0); ; waited += lockRetryStep {
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

		be, retry, err := connectHolder(ctx, env)
		if be != nil {
			return be, nil
		}
		if !retry || waited >= lockRetryWindow {
			return nil, err
		}
		t := env.Clock.NewTimer(lockRetryStep)
		select {
		case <-t.C():
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	}
}

// connectHolder 讀鎖檔判斷持有者並嘗試連線。回傳 backend，或 (nil, retry, err)：
// retry 為 true 表示這個狀況可能很快改變（值得重試），err 是重試用完之後要回報的說明。
func connectHolder(ctx context.Context, env app.Env) (be *backend, retry bool, err error) {
	cfg, err := config.Load(config.Sources{Env: env.Vars})
	if err != nil {
		return nil, false, err
	}
	info, err := store.ReadLockInfo(cfg.Data)
	switch {
	case err != nil:
		// 讀到寫到一半的內容：持有者正在更新鎖檔，稍後會完整。
		return nil, true, err
	case info == nil || (info.Holder == holderServe && info.Listen == ""):
		// 鎖檔是空的，或 serve 只寫了持有者：serve 正在啟動，還沒寫入連線資訊。
		return nil, true, errors.New("資料目錄已被鎖定，但持有者還沒寫入連線資訊（notesteme serve 正在啟動），請稍後再試。")
	case info.Holder == holderServe && info.Socket == "":
		// serve 已完成啟動，但路徑太長所以沒有建立本機 socket；等待也不會改變。
		return nil, false, errors.New("serve 沒有提供本機 socket（資料目錄路徑太長），請改用 --server。")
	case info.Holder == holderServe:
		// 以 CLI 自己算出的絕對路徑為準，不信任鎖檔裡的路徑（可能是別的工作目錄寫的）。
		path := app.SocketPath(cfg.Data)
		if err := probeSocket(ctx, env, path); err != nil {
			return nil, true, fmt.Errorf("無法連到 serve 的本機 socket %s：%w", path, err)
		}
		return openSocket(path, env), false, nil
	case info.Holder == holderCLI:
		return nil, true, fmt.Errorf("資料目錄正被另一個 notesteme 指令使用中（%d），請等它結束。", info.PID)
	}
	return nil, false, fmt.Errorf("資料目錄已被鎖定，持有者不明（%q）。", info.Holder)
}

// 鎖檔 Holder 的值（與 app 套件的 holderServe、holderCLI 相同）。
const (
	holderServe = "serve"
	holderCLI   = "cli"
)

// probeSocket 試著連一次 path 再關閉：確認 serve 的 socket 已經可以連線。
func probeSocket(ctx context.Context, env app.Env, path string) error {
	ctx, cancel := context.WithTimeout(ctx, socketProbeTimeout)
	defer cancel()
	c, err := dialFunc(env, 0)(ctx, "unix", path)
	if err != nil {
		return err
	}
	return c.Close()
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
	if body == nil {
		return b.openRaw(ctx, method, path, "", nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return b.openRaw(ctx, method, path, "application/json", raw)
}

// openRaw 同 open，但 body 照原樣送出（例如 multipart）；contentType 非空時設為 Content-Type。
func (b *backend) openRaw(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
	return readResponse(resp)
}

// callRaw 同 call，但 body 照原樣送出（見 openRaw）。
func (b *backend) callRaw(ctx context.Context, method, path, contentType string, body []byte) (raw []byte, status int, err error) {
	resp, err := b.openRaw(ctx, method, path, contentType, body)
	if err != nil {
		return nil, 0, err
	}
	return readResponse(resp)
}

// readResponse 讀完回應並關閉；非 2xx 回傳 *apiError。
func readResponse(resp *http.Response) (raw []byte, status int, err error) {
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
//
// 回應以串流交給 client：handler 在另一個 goroutine 執行，標頭一送出（WriteHeader、第一次 Write、Flush，
// 或 handler 結束）就回傳 *http.Response，本體透過 io.Pipe 傳遞。大型回應（備份，OPS-R1）因此不會整份留在記憶體。
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	pr, pw := io.Pipe()
	w := &streamWriter{header: http.Header{}, pw: pw, committed: make(chan struct{})}
	r := req.Clone(ctx)
	r.RemoteAddr = "127.0.0.1:0"
	r.RequestURI = req.URL.RequestURI()
	if r.Body == nil {
		r.Body = http.NoBody
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			// 中斷（http.ErrAbortHandler，例如備份串流到一半失敗）要讓 client 讀到錯誤，而不是一份看似完整的內容；
			// 其他 panic 也轉成讀取錯誤，不讓整個程序崩潰。
			if v := recover(); v != nil {
				w.commit()
				if v == http.ErrAbortHandler { //nolint:errorlint // 官方建議以相等比較
					pw.CloseWithError(io.ErrUnexpectedEOF)
					return
				}
				pw.CloseWithError(fmt.Errorf("API handler 發生 panic：%v", v))
			}
		}()
		t.h.ServeHTTP(w, r)
		w.commit()
		_ = pw.Close()
	}()

	select {
	case <-w.committed:
	case <-ctx.Done():
		_ = pr.CloseWithError(ctx.Err())
		<-done
		cancel()
		return nil, ctx.Err()
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", w.status, http.StatusText(w.status)),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.sent,
		Body:          &streamBody{pr: pr, cancel: cancel, done: done},
		ContentLength: -1,
		Request:       req,
	}, nil
}

// streamWriter 是 http.ResponseWriter：標頭送出後，本體寫進 pipe（寫入會等到 client 讀走）。
type streamWriter struct {
	header http.Header
	pw     *io.PipeWriter

	once      sync.Once
	committed chan struct{}
	status    int
	sent      http.Header // 標頭送出當下的快照；之後 handler 再改 Header() 不影響 client
}

func (w *streamWriter) Header() http.Header { return w.header }

// commit 在第一次呼叫時固定狀態與標頭，並通知 RoundTrip 可以回傳了。
func (w *streamWriter) commit() { w.commitWith(http.StatusOK) }

func (w *streamWriter) commitWith(status int) {
	w.once.Do(func() {
		w.status, w.sent = status, w.header.Clone()
		close(w.committed)
	})
}

func (w *streamWriter) WriteHeader(status int) { w.commitWith(status) }

func (w *streamWriter) Write(p []byte) (int, error) {
	w.commit()
	return w.pw.Write(p)
}

// Flush 與真正的 server 一樣：把標頭送出。
func (w *streamWriter) Flush() { w.commit() }

// streamBody 是回應的本體。Close 會中止仍在寫入的 handler，並等它結束：
// 獨立模式隨後會釋放資料目錄，不能有 handler 還在使用它。
type streamBody struct {
	pr     *io.PipeReader
	cancel context.CancelFunc
	done   chan struct{}
}

func (b *streamBody) Read(p []byte) (int, error) { return b.pr.Read(p) }

func (b *streamBody) Close() error {
	b.cancel()
	_ = b.pr.Close()
	<-b.done
	return nil
}
