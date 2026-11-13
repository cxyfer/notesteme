package features

// 通過場景的棘輪（ratchet）。
//
// M1 期間使用非 strict 模式，「501 → pending」這類規則可能讓原本通過的場景退步成 pending，整套測試卻仍然是綠的
// （審查用變異測試證明過：讓 GET /api/items 回 501，CLI 場景 5 從 passed 變成 pending）。
// features/passing.txt 列出目前預設 tag 下所有 passed 的場景，TestFeatures 跑完後逐一檢查：
// 清單中的場景只要不是 passed（pending、failed、undefined、沒有被執行）就讓測試失敗。
//
// 場景的結果不能用 After hook 收集：godog 傳給 After hook 的是「最後一個步驟」的錯誤，
// 前面的步驟 pending 時，後面的步驟是 skipped，最後一個步驟的錯誤是 nil，pending 場景會被誤判為通過。
// 所以改用自訂的 formatter，逐步驟收集結果（與 godog 自己的統計同一個來源）。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/formatters"
	messages "github.com/cucumber/messages/go/v34"
)

// passingFile 是通過場景的清單，一行一個場景，格式見 scenarioKey。
const passingFile = "passing.txt"

// ratchetFormatName 是記錄場景結果的 formatter 名稱。
const ratchetFormatName = "ratchet"

// statusPassed 是通過場景的狀態；其他狀態是第一個不是 passed 的步驟狀態。
const statusPassed = "passed"

// scenarioResult 是一個場景（Scenario Outline 的一個 example 也算一個場景）的結果。
type scenarioResult struct {
	// key 是清單中的名稱，見 scenarioKey。
	key string
	// file、line、example 只用來排序，讓清單維持 feature 檔中的順序。
	file          string
	line, example int
	// total 是場景的步驟數，passed 是通過的步驟數；other 是第一個不是 passed 的步驟狀態。
	total, passed int
	other         string
}

// status 回傳場景的結果：每個步驟都通過才是 passed。
func (r *scenarioResult) status() string {
	switch {
	case r.other != "":
		return r.other
	case r.total > 0 && r.passed == r.total:
		return statusPassed
	default:
		return "incomplete" // 沒有收到完整的步驟結果
	}
}

// ratchetRecorder 收集每個場景的結果。方法並行安全（godog.concurrency 大於 1 時 formatter 會被並行呼叫）。
type ratchetRecorder struct {
	mu       sync.Mutex
	docs     map[string]*messages.GherkinDocument // 依 feature 的 uri
	byPickle map[string]*scenarioResult           // 依 pickle id
	order    []*scenarioResult
}

// ratchet 是 TestFeatures 使用的收集器；formatter 在 godog 內建立，所以用 package 層級的變數交換資料。
var ratchet = &ratchetRecorder{}

func (r *ratchetRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.docs = map[string]*messages.GherkinDocument{}
	r.byPickle = map[string]*scenarioResult{}
	r.order = nil
}

func (r *ratchetRecorder) feature(doc *messages.GherkinDocument, uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.docs == nil {
		r.docs = map[string]*messages.GherkinDocument{}
	}
	r.docs[uri] = doc
}

func (r *ratchetRecorder) pickle(p *messages.Pickle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byPickle == nil {
		r.byPickle = map[string]*scenarioResult{}
	}
	res := &scenarioResult{total: len(p.Steps), file: filepath.Base(p.Uri)}
	var scenarioID, rowID string
	if len(p.AstNodeIds) > 0 {
		scenarioID = p.AstNodeIds[0]
	}
	if len(p.AstNodeIds) > 1 {
		rowID = p.AstNodeIds[len(p.AstNodeIds)-1]
	}
	res.line, res.example = locateScenario(r.docs[p.Uri], scenarioID, rowID)
	res.key = scenarioKey(p.Uri, p.Name, res.example)
	r.byPickle[p.Id] = res
	r.order = append(r.order, res)
}

// step 記錄一個步驟的結果。skipped 是前面的步驟沒通過造成的，不覆蓋比較有意義的狀態。
func (r *ratchetRecorder) step(p *messages.Pickle, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.byPickle[p.Id]
	if res == nil {
		return
	}
	if status == statusPassed {
		res.passed++
		return
	}
	if res.other == "" || res.other == "skipped" {
		res.other = status
	}
}

// results 回傳目前收集到的場景結果，依 feature 檔名、場景在檔案中的位置與 example 排序。
func (r *ratchetRecorder) results() []*scenarioResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]*scenarioResult(nil), r.order...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.example < b.example
	})
	return out
}

// scenarioKey 回傳場景在 passing.txt 中的名稱：`<feature 檔名>: <場景名稱>`。
// Scenario Outline 的每個 example 另外加上 ` [example N]`（N 是整份 Examples 的第幾列，從 1 算起），
// 因為同一個 outline 的 example 場景名稱相同（除非名稱本身用了 <欄位>）。
// 用列序而不是行號：feature 檔前面增減內容時清單不用跟著改。
func scenarioKey(uri, name string, example int) string {
	key := filepath.Base(uri) + ": " + name
	if example > 0 {
		key += fmt.Sprintf(" [example %d]", example)
	}
	return key
}

// locateScenario 在 doc 中找出 scenarioID 對應的場景行號；rowID 不為空時，
// 同時回傳 example 列是整份 Examples 的第幾列（從 1 算起），否則 example 是 0。
func locateScenario(doc *messages.GherkinDocument, scenarioID, rowID string) (line, example int) {
	if doc == nil || doc.Feature == nil {
		return 0, 0
	}
	var visit func(children []*messages.FeatureChild) bool
	visit = func(children []*messages.FeatureChild) bool {
		for _, c := range children {
			if c.Rule != nil {
				if visit(ruleChildren(c.Rule)) {
					return true
				}
				continue
			}
			sc := c.Scenario
			if sc == nil || sc.Id != scenarioID {
				continue
			}
			line = int(sc.Location.Line)
			n := 0
			for _, ex := range sc.Examples {
				for _, row := range ex.TableBody {
					n++
					if rowID != "" && row.Id == rowID {
						example = n
					}
				}
			}
			return true
		}
		return false
	}
	visit(doc.Feature.Children)
	return line, example
}

