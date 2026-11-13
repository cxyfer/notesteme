package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/cli"
)

// cliRun 是最近一次 `the owner runs` 的結果。
type cliRun struct {
	res cli.Result
	// out、errOut 已經把實際路徑換回虛擬路徑（Unexpand）。
	out, errOut string
	// step 是最近一個用到這個結果的步驟序號：執行指令的步驟，或之後通過的斷言步驟（見 fresh、touch）。
	step int
}

// fresh 回報這個結果是否來自前一個步驟：前一個步驟剛執行了指令，或剛對這個結果做了通過的斷言。
// 例如 `ran in client mode` 通過之後的 `exits with`，仍然是在檢查同一個結果。
func (c *cliRun) fresh(step int) bool { return c.step == step-1 }

// notImplemented 回報指令是不是因為功能還沒做而結束（API 回 501，或子命令屬於之後的切片）。
func (c *cliRun) notImplemented() bool { return c.res.ErrorCode == "not_implemented" }

// registerCLI 註冊「CLI」的步驟，句型見 docs/specs/steps.md。
func registerCLI(r *Registry, s *Scenario) {
	r.Step(`the owner runs {string}`, s.ownerRuns)
	r.Step(`the command exits with {int}`, s.commandExits)
	r.Step(`the command output contains {string}`, s.commandOutputContains)
	r.Step(`the command output is JSON containing:`, s.commandOutputJSON)
	r.Step(`the command ran in {word} mode`, s.commandRanIn)
}

// ownerRuns 在程序內執行 cli.Execute（ADR-0015）。
//
// 這個步驟不會隱含啟動 server：CLI 自己判斷模式，沒有 server 時就是獨立模式（場景 2）。
// 所以也不呼叫 prepareOwner：Given 累積的全域設定要靠 server 套用，沒有 server 時無處可套，直接失敗，
// 不默默忽略。
func (s *Scenario) ownerRuns(line string) error {
	// 先代入 {token:…}，再把虛擬路徑換成實際路徑，最後才切開：代入的內容不會被當成引號或空白處理。
	argv, err := harness.SplitArgs(s.World.Expand(s.World.Subst(line)))
	if err != nil {
		return err
	}
	if len(argv) == 0 {
		return fmt.Errorf("指令是空的：%q", line)
	}
	if argv[0] != "notesteme" {
		// 這個步驟執行的是 notesteme 的 CLI；其他程式（例如 docker compose）harness 不會執行。
		// TODO(OPS-R9)：這個場景要另外的驗證方式（讀 deploy/docker/compose.yaml），或改成 @manual；
		// 這是 handoff 允許的三種 pending 之外的一種，已在回報中請 owner 決定。
		return fmt.Errorf("harness 不執行 %q（只執行 notesteme 的 CLI）：%w", argv[0], godog.ErrPending)
	}
	args := argv[1:]

	q := s.givenQueue
	if s.server == nil && (len(s.givenSettings) > 0 || q.limits != nil || q.pausedQueue || len(q.pausedClasses) > 0) {
		return errors.New("場景用 Given 設定了全域設定或佇列，但 server 沒有在執行：這些設定要靠 server 套用，CLI 的獨立模式不會套用（需要 server 的步驟才會隱含啟動它）")
	}
	// TODO：client 模式又帶 --wait 時，harness 要在另一個 goroutine 反覆 RunUntilIdle（手動 worker 不會自己執行任務）。
	if s.server != nil && slices.Contains(args, "--wait") && !slices.Contains(args, "--server") {
		return errors.New("尚未支援：server 的 worker 是手動模式，client 模式的 --wait 需要 harness 在 CLI 執行期間推動 worker")
	}

	env := s.World.AppEnv()
	env.Dial = s.World.Dial
	var stdout, stderr bytes.Buffer
	res := cli.Execute(s.ctx, args, app.IO{Stdout: &stdout, Stderr: &stderr}, env)
	s.cmd = &cliRun{
		res: res, out: s.World.Unexpand(stdout.String()), errOut: s.World.Unexpand(stderr.String()), step: s.step,
	}
	return nil
}

// needCommand 回傳最近一次的指令結果；還沒有執行過指令時失敗。
func (s *Scenario) needCommand() (*cliRun, error) {
	if s.cmd == nil {
		return nil, errors.New("還沒有執行過指令（缺少 `the owner runs`）")
	}
	return s.cmd, nil
}

// commandExits 比對結束碼。
//
// 指令因為功能還沒做而結束（ErrorCode 是 not_implemented），而且結果來自前一個步驟時回 pending
// （ADR-0015 的第一種 pending 延伸到 CLI）：還沒做的指令不能算通過，即使結束碼剛好相同
// （例如 501 對應的 1，剛好等於場景預期的 1）。其他情況結束碼不符一律失敗。
func (s *Scenario) commandExits(want int) error {
	c, err := s.needCommand()
	if err != nil {
		return err
	}
	if c.notImplemented() && c.fresh(s.step) {
		return fmt.Errorf("指令還沒實作（結束碼 %d）：%s%w", c.res.Code, snippet([]byte(c.errOut)), godog.ErrPending)
	}
	if c.res.Code != want {
		return fmt.Errorf("指令結束碼是 %d，預期 %d\nstdout：%s\nstderr：%s", c.res.Code, want, snippet([]byte(c.out)), snippet([]byte(c.errOut)))
	}
	c.step = s.step
	return nil
}

// commandOutputContains 比對 stdout 或 stderr 含有指定文字。
func (s *Scenario) commandOutputContains(want string) error {
	c, err := s.needCommand()
	if err != nil {
		return err
	}
	want = s.World.Expand(s.World.Subst(want))
	if !strings.Contains(c.out, want) && !strings.Contains(c.errOut, want) {
		return fmt.Errorf("指令的輸出沒有 %q\nstdout：%s\nstderr：%s", want, snippet([]byte(c.out)), snippet([]byte(c.errOut)))
	}
	c.step = s.step
	return nil
}

// commandOutputJSON 比對 stdout 是 JSON 且包含指定內容（子集比對，見 jsonSubset）。
// stdout 是 JSON Lines（多個物件）時只比對第一個物件。
func (s *Scenario) commandOutputJSON(doc *godog.DocString) error {
	c, err := s.needCommand()
	if err != nil {
		return err
	}
	want := []byte(s.World.Expand(s.World.Subst(doc.Content)))
	got := strings.TrimSpace(c.out)
	if !json.Valid([]byte(got)) {
		got, _, _ = strings.Cut(got, "\n")
	}
	if err := jsonSubset(want, []byte(got)); err != nil {
		return fmt.Errorf("%w\nstderr：%s", err, snippet([]byte(c.errOut)))
	}
	c.step = s.step
	return nil
}

// commandRanIn 比對 CLI 實際使用的模式。
func (s *Scenario) commandRanIn(want string) error {
	c, err := s.needCommand()
	if err != nil {
		return err
	}
	if string(c.res.Mode) != want {
		return fmt.Errorf("指令使用的模式是 %q，預期 %q\nstderr：%s", c.res.Mode, want, snippet([]byte(c.errOut)))
	}
	c.step = s.step
	return nil
}
