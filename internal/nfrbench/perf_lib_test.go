package nfrbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/benchdata"
)

type listPage struct {
	Items []struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	} `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor"`
}

func (s *server) listPage(b *testing.B, query string) listPage {
	b.Helper()
	body, err := s.get("/api/items?"+query, http.StatusOK)
	if err != nil {
		b.Fatal(err)
	}
	var p listPage
	if err := json.Unmarshal(body, &p); err != nil {
		b.Fatal(err)
	}
	return p
}

// latencyCases 依序量每個查詢的 p95；nonEmpty 為 true 的查詢必須有結果，避免量到空查詢。
type queryCase struct {
	name, path string
	nonEmpty   bool
}

func runQueries(b *testing.B, s *server, c *checker, id, data string, cases []queryCase, warmup, n int, limit time.Duration) {
	b.Helper()
	for _, q := range cases {
		body, err := s.get(q.path, http.StatusOK)
		if err != nil {
			b.Fatal(err)
		}
		var head struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(body, &head)
		if q.nonEmpty && head.Total == 0 {
			b.Fatalf("%s 沒有結果：%s", q.path, truncate(body))
		}
		st := sample(b, warmup, n, func() error { _, err := s.get(q.path, http.StatusOK); return err })
		c.checkLatency(id, q.name, data, "p95", st, limit)
		fmt.Fprintf(os.Stderr, "    %s：total=%d\n", q.path, head.Total)
	}
}

// NFR-PERF-1：GET /api/items（含篩選、單頁 200 筆）在 lib-10k 上 p95 < 200ms。
// 真正的 HTTP handler 與 SQLite（in-process 的 serve）；每種查詢各 50 次，各自判定。
func BenchmarkPerf1ItemsList(b *testing.B) {
	dir, name := lib(b)
	s := startServer(b, dir, nil)
	c := newChecker(b)
	first := s.listPage(b, "limit=200")
	if len(first.Items) == 0 {
		b.Fatal("媒體庫是空的")
	}
	ch := first.Items[0].ChannelID
	// 深頁：沿著 next_cursor 走到最舊的一端（lib-10k 是第 41 頁），檢查 keyset 分頁不會變慢。
	cur := first.NextCursor
	for range 40 {
		if cur == "" {
			break
		}
		p := s.listPage(b, "limit=200&cursor="+url.QueryEscape(cur))
		if p.NextCursor == "" {
			break
		}
		cur = p.NextCursor
	}
	deep := "limit=200"
	if cur != "" {
		deep += "&cursor=" + url.QueryEscape(cur)
	}
	for b.Loop() {
		runQueries(b, s, c, "NFR-PERF-1", name, []queryCase{
			{"first_page", "/api/items?limit=200", true},
			{"deep_page", "/api/items?" + deep, true},
			{"platform", "/api/items?platform=youtube&limit=200", true},
			{"channel", "/api/items?channel=" + url.QueryEscape(ch) + "&limit=200", true},
			{"status", "/api/items?status=normalize:done&status=segment:done&status=segment:failed&limit=200", true},
			{"failed", "/api/items?status=segment:failed&limit=200", false},
			{"timing", "/api/items?timing=interpolated&limit=200", true},
			{"lang_duration", "/api/items?lang=en&duration_min=600&duration_max=3600&limit=200", true},
			{"published", "/api/items?published_from=2025-01-02T00:00:00Z&published_to=2025-01-20T00:00:00Z&limit=200", true},
			{"read_state", "/api/items?read_state=unread&limit=200", true},
			{"all_filters", "/api/items?platform=youtube&lang=en&timing=interpolated&status=normalize:done&duration_min=10&q=postgresql&limit=200", true},
			{"group_channel", "/api/items?group_by=channel&limit=200", true},
			{"group_published", "/api/items?group_by=published&limit=200", true},
			{"group_status", "/api/items?group_by=status&limit=200", true},
		}, 3, 50, 200*time.Millisecond)
	}
}

