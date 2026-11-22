package steps

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/cli"
)

// `titled` 補上的預設語言就是假 yt-dlp 實際回報的語言（steps.md 4.3）：
// hasTranscriptSource 照它判斷人工字幕軌是否符合 caption_priority，場景有準備字幕來源。
func TestTitledDefaultLanguageIsDeclared(t *testing.T) {
	s := newTestScenario(t)
	if err := s.youtubeVideoTitled("yt:abc123xxxxx", "T"); err != nil {
		t.Fatal(err)
	}
	v := s.World.Video("abc123xxxxx")
	if v.Language != "en" {
		t.Fatalf("假 yt-dlp 的語言 = %q，預期 en", v.Language)
	}
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("沒有字幕軌，不算有字幕來源")
	}
	v.Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if !s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("預設語言 en 的人工字幕軌符合 caption_priority，算有字幕來源")
	}

	// 字幕軌的語言與影片的語言不符（又沒有其他可選的項目）時不算。
	s2 := newTestScenario(t)
	_ = s2.youtubeVideoTitled("yt:abc123xxxxx", "T")
	s2.World.Video("abc123xxxxx").Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "ja"}}
	if s2.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("影片語言 en、只有 ja 的人工字幕軌：不符合 caption_priority")
	}
}

// 指令因為功能還沒做而結束，而且結果來自前一個步驟時回 pending，結束碼相同也一樣；其他情況嚴格比對。
func TestCommandExitsPending(t *testing.T) {
	s := newTestScenario(t)
	if err := s.commandExits(0); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("還沒執行指令應該失敗，得到 %v", err)
	}

	s.step = 1
	s.cmd = &cliRun{res: cli.Result{Code: 1, ErrorCode: "not_implemented"}, step: 1}
	s.step = 2
	for _, want := range []int{0, 1} {
		if err := s.commandExits(want); !errors.Is(err, godog.ErrPending) {
			t.Errorf("前一個步驟的 not_implemented，預期 %d 應該是 pending，得到 %v", want, err)
		}
	}
	// 中間隔了其他步驟（沒有對這個結果做通過的斷言）：不再是前一個步驟。
	s.step = 3
	if err := s.commandExits(0); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("不是前一個步驟的結果，應該失敗，得到 %v", err)
	}

	// 其他錯誤碼不會變成 pending。
	s.cmd = &cliRun{res: cli.Result{Code: 1, ErrorCode: "internal_error"}, step: 2}
	if err := s.commandExits(0); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("一般的失敗不應該是 pending，得到 %v", err)
	}
	if err := s.commandExits(1); err != nil {
		t.Errorf("結束碼相同應該通過：%v", err)
	}
}

// 斷言通過之後，後面的 exits with 仍然算前一個步驟（GRD 的 `ran in client mode` 接 `exits with`）。
func TestCommandAssertionKeepsResultFresh(t *testing.T) {
	s := newTestScenario(t)
	s.step = 1
	s.cmd = &cliRun{res: cli.Result{Code: 1, Mode: cli.ModeClient, ErrorCode: "not_implemented"}, step: 1}
	s.step = 2
	if err := s.commandRanIn("client"); err != nil {
		t.Fatal(err)
	}
	s.step = 3
	if err := s.commandExits(1); !errors.Is(err, godog.ErrPending) {
		t.Errorf("得到 %v", err)
	}
	if err := s.commandRanIn("standalone"); err == nil || !strings.Contains(err.Error(), "client") {
		t.Errorf("模式不符應該失敗，得到 %v", err)
	}
}

// docker 由 fakecompose 模擬，不支援的 docker 指令是失敗而不是 pending（見 docker_test.go）；
// 其他不是 notesteme 的指令（例如打錯字）一律失敗並說明原因。
func TestOwnerRunsRejectsOtherCommands(t *testing.T) {
	s := newTestScenario(t)
	if err := s.ownerRuns(`docker compose config`); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("docker：%v", err)
	}
	for _, line := range []string{`notestem items ls`, `Docker compose config`, `ls -l`, `docker-compose up`} {
		err := s.ownerRuns(line)
		if err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "只執行 notesteme") {
			t.Errorf("%q 應該失敗並說明原因，得到 %v", line, err)
		}
	}
	if err := s.ownerRuns(`   `); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("空指令應該失敗：%v", err)
	}
}

// 沒有 server 卻有尚未送出的 Given 設定時失敗，不默默忽略；引號沒有結束也失敗。
func TestOwnerRunsGivenWithoutServer(t *testing.T) {
	s := newTestScenario(t)
	s.givenSettings = map[string]any{"language": map[string]any{}}
	if err := s.ownerRuns(`notesteme items ls`); err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "server 沒有在執行") {
		t.Errorf("有尚未送出的設定：%v", err)
	}
	s.givenSettings = nil
	s.givenQueue.pausedQueue = true
	if err := s.ownerRuns(`notesteme items ls`); err == nil || !strings.Contains(err.Error(), "server 沒有在執行") {
		t.Errorf("有尚未送出的佇列設定：%v", err)
	}
	s.givenQueue = queueConfig{}
	if err := s.ownerRuns(`notesteme export "unterminated`); err == nil {
		t.Error("引號沒有結束應該失敗")
	}
}

