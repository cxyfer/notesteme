package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// waitOpts 是 --wait 的選項。
type waitOpts struct {
	// until 是要等的步驟；空字串代表最後一個步驟。
	until   string
	timeout time.Duration
}

// verdict 是等待中的影音目前的狀況。數字越大越嚴重，多部影音取最大的。
type verdict int

const (
	verdictDone    verdict = iota // 指定的步驟完成
	verdictWaiting                // 還在進行，或在等待時間到、重試
	verdictBlocked                // 需要使用者處理
	verdictFailed                 // 失敗
)

// exitCode 把 verdict 對應成結束碼（CLI-R6）。
func (v verdict) exitCode() int {
	switch v {
	case verdictDone:
		return exitOK
	case verdictBlocked:
		return exitBlocked
	case verdictFailed:
		return exitError
	}
	return exitWaiting
}

// judge 判斷影音相對於 until 的狀況（CLI-R7）：
// 從第一個步驟到 until（沒有指定時是最後一個步驟），任何一個 failed 或 blocked 都會讓後面的步驟停住，
// 所以先看它們；之後 until 這個步驟是 done 才算完成，其他狀態都繼續等。
func judge(v itemView, until string) (verdict, *stageView) {
	status := map[string]*stageView{}
	for i := range v.Stages {
		status[v.Stages[i].Stage] = &v.Stages[i]
	}
	target := until
	if target == "" {
		// 沒有指定 until 時等最後一個步驟；M1 建立的影音沒有後面的步驟，
		// 改用實際存在的最後一個步驟，否則這種影音永遠等不到（會一直回 waiting）。
		target = pipeline.Stages[len(pipeline.Stages)-1]
		if status[target] == nil {
			for i := len(pipeline.Stages) - 1; i >= 0; i-- {
				if status[pipeline.Stages[i]] != nil {
					target = pipeline.Stages[i]
					break
				}
			}
		}
	}
	for _, name := range pipeline.Stages {
		if st := status[name]; st != nil {
			switch st.Status {
			case store.StatusFailed:
				return verdictFailed, st
			case store.StatusBlocked:
				return verdictBlocked, st
			}
		}
		if name == target {
			break
		}
	}
	if st := status[target]; st != nil && (st.Status == store.StatusDone || st.Status == store.StatusSkipped) {
		return verdictDone, st
	}
	return verdictWaiting, status[target]
}

// snapshot 是一次取得全部影音的狀況。
type snapshot struct {
	items []itemView
	v     verdict
}

func (s *session) snapshot(ctx context.Context, ids []ingested, until string) (snapshot, error) {
	var sn snapshot
	for _, it := range ids {
		v, _, err := s.fetchItem(ctx, it.id)
		if err != nil {
			return sn, err
		}
		sn.items = append(sn.items, v)
		if j, _ := judge(v, until); j > sn.v {
			sn.v = j
		}
	}
	return sn, nil
}

// waitFor 等到所有影音的指定步驟完成或停住，回傳結束碼；blocked 與 failed 的說明會印出來。
// 獨立模式自己執行任務；client 模式訂閱 serve 的 SSE，由 serve 的 worker 執行。
func (s *session) waitFor(ctx context.Context, ids []ingested, opts waitOpts) (int, error) {
	var (
		sn  snapshot
		err error
	)
	if s.be.standalone != nil {
		sn, err = s.waitStandalone(ctx, ids, opts)
	} else {
		sn, err = s.waitClient(ctx, ids, opts)
	}
	if err != nil {
		return 0, err
	}
	s.reportStopped(sn, opts.until)
	return sn.v.exitCode(), nil
}

// waitStandalone 反覆「執行任務到閒置 → 取得影音 → 判斷」。閒置之後仍然沒有結果時：
// 有任務在等未來的時間（重試的退避、等待字幕），而且在 --timeout 之內，就等到那個時間再繼續；
// 否則回報目前的狀況（waiting 為 4）。
//
// --timeout 在任務執行期間也要生效：用注入的 clock 建立計時器，時間到就取消 tctx，
// 執行中的任務隨之被取消（租約由 Close 的 Drain 放回佇列），結果是 waiting（結束碼 4），不是錯誤。
func (s *session) waitStandalone(ctx context.Context, ids []ingested, opts waitOpts) (snapshot, error) {
	deadline := s.clock.Now().Add(opts.timeout)
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := s.clock.NewTimer(opts.timeout)
	defer timer.Stop()
	go func() {
		select {
		case <-timer.C():
			cancel()
		case <-tctx.Done():
		}
	}()
	// timedOut 只在 --timeout 到期時為 true；使用者中斷（ctx 取消）仍然是錯誤。
	timedOut := func() bool { return ctx.Err() == nil && tctx.Err() != nil }

	for {
		if err := s.be.standalone.RunUntilIdle(tctx); err != nil && !timedOut() {
			return snapshot{}, fmt.Errorf("執行任務失敗：%w", err)
		}
		sn, err := s.snapshot(ctx, ids, opts.until)
		if err != nil || sn.v != verdictWaiting || timedOut() {
			return sn, err
		}
		now := s.clock.Now()
		next, ok := earliestNotBefore(sn.items, now)
		if !ok || next.After(deadline) {
			return sn, nil
		}
		t := s.clock.NewTimer(next.Sub(now))
		select {
		case <-t.C():
		case <-tctx.Done():
			t.Stop()
			if timedOut() {
				return sn, nil
			}
			return sn, ctx.Err()
		}
	}
}

