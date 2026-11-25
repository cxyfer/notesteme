package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentAPIWritesNeverBusy 是 NFR-REL-4 的 API 層：多個用戶端同時經 HTTP 寫入（新增影音、改佇列的並行上限、
// 暫停與繼續、改全域設定），同時有讀取者與備份（VACUUM INTO，另一條連線）。每個請求都要成功（沒有 5xx），
// log 中沒有 SQLITE_BUSY／database is locked，最後的資料與寫入的一致。
func TestConcurrentAPIWritesNeverBusy(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true // 只驗證寫入路徑；任務留在佇列中，不執行
	var logs syncBuffer
	env.Log = &logs
	srv, err := StartServer(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Kill)
	c := login(t, srv)

	const (
		clients   = 8
		perClient = 10
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var (
		mu   sync.Mutex
		errs []string
	)
	fail := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, fmt.Sprintf(format, args...))
	}
	send := func(method, path, body string) {
		req, err := http.NewRequestWithContext(ctx, method, "http://"+c.addr+path, strings.NewReader(body))
		if err != nil {
			fail("%s %s：%v", method, path, err)
			return
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, ck := range c.cook {
			req.AddCookie(ck)
		}
		req.Header.Set("X-CSRF-Token", c.csrf)
		resp, err := (&http.Client{Transport: c.tr}).Do(req)
		if err != nil {
			fail("%s %s：%v", method, path, err)
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			fail("%s %s = %d %s", method, path, resp.StatusCode, b)
		}
	}

	var writers, bg sync.WaitGroup
	stop := make(chan struct{})
	for k := range clients {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := range perClient {
				send("POST", "/api/items", fmt.Sprintf(`{"url":"https://www.youtube.com/watch?v=c%02di%02dxxxxx"}`, k, i))
				switch i % 3 {
				case 0:
					send("PATCH", "/api/queue/limits", fmt.Sprintf(`{"download":%d}`, 1+(k+i)%4))
				case 1:
					send("POST", "/api/queue/pause", `{"class":"frames"}`)
					send("POST", "/api/queue/resume", `{"class":"frames"}`)
				case 2:
					send("PUT", "/api/settings/language", `{"targets":["zh-Hant-TW"]}`)
				}
			}
		}()
	}
	for range 3 {
		bg.Add(1)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				send("GET", "/api/items", "")
				send("GET", "/api/queue", "")
			}
		}()
	}
	bg.Add(1)
	go func() {
		defer bg.Done()
		dir := t.TempDir()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := srv.store.SnapshotTo(ctx, filepath.Join(dir, fmt.Sprintf("snap-%d.db", i%2))); err != nil {
				fail("備份：%v", err)
			}
		}
	}()
	writers.Wait()
	close(stop)
	bg.Wait()
	if ctx.Err() != nil {
		t.Fatal("並行寫入在 2 分鐘內沒有做完（疑似卡住）")
	}
	for _, e := range errs {
		t.Error(e)
	}
	if l := strings.ToLower(logs.String()); strings.Contains(l, "database is locked") || strings.Contains(l, "sqlite_busy") {
		t.Errorf("log 中出現 SQLITE_BUSY（NFR-REL-4）")
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if code := c.json("GET", "/api/items?limit=200", "", &list); code != 200 || len(list.Items) != clients*perClient {
		t.Errorf("GET /api/items = %d，%d 部影音，應該是 %d 部", code, len(list.Items), clients*perClient)
	}
	var settings map[string]any
	if code := c.json("GET", "/api/settings/language", "", &settings); code != 200 {
		t.Errorf("GET /api/settings/language = %d", code)
	} else if b, _ := json.Marshal(settings["targets"]); string(b) != `["zh-Hant-TW"]` {
		t.Errorf("language.targets = %s", b)
	}
}

// syncBuffer 是並行安全的 log 輸出。
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
