package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/cucumber/godog"
)

// gateWaitLimit 是等被 gate 擋住的程序就位的真實時間上限，只用來防止卡死；結果由標記檔決定。
const gateWaitLimit = 30 * time.Second

// registerQueue 註冊「Pipeline 與佇列」的步驟，句型見 docs/specs/steps.md。
func registerQueue(r *Registry, s *Scenario) {
	r.Step(`the concurrency limit of {string} is {int}`, s.concurrencyLimitIs)
	r.Step(`the queue is paused`, s.queueIsPaused)
	r.Step(`the resource class {string} is paused`, s.resourceClassIsPaused)
	r.Step(`the owner pauses the queue`, func() error { return s.queueControl("pause") })
	r.Step(`the owner resumes the queue`, func() error { return s.queueControl("resume") })
	r.Step(`the worker starts one round`, s.workerStartsOneRound)
	r.Step(`{int} tasks of {string} are running`, s.tasksOfClassAreRunning)
	r.Step(`1 task of {string} is running`, func(class string) error { return s.tasksOfClassAreRunning(1, class) })
	r.Step(`the implementation version of the stage {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the next task belongs to {string}`, s.nextTaskBelongsTo)
	r.Step(`the task of {string} for {string} has {int} attempts`, s.taskHasAttempts)
	r.Step(`the source {string} was called {int} times`, s.sourceWasCalled)
	r.Step(`no task is running`, s.noTaskIsRunning)
}

// queueConfig 是 Given 累積的佇列設定；第一個送出請求的步驟才套用（見 flushQueue，ADR-0015）。
type queueConfig struct {
	limits        map[string]int
	pausedQueue   bool
	pausedClasses []string
}

func (s *Scenario) concurrencyLimitIs(class string, n int) error {
	if s.givenQueue.limits == nil {
		s.givenQueue.limits = map[string]int{}
	}
	s.givenQueue.limits[class] = n
	return nil
}

func (s *Scenario) queueIsPaused() error {
	s.givenQueue.pausedQueue = true
	return nil
}

func (s *Scenario) resourceClassIsPaused(class string) error {
	s.givenQueue.pausedClasses = append(s.givenQueue.pausedClasses, class)
	return nil
}

// flushQueue 把累積的上限與暫停設定送給 server。
func (s *Scenario) flushQueue() error {
	cfg := s.givenQueue
	s.givenQueue = queueConfig{}
	if len(cfg.limits) > 0 {
		body, err := json.Marshal(cfg.limits)
		if err != nil {
			return err
		}
		if err := s.queueRequest("PATCH /api/queue/limits", body); err != nil {
			return err
		}
	}
	if cfg.pausedQueue {
		if err := s.queueRequest("POST /api/queue/pause", nil); err != nil {
			return err
		}
	}
	for _, class := range cfg.pausedClasses {
		body, err := json.Marshal(map[string]string{"class": class})
		if err != nil {
			return err
		}
		if err := s.queueRequest("POST /api/queue/pause", body); err != nil {
			return err
		}
	}
	return nil
}

// queueRequest 送出佇列控制的請求，成功（2xx）才算完成。
func (s *Scenario) queueRequest(spec string, body []byte) error {
	method, path, err := parseRequest(spec)
	if err != nil {
		return err
	}
	resp, err := s.do(method, path, requestOpts{who: asOwner, body: body})
	if err != nil {
		return err
	}
	s.last = resp
	if resp.status < 200 || resp.status > 299 {
		return fmt.Errorf("%s：HTTP %d %s", spec, resp.status, snippet(resp.body))
	}
	return nil
}

// queueControl 送出 POST /api/queue/pause 或 /resume，作用在整個佇列。
func (s *Scenario) queueControl(action string) error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	return s.queueRequest("POST /api/queue/"+action, nil)
}

// workerStartsOneRound 讓 worker 領取一輪任務並開始執行，不等完成。
// 先啟用 gate，被領取的任務在假 yt-dlp 前停住；領到幾個就等幾個程序就位，之後任務維持 running，不靠時序。
func (s *Scenario) workerStartsOneRound() error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	if err := s.World.ArmGate(); err != nil {
		return fmt.Errorf("啟用 gate：%w", err)
	}
	before, err := s.World.GateWaiting()
	if err != nil {
		return err
	}
	n, err := s.server.RunRound(s.ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return s.World.WaitGateWaiting(before+n, gateWaitLimit)
}

