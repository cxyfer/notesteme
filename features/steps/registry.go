// Package steps 註冊 docs/specs/steps.md 列出的全部 godog 步驟。
//
// 步驟實作依 steps.md 第 4 節的分組拆成多個檔案，每個檔案一個 registerXxx。
// 尚未實作的 handler 回傳 godog.ErrPending，之後逐步換成真正的實作。
// 場景的狀態（World、server、HTTP client、最後的回應）放在 Scenario，見 scenario.go。
package steps

import (
	"regexp"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/vocab"
)

// Def 是一個已註冊的步驟定義。
type Def struct {
	// Keyword 是 "Given"、"Then"，或空字串（不分關鍵字）。
	Keyword string
	// Template 是句型，與 steps.md 去掉關鍵字前綴後逐字相同。
	Template string
	// Expr 是 Template 編譯後的 regexp。
	Expr *regexp.Regexp

	// handler 保留給單元測試檢查參數型別。
	handler any
}

// Registry 把步驟定義註冊到 godog，並記錄所有定義。
// sc 為 nil 時只記錄定義，供 speccheck 使用。
type Registry struct {
	sc   *godog.ScenarioContext
	defs []Def
}

// Step 註冊不分關鍵字的步驟。
func (r *Registry) Step(template string, handler any) {
	expr := r.add("", template, handler)
	if r.sc != nil {
		r.sc.Step(expr, handler)
	}
}

// Given 註冊只比對 Given 的步驟。
func (r *Registry) Given(template string, handler any) {
	expr := r.add("Given", template, handler)
	if r.sc != nil {
		r.sc.Given(expr, handler)
	}
}

// Then 註冊只比對 Then 的步驟。
func (r *Registry) Then(template string, handler any) {
	expr := r.add("Then", template, handler)
	if r.sc != nil {
		r.sc.Then(expr, handler)
	}
}

func (r *Registry) add(keyword, template string, handler any) *regexp.Regexp {
	expr := vocab.Compile(template)
	r.defs = append(r.defs, Def{Keyword: keyword, Template: template, Expr: expr, handler: handler})
	return expr
}

// register 依序呼叫 steps.md 第 4 節各分組的註冊函式。
// s 是 handler 共用的場景狀態；只收集定義時（Definitions）傳入的 s 不會被使用。
func register(r *Registry, s *Scenario) {
	registerEnv(r, s)
	registerAccess(r, s)
	registerEvents(r, s)
	registerSources(r, s)
	registerItems(r, s)
	registerDelete(r, s)
	registerQueue(r, s)
	registerLLM(r, s)
	registerAgent(r, s)
	registerCLI(r, s)
	registerFiles(r, s)
	registerNetwork(r, s)
	registerConfig(r, s)
	registerOps(r, s)
	registerChapters(r, s)
	registerFrames(r, s)
}

// Initialize 把全部步驟註冊到 godog 的 ScenarioContext。
//
// godog 為每個場景建立一個 ScenarioContext，所以這裡的 Scenario 每個場景各一份。
// 每個場景開始前建立新的 World（獨立的暫存目錄、fake clock、fakenet、假工具），
// 結束後停止 server、檢查是否有未攔截的對外連線與 log 中的 secret，最後清掉暫存目錄。
// 結束階段發現的違規也會記進 HarnessFailures，見 hooks.go。
func Initialize(sc *godog.ScenarioContext) {
	s := &Scenario{}
	installHooks(sc, s)
	register(&Registry{sc: sc}, s)
}

// Definitions 回傳全部步驟定義，不需要 ScenarioContext。
func Definitions() []Def {
	r := &Registry{}
	register(r, &Scenario{})
	return r.defs
}
