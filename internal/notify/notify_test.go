package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

const testSecret = "whsec-0123456789abcdef"

// rig 是 Service 的測試環境：真的 store 與資料金鑰、假的網路（所有主機都連到同一個 handler）。
type rig struct {
	st   *store.Store
	clk  *clock.Fake
	svc  *Service
	ob   *Outbox
	logs *bytes.Buffer
	dir  string
	tn   *testNet

	mu       sync.Mutex
	status   []int // 依序回應的狀態碼；用完之後回 204
	requests []recorded
}

type recorded struct {
	path   string
	header http.Header
	body   []byte
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := secrets.Open(ctx, st, "test-master-key", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{st: st, clk: clk, logs: &bytes.Buffer{}, dir: dir}
	c, tn := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.requests = append(g.requests, recorded{path: r.URL.Path, header: r.Header.Clone(), body: b})
		code := 204
		if len(g.status) > 0 {
			code, g.status = g.status[0], g.status[1:]
		}
		g.mu.Unlock()
		if code == 429 {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(code)
	}))
	g.tn = tn
	g.svc = New(Deps{
		Store: st, Keys: keys, Clock: clk, HTTP: c, Version: "test",
		Log:   slog.New(slog.NewTextHandler(g.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Hosts: HostPolicy{AllowHTTP: func(string) bool { return true }},
	})
	g.ob = &Outbox{Clock: clk}
	return g
}

func (g *rig) answer(codes ...int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = append(g.status, codes...)
}

func (g *rig) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.requests)
}

func (g *rig) last() recorded {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests[len(g.requests)-1]
}

func (g *rig) webhook(t *testing.T, id string, events ...string) {
	t.Helper()
	_, err := g.svc.Create(context.Background(), CreateInput{ID: id, Kind: KindWebhook,
		Config: map[string]any{"url": "http://hooks.example.com/" + id, "secret": testSecret}, Events: events})
	if err != nil {
		t.Fatal(err)
	}
}

