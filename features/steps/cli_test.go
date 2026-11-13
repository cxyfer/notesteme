package steps

import (
	"errors"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/cli"
)

// `titled` 補上的預設語言讓假 yt-dlp 回報英文，但不算場景宣告的語言：
// hasTranscriptSource 仍然視為「沒有準備字幕來源」（DEL 等場景維持 pending）。
func TestTitledDefaultLanguageIsNotDeclared(t *testing.T) {
	s := newTestScenario(t)
	if err := s.youtubeVideoTitled("yt:abc123xxxxx", "T"); err != nil {
		t.Fatal(err)
	}
	v := s.World.Video("abc123xxxxx")
	if v.Language != "en" {
		t.Fatalf("假 yt-dlp 的語言 = %q，預期 en", v.Language)
	}
	v.Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("預設語言不算場景宣告的語言")
	}

	// 場景自己宣告語言之後就算。
	s2 := newTestScenario(t)
	_ = s2.youtubeVideoTitled("yt:abc123xxxxx", "T")
	s2.defaultedLang = map[string]bool{}
	s2.World.Video("abc123xxxxx").Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if !s2.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("宣告過語言的影片有符合的字幕軌")
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

// 不是 notesteme 的指令 harness 不會執行；沒有 server 卻有尚未送出的 Given 設定時失敗，不默默忽略。
func TestOwnerRunsGuards(t *testing.T) {
	s := newTestScenario(t)
	if err := s.ownerRuns(`docker compose config`); !errors.Is(err, godog.ErrPending) {
		t.Errorf("docker：%v", err)
	}
	s.givenSettings = map[string]any{"language": map[string]any{}}
	if err := s.ownerRuns(`notesteme items ls`); err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "server 沒有在執行") {
		t.Errorf("有尚未送出的設定：%v", err)
	}
	s.givenSettings = nil
	if err := s.ownerRuns(`notesteme export "unterminated`); err == nil {
		t.Error("引號沒有結束應該失敗")
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
