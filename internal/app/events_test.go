package app

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/events"
)

// newEventsServer 建立只有 SSE 所需部分的 Server，以及掛上 handler 的測試 server。
func newEventsServer(t *testing.T) (*Server, *clock.Fake, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	s := &Server{root: ctx}
	s.events = eventsState{bus: events.New(4), clock: clk}
	ts := httptest.NewServer(s.eventsHandler())
	t.Cleanup(func() { cancel(); ts.Close() })
	return s, clk, ts
}

// readUntil 讀到第一個包含 want 的行；等不到時回報失敗。
func readUntil(t *testing.T, r *bufio.Reader, want string) string {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if strings.Contains(line, want) {
			return line
		}
		if err != nil {
			t.Fatalf("等不到 %q：%v", want, err)
		}
	}
}

func TestEventsHandlerStreamsEventsAfterOk(t *testing.T) {
	s, _, ts := newEventsServer(t)
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	readUntil(t, r, ": ok")

	s.events.bus.Publish(events.Event{Type: events.TypeStageStatus, Item: "yt:a", ItemID: "i", Stage: "metadata", Status: "pending"})
	readUntil(t, r, "event: stage.status")
	data := readUntil(t, r, "data:")
	for _, want := range []string{`"item":"yt:a"`, `"stage":"metadata"`, `"status":"pending"`} {
		if !strings.Contains(data, want) {
			t.Errorf("data = %s，缺少 %s", data, want)
		}
	}
}

func TestEventsHandlerHeartbeatUsesInjectedClock(t *testing.T) {
	_, clk, ts := newEventsServer(t)
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readUntil(t, r, ": ok")

	// 等 handler 建好計時器，再推進 fake clock。
	deadline := time.Now().Add(2 * time.Second)
	for clk.PendingTimers() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler 沒有建立 heartbeat 計時器")
		}
		time.Sleep(time.Millisecond)
	}
	clk.Advance(heartbeatInterval)
	readUntil(t, r, ": ping")
}

func TestStopEventsEndsStreamsAndRefusesNewOnes(t *testing.T) {
	s, _, ts := newEventsServer(t)
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readUntil(t, r, ": ok")

	s.stopEvents()
	// 串流應該結束：讀到 EOF，而不是卡住。
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopEvents 後串流沒有結束")
	}

	resp2, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("關閉後的新連線 = %d，預期 503", resp2.StatusCode)
	}
}