// earliestNotBefore 回傳還沒到期的 not_before 中最早的一個。
func earliestNotBefore(items []itemView, now time.Time) (time.Time, bool) {
	var best time.Time
	for _, it := range items {
		for _, st := range it.Stages {
			if st.NotBefore == nil || !st.NotBefore.After(now) {
				continue
			}
			if st.Status != store.StatusPending && st.Status != store.StatusWaiting {
				continue
			}
			if best.IsZero() || st.NotBefore.Before(best) {
				best = *st.NotBefore
			}
		}
	}
	return best, !best.IsZero()
}

// waitClient 先訂閱 SSE，再取得影音，之後每收到事件就重新取得，直到有結果或超過 --timeout（用注入的 clock）。
func (s *session) waitClient(ctx context.Context, ids []ingested, opts waitOpts) (snapshot, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ev, err := s.subscribe(ctx)
	if err != nil {
		return snapshot{}, err
	}
	defer ev.close()

	timer := s.clock.NewTimer(opts.timeout)
	defer timer.Stop()
	for {
		sn, err := s.snapshot(ctx, ids, opts.until)
		if err != nil || sn.v != verdictWaiting {
			return sn, err
		}
		select {
		case <-ev.changed:
		case <-timer.C():
			return sn, nil
		case <-ev.done:
			// 串流結束：可能是 serve 關閉。最後再看一次狀況再決定。
			if err := ev.err(); err != nil {
				return sn, fmt.Errorf("事件串流中斷：%w", err)
			}
			return sn, errors.New("事件串流已結束")
		case <-ctx.Done():
			return sn, ctx.Err()
		}
	}
}

// eventStream 是 GET /api/events 的訂閱：每個事件都會通知 changed。
type eventStream struct {
	body    io.Closer
	changed chan struct{}
	done    chan struct{}

	mu     sync.Mutex
	readEr error
}

func (e *eventStream) close() {
	e.body.Close()
	<-e.done
}

func (e *eventStream) err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.readEr
}

// subscribe 訂閱 SSE，讀到 `: ok`（server 已經登記訂閱）才返回，之後的變更一定收得到。
func (s *session) subscribe(ctx context.Context) (*eventStream, error) {
	resp, err := s.be.open(ctx, http.MethodGet, "/api/events", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, errorFromResponse(resp.StatusCode, raw)
	}
	e := &eventStream{body: resp.Body, changed: make(chan struct{}, 1), done: make(chan struct{})}
	ready := make(chan struct{})
	go func() {
		defer close(e.done)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var once sync.Once
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ":"):
				if strings.TrimSpace(line[1:]) == "ok" {
					once.Do(func() { close(ready) })
				}
			case line == "":
				// 事件結束；沒有資料的空行（例如註解後的）也只是多喚醒一次，無害。
				select {
				case e.changed <- struct{}{}:
				default:
				}
			}
		}
		if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
			e.mu.Lock()
			e.readEr = err
			e.mu.Unlock()
		}
	}()
	select {
	case <-ready:
		return e, nil
	case <-e.done:
		return nil, errors.New("事件串流在就緒前結束")
	case <-ctx.Done():
		e.close()
		return nil, ctx.Err()
	}
}

// reportStopped 把停住的步驟說明印出來：blocked 要說明需要做什麼（訊息與動作），failed 說明原因。
// blocked 是給人看的結果，文字模式印到 stdout；failed 與還在等待的說明印到 stderr。
// --json 時 stdout 只有 JSON，全部印到 stderr。
func (s *session) reportStopped(sn snapshot, until string) {
	for _, it := range sn.items {
		j, st := judge(it, until)
		if st == nil {
			continue
		}
		switch j {
		case verdictBlocked:
			w := s.stdout
			if s.g.json {
				w = s.stderr
			}
			fmt.Fprintf(w, "%s 的步驟 %s 需要你處理：%s\n", it.label(), st.Stage, st.Message)
			if len(st.Actions) > 0 {
				fmt.Fprintf(w, "可以做的事：%s\n", strings.Join(st.Actions, "、"))
			}
		case verdictFailed:
			fmt.Fprintf(s.stderr, "%s 的步驟 %s 失敗（%s）：%s\n", it.label(), st.Stage, st.ErrorClass, st.Message)
		case verdictWaiting:
			fmt.Fprintf(s.stderr, "%s 的步驟 %s 還沒完成（%s）。\n", it.label(), st.Stage, st.Status)
		}
	}
}
