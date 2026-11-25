package nfrbench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// NFR-PERF-2：GET /api/items/:id 與字幕句列表（/cues）在 item-long 上 p95 < 150ms。
func BenchmarkPerf2ItemDetail(b *testing.B) {
	data, id := itemLong(b)
	s := startServer(b, data, nil)
	c := newChecker(b)
	for b.Loop() {
		for _, tc := range []struct{ name, path string }{
			{"item", "/api/items/" + id},
			{"cues", "/api/items/" + id + "/cues"},
		} {
			st := sample(b, 5, 100, func() error {
				body, err := s.get(tc.path, http.StatusOK)
				if err == nil && tc.name == "cues" && !strings.Contains(string(body), `"index":3000`) {
					err = fmt.Errorf("/cues 沒有回傳全部 3,000 句")
				}
				return err
			})
			c.checkLatency("NFR-PERF-2", tc.name, "item-long", "p95", st, 150*time.Millisecond)
		}
	}
}

// NFR-PERF-4：字幕即時渲染（SRT、VTT、純文字、ASS 與雙語）在 item-long 上 < 100ms。
// 經過真正的下載端點（讀檔、驗證 sha256、解碼、渲染、寫出），比只量 internal/subtitle 的函式更嚴；
// 規格沒有寫百分位數，以 30 次取樣的最大值判定（最保守的解讀）。
func BenchmarkPerf4Render(b *testing.B) {
	data, id := itemLong(b)
	s := startServer(b, data, nil)
	c := newChecker(b)
	for b.Loop() {
		for _, f := range []string{"srt", "vtt", "txt", "ass", "bilingual-srt"} {
			path := "/api/items/" + id + "/subtitles?format=" + f
			st := sample(b, 3, 30, func() error {
				resp, err := s.hc.Get(s.base + path)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				n, err := countBytes(resp)
				if err != nil {
					return err
				}
				if resp.StatusCode != http.StatusOK || n < 100_000 {
					return fmt.Errorf("%s = %d（%d bytes）", path, resp.StatusCode, n)
				}
				if (f == "ass" || f == "bilingual-srt") && resp.Header.Get("X-Notesteme-Bilingual") != "true" {
					return fmt.Errorf("%s 不是雙語輸出", path)
				}
				return nil
			})
			c.checkLatency("NFR-PERF-4", f, "item-long", "max", st, 100*time.Millisecond)
		}
	}
}

func countBytes(resp *http.Response) (int64, error) {
	var n int64
	buf := make([]byte, 64<<10)
	for {
		k, err := resp.Body.Read(buf)
		n += int64(k)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, err
		}
	}
}

