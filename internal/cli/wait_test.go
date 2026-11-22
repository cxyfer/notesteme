package cli

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

func stagesOf(pairs ...string) []stageView {
	var out []stageView
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, stageView{Stage: pairs[i], Status: pairs[i+1]})
	}
	return out
}

// stagesFor 依 pipeline.Stages 產生所有步驟，狀態都是 status；last 不為空時，最後一個步驟改用 last。
func stagesFor(status, last string) []stageView {
	out := make([]stageView, 0, len(pipeline.Stages))
	for _, name := range pipeline.Stages {
		out = append(out, stageView{Stage: name, Status: status})
	}
	if last != "" {
		out[len(out)-1].Status = last
	}
	return out
}

func TestJudge(t *testing.T) {
	cases := []struct {
		name   string
		stages []stageView
		until  string
		want   verdict
		code   int
	}{
		{"指定步驟完成", stagesOf("metadata", "done", "transcript", "done"), "transcript", verdictDone, 0},
		{"沒有 until 時看最後一個步驟", stagesOf("metadata", "done", "transcript", "done", "normalize", "pending"), "", verdictWaiting, 4},
		// 以 pipeline.Stages 產生，不論步驟有幾個都成立（最後一個步驟 skipped 也算完成）。
		{"最後一個步驟完成", stagesFor("done", ""), "", verdictDone, 0},
		{"最後一個步驟 skipped", stagesFor("done", "skipped"), "", verdictDone, 0},
		{"最後一個步驟 pending", stagesFor("done", "pending"), "", verdictWaiting, 4},
		// M1 建立的影音沒有 segment 之後的步驟：等「最後一個步驟」時改用實際存在的最後一個。
		{"舊資料沒有後面的步驟，最後一個完成", stagesOf("metadata", "done", "transcript", "done", "normalize", "done"), "", verdictDone, 0},
		{"舊資料沒有後面的步驟，最後一個還沒完成", stagesOf("metadata", "done", "transcript", "pending"), "", verdictWaiting, 4},
		{"舊資料的後面步驟 failed", stagesOf("metadata", "done", "transcript", "failed"), "", verdictFailed, 1},
		{"skipped 算完成", stagesOf("metadata", "done", "transcript", "skipped"), "transcript", verdictDone, 0},
		{"指定步驟 blocked", stagesOf("metadata", "done", "transcript", "blocked"), "transcript", verdictBlocked, 3},
		{"指定步驟 failed", stagesOf("metadata", "done", "transcript", "failed"), "transcript", verdictFailed, 1},
		{"前面的步驟 failed", stagesOf("metadata", "failed"), "transcript", verdictFailed, 1},
		{"前面的步驟 blocked，後面還沒建立", stagesOf("metadata", "done", "transcript", "blocked"), "", verdictBlocked, 3},
		{"until 之後的步驟不影響", stagesOf("metadata", "done", "transcript", "done", "normalize", "failed"), "transcript", verdictDone, 0},
		{"waiting", stagesOf("metadata", "done", "transcript", "waiting"), "transcript", verdictWaiting, 4},
		{"running", stagesOf("metadata", "running"), "metadata", verdictWaiting, 4},
		{"還沒有步驟", nil, "metadata", verdictWaiting, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := judge(itemView{Stages: c.stages}, c.until)
			if got != c.want || got.exitCode() != c.code {
				t.Errorf("judge = %v（結束碼 %d），預期 %v（%d）", got, got.exitCode(), c.want, c.code)
			}
		})
	}
}

func TestExitFor(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{usagef("x"), 2},
		{&apiError{Status: 400}, 2},
		{&apiError{Status: 422, Code: "invalid_request"}, 2},
		{&apiError{Status: 401}, 1}, {&apiError{Status: 403}, 1}, {&apiError{Status: 404}, 1},
		{&apiError{Status: 500}, 1}, {&apiError{Status: 501, Code: "not_implemented"}, 1},
		{fmt.Errorf("連線失敗"), 1},
	}
	for _, c := range cases {
		if got, _ := exitFor(c.err); got != c.code {
			t.Errorf("exitFor(%v) = %d，預期 %d", c.err, got, c.code)
		}
	}
}

// waitAPI 是 client 模式 --wait 的對象：item 的狀態由 set 切換，SSE 的事件由 emit 送出。
type waitAPI struct {
	mu       sync.Mutex
	stages   string // transcript 步驟的 JSON 欄位
	gets     int
	events   chan struct{}
	subbed   chan struct{}
	onFirstG func()
}

func newWaitAPI(t *testing.T, transcript string) (*waitAPI, string) {
	t.Helper()
	w := &waitAPI{stages: transcript, events: make(chan struct{}, 8), subbed: make(chan struct{})}
	var once sync.Once
	_, url := newFakeAPI(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/items":
			writeJSON(rw, 201, `{"items":[{"id":"i1","ref":"yt:abc123xxxxx","already_in_library":false}]}`)
		case r.URL.Path == "/api/events":
			rw.Header().Set("Content-Type", "text/event-stream")
			rw.WriteHeader(200)
			fl := rw.(http.Flusher)
			fmt.Fprint(rw, ": ok\n\n")
			fl.Flush()
			once.Do(func() { close(w.subbed) })
			for {
				select {
				case <-w.events:
					fmt.Fprint(rw, "event: stage.changed\ndata: {}\n\n")
					fl.Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			w.mu.Lock()
			w.gets++
			first, body := w.gets == 1, w.stages
			w.mu.Unlock()
			writeJSON(rw, 200, `{"id":"i1","ref":"yt:abc123xxxxx","title":"T","stages":[{"stage":"metadata","status":"done"},`+body+`]}`)
			if first && w.onFirstG != nil {
				w.onFirstG()
			}
		}
	})
	return w, url
}

