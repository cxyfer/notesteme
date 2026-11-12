package steps

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
)

// eventWait 是等待 SSE 就緒或事件到達的真實時間上限，只用來防止卡死（不依賴 fake clock）。
const eventWait = 5 * time.Second

// eventSteps 是 `GET /api/events` 的訂閱狀態；每個場景一份（Initialize 為每個場景建立 ScenarioContext）。
type eventSteps struct {
	s *Scenario
	l *eventListener
}

// eventListener 持有一條 SSE 串流與已收到的事件。
type eventListener struct {
	cancel context.CancelFunc
	// ready 在讀到 `: ok` 註解時關閉。
	ready chan struct{}
	// done 在背景讀取結束時關閉。
	done chan struct{}

	mu sync.Mutex
	// events 是依序收到的事件 data（JSON）。
	events [][]byte
	// changed 在有新事件或串流結束時關閉並換成新的 channel，讓等待者醒來。
	changed chan struct{}
	err     error
	// status 與 body 只在回應不是 200 時有值。
	status int
	body   []byte
}

// registerEvents 註冊 SSE 事件的步驟，並在場景結束時關閉串流。
func registerEvents(r *Registry, s *Scenario) {
	e := &eventSteps{s: s}
	r.Step(`the owner is listening to events`, e.listen)
	r.Step(`an event is received:`, e.received)
	if r.sc != nil {
		r.sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			e.close()
			return ctx, nil
		})
	}
}

// listen 以 owner 的身分訂閱 GET /api/events，讀到 `: ok` 才返回。
func (e *eventSteps) listen() error {
	s := e.s
	if err := s.prepareOwner(); err != nil {
		return err
	}
	e.close()

	// SSE 是長連線：client 不能設 Timeout，改由 context 在場景結束時中斷。
	client := s.newClient(nil)
	client.Timeout = 0
	ctx, cancel := context.WithCancel(s.ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.server.Addr()+"/api/events", nil)
	if err != nil {
		cancel()
		return err
	}
	for name, v := range s.owner {
		req.AddCookie(&http.Cookie{Name: name, Value: v})
	}
	req.Header.Set("Accept", "text/event-stream")

	l := &eventListener{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), changed: make(chan struct{})}
	e.l = l
	respCh := make(chan *http.Response, 1)
	go func() {
		defer close(l.done)
		defer l.wake()
		resp, err := client.Do(req)
		if err != nil {
			l.fail(err)
			respCh <- nil
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			l.mu.Lock()
			l.status, l.body = resp.StatusCode, body
			l.mu.Unlock()
			respCh <- nil
			return
		}
		respCh <- resp
		l.read(resp.Body)
	}()

	timer := time.NewTimer(eventWait)
	defer timer.Stop()
	select {
	case <-l.ready:
		return nil
	case <-l.done:
		<-respCh
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.status == http.StatusNotImplemented {
			var body struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(l.body, &body) == nil && body.Code == "not_implemented" {
				return godog.ErrPending
			}
		}
		if l.status != 0 {
			return fmt.Errorf("GET /api/events 回應 HTTP %d，預期 200\n回應：%s", l.status, snippet(l.body))
		}
		return fmt.Errorf("事件串流在就緒前結束：%v", l.err)
	case <-timer.C:
		return fmt.Errorf("等了 %s 還沒收到 SSE 的 `: ok`", eventWait)
	}
}

// received 比對已收到的事件；沒有符合的就等新事件，最多等 eventWait。
func (e *eventSteps) received(doc *godog.DocString) error {
	if e.l == nil {
		return errors.New("還沒有訂閱事件（缺少 `the owner is listening to events`）")
	}
	l := e.l
	want := []byte(e.s.World.Subst(e.s.World.Expand(doc.Content)))

	timer := time.NewTimer(eventWait)
	defer timer.Stop()
	checked := 0
	var lastErr error
	for {
		l.mu.Lock()
		events, changed, ended, endErr := l.events[checked:], l.changed, isClosed(l.done), l.err
		checked = len(l.events)
		l.mu.Unlock()

		for _, ev := range events {
			err := jsonSubset(want, ev)
			if err == nil {
				return nil
			}
			lastErr = err
		}
		if ended {
			return fmt.Errorf("事件串流已結束，仍沒有符合的事件（%v）%s", endErr, e.dump(lastErr))
		}
		select {
		case <-changed:
		case <-timer.C:
			return fmt.Errorf("等了 %s 仍沒有符合的事件%s", eventWait, e.dump(lastErr))
		}
	}
}

// dump 列出目前收到的事件，放進失敗訊息。
func (e *eventSteps) dump(lastErr error) string {
	l := e.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return "\n目前沒有收到任何事件"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n已收到 %d 個事件，最後一次比對的差異：%v", len(l.events), lastErr)
	for _, ev := range l.events {
		fmt.Fprintf(&b, "\n  %s", snippet(ev))
	}
	return b.String()
}

// close 中斷串流並等背景讀取結束。
func (e *eventSteps) close() {
	if e.l == nil {
		return
	}
	e.l.cancel()
	<-e.l.done
	e.l = nil
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// wake 喚醒等待新事件的步驟。
func (l *eventListener) wake() {
	l.mu.Lock()
	defer l.mu.Unlock()
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *eventListener) fail(err error) {
	l.mu.Lock()
	l.err = err
	l.mu.Unlock()
}

// read 解析 SSE：`: ok` 註解代表就緒，空行結束一個事件，data 行串成事件內容。
func (l *eventListener) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data []string
	readyOnce := sync.Once{}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				l.mu.Lock()
				l.events = append(l.events, []byte(strings.Join(data, "\n")))
				l.mu.Unlock()
				l.wake()
				data = nil
			}
		case strings.HasPrefix(line, ":"):
			if strings.TrimSpace(line[1:]) == "ok" {
				readyOnce.Do(func() { close(l.ready) })
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
		l.fail(err)
	}
}
