package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakecompose"
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

// cliTimeout 是單一 CLI 指令的真實時間上限。
// 這是防止卡死的保護，不是被測行為：場景的時間都用 fake clock，不受它影響；
// 只有指令在真實時間內完全沒有結束（例如 --wait 在等沒有人推進的 fake clock）才會觸發。
// 測試可以調短它。
var cliTimeout = 30 * time.Second

// ownerRuns 在程序內執行 cli.Execute（ADR-0015）。
//
// 這個步驟通常不會隱含啟動 server：CLI 自己判斷模式，沒有 server 時就是獨立模式（場景 2）。
// 例外是有尚未寫入的 prompt、處理方案或頻道覆寫（見下方）。
// Given 累積的全域設定與佇列要靠 server 套用：
//   - server 已在執行：先呼叫 prepareOwner 把設定與佇列送出，CLI 才看得到它們。
//   - server 沒有在執行：無處可套，直接失敗，不默默忽略。
//
// 只執行 notesteme 與 docker：docker 由 fakecompose 模擬（只支援 `docker compose … config --format json`，
// 讀取 repo 內真正的 compose 檔，不會呼叫真正的 docker，見 runDocker）；其他指令一律失敗。
// handoff 第 4 節允許清單中的「docker 指令 pending」到此結束：不支援的 docker 指令現在是失敗，不是 pending。
func (s *Scenario) ownerRuns(line string) error {
	// 先依引號切開，再對每個參數代入 {token:…} 並把虛擬路徑換成實際路徑：
	// 含空白的暫存路徑與代入的內容都不會被切壞，也不會被當成引號或空白處理。
	argv, err := harness.SplitArgs(line)
	if err != nil {
		return err
	}
	if len(argv) == 0 {
		return fmt.Errorf("指令是空的：%q", line)
	}
	for i, a := range argv {
		// /tmp/<x> 對應到 `Given the file "/tmp/<x>" contains:` 寫的檔案（steps.md 第 4 節），不碰真正的 /tmp。
		argv[i] = s.expandArg(a)
	}
	// 對應的 <場景根目錄>/tmp 先建立，指令才能把輸出寫到 /tmp/<x>（例如 BND 的 export --out）。
	if err := os.MkdirAll(filepath.Join(s.World.Root, "tmp"), 0o700); err != nil {
		return err
	}
	switch argv[0] {
	case "notesteme":
	case "docker":
		return s.runDocker(argv[1:])
	default:
		return fmt.Errorf("harness 只執行 notesteme 的 CLI，不認得指令 %q（打錯字？）", argv[0])
	}
	args := argv[1:]

	// prompt、處理方案與頻道覆寫是 server 擁有的資料（存在資料庫、經 API 寫入），
	// 不是 server 啟動時套用的設定：有尚未寫入的這類 Given 時隱含啟動 server 寫入，CLI 之後以 client 模式執行。
	if s.server == nil && s.hasPendingConfig() {
		if err := s.ensureServer(); err != nil {
			return err
		}
	}
	if s.hasGivenConfig() {
		if s.server == nil {
			return errors.New("場景用 Given 設定了全域設定或佇列，但 server 沒有在執行：這些設定要靠 server 套用，CLI 的獨立模式不會套用（需要 server 的步驟才會隱含啟動它）")
		}
		if err := s.prepareOwner(); err != nil {
			return err
		}
	}
	// TODO：client 模式又帶 --wait 時，harness 要在另一個 goroutine 反覆 RunUntilIdle（手動 worker 不會自己執行任務）。
	if s.server != nil && slices.Contains(args, "--wait") && !slices.Contains(args, "--server") {
		return errors.New("尚未支援：server 的 worker 是手動模式，client 模式的 --wait 需要 harness 在 CLI 執行期間推動 worker")
	}

	env := s.World.AppEnv()
	env.Dial = s.World.Dial
	var stdout, stderr bytes.Buffer
	cctx, cancel := context.WithTimeout(s.ctx, cliTimeout)
	defer cancel()
	res := cli.Execute(cctx, args, app.IO{Stdout: &stdout, Stderr: &stderr}, env)
	if errors.Is(cctx.Err(), context.DeadlineExceeded) && s.ctx.Err() == nil {
		return fmt.Errorf("CLI 在 %d 秒內沒有結束，可能是 --wait 在等 fake clock", int(cliTimeout.Seconds()))
	}
	s.cmd = &cliRun{
		res: res, out: s.World.Unexpand(stdout.String()), errOut: s.World.Unexpand(stderr.String()), step: s.step,
	}
	return nil
}

// composeEnv 是 fakecompose 代入變數用的環境：只有 compose.yaml 要求必要的變數，值是假的。
// 不用 World.Env：真正的測試 master key 不該出現在 `config` 的輸出裡（compose 會把 environment 原樣印出）。
var composeEnv = map[string]string{"NOTESTEME_MASTER_KEY": "fake-master-key-for-compose-config"}

// runDocker 以 fakecompose 模擬 `docker <args>`（OPS-R9）。
// compose 自己會失敗的情況（例如必要的變數沒有設定）變成非零結束碼與 stderr，和真的 docker 一樣；
// 不支援的指令或 compose 功能是 harness 的限制，讓步驟失敗，不能被當成被測行為。
func (s *Scenario) runDocker(args []string) error {
	root, err := fakecompose.FindRoot(".")
	if err != nil {
		return err
	}
	out, err := fakecompose.Run(root, args, s.composeVars())
	var exit *fakecompose.ExitError
	switch {
	case err == nil:
		s.cmd = &cliRun{res: cli.Result{Code: 0}, out: out, step: s.step}
	case errors.As(err, &exit):
		s.cmd = &cliRun{res: cli.Result{Code: exit.Code}, errOut: exit.Msg + "\n", step: s.step}
	default:
		return fmt.Errorf("harness 無法模擬這個 docker 指令（docker %s）：%w", strings.Join(args, " "), err)
	}
	return nil
}

// hasGivenConfig 回報是否有 Given 累積、尚未送出的全域設定或佇列設定（見 prepareOwner）。
func (s *Scenario) hasGivenConfig() bool {
	q := s.givenQueue
	return len(s.givenSettings) > 0 || q.limits != nil || q.pausedQueue || len(q.pausedClasses) > 0 || s.hasPendingConfig()
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
	// 輸出已經 Unexpand 成虛擬路徑（/data/…），預期值只代入 token，不 Expand，兩邊才對稱。
	want = s.World.Subst(want)
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
	// 同 commandOutputContains：輸出已是虛擬路徑，預期值不 Expand。
	want := []byte(s.World.Subst(doc.Content))
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

// expandArg 代入 CLI 的一個參數：/tmp/<x>、{token:…} 與 M5 的參照、虛擬路徑。
func (s *Scenario) expandArg(a string) string {
	return s.World.Expand(s.World.SubstRequest(s.mapTmpPaths(a)))
}