func (w *waitAPI) set(transcript string) {
	w.mu.Lock()
	w.stages = transcript
	w.mu.Unlock()
}

func (w *waitAPI) emit() { w.events <- struct{}{} }

const (
	transcriptPending = `{"stage":"transcript","status":"pending"}`
	transcriptDone    = `{"stage":"transcript","status":"done"}`
)

func TestWaitClientReactsToEvents(t *testing.T) {
	w, url := newWaitAPI(t, transcriptPending)
	// 第一次取得影音之後（還是 pending），步驟完成並送出事件。
	w.onFirstG = func() { w.set(transcriptDone); w.emit() }
	res, out, errOut := run(t, app.Env{Clock: clock.NewFake(time.Now())}, "ingest", "yt:abc123xxxxx", "--until", "transcript", "--wait", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(out, `"transcript":"done"`) {
		t.Errorf("輸出 = %q", out)
	}
}

func TestWaitClientTimeoutExits4(t *testing.T) {
	w, url := newWaitAPI(t, transcriptPending)
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	done := make(chan Result, 1)
	go func() {
		res, _, _ := run(t, app.Env{Clock: clk}, "ingest", "yt:abc123xxxxx", "--until", "transcript", "--wait", "--timeout", "5m", "--server", url, "--token", "t")
		done <- res
	}()
	<-w.subbed
	// 等 CLI 建立好 --timeout 的計時器（用 fake clock，不真的等待）。
	for deadline := time.Now().Add(5 * time.Second); clk.PendingTimers() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("CLI 沒有建立 --timeout 的計時器")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case res := <-done:
		t.Fatalf("時間還沒到就結束了：%+v", res)
	default:
	}
	clk.Advance(5 * time.Minute)
	select {
	case res := <-done:
		if res.Code != 4 || res.Mode != ModeClient {
			t.Errorf("結果 = %+v，預期結束碼 4", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("超過 --timeout 後沒有結束")
	}
}

func TestWaitBlockedExits3AndShowsAction(t *testing.T) {
	_, url := newWaitAPI(t, `{"stage":"transcript","status":"blocked","message":"需要 Bilibili 的 cookie","actions":["上傳 cookie"]}`)
	res, out, errOut := run(t, app.Env{Clock: clock.NewFake(time.Now())}, "ingest", "yt:abc123xxxxx", "--until", "transcript", "--wait", "--server", url, "--token", "t")
	if res.Code != 3 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if !strings.Contains(out, "上傳 cookie") || !strings.Contains(out, "需要 Bilibili 的 cookie") {
		t.Errorf("輸出 = %q", out)
	}
	// --json 時 stdout 只有 JSON，說明印在 stderr。
	_, url = newWaitAPI(t, `{"stage":"transcript","status":"blocked","message":"需要 cookie","actions":["上傳 cookie"]}`)
	res, out, errOut = run(t, app.Env{Clock: clock.NewFake(time.Now())}, "ingest", "yt:abc123xxxxx", "--until", "transcript", "--wait", "--json", "--server", url, "--token", "t")
	if res.Code != 3 || strings.Contains(out, "上傳 cookie") || !strings.Contains(errOut, "上傳 cookie") {
		t.Errorf("--json：%+v stdout=%q stderr=%q", res, out, errOut)
	}
}

func TestWaitFailedExits1(t *testing.T) {
	_, url := newWaitAPI(t, `{"stage":"transcript","status":"failed","error_class":"permanent","message":"找不到可用的字幕來源。"}`)
	res, _, errOut := run(t, app.Env{Clock: clock.NewFake(time.Now())}, "ingest", "yt:abc123xxxxx", "--until", "transcript", "--wait", "--server", url, "--token", "t")
	if res.Code != 1 || !strings.Contains(errOut, "找不到可用的字幕來源") {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
}

// 串流中斷時不能無限等下去。
func TestWaitClientStreamEndsExits1(t *testing.T) {
	_, url := newFakeAPI(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			writeJSON(rw, 201, `{"items":[{"id":"i1"}]}`)
		case r.URL.Path == "/api/events":
			rw.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(rw, ": ok\n\n")
		default:
			writeJSON(rw, 200, `{"id":"i1","stages":[{"stage":"metadata","status":"running"}]}`)
		}
	})
	res, _, errOut := run(t, app.Env{Clock: clock.NewFake(time.Now())}, "ingest", "yt:abc123xxxxx", "--wait", "--server", url, "--token", "t")
	if res.Code != 1 {
		t.Errorf("結果 = %+v，stderr：%s", res, errOut)
	}
}
