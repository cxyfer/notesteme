package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
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
	r.Step(`the implementation version of the stage {string} is {int}`, s.implementationVersionIs)
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

// implementationVersionIs 覆寫步驟的 impl_version（PIPE-R6）。
// 覆寫記在 World，啟動（或重啟）時經 TestControls 帶入；server 已啟動時另外呼叫測試介面，立即生效。
func (s *Scenario) implementationVersionIs(stage string, v int) error {
	if !pipeline.ValidStage(stage) {
		return fmt.Errorf("步驟 %q 不正確，應是 %v 其中之一", stage, pipeline.Stages)
	}
	// 不論 server 有沒有在執行都記在 World：重啟後的 server 由 AppEnv 帶入同一份覆寫。
	if s.World.ImplVersions == nil {
		s.World.ImplVersions = map[string]int{}
	}
	s.World.ImplVersions[stage] = v
	if s.server != nil {
		return s.server.SetImplVersion(stage, v)
	}
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
	resp.step = s.step
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
	_, err := s.startRound()
	return err
}

// startRound 執行一輪領取並回傳領到的任務數。before 只數存活的程序，所以先前被 kill 而留下標記的程序不會讓 before+n 偏掉。
func (s *Scenario) startRound() (int, error) {
	if err := s.prepareOwner(); err != nil {
		return 0, err
	}
	if err := s.World.ArmGate(); err != nil {
		return 0, fmt.Errorf("啟用 gate：%w", err)
	}
	before, err := s.World.GateWaiting()
	if err != nil {
		return 0, err
	}
	n, err := s.server.RunRound(s.ctx)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.World.WaitGateWaiting(before+n, gateWaitLimit)
}

// 佇列 API 回應的形狀（只列出步驟會用到的欄位）。
type queueJSON struct {
	Paused bool `json:"paused"`
	Lanes  []struct {
		Class   string `json:"class"`
		Limit   int    `json:"limit"`
		Running int    `json:"running"`
		Waiting int    `json:"waiting"`
		Paused  bool   `json:"paused"`
	} `json:"lanes"`
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

// processWaitLimit 是等存活的假工具程序數符合預期的真實時間上限，只用來防止卡死。
// 是變數只為了讓單元測試縮短它。
var processWaitLimit = 10 * time.Second

// leasedByClass 回傳目前被領取（leased）的任務數，依資源類別分組。
// server 在執行時讀 GET /api/queue；場景讓 server 停下之後直接讀資料庫，不隱含啟動新的 server。
func (s *Scenario) leasedByClass() (map[string]int, error) {
	if s.stopped {
		return s.World.DBLeasedTasks()
	}
	q, err := s.queueState()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, t := range q.running("") {
		out[t.ResourceClass]++
	}
	return out, nil
}

// checkRunning 確認執行中的任務數：資料庫的狀態與實際存活的程序要同時符合。
//   - class 為空代表不限資源類別。
//   - 只看 DB 的話，任務被標成取消、程序卻還在跑也會通過（取消沒有真的停止工作）；
//     所以 gate 啟用時，存活的假工具程序數也必須等於被領取的任務總數（被 kill 的程序不算）。
func (s *Scenario) checkRunning(class string, want int) error {
	leased, err := s.leasedByClass()
	if err != nil {
		return err
	}
	total, got := 0, 0
	for c, n := range leased {
		total += n
		if class == "" || c == class {
			got += n
		}
	}
	if got != want {
		// 緊接在前一個步驟送出的請求是還沒實作的路由（例如刪除）：任務還在執行是因為那個動作沒有發生。
		if s.previousRequestNotImplemented() {
			return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
		}
		if class == "" {
			return fmt.Errorf("有 %d 個任務在執行，預期 %d 個：%v", got, want, leased)
		}
		return fmt.Errorf("資源類別 %q 有 %d 個任務在執行，預期 %d 個：%v", class, got, want, leased)
	}
	if s.World.Tools.Gate == "" {
		return nil // 場景沒有讓任何程序停在 gate 前，沒有程序可數
	}
	if err := s.World.WaitGateCount(total, processWaitLimit); err != nil {
		return fmt.Errorf("資料庫有 %d 個被領取的任務，但存活的程序數不符：%w", total, err)
	}
	return nil
}

func (s *Scenario) tasksOfClassAreRunning(want int, class string) error {
	return s.checkRunning(class, want)
}

func (s *Scenario) noTaskIsRunning() error { return s.checkRunning("", 0) }

// nextTaskBelongsTo 讓 worker 實際領取一輪，檢查被領取（leased）的那一筆任務屬於哪部影音。
// 只看列表排序驗證不到領取的規則（ClaimTasks 的排序）。場景以並行上限 1 讓每輪只領取一筆。
func (s *Scenario) nextTaskBelongsTo(ref string) error {
	n, err := s.startRound()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("worker 沒有領取任何任務")
	}
	q, err := s.queueState()
	if err != nil {
		return err
	}
	leased := q.running("")
	if len(leased) != 1 {
		return fmt.Errorf("這一輪領取了 %d 個任務，預期 1 個（場景應設定並行上限為 1）：%+v", len(leased), leased)
	}
	if leased[0].Item != ref {
		return fmt.Errorf("下一個被領取的任務屬於 %q，預期 %q：%+v", leased[0].Item, ref, q.Tasks)
	}
	return nil
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
	if id == podcastTranscriptID {
		// feed 上的字幕檔由 fakepodcast 計次。
		return s.World.Podcasts.TranscriptCalls(), nil
	}
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