// server 已經在執行時，尚未送出的 Given 設定與佇列要先送出，不能被默默忽略（P1）。
func TestOwnerRunsFlushesGivenWhenServerRunning(t *testing.T) {
	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	s.givenQueue.pausedQueue = true
	s.givenSettings = map[string]any{"language": map[string]any{}}
	if err := s.ownerRuns(`notesteme items ls --json`); err != nil {
		t.Fatalf("ownerRuns：%v", err)
	}
	if s.hasGivenConfig() {
		t.Error("設定與佇列應該已經送出")
	}
	if !s.ownerLoggedIn {
		t.Error("送出設定前要先登入 owner")
	}
	q, err := s.queueState()
	if err != nil {
		t.Fatal(err)
	}
	if !q.Paused {
		t.Error("Given 的暫停沒有套用到 server")
	}
}

// 輸出已經 Unexpand 成虛擬路徑；預期值不 Expand，兩邊才對稱。
func TestCommandOutputExpectationsStayVirtual(t *testing.T) {
	s := newTestScenario(t)
	real := s.World.DataDir()
	s.step = 1
	s.cmd = &cliRun{out: s.World.Unexpand(`{"path":"` + real + `/x.srt"}`), step: 1}
	s.step = 2
	if err := s.commandOutputContains(`/data/x.srt`); err != nil {
		t.Errorf("預期值是虛擬路徑：%v", err)
	}
	if err := s.commandOutputContains(real + `/x.srt`); err == nil {
		t.Error("預期值不應該被 Expand 成實際路徑，輸出裡沒有實際路徑")
	}
	if err := s.commandOutputJSON(&godog.DocString{Content: `{"path":"/data/x.srt"}`}); err != nil {
		t.Errorf("JSON 預期值是虛擬路徑：%v", err)
	}
	if err := s.commandOutputJSON(&godog.DocString{Content: `{"path":"` + real + `/x.srt"}`}); err == nil {
		t.Error("JSON 預期值不應該被 Expand")
	}
}

// JSON Lines 時只比對第一個物件。
func TestCommandOutputJSONFirstLine(t *testing.T) {
	s := newTestScenario(t)
	s.step = 1
	s.cmd = &cliRun{out: "{\"a\":1}\n{\"a\":2}\n", step: 1}
	s.step = 2
	if err := s.commandOutputJSON(&godog.DocString{Content: `{"a":1}`}); err != nil {
		t.Errorf("第一個物件：%v", err)
	}
	if err := s.commandOutputJSON(&godog.DocString{Content: `{"a":2}`}); err == nil {
		t.Error("第二個物件不比對")
	}
}

// 有尚未寫入的處理方案時，隱含啟動 server 寫入，不因為 server 沒有在執行而失敗。
func TestOwnerRunsStartsServerForPendingConfig(t *testing.T) {
	s := newTestScenario(t)
	s.pendingConfig.pendingProfiles = []pendingProfile{{name: "standard", config: map[string]any{"language": map[string]any{"targets": []any{"zh-Hant-TW"}}}}}
	if err := s.ownerRuns(`notesteme items ls --json`); err != nil {
		t.Fatalf("ownerRuns：%v", err)
	}
	if s.server == nil {
		t.Fatal("應該隱含啟動 server")
	}
	if s.hasPendingConfig() {
		t.Error("處理方案應該已經寫入")
	}
}

// 指令參數中的 /tmp/<x> 對應到 `Given the file "/tmp/<x>" contains:` 寫的檔案（steps.md 第 4 節）：
// CLI 讀得到內容，而且真正的 /tmp 不會被碰到。
func TestOwnerRunsMapsTmpPathsInArguments(t *testing.T) {
	s := newTestScenario(t)
	if err := s.fileContainsDoc("/tmp/notesteme-mapcheck.yaml", &godog.DocString{Content: "profiles: {mapped: {}}\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/tmp/notesteme-mapcheck.yaml"); err == nil {
		t.Fatal("不應該寫到真正的 /tmp")
	}
	// 沒有 server 時 CLI 用獨立模式，直接走到 API。
	if err := s.ownerRuns(`notesteme config import /tmp/notesteme-mapcheck.yaml`); err != nil {
		t.Fatalf("ownerRuns：%v", err)
	}
	if s.cmd.res.Code != 0 || !strings.Contains(s.cmd.out, "mapped") {
		t.Errorf("結果 = %+v，stdout：%s，stderr：%s", s.cmd.res, s.cmd.out, s.cmd.errOut)
	}
}