// ruleChildren 把 Rule 的子節點轉成 FeatureChild，讓 Rule 底下的場景也能用同一套走訪。
func ruleChildren(r *messages.Rule) []*messages.FeatureChild {
	out := make([]*messages.FeatureChild, 0, len(r.Children))
	for _, c := range r.Children {
		out = append(out, &messages.FeatureChild{Scenario: c.Scenario, Background: c.Background})
	}
	return out
}

// ratchetFormatter 把 godog 的事件轉給 ratchet；輸出內容由其他 formatter（pretty）負責，這個不寫任何東西。
type ratchetFormatter struct{ r *ratchetRecorder }

func init() {
	godog.Format(ratchetFormatName, "記錄每個場景的結果，供 passing.txt 的棘輪檢查使用（不輸出）。",
		func(string, io.Writer) formatters.Formatter { return ratchetFormatter{r: ratchet} })
}

func (f ratchetFormatter) TestRunStarted() { f.r.reset() }
func (f ratchetFormatter) Feature(doc *messages.GherkinDocument, uri string, _ []byte) {
	f.r.feature(doc, uri)
}
func (f ratchetFormatter) Pickle(p *messages.Pickle) { f.r.pickle(p) }
func (f ratchetFormatter) Defined(*messages.Pickle, *messages.PickleStep, *formatters.StepDefinition) {
}
func (f ratchetFormatter) Passed(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition) {
	f.r.step(p, statusPassed)
}
func (f ratchetFormatter) Failed(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition, _ error) {
	f.r.step(p, "failed")
}
func (f ratchetFormatter) Skipped(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition) {
	f.r.step(p, "skipped")
}
func (f ratchetFormatter) Undefined(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition) {
	f.r.step(p, "undefined")
}
func (f ratchetFormatter) Pending(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition) {
	f.r.step(p, "pending")
}
func (f ratchetFormatter) Ambiguous(p *messages.Pickle, _ *messages.PickleStep, _ *formatters.StepDefinition, _ error) {
	f.r.step(p, "ambiguous")
}
func (f ratchetFormatter) Summary() {}

// withRatchetFormat 在使用者指定的 formatter 之後加上記錄結果的 formatter。
func withRatchetFormat(format string) string {
	for _, name := range strings.Split(format, ",") {
		if strings.SplitN(strings.TrimSpace(name), ":", 2)[0] == ratchetFormatName {
			return format
		}
	}
	if strings.TrimSpace(format) == "" {
		return ratchetFormatName
	}
	return format + "," + ratchetFormatName
}

// parsePassing 讀取 passing.txt：一行一個場景；空行與以 # 開頭的行忽略；重複的行視為錯誤。
func parsePassing(content string) ([]string, error) {
	var keys []string
	seen := map[string]int{}
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first, dup := seen[line]; dup {
			return nil, fmt.Errorf("%s 第 %d 行與第 %d 行重複：%s", passingFile, i+1, first, line)
		}
		seen[line] = i + 1
		keys = append(keys, line)
	}
	return keys, nil
}

// ratchetReport 是棘輪檢查的結果。
type ratchetReport struct {
	// failures 是讓測試失敗的項目。
	failures []string
	// unlisted 是通過但不在清單中的場景，只提示不失敗。
	unlisted []string
}

// checkRatchet 比對清單與這次的結果。
//
// full 是 true（預設 tag 的完整執行）時，清單中沒有被執行的場景也算失敗（改名、刪除或被 tag 排除了）；
// 否則（使用者用 -godog.tags 指定其他 tag）只檢查實際有執行到的場景。
// 同一份結果中名稱重複的場景無法區分，一律算失敗。
func checkRatchet(listed []string, results []*scenarioResult, full bool) ratchetReport {
	var rep ratchetReport
	status := map[string]string{}
	for _, r := range results {
		if _, dup := status[r.key]; dup {
			rep.failures = append(rep.failures, fmt.Sprintf("場景名稱重複，棘輪無法區分：%s", r.key))
			continue
		}
		status[r.key] = r.status()
	}
	inList := map[string]bool{}
	for _, key := range listed {
		inList[key] = true
		st, ran := status[key]
		switch {
		case !ran && full:
			rep.failures = append(rep.failures, fmt.Sprintf("清單中的場景沒有被執行（改名、刪除或被 tag 排除？）：%s", key))
		case ran && st != statusPassed:
			rep.failures = append(rep.failures, fmt.Sprintf("清單中的場景不再通過（%s）：%s", st, key))
		}
	}
	for _, r := range results {
		if r.status() == statusPassed && !inList[r.key] {
			rep.unlisted = append(rep.unlisted, r.key)
		}
	}
	return rep
}

// passedKeys 回傳通過的場景名稱（依結果的順序）。
func passedKeys(results []*scenarioResult) []string {
	var keys []string
	for _, r := range results {
		if r.status() == statusPassed {
			keys = append(keys, r.key)
		}
	}
	return keys
}

// writePassing 把通過的場景寫成 passing.txt 的內容。
func writePassing(path string, results []*scenarioResult) error {
	keys := passedKeys(results)
	return os.WriteFile(path, []byte(strings.Join(keys, "\n")+"\n"), 0o644)
}