// NFR-PERF-5：步驟狀態改變到 SSE 事件送出 < 1 秒。
// 量的是「送出重跑請求」到「client 收到該步驟的 stage.status 事件」：狀態在請求處理中改變，
// 所以量到的值是上限（多含了一次 HTTP 請求）。以 20 次的最大值判定。
func BenchmarkPerf5SSE(b *testing.B) {
	data, id := itemLong(b)
	s := startServer(b, data, nil)
	c := newChecker(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api/events", nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("/api/events = %d", resp.StatusCode)
	}
	type ev struct {
		Type, ItemID, Stage, Status string
		at                          time.Time
	}
	events := make(chan ev, 256)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var e struct {
				Type   string `json:"type"`
				ItemID string `json:"item_id"`
				Stage  string `json:"stage"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(line), &e) == nil {
				events <- ev{e.Type, e.ItemID, e.Stage, e.Status, time.Now()}
			}
		}
		close(events)
	}()
	wait := func(match func(ev) bool) (ev, error) {
		timeout := time.After(30 * time.Second)
		for {
			select {
			case e, ok := <-events:
				if !ok {
					return ev{}, fmt.Errorf("SSE 串流中斷")
				}
				if match(e) {
					return e, nil
				}
			case <-timeout:
				return ev{}, fmt.Errorf("30 秒內沒有收到事件")
			}
		}
	}
	terminal := map[string]bool{"done": true, "failed": true, "skipped": true, "blocked": true}
	for b.Loop() {
		var ds []time.Duration
		for range 20 {
			start := time.Now()
			if _, err := s.post("/api/items/"+id+"/rerun", `{"from":"normalize","until":"normalize"}`, http.StatusAccepted); err != nil {
				b.Fatal(err)
			}
			e, err := wait(func(e ev) bool { return e.Type == "stage.status" && e.ItemID == id && e.Stage == "normalize" })
			if err != nil {
				b.Fatal(err)
			}
			ds = append(ds, e.at.Sub(start))
			if !terminal[e.Status] {
				if _, err := wait(func(e ev) bool {
					return e.Type == "stage.status" && e.ItemID == id && e.Stage == "normalize" && terminal[e.Status]
				}); err != nil {
					b.Fatal(err)
				}
			}
		}
		c.checkLatency("NFR-PERF-5", "rerun_to_event", "item-long", "max", summarize(ds), time.Second)
	}
}

// NFR-PERF-6：使用 fixture（不含網路）時，一部有人工字幕的 YouTube 影片從新增到可以下載 SRT < 5 秒。
// 真正的 worker 與 pipeline；yt-dlp 是 in-process 的替身（約 10 分鐘、200 句的人工字幕）。以 10 部的最大值判定。
func BenchmarkPerf6IngestToSRT(b *testing.B) {
	data := shortDir(b) + "/data"
	s := startServer(b, data, newYTRunner(200))
	c := newChecker(b)
	n := 0
	for b.Loop() {
		var ds []time.Duration
		for range 10 {
			n++
			vid := fmt.Sprintf("bench%06d", n)
			start := time.Now()
			body, err := s.post("/api/items", `{"url":"https://www.youtube.com/watch?v=`+vid+`"}`, http.StatusCreated)
			if err != nil {
				b.Fatal(err)
			}
			var out struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			if err := json.Unmarshal(body, &out); err != nil || len(out.Items) != 1 {
				b.Fatalf("POST /api/items：%v %s", err, body)
			}
			path := "/api/items/" + out.Items[0].ID + "/subtitles?format=srt"
			for {
				resp, err := s.hc.Get(s.base + path)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = countBytes(resp)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
				if time.Since(start) > 60*time.Second {
					detail, _ := s.get("/api/items/"+out.Items[0].ID, http.StatusOK)
					b.Fatalf("60 秒內沒有可下載的 SRT：%s", truncate(detail))
				}
				time.Sleep(5 * time.Millisecond)
			}
			ds = append(ds, time.Since(start))
		}
		c.checkLatency("NFR-PERF-6", "add_to_srt", "fixture", "max", summarize(ds), 5*time.Second)
	}
}

// NFR-PERF-7：serve 冷啟動到 /readyz 回傳 200 < 3 秒，不含 migration 與 yt-dlp 自我檢查。
// 執行真正建置的 binary：先啟動一次讓 migration 跑完，之後每次都是新的程序；/readyz 目前不做 yt-dlp 的自我檢查
// （OPS-R6 的 TODO），假的 ffmpeg／yt-dlp 只需要可以執行。以 10 次的最大值判定。
func BenchmarkPerf7ColdStart(b *testing.B) {
	requireLinux(b)
	data, _ := itemLong(b)
	root := shortDir(b)
	fakeTools(b, root)
	if err := writeNoNetConfig(root); err != nil {
		b.Fatal(err)
	}
	first := startBinary(b, root, data)
	first.waitReady(b, time.Now(), 60*time.Second)
	first.stop(b)
	c := newChecker(b)
	for b.Loop() {
		var ds []time.Duration
		for range 10 {
			start := time.Now()
			p := startBinary(b, root, data)
			ds = append(ds, p.waitReady(b, start, 30*time.Second))
			p.stop(b)
		}
		c.checkLatency("NFR-PERF-7", "start_to_readyz", "item-long", "max", summarize(ds), 3*time.Second)
	}
}
