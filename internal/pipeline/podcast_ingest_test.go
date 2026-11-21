package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// podEp 是測試 feed 中的一集；audio 為空代表沒有 enclosure，extra 是附加在 <item> 裡的原始 XML。
type podEp struct{ guid, title, audio, extra string }

func podFeed(title string, eps ...podEp) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>` + title + `</title><language>en</language>`)
	for _, e := range eps {
		b.WriteString("<item>")
		if e.guid != "" {
			b.WriteString("<guid>" + e.guid + "</guid>")
		}
		b.WriteString("<title>" + e.title + "</title>")
		if e.audio != "" {
			b.WriteString(`<enclosure url="` + e.audio + `" type="audio/mpeg" length="1"/>`)
		}
		b.WriteString(e.extra + "</item>")
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

// feedSet 依網址回應 feed 內容；沒登記的網址回 404。記錄每次請求的網址。
type feedSet struct {
	mu     sync.Mutex
	bodies map[string]string
	hits   []string
}

func (f *feedSet) client() *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits = append(f.hits, r.URL.String())
		body, ok := f.bodies[r.URL.String()]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

const (
	showURL = "https://feeds.example.com/show.xml"
	audio42 = "https://media.example.com/show/ep42.mp3"
	audio41 = "https://media.example.com/show/ep41.mp3"
)

func showFeed() string {
	return podFeed("The Show", podEp{guid: "ep-41", title: "Episode 41", audio: audio41}, podEp{guid: "ep-42", title: "Episode 42", audio: audio42})
}

// newPodcastRig 建立 pipeline，並把 feed 登記到假的對外 client；checks 記錄預檢過的網址。
func newPodcastRig(t *testing.T, feeds map[string]string) (*rig, *feedSet, *[]string) {
	t.Helper()
	r := newRig(t)
	fs := &feedSet{bodies: feeds}
	r.p.http = fs.client()
	var checks []string
	r.p.checkFn = func(_ context.Context, u string) error { checks = append(checks, u); return nil }
	return r, fs, &checks
}

func (r *rig) ingestPodcast(feedURL string, episodes ...string) ([]IngestResult, error) {
	r.t.Helper()
	return r.p.Ingest(context.Background(), IngestRequest{URL: feedURL, Episodes: episodes})
}

func TestIngestPodcastCreatesEpisode(t *testing.T) {
	r, _, checks := newPodcastRig(t, map[string]string{showURL: showFeed()})
	// 網址的主機大小寫與 fragment 不影響身分；items.url 存正規化的 feed 網址。
	res, err := r.ingestPodcast("HTTPS://Feeds.Example.com:443/show.xml#top", "ep-42")
	if err != nil || len(res) != 1 || res[0].AlreadyInLibrary || res[0].InTrash {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	it := res[0].Item
	if it.Platform != "podcast" || it.ExternalID != "show/ep-42" || it.Part != 0 || it.URL != showURL {
		t.Fatalf("item = %+v", it)
	}
	if ref := source.Ref(it.Platform, it.ExternalID, it.Part); ref != "pod:show/ep-42" {
		t.Errorf("ref = %q", ref)
	}
	// channel 指向 feed。
	ctx := context.Background()
	ch, err := r.st.Read().ChannelByKey(ctx, "podcast", "show")
	if err != nil || ch.URL != showURL || ch.Title != "The Show" || it.ChannelID != ch.ID {
		t.Fatalf("channel = %+v, %v；item.ChannelID = %q", ch, err, it.ChannelID)
	}
	got, err := r.st.Read().ItemByID(ctx, it.ID)
	if err != nil || got.ChannelID != ch.ID {
		t.Errorf("讀回的影音 = %+v, %v", got, err)
	}
	// 步驟都是 pending，metadata 任務是手動新增的優先順序。
	for _, stage := range Stages {
		if s := r.stage(it, stage); s.Status != store.StatusPending {
			t.Errorf("%s = %s", stage, s.Status)
		}
	}
	task, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
	if err != nil || task.Priority != queue.PriorityManual {
		t.Errorf("task = %+v, %v", task, err)
	}
	if len(*checks) != 1 || (*checks)[0] != audio42 {
		t.Errorf("預檢 = %v，預期只檢查選中那一集的音檔", *checks)
	}
	if n := r.count(`SELECT COUNT(*) FROM items`); n != 1 {
		t.Errorf("影音數 = %d", n)
	}
}

func TestIngestPodcastUntilAndOverrides(t *testing.T) {
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Until: StageMetadata})
	if err != nil || res[0].Item.UntilStage != StageMetadata {
		t.Fatalf("until：%+v, %v", res, err)
	}
	// 單次覆寫不正確：在抓取 feed 之前就失敗。
	r2, fs, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	var inv *InvalidError
	if _, err := r2.p.Ingest(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Overrides: map[string]any{"language": map[string]any{"targets": []any{}}}}); !errors.As(err, &inv) {
		t.Fatalf("不正確的覆寫應是 InvalidError：%v", err)
	}
	if len(fs.hits) != 0 {
		t.Errorf("覆寫不正確時不該抓取 feed：%v", fs.hits)
	}
}

// 兩集在同一個交易：第二集失敗時，第一集與頻道都不會留下。
func TestIngestPodcastEpisodesShareOneTransaction(t *testing.T) {
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	res, err := r.ingestPodcast(showURL, "ep-41", "ep-42")
	if err != nil || len(res) != 2 || res[0].Item.ExternalID != "show/ep-41" || res[1].Item.ExternalID != "show/ep-42" {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if res[0].Item.ChannelID == "" || res[0].Item.ChannelID != res[1].Item.ChannelID {
		t.Errorf("兩集應指向同一個頻道：%q、%q", res[0].Item.ChannelID, res[1].Item.ChannelID)
	}

	r2, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	if _, err := r2.rawDB().Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON items WHEN NEW.external_id = 'show/ep-42' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.ingestPodcast(showURL, "ep-41", "ep-42"); err == nil {
		t.Fatal("第二集失敗，整批應該失敗")
	}
	for _, table := range []string{"items", "channels", "stage_runs", "tasks"} {
		if n := r2.count(`SELECT COUNT(*) FROM ` + table); n != 0 {
			t.Errorf("%s 還有 %d 筆，交易應整個回復", table, n)
		}
	}
}

func TestIngestPodcastRequiresEpisodes(t *testing.T) {
	r, fs, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	_, err := r.p.Ingest(context.Background(), IngestRequest{URL: showURL})
	var inv *InvalidError
	if !errors.As(err, &inv) || inv.Msg != "這是 Podcast feed，請在 episodes 指定要新增的集。" {
		t.Fatalf("err = %v", err)
	}
	if len(fs.hits) != 1 {
		t.Errorf("要先抓取 feed 確認它真的是 feed：%v", fs.hits)
	}
	if n := r.count(`SELECT COUNT(*) FROM items`); n != 0 {
		t.Errorf("影音數 = %d", n)
	}
	// 不是 feed 的網址不會得到這個訊息，而是無法辨識。
	r2, _, _ := newPodcastRig(t, map[string]string{"https://example.com/some/page": "<html><body>hi</body></html>"})
	if _, err := r2.p.Ingest(context.Background(), IngestRequest{URL: "https://example.com/some/page"}); !errors.Is(err, ErrUnrecognizedURL) {
		t.Errorf("html：%v", err)
	}
}

func TestIngestPodcastEpisodeValidation(t *testing.T) {
	feed := podFeed("The Show",
		podEp{guid: "ep-42", title: "Episode 42", audio: audio42},
		podEp{guid: "no-audio", title: "No audio"},
		podEp{title: "No guid", audio: "https://media.example.com/show/noguid.mp3"},
	)
	for name, tc := range map[string]struct {
		episodes []string
		want     string
	}{
		"找不到":     {[]string{"ep-42", "ep-99"}, "feed 中找不到這一集：ep-99。"},
		"沒有音檔":    {[]string{"no-audio"}, "這一集沒有音檔：no-audio。"},
		"空的 guid": {[]string{" "}, "feed 中找不到這一集：。"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, checks := newPodcastRig(t, map[string]string{showURL: feed})
			_, err := r.ingestPodcast(showURL, tc.episodes...)
			var inv *InvalidError
			if !errors.As(err, &inv) || inv.Msg != tc.want {
				t.Fatalf("err = %v，預期 %q", err, tc.want)
			}
			if n := r.count(`SELECT COUNT(*) FROM items`) + r.count(`SELECT COUNT(*) FROM channels`); n != 0 || len(*checks) != 0 {
				t.Errorf("驗證失敗時不該建立任何東西、也不預檢：%d 筆、%v", n, *checks)
			}
		})
	}

	// 沒有 guid 的集用音檔網址當 guid；可以含 "/"。
	r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	res, err := r.ingestPodcast(showURL, "https://media.example.com/show/noguid.mp3")
	if err != nil || res[0].Item.ExternalID != "show/https://media.example.com/show/noguid.mp3" {
		t.Fatalf("沒有 guid：%+v, %v", res, err)
	}
	if ref := source.Ref(res[0].Item.Platform, res[0].Item.ExternalID, 0); ref != "pod:show/https://media.example.com/show/noguid.mp3" {
		t.Errorf("ref = %q", ref)
	}

	// 重複指定同一集（含前後空白）只建立一次。
	r2, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	res, err = r2.ingestPodcast(showURL, "ep-42", " ep-42 ", "ep-42")
	if err != nil || len(res) != 1 {
		t.Fatalf("重複的 guid：%+v, %v", res, err)
	}
}

func TestIngestPodcastRejectsOptionsAndURLs(t *testing.T) {
	r, fs, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	ctx := context.Background()

	var ni *NotImplementedError
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Profile: "fast"}); !errors.As(err, &ni) {
		t.Errorf("profile 應回 501：%v", err)
	}
	var inv *InvalidError
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Parts: []int{1}}); !errors.As(err, &inv) || inv.Msg != "Podcast 沒有分P 可以選擇。" {
		t.Errorf("parts：%v", err)
	}
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Until: "bogus"}); !errors.As(err, &inv) {
		t.Errorf("until：%v", err)
	}
	for _, u := range []string{
		"ftp://example.com/feed", "not a url", "", "file:///etc/passwd", "https:///x",
		"https://www.youtube.com/feed/trending", "https://www.bilibili.com/anime", "https://YouTube.com./x", "https://b23.tv/abc",
	} {
		if _, err := r.p.Ingest(ctx, IngestRequest{URL: u, Episodes: []string{"ep-42"}}); !errors.Is(err, ErrUnrecognizedURL) {
			t.Errorf("%q：%v", u, err)
		}
	}
	if len(fs.hits) != 0 {
		t.Errorf("以上都不該抓取：%v", fs.hits)
	}
	// 頻道網址仍然是 ErrChannel，不當成 feed。
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: "https://www.youtube.com/@somechannel"}); !errors.Is(err, ErrChannel) {
		t.Errorf("頻道：%v", err)
	}
}

// 抓取 feed 失敗：對應同 Resolve（防護擋下是 *PolicyError，其他是無法辨識）。
func TestIngestPodcastFetchFailures(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		client    *http.Client
		wantErr   error
		wantMsg   string
		wantBlock bool
	}{
		"404":     {client: feedClient(404, "", new([]string)), wantErr: ErrUnrecognizedURL},
		"不是 feed": {client: feedClient(200, "<html/>", new([]string)), wantErr: ErrUnrecognizedURL},
		"連線失敗":    {client: errClient(errors.New("no route")), wantErr: ErrUnrecognizedURL},
		"被擋": {client: errClient(fmt.Errorf("%w: 10.0.0.5", netguard.ErrBlockedAddress)), wantBlock: true,
			wantMsg: "這個網址指向內部網路，已封鎖。"},
		"太大": {client: errClient(&netguard.TooLargeError{Limit: netguard.LimitFeed}), wantBlock: true, wantMsg: "回應超過大小上限（10 MiB）。"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.p.http = tc.client
			_, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}})
			var pe *PolicyError
			switch {
			case tc.wantBlock:
				if !errors.As(err, &pe) || pe.Msg != tc.wantMsg {
					t.Fatalf("err = %v，預期 *PolicyError %q", err, tc.wantMsg)
				}
			case !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v，預期 %v", err, tc.wantErr)
			}
		})
	}
	// 沒有設定對外 client：無法辨識。
	r := newRig(t)
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}}); !errors.Is(err, ErrUnrecognizedURL) {
		t.Errorf("沒有 client：%v", err)
	}
}

func TestIngestPodcastPrecheck(t *testing.T) {
	ctx := context.Background()
	blocked := fmt.Errorf("%w: 192.168.1.1（主機 rebind.example.com）", netguard.ErrBlockedAddress)

	t.Run("被擋", func(t *testing.T) {
		r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
		r.p.checkFn = func(context.Context, string) error { return blocked }
		_, err := r.ingestPodcast(showURL, "ep-42")
		var pe *PolicyError
		if !errors.As(err, &pe) || pe.Msg != "這個網址指向內部網路，已封鎖。" || !errors.Is(err, netguard.ErrBlockedAddress) {
			t.Fatalf("err = %v", err)
		}
		if n := r.count(`SELECT COUNT(*) FROM items`) + r.count(`SELECT COUNT(*) FROM channels`); n != 0 {
			t.Errorf("被擋時不該建立任何東西：%d", n)
		}
	})

	t.Run("DNS 失敗放寬", func(t *testing.T) {
		r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
		r.p.checkFn = func(context.Context, string) error { return errors.New("lookup media.example.com: no such host") }
		res, err := r.ingestPodcast(showURL, "ep-42")
		if err != nil || len(res) != 1 {
			t.Fatalf("DNS 失敗不該擋下新增：%+v, %v", res, err)
		}
	})

	t.Run("每個主機只檢查一次，全部被擋才整批失敗", func(t *testing.T) {
		feed := podFeed("The Show",
			podEp{guid: "a", title: "A", audio: "https://one.example.com/a.mp3"},
			podEp{guid: "b", title: "B", audio: "https://ONE.example.com/b.mp3"},
			podEp{guid: "c", title: "C", audio: "https://two.example.com/c.mp3"},
		)
		// 全部被擋：每個主機各檢查一次，回第一個被擋的原因，不建立任何東西。
		r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		var checks []string
		r.p.checkFn = func(_ context.Context, u string) error {
			checks = append(checks, u)
			return blocked
		}
		var pe *PolicyError
		if _, err := r.ingestPodcast(showURL, "a", "b", "c"); !errors.As(err, &pe) {
			t.Fatalf("err = %v", err)
		}
		if len(checks) != 2 || checks[0] != "https://one.example.com/a.mp3" || checks[1] != "https://two.example.com/c.mp3" {
			t.Errorf("預檢 = %v", checks)
		}
		if n := r.count(`SELECT COUNT(*) FROM items`); n != 0 {
			t.Errorf("影音數 = %d", n)
		}

		// 只有一部分被擋（不論順序）：其他位址可以連，整批照常新增，連線時再擋（GRD-R2）。
		for _, blockedHost := range []string{"one.example.com", "two.example.com"} {
			r2, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
			r2.p.checkFn = func(_ context.Context, u string) error {
				if strings.Contains(strings.ToLower(u), blockedHost) {
					return blocked
				}
				return nil
			}
			if res, err := r2.ingestPodcast(showURL, "a", "c"); err != nil || len(res) != 2 {
				t.Fatalf("只有 %s 被擋：%+v, %v", blockedHost, res, err)
			}
		}

		// 被擋的之外只有預檢沒有結果的位址：不能斷定全部被擋，照常新增。
		r3, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		r3.p.checkFn = func(_ context.Context, u string) error {
			if strings.Contains(u, "two.example.com") {
				return errors.New("lookup two.example.com: no such host")
			}
			return blocked
		}
		if res, err := r3.ingestPodcast(showURL, "a", "c"); err != nil || len(res) != 2 {
			t.Fatalf("另一個位址沒有結果：%+v, %v", res, err)
		}
	})

	t.Run("去重鍵含 scheme：同主機不同 scheme 各檢查一次", func(t *testing.T) {
		feed := podFeed("The Show",
			podEp{guid: "a", title: "A", audio: "https://one.example.com/a.mp3"},
			podEp{guid: "b", title: "B", audio: "ftp://one.example.com/b.mp3"},
			podEp{guid: "c", title: "C", audio: "https://one.example.com/c.mp3"},
		)
		r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		var checks []string
		r.p.checkFn = func(_ context.Context, u string) error {
			checks = append(checks, u)
			return blocked
		}
		var pe *PolicyError
		if _, err := r.ingestPodcast(showURL, "a", "b", "c"); !errors.As(err, &pe) {
			t.Fatalf("err = %v", err)
		}
		if len(checks) != 2 || checks[0] != "https://one.example.com/a.mp3" || checks[1] != "ftp://one.example.com/b.mp3" {
			t.Errorf("預檢 = %v", checks)
		}
		// 真正的預檢：只選 ftp 那一集時要被擋下（https 已檢查過不能讓 ftp 略過）。
		r2, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		r2.p.checkFn = func(ctx context.Context, u string) error { return netguard.CheckURL(ctx, netguard.Deps{}, u) }
		if _, err := r2.ingestPodcast(showURL, "b"); !errors.As(err, &pe) || pe.Msg != "只支援 http 與 https 網址。" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("整體逾時：預檢卡住不拖住新增", func(t *testing.T) {
		old := precheckTimeout
		precheckTimeout = 50 * time.Millisecond
		defer func() { precheckTimeout = old }()
		feed := podFeed("The Show",
			podEp{guid: "a", title: "A", audio: "https://slow.example.com/a.mp3"},
			podEp{guid: "b", title: "B", audio: "https://other.example.com/b.mp3"},
		)
		r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		var checked []string
		r.p.checkFn = func(ctx context.Context, u string) error {
			checked = append(checked, u)
			<-ctx.Done() // 模擬 DNS 一直沒有回應
			return ctx.Err()
		}
		start := time.Now()
		res, err := r.ingestPodcast(showURL, "a", "b")
		if err != nil || len(res) != 2 {
			t.Fatalf("預檢逾時不該讓新增失敗：%+v, %v", res, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("新增花了 %v", d)
		}
		if len(checked) != 1 {
			t.Errorf("逾時之後不再預檢：%v", checked)
		}
	})

	t.Run("沒有設定預檢", func(t *testing.T) {
		r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
		r.p.checkFn = nil
		if _, err := r.ingestPodcast(showURL, "ep-42"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("scheme 不是 http 也被擋", func(t *testing.T) {
		feed := podFeed("The Show", podEp{guid: "a", title: "A", audio: "ftp://media.example.com/a.mp3"})
		r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
		r.p.checkFn = func(ctx context.Context, u string) error {
			return netguard.CheckURL(ctx, netguard.Deps{}, u)
		}
		var pe *PolicyError
		if _, err := r.ingestPodcast(showURL, "a"); !errors.As(err, &pe) || pe.Msg != "只支援 http 與 https 網址。" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("context 取消不被當成 DNS 失敗", func(t *testing.T) {
		r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
		cctx, cancel := context.WithCancel(ctx)
		r.p.checkFn = func(context.Context, string) error { cancel(); return errors.New("lookup: operation was canceled") }
		if _, err := r.p.Ingest(cctx, IngestRequest{URL: showURL, Episodes: []string{"ep-42"}}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestIngestPodcastAlreadyInLibraryAndTrash(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	first, err := r.ingestPodcast(showURL, "ep-42")
	if err != nil {
		t.Fatal(err)
	}
	// 再新增同一集（連同新的一集）：舊的不重新建立，新的建立。
	res, err := r.ingestPodcast(showURL, "ep-42", "ep-41")
	if err != nil || len(res) != 2 {
		t.Fatalf("%+v, %v", res, err)
	}
	if !res[0].AlreadyInLibrary || res[0].Item.ID != first[0].Item.ID || res[1].AlreadyInLibrary {
		t.Errorf("res = %+v", res)
	}
	if n := r.count(`SELECT COUNT(*) FROM items`); n != 2 {
		t.Errorf("影音數 = %d", n)
	}
	if n := r.count(`SELECT COUNT(*) FROM tasks`); n != 2 {
		t.Errorf("任務數 = %d，已存在的不該再建立任務", n)
	}
	// 只有已存在的：全部 already_in_library。
	res, err = r.ingestPodcast(showURL, "ep-42")
	if err != nil || len(res) != 1 || !res[0].AlreadyInLibrary {
		t.Fatalf("%+v, %v", res, err)
	}
	// 垃圾桶中的：標示 InTrash，不重新建立。
	if _, err := r.p.Trash(ctx, first[0].Item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err = r.ingestPodcast(showURL, "ep-42")
	if err != nil || len(res) != 1 || !res[0].InTrash || res[0].AlreadyInLibrary {
		t.Fatalf("%+v, %v", res, err)
	}
}

// 手動新增視為「想要這一集」：永久刪除留下的 tombstone 在同一個交易中移除（DEL-R11）。
func TestIngestPodcastRemovesTombstone(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newPodcastRig(t, map[string]string{showURL: showFeed()})
	first, err := r.ingestPodcast(showURL, "ep-42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Trash(ctx, first[0].Item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Purge(ctx, first[0].Item.ID); err != nil {
		t.Fatal(err)
	}
	const q = `SELECT COUNT(*) FROM item_tombstones WHERE platform = 'podcast' AND external_id = 'show/ep-42'`
	if n := r.count(q); n != 1 {
		t.Fatalf("永久刪除後 tombstone = %d 筆", n)
	}
	res, err := r.ingestPodcast(showURL, "ep-42")
	if err != nil || len(res) != 1 || res[0].AlreadyInLibrary || res[0].InTrash {
		t.Fatalf("%+v, %v", res, err)
	}
	if n := r.count(q); n != 0 {
		t.Errorf("新增之後 tombstone 還有 %d 筆", n)
	}
	if res[0].Item.ID == first[0].Item.ID {
		t.Error("應建立新的影音")
	}
}

// 同一個 feed 網址得到同一個 feedKey；同 slug 的另一個 feed 得到 -2。
func TestIngestPodcastFeedKeys(t *testing.T) {
	other := "https://other.example.org/show.xml"
	r, _, _ := newPodcastRig(t, map[string]string{
		showURL: showFeed(),
		other:   podFeed("Other Show", podEp{guid: "ep-42", title: "Other 42", audio: "https://other.example.org/42.mp3"}),
	})
	a, err := r.ingestPodcast(showURL, "ep-42")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ingestPodcast(other, "ep-42")
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.ingestPodcast("https://FEEDS.example.com/show.xml", "ep-41")
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Item.ExternalID != "show/ep-42" || b[0].Item.ExternalID != "show-2/ep-42" || c[0].Item.ExternalID != "show/ep-41" {
		t.Errorf("external_id = %q、%q、%q", a[0].Item.ExternalID, b[0].Item.ExternalID, c[0].Item.ExternalID)
	}
	if b[0].Item.URL != other || b[0].Item.ChannelID == a[0].Item.ChannelID || c[0].Item.ChannelID != a[0].Item.ChannelID {
		t.Errorf("url／channel：%+v、%+v、%+v", a[0].Item, b[0].Item, c[0].Item)
	}
	if n := r.count(`SELECT COUNT(*) FROM channels`); n != 2 {
		t.Errorf("頻道數 = %d", n)
	}
}

// 相同 guid 在不同 feed 是不同的影音；guid 內含 "/" 也不影響。
func TestIngestPodcastGUIDWithSlash(t *testing.T) {
	feed := podFeed("The Show", podEp{guid: "2025/10/ep-1", title: "E", audio: audio42})
	r, _, _ := newPodcastRig(t, map[string]string{showURL: feed})
	res, err := r.ingestPodcast(showURL, "2025/10/ep-1")
	if err != nil || res[0].Item.ExternalID != "show/2025/10/ep-1" {
		t.Fatalf("%+v, %v", res, err)
	}
}