// 佇列 API 回應的形狀（只列出步驟會用到的欄位）。
type queueJSON struct {
	Paused  bool `json:"paused"`
	Classes []struct {
		Class   string `json:"class"`
		Limit   int    `json:"limit"`
		Running int    `json:"running"`
		Pending int    `json:"pending"`
		Paused  bool   `json:"paused"`
	} `json:"classes"`
	Tasks []queueTaskJSON `json:"tasks"`
}

type queueTaskJSON struct {
	ID            string `json:"id"`
	ItemID        string `json:"item_id"`
	Item          string `json:"item"`
	Stage         string `json:"stage"`
	ResourceClass string `json:"resource_class"`
	State         string `json:"state"`
	Attempts      int    `json:"attempts"`
}

func (s *Scenario) queueState() (queueJSON, error) {
	var q queueJSON
	return q, s.apiJSON(http.MethodGet, "/api/queue", nil, &q)
}

// running 是任務清單中被領取（leased）的任務；class 為空代表不限資源類別。
func (q queueJSON) running(class string) []queueTaskJSON {
	var out []queueTaskJSON
	for _, t := range q.Tasks {
		if t.State == "leased" && (class == "" || t.ResourceClass == class) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Scenario) tasksOfClassAreRunning(want int, class string) error {
	q, err := s.queueState()
	if err != nil {
		return err
	}
	if got := q.running(class); len(got) != want {
		return fmt.Errorf("資源類別 %q 有 %d 個任務在執行，預期 %d 個：%+v", class, len(got), want, q.Tasks)
	}
	return nil
}

func (s *Scenario) noTaskIsRunning() error {
	q, err := s.queueState()
	if err != nil {
		return err
	}
	if got := q.running(""); len(got) > 0 {
		return fmt.Errorf("有 %d 個任務在執行，預期沒有：%+v", len(got), got)
	}
	return nil
}

// nextTaskBelongsTo 看佇列中 pending 任務的第一筆：它就是下一個會被領取的任務。
func (s *Scenario) nextTaskBelongsTo(ref string) error {
	q, err := s.queueState()
	if err != nil {
		return err
	}
	for _, t := range q.Tasks {
		if t.State != "pending" {
			continue
		}
		if t.Item != ref {
			return fmt.Errorf("下一個任務屬於 %q，預期 %q：%+v", t.Item, ref, q.Tasks)
		}
		return nil
	}
	return fmt.Errorf("佇列中沒有 pending 的任務：%+v", q.Tasks)
}

// taskHasAttempts 比對步驟最近一個任務的嘗試次數（影音詳情的 stages[].attempts）。
func (s *Scenario) taskHasAttempts(stage, ref string, want int) error {
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	var detail struct {
		Stages []struct {
			Stage    string `json:"stage"`
			Status   string `json:"status"`
			Attempts int    `json:"attempts"`
		} `json:"stages"`
	}
	if err := s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id), nil, &detail); err != nil {
		return err
	}
	for _, st := range detail.Stages {
		if st.Stage == stage {
			if st.Attempts != want {
				return fmt.Errorf("%s 的步驟 %s 嘗試了 %d 次（%s），預期 %d 次", ref, stage, st.Attempts, st.Status, want)
			}
			return nil
		}
	}
	return fmt.Errorf("%s 沒有步驟 %q", ref, stage)
}

// platformCaptionFlag 是平台字幕來源對應的 yt-dlp 旗標：下載字幕時，人工字幕用 --write-subs，自動字幕用 --write-auto-subs。
var platformCaptionFlag = map[string]string{
	"youtube-manual": "--write-subs",
	"youtube-auto":   "--write-auto-subs",
}

// sourceWasCalled 比對字幕來源被呼叫的次數。
// 平台來源是假 yt-dlp 的呼叫紀錄中下載對應字幕的次數；其他來源（Supadata、ASR）是 fakesource 腳本記錄的請求數。
func (s *Scenario) sourceWasCalled(id string, want int) error {
	got, err := s.sourceCalls(id)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("來源 %q 被呼叫 %d 次，預期 %d 次", id, got, want)
	}
	return nil
}

func (s *Scenario) sourceCalls(id string) (int, error) {
	flag, platform := platformCaptionFlag[id]
	if !platform {
		return s.World.Sources.Calls(id), nil
	}
	calls, err := s.World.ToolCalls()
	if err != nil {
		return 0, fmt.Errorf("讀取假工具的呼叫紀錄：%w", err)
	}
	n := 0
	for _, c := range calls {
		if c.Tool == "yt-dlp" && slices.Contains(c.Argv, "--skip-download") && slices.Contains(c.Argv, flag) {
			n++
		}
	}
	return n, nil
}
