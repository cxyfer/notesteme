//go:build faultinject && unix

package fault

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/features/steps"
)

// 父程序交給子程序的環境變數。
const (
	envChildRoot  = "NST_FAULT_CHILD_ROOT"  // 場景根目錄（被殺的程序與重新啟動的程序共用）
	envChildPhase = "NST_FAULT_CHILD_PHASE" // first（新增影音並處理）、restart（重新啟動後繼續）或 corrupt（讀取損毀的產出）
	envChildRead  = "NST_FAULT_CHILD_READ"  // corrupt 階段要讀的字幕：variant=…&lang=…
)

// 子程序的兩個階段。
const (
	phaseFirst   = "first"
	phaseRestart = "restart"
	phaseCorrupt = "corrupt"
)

// paidCallsFile 是付費呼叫的計數檔：放在場景根目錄（/data 之外），被殺的程序與重新啟動的程序都附加到同一個檔。
const paidCallsFile = "paid-calls.log"

// itemRef 是劇本中的影音：沒有平台字幕，字幕來自 groq（ASR，付費），再經過 correct 與 translate（LLM，付費）。
const itemRef = "yt:fault1xxxxx"

// fakeSetup 只設定程序內的 fake（假 yt-dlp 的影片、字幕來源與 LLM 的劇本），兩個階段都要執行；
// 不打 API、不寫 DB，所以重新啟動時重放是安全的。
const fakeSetup = `
    Given the owner is signed in
    And a YouTube video "` + itemRef + `" titled "Fault injection run"
    And "` + itemRef + `" has no caption tracks
    And the source "groq" returns "groq-en.json" for "` + itemRef + `"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    And the LLM provider "anthropic" answers "translate" with "translate-echo-target"
`

// firstRun 在新的 /data 上設定處理方案、新增影音並處理到閒置。故障注入時會在中途被 SIGKILL。
const firstRun = `Feature: fault injection (first run)
  Scenario: Process an item from scratch
` + fakeSetup + `
    And the global settings are:
      """
      language:
        targets: [zh-Hant-TW]
        fallback: [groq]
      """
    And the profile "fault" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
        translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
      """
    And the default profile is "fault"
    When the owner ingests "` + itemRef + `"
    And the worker runs until idle
`

// restartRun 在同一份 /data 上重新啟動 server（PIPE-R11）並處理到閒置；處理方案與影音都已在 DB 中。
const restartRun = `Feature: fault injection (restart)
  Scenario: Continue after the process was killed
` + fakeSetup + `
    When the worker runs until idle
`

// corruptRun 讀取一份已經被改掉一個 byte 的字幕產出（NFR-REL-2）：第一次讀取回 409 artifact_corrupt，
// 從產生它的步驟重跑到閒置之後，再讀一次就回 200。query 是 variant=…&lang=…。
func corruptRun(query string) string {
	get := `"GET /api/items/` + itemRef + `/subtitles?format=srt&` + query + `"`
	return `Feature: fault injection (corrupt artifact)
  Scenario: A corrupt artifact is detected, marked and regenerated
` + fakeSetup + `
    When the owner sends ` + get + `
    Then the response status is 409
    And the response contains:
      """
      { "code": "artifact_corrupt" }
      """
    When the worker runs until idle
    And the owner sends ` + get + `
    Then the response status is 200
`
}

// TestFaultChild 是子程序的本體；父程序沒有設定 NST_FAULT_CHILD_ROOT 時跳過。
func TestFaultChild(t *testing.T) {
	root := os.Getenv(envChildRoot)
	if root == "" {
		t.Skip("只在故障注入的子程序中執行（由 TestFaultInjection 啟動）")
	}
	var content string
	switch phase := os.Getenv(envChildPhase); phase {
	case phaseFirst:
		content = firstRun
	case phaseRestart:
		content = restartRun
	case phaseCorrupt:
		content = corruptRun(os.Getenv(envChildRead))
	default:
		t.Fatalf("不認識的階段 %q", phase)
	}
	calls, err := os.OpenFile(filepath.Join(root, paidCallsFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer calls.Close()
	harness.FixedRoot = root
	harness.OnNew = func(w *harness.World) error {
		w.Net.Observe(paidCallRecorder(calls))
		return nil
	}
	suite := godog.TestSuite{
		Name:                "fault",
		ScenarioInitializer: steps.Initialize,
		Options: &godog.Options{
			Format:          "progress",
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "fault.feature", Contents: []byte(content)}},
			TestingT:        t,
		},
	}
	status := suite.Run()
	for _, f := range steps.HarnessFailures() {
		t.Errorf("harness 違規：%s", f)
	}
	if status != 0 {
		t.Fatalf("劇本失敗（status %d）", status)
	}
}

// paidHosts 是付費的 fake provider：Groq（ASR）與 anthropic（LLM）。
var paidHosts = map[string]bool{harness.GroqHost: true, fakellm.AnthropicHost: true}

// paidCallRecorder 回傳 fakenet 的觀察者：每個送到付費 provider 的 POST 在回應之前，先以一次 write 附加
// 「<host> <path> <body 的 sha256>」到計數檔並 fsync，所以程序之後被殺也不會少算（provider 收到就算付費）。
// multipart 的 boundary 是亂數，雜湊之前換成固定字串，同樣的請求才有同樣的 key。
func paidCallRecorder(f *os.File) func(host string, r *http.Request) {
	var mu sync.Mutex
	return func(host string, r *http.Request) {
		if !paidHosts[host] || r.Method != http.MethodPost {
			return
		}
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			return
		}
		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && params["boundary"] != "" {
			body = bytes.ReplaceAll(body, []byte(params["boundary"]), []byte("BOUNDARY"))
		}
		sum := sha256.Sum256(body)
		line := fmt.Sprintf("%s %s %s\n", host, strings.ReplaceAll(r.URL.Path, " ", "%20"), hex.EncodeToString(sum[:]))
		mu.Lock()
		defer mu.Unlock()
		_, _ = f.WriteString(line)
		_ = f.Sync()
	}
}
