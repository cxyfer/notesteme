package nfrbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/benchdata"
)

// NFR-RES-1：serve 閒置時 RSS < 150MB；處理中 < 500MB，不含子程序（ffmpeg、yt-dlp）。
// 執行真正建置的 binary，讀 /proc/<pid>/status：閒置取就緒後 10 秒內每 200ms 的 VmRSS 最大值；
// 處理中取 VmHWM（程序的 RSS 峰值，涵蓋整段處理），處理的是 20 部有 3 小時人工字幕（3,000 句）的影片，
// yt-dlp 是 shell script 的替身（不連網路，它的 RSS 屬於子程序，不計入）。
func BenchmarkRes1RSS(b *testing.B) {
	requireLinux(b)
	root := shortDir(b)
	if err := writeFakeYtDlpScript(root, benchdata.ItemLong.CuesPerItem); err != nil {
		b.Fatal(err)
	}
	if err := writeNoNetConfig(root); err != nil {
		b.Fatal(err)
	}
	data := root + "/data"
	c := newChecker(b)
	for b.Loop() {
		p := startBinary(b, root, data)
		p.waitReady(b, time.Now(), 60*time.Second)
		pid := p.cmd.Process.Pid
		time.Sleep(5 * time.Second)
		var idle int64
		for range 50 {
			v, err := procStatus(pid, "VmRSS")
			if err != nil {
				b.Fatal(err)
			}
			idle = max(idle, v)
			time.Sleep(200 * time.Millisecond)
		}
		c.check(result{ID: "NFR-RES-1", Case: "idle", Metric: "max_vmrss", Value: float64(idle) / 1024, Threshold: 150, Unit: "MB", Data: "empty"})

		hc := &http.Client{Timeout: 30 * time.Second}
		const n = 20
		for i := range n {
			body := fmt.Sprintf(`{"url":"https://www.youtube.com/watch?v=res1%07d"}`, i)
			resp, err := hc.Post(p.base+"/api/items", "application/json", strings.NewReader(body))
			if err != nil {
				b.Fatal(err)
			}
			out, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				b.Fatalf("POST /api/items = %d：%s", resp.StatusCode, truncate(out))
			}
		}
		var sampled int64
		deadline := time.Now().Add(5 * time.Minute)
		for done := 0; done < n; {
			if time.Now().After(deadline) {
				b.Fatalf("5 分鐘內只完成 %d/%d 部；log：\n%s", done, n, p.tail())
			}
			if v, err := procStatus(pid, "VmRSS"); err == nil {
				sampled = max(sampled, v)
			}
			resp, err := hc.Get(p.base + "/api/items?limit=1&status=normalize:done")
			if err != nil {
				b.Fatal(err)
			}
			var page struct {
				Total int `json:"total"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&page)
			resp.Body.Close()
			done = page.Total
			time.Sleep(20 * time.Millisecond)
		}
		hwm, err := procStatus(pid, "VmHWM")
		if err != nil {
			b.Fatal(err)
		}
		c.check(result{ID: "NFR-RES-1", Case: "processing", Metric: "vmhwm", Value: float64(max(hwm, sampled)) / 1024, Threshold: 500, Unit: "MB",
			Data: "20×item-long ingest", Note: fmt.Sprintf("（取樣的 VmRSS 最大 %.1fMB）", float64(sampled)/1024)})
		p.stop(b)
	}
}

// NFR-RES-2：佇列沒有任務時 CPU 使用率 < 1%。對 /proc/<pid>/stat 的 utime＋stime 取樣
// NOTESTEME_BENCH_IDLE_SECONDS 秒（預設 30，bench.yml 用 60），從就緒後 5 秒開始。
func BenchmarkRes2IdleCPU(b *testing.B) {
	requireLinux(b)
	secs := 30
	if v, err := strconv.Atoi(os.Getenv("NOTESTEME_BENCH_IDLE_SECONDS")); err == nil && v >= 30 {
		secs = v
	}
	data, _ := itemLong(b)
	root := shortDir(b)
	fakeTools(b, root)
	if err := writeNoNetConfig(root); err != nil {
		b.Fatal(err)
	}
	c := newChecker(b)
	for b.Loop() {
		p := startBinary(b, root, data)
		p.waitReady(b, time.Now(), 60*time.Second)
		pid := p.cmd.Process.Pid
		time.Sleep(5 * time.Second)
		t0, err := procCPUTicks(pid)
		if err != nil {
			b.Fatal(err)
		}
		start := time.Now()
		time.Sleep(time.Duration(secs) * time.Second)
		t1, err := procCPUTicks(pid)
		if err != nil {
			b.Fatal(err)
		}
		wall := time.Since(start).Seconds()
		pct := float64(t1-t0) / clockTicks() / wall * 100
		c.check(result{ID: "NFR-RES-2", Case: "idle", Metric: "cpu", Value: pct, Threshold: 1, Unit: "%", Data: "item-long",
			Note: fmt.Sprintf("（%d ticks／%.0f 秒）", t1-t0, wall)})
		p.stop(b)
	}
}

// NFR-RES-3：字幕渲染、匯出與下載都以串流輸出，記憶體用量不隨片長線性成長。
// 同一個 server 對 1 小時（1,000 句）與 10 小時（10,000 句）的影片各下載數次，量請求期間存活 heap 的峰值
// 比請求前多出多少（heapGrowthDuring），取 3 次的最小值。片長 10 倍時，線性成長約是 10 倍。
// 判定（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：10 小時的增量 ÷ max(1 小時的增量, 512KB) < 3，
// 即成長不超過線性的 30%；512KB 是這個量法的解析度（請求期間 in-flight 的緩衝與 server 背景 goroutine 的配置，
// 實測單軌串流在 1、10、100 小時都落在 0.1～0.9MB）。
//
// 單軌的格式（SRT、VTT、純文字、cues JSON）第一輪就改成逐句串流；ASS、雙語 SRT（兩軌以時間視窗合併）與 bundle 匯出
// （逐檔逐段寫進 zip）在第二輪改成串流之後也搬回這裡（nfr.md 第 10 節）。
func BenchmarkRes3Streaming(b *testing.B) {
	res3(b, []string{"srt", "vtt", "txt", "cues-json", "ass", "bilingual-srt", "bundle"})
}

// res3Floor 是 heapGrowthDuring 的解析度。
const res3Floor = 512 << 10

func res3(b *testing.B, formats []string) {
	root := shortDir(b)
	data := root + "/data"
	ids := map[string]string{}
	for _, l := range []struct {
		name string
		cues int
	}{{"1h", 1000}, {"10h", 10000}} {
		p := benchdata.ItemLong
		p.Name, p.CuesPerItem, p.Seed = "res3-"+l.name, l.cues, uint64(l.cues)
		res, err := benchdata.Generate(context.Background(), data, p, benchdata.Options{})
		if err != nil {
			b.Fatal(err)
		}
		ids[l.name] = res.ItemIDs[0]
	}
	s := startServer(b, data, nil)
	c := newChecker(b)
	for b.Loop() {
		for _, f := range formats {
			peak := map[string]uint64{}
			for _, l := range []string{"1h", "10h"} {
				path := "/api/items/" + ids[l] + "/subtitles?format=" + f
				if f == "bundle" {
					path = "/api/items/" + ids[l] + "/bundle"
				}
				// 先暖身一次（樣式、prepared statement 等一次性的配置不算進去），取 3 次的最小值降低雜訊。
				if _, err := s.get(path, http.StatusOK); err != nil {
					b.Fatal(err)
				}
				best := uint64(1 << 62)
				for range 3 {
					v, err := heapGrowthDuring(func() error {
						resp, err := s.hc.Get(s.base + path)
						if err != nil {
							return err
						}
						defer resp.Body.Close()
						_, err = io.Copy(io.Discard, resp.Body)
						if err == nil && resp.StatusCode != http.StatusOK {
							err = fmt.Errorf("%s = %d", path, resp.StatusCode)
						}
						return err
					})
					if err != nil {
						b.Fatal(err)
					}
					best = min(best, v)
				}
				peak[l] = best
			}
			ratio := float64(peak["10h"]) / float64(max(peak["1h"], res3Floor))
			b.ReportMetric(float64(peak["10h"])/(1<<20), f+"_10h_peak_MB")
			c.check(result{ID: "NFR-RES-3", Case: f, Metric: "peak_ratio_10h_1h", Value: ratio, Threshold: 3, Unit: "x", Data: "1h vs 10h",
				Note: fmt.Sprintf("（1h +%.2fMB、10h +%.2fMB）", float64(peak["1h"])/(1<<20), float64(peak["10h"])/(1<<20))})
		}
	}
}

// heapGrowthDuring 回傳 fn 執行期間存活 heap 的峰值減去執行前的值。請求期間另一個 goroutine 反覆執行完整的 GC，
// 每次 GC 之後讀 /gc/heap/live:bytes（標記為存活的資料量），所以量到的是請求同時握著的資料，不受 GC 時機影響。
func heapGrowthDuring(fn func() error) (uint64, error) {
	read := func() uint64 {
		runtime.GC()
		s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		metrics.Read(s)
		return s[0].Value.Uint64()
	}
	base := read()
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if v := read(); v > peak.Load() {
				peak.Store(v)
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	err := fn()
	close(stop)
	wg.Wait()
	if peak.Load() < base {
		return 0, err
	}
	return peak.Load() - base, err
}
