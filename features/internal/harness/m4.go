package harness

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/features/internal/fakebatch"
	"github.com/cxyfer/notesteme/features/internal/fakefeed"
	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/features/internal/fakenotify"
	"github.com/cxyfer/notesteme/features/internal/fakeoidc"
	"github.com/cxyfer/notesteme/internal/config"
)

// M4 的假服務接線（features/fixtures/README.md 的「M4 的假服務」，M4-S0）。
//
// 依 README 的設計，只有 fakebatch 的路徑分派是每個場景都有的（和 fakellm 共用主機）；
// fakefeed、fakenotify、fakeoidc 由各自的閘門句型登記（ServeFeeds、ReachNotifyEndpoints、StartOIDC），
// 不預設登記，既有場景不受影響。假服務都掛在正式的主機名稱上，產品照預設設定連線（經 netguard 與 fakenet），
// 不需要把位址注入設定檔；OIDC 的 issuer 由場景的設定檔指定，StartOIDC 把它的主機導到 fakeoidc。

// TestOIDCClientSecret 是測試環境的 NOTESTEME_OIDC_CLIENT_SECRET（AUTH-R16）：產品只在設定了 auth.oidc 時讀它。
// 它也在 NFR-SEC-3 的掃描清單中（log 中不能出現）。
const TestOIDCClientSecret = "nst-test-oidc-secret-91b2e4c7a0"

// OIDCClientSecretEnv 是 OIDC client secret 的環境變數。
const OIDCClientSecretEnv = config.EnvOIDCClientSecret

// m4Fakes 是 World 的 M4 假服務狀態。
// Feed、Notify 是 World 的欄位（場景直接設定它們的狀態）。
type m4Fakes struct {
	mu sync.Mutex
	// batch 是假的 Batch API（BATCH）；EnableBatch 之前是 nil，batch 路徑回 404 並記違規。
	batch *fakebatch.Server
	// oidc 是 StartOIDC 建立的假 IdP，以 issuer 的主機名稱為 key。
	oidc       map[string]*fakeoidc.Server
	violations []string
	// notifyAt 是 fakenotify 每個請求抵達時的 fake clock 時間（ReachNotifyEndpoints）。
	notifyAt []time.Time
}

func newM4Fakes() *m4Fakes { return &m4Fakes{oidc: map[string]*fakeoidc.Server{}} }

// ServeFeeds 把 fakefeed 登記到 www.youtube.com（`the YouTube feeds are served from the registered videos`，SUB 的閘門）。
// 同一主機以 Route 登記的網址（`the URL … serves …`）仍然優先（fakenet 先比對 Route）。
func (w *World) ServeFeeds() *fakefeed.Server {
	w.Net.Handle(fakefeed.Host, w.Feed)
	return w.Feed
}

// ReachNotifyEndpoints 把 fakenotify 登記到 ntfy、Discord、Telegram 與一般 webhook 的主機
// （`the notification endpoints are reachable`，NOTIFY、DIG 彙整與 OPS 告警的閘門）。
// 四個主機依序解析到公開的測試 IP（steps.md 4.18）；場景之後可以用 `the host … resolves to …` 改寫。
// 每個請求抵達時記下 fake clock 的時間（NotifyReceivedAt），`is signed with` 用它比對簽章的時間。
func (w *World) ReachNotifyEndpoints() (*fakenotify.Server, error) {
	h := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.m4.mu.Lock()
		w.m4.notifyAt = append(w.m4.notifyAt, w.Clock.Now())
		w.m4.mu.Unlock()
		w.Notify.ServeHTTP(rw, r)
	})
	for i, host := range fakenotify.Hosts {
		if err := w.Net.Resolves(host, NotifyIPs[i]); err != nil {
			return nil, err
		}
		w.Net.Handle(host, h)
	}
	return w.Notify, nil
}

// NotifyIPs 是 fakenotify.Hosts 依序解析到的公開測試 IP（steps.md 4.18）。
var NotifyIPs = []string{"93.184.216.60", "93.184.216.61", "93.184.216.62", "93.184.216.63"}

// NotifyReceivedAt 回傳 fakenotify 收到的第 i 個請求（從 0 起算，同 Notify.Requests 的順序）抵達時的 fake clock 時間。
func (w *World) NotifyReceivedAt(i int) (time.Time, bool) {
	w.m4.mu.Lock()
	defer w.m4.mu.Unlock()
	if i < 0 || i >= len(w.m4.notifyAt) {
		return time.Time{}, false
	}
	return w.m4.notifyAt[i], true
}

// EnableBatch 建立 fakebatch（第一次呼叫時），讓 LLM 主機上的 batch 路徑交給它（`the batch API of …`，BATCH）。
// answerer 產生每個請求的回答；BATCH 的切片接上 fakellm 的 answers（不記入一般請求數）。
func (w *World) EnableBatch(answerer fakebatch.Answerer) *fakebatch.Server {
	w.m4.mu.Lock()
	defer w.m4.mu.Unlock()
	if w.m4.batch == nil {
		b := fakebatch.New(w.Clock, answerer)
		b.RequireKey(fakebatch.Anthropic, fakellm.TestAnthropicKey)
		b.RequireKey(fakebatch.OpenAI, fakellm.TestOpenAIKey)
		w.m4.batch = b
	}
	return w.m4.batch
}