// NFR-PERF-3：標題搜尋（LIB-R3，含 2 字以下的掃描）在 lib-10k 上 p95 < 300ms。
func BenchmarkPerf3TitleSearch(b *testing.B) {
	dir, name := lib(b)
	s := startServer(b, dir, nil)
	c := newChecker(b)
	for b.Loop() {
		runQueries(b, s, c, "NFR-PERF-3", name, []queryCase{
			{"common", "/api/items?q=agile&limit=200", true},
			{"common_zh", "/api/items?q=" + url.QueryEscape("測試策略") + "&limit=200", true},
			{"rare", "/api/items?q=" + url.QueryEscape("video 42 about") + "&limit=200", true},
			{"none", "/api/items?q=zzzzqqqq&limit=200", false},
			{"short_1", "/api/items?q=" + url.QueryEscape("與") + "&limit=200", true},
			{"short_2", "/api/items?q=ag&limit=200", true},
			{"q_and_filter", "/api/items?q=rust&platform=bilibili&group_by=channel&limit=200", true},
		}, 3, 50, 300*time.Millisecond)
	}
}

// NFR-PERF-9：全文搜尋在 lib-10k 上 p95 < 500ms；2 字以下的掃描查詢 p95 < 2 秒。
// 查詢詞見 internal/benchdata 的詞彙（決定論，每晚的命中數相同）。單頁用 API 的預設 20 筆（Web UI 的用法）。
// 命中很多的查詢（常見詞、3 字中文、以語言篩選常見詞）與 2 字以下的掃描在第一輪最佳化之後仍未達標，
// 第二輪（search_groups、search_text，SRCH S9）之後搬回這裡（nfr.md 第 10 節）。
func BenchmarkPerf9FullTextSearch(b *testing.B) {
	perf9(b, []queryCase{
		{"rare", perf9Q(benchdata.RareTerm), true},
		{"phrase", perf9Q("the system"), false},
		{"none", perf9Q("qqqzzzxxx"), false},
		{"common", perf9Q(benchdata.CommonTerm), true},
		{"zh_3", perf9Q("記憶體"), true},
		{"lang", perf9Q(benchdata.CommonTerm) + "&lang=en", true},
	}, []queryCase{
		{"short_en", perf9Q(benchdata.ShortTermEN), true},
		{"short_zh", perf9Q(benchdata.ShortTermZH), true},
		{"short_1", perf9Q("的"), false},
	})
}

func perf9Q(v string) string { return "/api/search?q=" + url.QueryEscape(v) }

func perf9(b *testing.B, indexed, short []queryCase) {
	dir, name := lib(b)
	s := startServer(b, dir, nil)
	c := newChecker(b)
	for b.Loop() {
		runQueries(b, s, c, "NFR-PERF-9", name, indexed, 3, 50, 500*time.Millisecond)
		runQueries(b, s, c, "NFR-PERF-9", name, short, 2, 30, 2*time.Second)
	}
}

// NFR-PERF-8：CLI 獨立模式的 items ls --json 在 lib-10k 上 < 1 秒。執行真正建置的 binary（沒有 serve 在跑，
// 所以是獨立模式）；先執行一次（不計），之後 10 次的最大值判定。
func BenchmarkPerf8CLIItemsLs(b *testing.B) {
	requireLinux(b)
	dir, name := lib(b)
	root := shortDir(b)
	fakeTools(b, root)
	if err := writeNoNetConfig(root); err != nil {
		b.Fatal(err)
	}
	run := func() error {
		cmd := exec.Command(binary(b), "items", "ls", "--json")
		cmd.Env = binEnv(root, dir, "")
		var errb limitedBuffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("items ls --json：%v\n%s", err, errb.String())
		}
		if !json.Valid(out) || len(out) < 100 {
			return fmt.Errorf("items ls --json 的輸出不是 JSON：%s", truncate(out))
		}
		return nil
	}
	c := newChecker(b)
	for b.Loop() {
		st := sample(b, 1, 10, run)
		c.checkLatency("NFR-PERF-8", "items_ls_json", name, "max", st, time.Second)
	}
}

// limitedBuffer 只留前 8KB（stderr 用）。
type limitedBuffer struct{ b []byte }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 8<<10 - len(l.b); room > 0 {
		l.b = append(l.b, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return string(l.b) }