// emit 在一個交易中寫入事件，回傳寫入的筆數。
func (g *rig) emit(t *testing.T, ev Event) int {
	t.Helper()
	var n int
	if err := g.st.Tx(context.Background(), func(r *store.Repo) error {
		var err error
		n, err = g.ob.Emit(context.Background(), r, ev)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func (g *rig) deliver(t *testing.T) scheduler.Result {
	t.Helper()
	res, err := g.svc.Deliver(context.Background(), scheduler.Job{}, g.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (g *rig) outbox(t *testing.T, state string) []store.OutboxEntry {
	t.Helper()
	rows, err := g.st.Read().ListOutbox(context.Background(), state, 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func doneEvent(item string) Event {
	return Event{Name: EventItemDone, DedupeKey: DedupeItemDone(item, "run1"), Item: &Item{ID: item, Ref: "yt:" + item, Title: "T " + item}}
}

func TestCreateGetPatchDelete(t *testing.T) {
	ctx := context.Background()
	g := newRig(t)
	info, err := g.svc.Create(ctx, CreateInput{ID: "telegram", Kind: KindTelegram,
		Config: map[string]any{"bot_token": testBotToken, "chat_id": "42"}, Events: []string{"item.failed"}})
	if err != nil || !info.Enabled || info.Config["bot_token_last4"] != "6789" || info.Config["bot_token"] != nil {
		t.Fatalf("Create = %+v，%v", info, err)
	}
	if _, err := g.svc.Create(ctx, CreateInput{ID: "telegram", Kind: KindNtfy, Config: map[string]any{"topic": "t"}, Events: []string{"alert"}}); !errors.Is(err, ErrExists) {
		t.Errorf("重複：%v", err)
	}
	var inv *InvalidError
	if _, err := g.svc.Create(ctx, CreateInput{ID: "Bad", Kind: "email", Events: []string{"x"}}); !errors.As(err, &inv) || len(inv.Fields) != 3 {
		t.Errorf("422：%v", err)
	}
	// 密鑰不能出現在資料庫檔案中（NOTIFY-R6）。
	for _, f := range []string{"notesteme.db", "notesteme.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(g.dir, f))
		if bytes.Contains(b, []byte("ABCdefGHIjkl")) {
			t.Errorf("%s 含明文的 bot token", f)
		}
	}
	// PATCH 只改事件時，密鑰保留；改 chat_id 時其他欄位也保留。
	if _, err := g.svc.Patch(ctx, "telegram", PatchInput{Events: []string{"item.failed", "alert"}}); err != nil {
		t.Fatal(err)
	}
	off := false
	info, err = g.svc.Patch(ctx, "telegram", PatchInput{Config: map[string]any{"chat_id": "43"}, Enabled: &off})
	if err != nil || info.Config["chat_id"] != "43" || info.Config["bot_token_last4"] != "6789" || info.Enabled || len(info.Events) != 2 {
		t.Fatalf("Patch = %+v，%v", info, err)
	}
	n, _ := g.st.Read().Notifier(ctx, "telegram")
	cfg, err := g.svc.open(n)
	if err != nil || cfg["bot_token"] != testBotToken {
		t.Errorf("密鑰應保留：%v %v", cfg, err)
	}
	if _, err := g.svc.Patch(ctx, "telegram", PatchInput{Config: map[string]any{"api_base": "https://evil.example"}}); !errors.As(err, &inv) || inv.Fields[0].Message != msgTelegramHost {
		t.Errorf("PATCH 也驗證主機：%v", err)
	}
	if _, err := g.svc.Patch(ctx, "nope", PatchInput{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	if _, err := g.svc.Get(ctx, "telegram"); err != nil {
		t.Error(err)
	}
	list, _ := g.svc.List(ctx)
	if len(list) != 1 {
		t.Errorf("List = %+v", list)
	}
	if err := g.svc.Delete(ctx, "telegram"); err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Delete(ctx, "telegram"); !errors.Is(err, ErrNotFound) {
		t.Errorf("第二次刪除：%v", err)
	}
	if _, err := g.svc.Get(ctx, "telegram"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get 已刪除：%v", err)
	}
}

func TestTooManyNotifiers(t *testing.T) {
	g := newRig(t)
	for i := range MaxNotifiers {
		g.webhook(t, "w"+string(rune('a'+i)), "alert")
	}
	_, err := g.svc.Create(context.Background(), CreateInput{ID: "one-more", Kind: KindNtfy, Config: map[string]any{"topic": "t"}, Events: []string{"alert"}})
	if !errors.Is(err, ErrTooMany) {
		t.Errorf("第 21 個：%v", err)
	}
}

func TestEmitOnlyForSubscribersAndDedupes(t *testing.T) {
	g := newRig(t)
	if n := g.emit(t, doneEvent("a")); n != 0 {
		t.Fatalf("沒有 notifier 時不寫入：%d", n)
	}
	if _, err := g.st.Read().ScheduledJob(context.Background(), DeliverKind, DeliverKey); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("沒有寫入時不排程：%v", err)
	}
	g.webhook(t, "done", "item.done")
	g.webhook(t, "failed", "item.failed")
	if n := g.emit(t, doneEvent("a")); n != 1 {
		t.Fatalf("只寫給訂閱的 notifier：%d", n)
	}
	job, err := g.st.Read().ScheduledJob(context.Background(), DeliverKind, DeliverKey)
	if err != nil || !job.NextRunAt.Equal(g.clk.Now()) {
		t.Errorf("notify.deliver 要提前到現在：%+v %v", job, err)
	}
	if n := g.emit(t, doneEvent("a")); n != 0 {
		t.Errorf("24 小時內同一個鍵不再寫入：%d", n)
	}
	g.clk.Advance(25 * time.Hour)
	if n := g.emit(t, doneEvent("a")); n != 1 {
		t.Errorf("視窗之後再寫入：%d", n)
	}
	var nilOutbox *Outbox
	if n, err := nilOutbox.Emit(context.Background(), g.st.Write(), doneEvent("x")); n != 0 || err != nil {
		t.Errorf("nil Outbox：%d %v", n, err)
	}
	if _, err := g.ob.Emit(context.Background(), g.st.Write(), Event{Name: EventItemDone}); err == nil {
		t.Error("缺 dedupe 鍵要錯誤")
	}
}

func TestDeliverRetriesWithBackoffAndDrops(t *testing.T) {
	g := newRig(t)
	g.webhook(t, "auto", "item.done")
	g.answer(503, 503, 503, 503, 503, 503)
	g.emit(t, doneEvent("a"))
	waits := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	for i, w := range waits {
		g.deliver(t)
		if g.count() != i+1 {
			t.Fatalf("第 %d 次嘗試：%d 個請求", i+1, g.count())
		}
		// 還沒到時間不送。
		g.clk.Advance(w - time.Second)
		g.deliver(t)
		if g.count() != i+1 {
			t.Fatalf("退避 %v 之前送出了", w)
		}
		g.clk.Advance(time.Second)
	}
	g.deliver(t)
	dropped := g.outbox(t, store.OutboxDropped)
	if g.count() != 6 || len(dropped) != 1 || dropped[0].Attempts != 6 || dropped[0].LastError != "通知端點回應 HTTP 503。" {
		t.Fatalf("第 6 次失敗後 dropped：%d %+v", g.count(), dropped)
	}
	g.clk.Advance(24 * time.Hour)
	g.deliver(t)
	if g.count() != 6 {
		t.Error("dropped 之後不再送")
	}
	evs, _ := g.st.Read().CountEvents(context.Background())
	if evs != 1 {
		t.Errorf("notify.dropped 事件：%d", evs)
	}
	list, _ := g.svc.Notifications(context.Background(), store.OutboxDropped)
	if len(list) != 1 || list[0].Notifier != "auto" || *list[0].LastError != "通知端點回應 HTTP 503。" {
		t.Errorf("Notifications = %+v", list)
	}
}

func TestDeliverPermanentAndRetryAfter(t *testing.T) {
	g := newRig(t)
	g.webhook(t, "auto", "item.done")
	g.answer(404)
	g.emit(t, doneEvent("a"))
	g.deliver(t)
	g.clk.Advance(24 * time.Hour)
	g.deliver(t)
	if d := g.outbox(t, store.OutboxDropped); g.count() != 1 || len(d) != 1 || d[0].Attempts != 1 || d[0].LastError != "通知端點回應 HTTP 404。" {
		t.Errorf("permanent 第一次就 dropped：%d %+v", g.count(), d)
	}
	g.answer(429)
	g.emit(t, doneEvent("b"))
	g.deliver(t)
	g.clk.Advance(time.Minute)
	g.deliver(t)
	if g.count() != 2 {
		t.Errorf("Retry-After 120 秒比退避長：%d", g.count())
	}
	g.clk.Advance(time.Minute)
	g.deliver(t)
	if s := g.outbox(t, store.OutboxSent); g.count() != 3 || len(s) != 1 || s[0].Attempts != 2 || s[0].SentAt == nil {
		t.Errorf("Retry-After 之後送出：%d %+v", g.count(), s)
	}
}

func TestDeliverRateLimit(t *testing.T) {
	g := newRig(t)
	g.svc.d.RateCount, g.svc.d.RatePer = 2, 10*time.Minute
	g.webhook(t, "auto", "item.failed")
	for _, id := range []string{"a", "b", "c"} {
		g.emit(t, Event{Name: EventItemFailed, DedupeKey: DedupeItemFailed(id, "metadata"), Item: &Item{ID: id}})
	}
	res := g.deliver(t)
	if g.count() != 2 {
		t.Fatalf("速率上限 2：%d", g.count())
	}
	pend := g.outbox(t, store.OutboxPending)
	if len(pend) != 1 || pend[0].Attempts != 0 || !pend[0].NextAttemptAt.Equal(g.clk.Now().Add(10*time.Minute)) {
		t.Errorf("延後的項目不計入嘗試：%+v", pend)
	}
	if !res.Next.Equal(g.clk.Now().Add(time.Minute)) {
		t.Errorf("下一次最晚一分鐘後：%v", res.Next)
	}
	g.clk.Advance(10*time.Minute - time.Millisecond)
	g.deliver(t)
	if g.count() != 2 {
		t.Error("視窗還沒過")
	}
	g.clk.Advance(time.Millisecond)
	g.deliver(t)
	if g.count() != 3 {
		t.Errorf("視窗過了就送：%d", g.count())
	}
}

func TestDeliverWebhookBodyAndSecrets(t *testing.T) {
	g := newRig(t)
	g.webhook(t, "auto", "item.failed")
	g.emit(t, Event{Name: EventItemFailed, DedupeKey: "k", Item: &Item{ID: "i1", Ref: "yt:x", Title: "Removed video"}, Stage: "metadata", ErrorClass: "permanent", Message: "影片不存在。"})
	g.deliver(t)
	r := g.last()
	var v map[string]any
	if err := json.Unmarshal(r.body, &v); err != nil || v["event"] != "item.failed" || v["stage"] != "metadata" {
		t.Fatalf("body = %s", r.body)
	}
	if r.header.Get("X-Notesteme-Signature") != Sign(testSecret, g.clk.Now().Unix(), r.body) || r.header.Get("X-Notesteme-Delivery") != v["id"] {
		t.Errorf("簽章與 delivery = %v", r.header)
	}
	if bytes.Contains(r.body, []byte(testSecret)) || strings.Contains(g.logs.String(), testSecret) || strings.Contains(g.logs.String(), "/auto") {
		t.Error("body 或 log 不可含 secret 或完整網址")
	}
}

func TestDeliverDropsWhenSecretsWereReset(t *testing.T) {
	ctx := context.Background()
	g := newRig(t)
	g.webhook(t, "auto", "item.done")
	g.emit(t, doneEvent("a"))
	// 換一把資料金鑰（等同 secrets reset）：舊的密文解不開。
	other, err := secrets.Open(ctx, mustOpenStore(t), "other", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	g.svc.d.Keys = other
	g.deliver(t)
	d := g.outbox(t, store.OutboxDropped)
	if g.count() != 0 || len(d) != 1 || d[0].LastError != MsgSecretGone {
		t.Errorf("密鑰被重設：%d %+v", g.count(), d)
	}
	res, err := g.svc.Test(ctx, "auto")
	if err != nil || res.OK || res.Message != MsgSecretGone {
		t.Errorf("Test = %+v %v", res, err)
	}
	// PATCH 以非機密欄位為基礎，缺少的密鑰要重新輸入。
	var inv *InvalidError
	if _, err := g.svc.Patch(ctx, "auto", PatchInput{Config: map[string]any{"secret": testSecret}}); !errors.As(err, &inv) || inv.Fields[0].Path != "config.url" {
		t.Errorf("要重新輸入 url：%v", err)
	}
	if _, err := g.svc.Patch(ctx, "auto", PatchInput{Config: map[string]any{"url": "http://hooks.example.com/x", "secret": testSecret}}); err != nil {
		t.Errorf("重新設定：%v", err)
	}
}

func mustOpenStore(t *testing.T) *store.Store {
	st, err := store.Open(context.Background(), t.TempDir(), clock.NewFake(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDeliverDisabledAndDeleted(t *testing.T) {
	ctx := context.Background()
	g := newRig(t)
	g.webhook(t, "auto", "item.done")
	g.emit(t, doneEvent("a"))
	off := false
	if _, err := g.svc.Patch(ctx, "auto", PatchInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	g.deliver(t)
	if d := g.outbox(t, store.OutboxDropped); g.count() != 0 || len(d) != 1 {
		t.Errorf("停用的 notifier 不送：%+v", d)
	}
	on := true
	_, _ = g.svc.Patch(ctx, "auto", PatchInput{Enabled: &on})
	g.emit(t, doneEvent("b"))
	if err := g.svc.Delete(ctx, "auto"); err != nil {
		t.Fatal(err)
	}
	if p := g.outbox(t, store.OutboxPending); len(p) != 0 {
		t.Errorf("刪除 notifier 時刪掉 pending：%+v", p)
	}
}

func TestTestNotification(t *testing.T) {
	ctx := context.Background()
	g := newRig(t)
	g.webhook(t, "auto", "alert")
	res, err := g.svc.Test(ctx, "auto")
	if err != nil || !res.OK || res.Status != 204 {
		t.Fatalf("Test = %+v %v", res, err)
	}
	if !bytes.Contains(g.last().body, []byte(TestTitle)) {
		t.Errorf("測試通知內容：%s", g.last().body)
	}
	g.answer(401)
	if res, _ := g.svc.Test(ctx, "auto"); res.OK || res.ErrorClass != "permanent" || res.Message != "通知端點回應 HTTP 401。" || res.Status != 401 {
		t.Errorf("401：%+v", res)
	}
	g.tn.ips["hooks.example.com"] = []string{"10.0.0.5"}
	if res, _ := g.svc.Test(ctx, "auto"); res.OK || res.Message != MsgBlocked {
		t.Errorf("內網：%+v", res)
	}
	if _, err := g.svc.Test(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	if rows := g.outbox(t, ""); len(rows) != 0 {
		t.Errorf("測試通知不經 outbox：%+v", rows)
	}
}

func TestRegisterAndRetention(t *testing.T) {
	g := newRig(t)
	sch := scheduler.New(g.st, g.clk, nil, 0)
	if err := g.svc.Register(sch); err != nil {
		t.Fatal(err)
	}
	if k := sch.Kinds(); len(k) != 1 || k[0] != DeliverKind {
		t.Errorf("Kinds = %v", k)
	}
	g.webhook(t, "auto", "item.done")
	g.emit(t, doneEvent("a"))
	g.deliver(t)
	g.clk.Advance(31 * 24 * time.Hour)
	g.deliver(t)
	if rows := g.outbox(t, ""); len(rows) != 0 {
		t.Errorf("30 天之後清掉 sent：%+v", rows)
	}
	if ValidState("x") || !ValidState("sent") {
		t.Error("ValidState")
	}
}

func TestWants(t *testing.T) {
	g := newRig(t)
	var nilOutbox *Outbox
	if ok, err := nilOutbox.Wants(context.Background(), g.st.Write(), EventItemDone); ok || err != nil {
		t.Errorf("nil：%v %v", ok, err)
	}
	if ok, _ := g.ob.Wants(context.Background(), g.st.Write(), EventItemDone); ok {
		t.Error("沒有 notifier")
	}
	g.webhook(t, "auto", "item.done")
	if ok, _ := g.ob.Wants(context.Background(), g.st.Write(), EventItemDone); !ok {
		t.Error("有訂閱的 notifier")
	}
}