// Batch 回傳 EnableBatch 建立的 fakebatch；還沒有啟用時是 nil。
func (w *World) Batch() *fakebatch.Server {
	w.m4.mu.Lock()
	defer w.m4.mu.Unlock()
	return w.m4.batch
}

// llmHandler 是 LLM 主機的 handler：batch 的路徑（fakebatch.Owns）交給 fakebatch，其他原封不動交給 fakellm。
// 還沒有 EnableBatch 時，batch 路徑回 404 並記一筆違規（batch: off 的場景，產品不應該呼叫 batch API）。
func (w *World) llmHandler(provider string, llm http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !fakebatch.Owns(provider, r.URL.Path) {
			llm.ServeHTTP(rw, r)
			return
		}
		if b := w.Batch(); b != nil {
			b.Handler(provider).ServeHTTP(rw, r)
			return
		}
		w.m4.mu.Lock()
		w.m4.violations = append(w.m4.violations, fmt.Sprintf("%s %s%s：沒有啟用 batch API（`the batch API of …`）", r.Method, r.Host, r.URL.Path))
		w.m4.mu.Unlock()
		http.NotFound(rw, r)
	})
}

// StartOIDC 建立一個假 IdP，並把 issuer 的主機導到它（`the OIDC provider … signs in the subject …`，AUTH-07）。
// ClientSecret 沒有指定時用 TestOIDCClientSecret，Now 沒有指定時用場景的 fake clock。同一個 issuer 只建立一次。
func (w *World) StartOIDC(cfg fakeoidc.Config) (*fakeoidc.Server, error) {
	u, err := url.Parse(cfg.Issuer)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("harness: OIDC 的 issuer 必須是 https 網址：%q", cfg.Issuer)
	}
	host := strings.ToLower(u.Hostname())
	w.m4.mu.Lock()
	defer w.m4.mu.Unlock()
	if s, ok := w.m4.oidc[host]; ok {
		return s, nil
	}
	if cfg.ClientSecret == "" {
		cfg.ClientSecret = TestOIDCClientSecret
	}
	if cfg.Now == nil {
		cfg.Now = w.Clock.Now
	}
	s, err := fakeoidc.New(cfg)
	if err != nil {
		return nil, err
	}
	w.m4.oidc[host] = s
	w.Net.Handle(host, s)
	return s, nil
}

// m4Violations 取出 M4 假服務記錄的違規（World.Check 使用）。
func (w *World) m4Violations() []string {
	w.m4.mu.Lock()
	defer w.m4.mu.Unlock()
	out := w.m4.violations
	w.m4.violations = nil
	return out
}

// syncFeed 把假 yt-dlp 的 YouTube 影片（有 channel_id 的）同步到 fakefeed（SyncTools 呼叫，在場景的 goroutine 中執行）。
func (w *World) syncFeed() {
	if w.Feed == nil {
		return
	}
	var vs []fakefeed.Video
	for id, v := range w.Tools.Videos {
		if v == nil || v.ChannelID == "" || strings.Contains(id, "/") {
			continue
		}
		fv := fakefeed.Video{ID: id, ChannelID: v.ChannelID, Channel: v.Channel, Title: v.Title, Shorts: v.Shorts}
		if t, err := time.Parse(time.RFC3339, v.PublishedAt); err == nil {
			fv.PublishedAt = t
		}
		vs = append(vs, fv)
	}
	w.Feed.SetVideos(vs)
}

// subscriptionRef 是 `{subscription:<別名>}`（steps.md 4.17 的別名替換）。
var subscriptionRef = regexp.MustCompile(`\{subscription:([a-z0-9-]+)\}`)

// SetSubscription 記下訂閱別名對應的 id。
func (w *World) SetSubscription(alias, id string) {
	if w.Refs == nil {
		w.Refs = map[string]string{}
	}
	w.Refs["subscription:"+alias] = id
}

// Subscription 回傳訂閱別名對應的 id。
func (w *World) Subscription(alias string) (string, bool) {
	id, ok := w.Refs["subscription:"+alias]
	return id, ok
}

// substSubscriptions 代入 {subscription:<別名>}；別名不存在時維持原樣（請求會找不到訂閱、斷言會失敗）。
func (w *World) substSubscriptions(s string) string {
	if !strings.Contains(s, "{subscription:") {
		return s
	}
	return subscriptionRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := w.Refs["subscription:"+subscriptionRef.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
}

// DBCountSubscriptions 以唯讀方式查詢訂閱數（server 已明確停下時，`the number of subscriptions is`）。
func (w *World) DBCountSubscriptions() (int, error) {
	n := 0
	err := w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions`).Scan(&n)
	})
	return n, err
}
