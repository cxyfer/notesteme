package steps

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/config"
)

// registerOpsUnattended 註冊 OPS-06～08 的句型（steps.md 4.18 的「OPS-06～08」）。這些句型只有 OPS-unattended.feature 使用；
// OPS 的場景（@OPS-R11～@OPS-R21）也放行 NOTIFY 的句型（notifyGate 的 opsGate）。
func registerOpsUnattended(r *Registry, s *Scenario) {
	r.Step(`the backup directory {string} contains:`, s.backupDirContains)
	r.Step(`the backup directory {string} contains exactly:`, s.backupDirContainsExactly)
	r.Step(`the data volume is {int} percent full`, s.dataVolumeFull)
	r.Step(`the cookie for {string} was last used successfully at {string}`, s.cookieLastUsed)
	r.Step(`yt-dlp recorded {int} failures out of {int} calls in the last hour`, s.ytdlpRecorded)
	r.Step(`the instance is deployed with the {string} profile`, s.deployedWithProfile)
	r.Step(`the server starts with the flag {string}`, s.serverStartsWithFlag)
	r.Step(`the compose .env file contains:`, s.composeDotEnv)
}

// opsRules 是 OPS 的 M4 規則（OPS-R11～R21）：M1 的 OPS-R1～R10 不在內，所以不用前綴比對（@OPS-R1 會吃到 R10～R19）。
var opsRules = []string{"@OPS-R11", "@OPS-R12", "@OPS-R13", "@OPS-R14", "@OPS-R15", "@OPS-R16", "@OPS-R17", "@OPS-R18", "@OPS-R19", "@OPS-R20", "@OPS-R21"}

// opsGate 回報場景是不是 OPS 的 M4 場景（放行 NOTIFY 的句型）。
func (s *Scenario) opsGate() bool {
	return slices.ContainsFunc(s.tags, func(t string) bool { return slices.Contains(opsRules, t) })
}

// opsState 是 OPS 句型累積、要在第一個送出請求的步驟寫入的狀態（flushOps）。
type opsState struct {
	cookies []opsCookie
	calls   []opsCalls
	// dotenv 是 `the compose .env file contains:` 的變數（fakecompose 代入時優先）。
	dotenv map[string]string
}

type opsCookie struct {
	platform string
	at       time.Time
}

type opsCalls struct{ failures, calls int }

// opsFixtureContent 是 `the backup directory … contains:` 寫入的固定假資料（不是有效的備份）。
const opsFixtureContent = "fixture: not a real backup\n"

func (s *Scenario) backupDirContains(dir string, table *godog.Table) error {
	names, err := fileColumn(table)
	if err != nil {
		return err
	}
	real := s.filePath(dir)
	if err := os.MkdirAll(real, 0o700); err != nil {
		return err
	}
	for _, n := range names {
		p := filepath.Join(real, n)
		if err := os.WriteFile(p, []byte(opsFixtureContent), 0o600); err != nil {
			return err
		}
		// After hook 的 secret 掃描照字面讀它們（不是 tar.gz）。
		if s.fixtureFiles == nil {
			s.fixtureFiles = map[string][]byte{}
		}
		s.fixtureFiles[p] = []byte(opsFixtureContent)
	}
	return nil
}

// fileColumn 讀出表格的 file 欄位（第一列是標題）。
func fileColumn(table *godog.Table) ([]string, error) {
	if table == nil || len(table.Rows) < 1 || len(table.Rows[0].Cells) != 1 || table.Rows[0].Cells[0].Value != "file" {
		return nil, errors.New("表格必須只有一個欄位 file")
	}
	var out []string
	for _, row := range table.Rows[1:] {
		v := strings.TrimSpace(row.Cells[0].Value)
		if v == "" || strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
			return nil, fmt.Errorf("檔名 %q 不正確（不能含路徑）", v)
		}
		out = append(out, v)
	}
	return out, nil
}

// backupDirContainsExactly 比對目錄中的一般檔案與 symlink（不含子目錄），不比順序。
func (s *Scenario) backupDirContainsExactly(dir string, table *godog.Table) error {
	want, err := fileColumn(table)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.filePath(dir))
	if err != nil {
		return err
	}
	var got []string
	for _, e := range entries {
		if t := e.Type(); t == 0 || t&fs.ModeSymlink != 0 {
			got = append(got, e.Name())
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("目錄 %s 的內容 = %v\n預期 %v", dir, got, want)
	}
	return nil
}

func (s *Scenario) dataVolumeFull(percent int) error {
	if percent < 0 || percent > 100 {
		return fmt.Errorf("用量 %d%% 不正確", percent)
	}
	s.World.SetDiskUsage(percent)
	return nil
}

func (s *Scenario) cookieLastUsed(platform, at string) error {
	if platform != "youtube" && platform != "bilibili" {
		return fmt.Errorf("平台 %q 不正確（youtube、bilibili）", platform)
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339：%w", at, err)
	}
	s.ops.cookies = append(s.ops.cookies, opsCookie{platform: platform, at: t})
	return nil
}

func (s *Scenario) ytdlpRecorded(failures, calls int) error {
	if failures < 0 || calls < 1 || failures > calls {
		return fmt.Errorf("%d 次失敗、%d 次呼叫不合理", failures, calls)
	}
	s.ops.calls = append(s.ops.calls, opsCalls{failures: failures, calls: calls})
	return nil
}

// opsCookieContent 是測試 cookie 的內容（Netscape 格式）。
var opsCookieContent = map[string]string{
	"youtube":  "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tops-stale-youtube-sid",
	"bilibili": "# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tops-stale-bilibili-sess",
}

// flushOps 寫入累積的 cookie（POST /api/cookies 後設定時間）與 yt-dlp 的呼叫統計（steps.md 4.18）。
func (s *Scenario) flushOps() error {
	cookies, calls := s.ops.cookies, s.ops.calls
	s.ops.cookies, s.ops.calls = nil, nil
	for _, c := range cookies {
		body := map[string]any{"platform": c.platform, "content": opsCookieContent[c.platform]}
		if _, err := s.configWrite(http.MethodPost, "/api/cookies", body, http.StatusOK, "上傳 "+c.platform+" cookie"); err != nil {
			return err
		}
		if err := s.server.SetCookieUsedAt(s.ctx, c.platform, c.at); err != nil {
			return err
		}
	}
	// 平均分布在現在之前 1 小時內的 12 個 5 分鐘時間格。
	now := s.World.Clock.Now()
	for _, c := range calls {
		const buckets = 12
		start := now.Add(-time.Hour)
		for b := range buckets {
			n := c.calls*(b+1)/buckets - c.calls*b/buckets
			f := c.failures*(b+1)/buckets - c.failures*b/buckets
			if n == 0 && f == 0 {
				continue
			}
			at := start.Add(time.Duration(b) * 5 * time.Minute)
			if err := s.server.SetToolCalls(s.ctx, "yt-dlp", at, n, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// deployedWithProfile 把參數原樣設為 NOTESTEME_DEPLOYMENT（不檢查值），必須在 server 啟動之前。
func (s *Scenario) deployedWithProfile(kind string) error {
	if s.server != nil {
		return errors.New("NOTESTEME_DEPLOYMENT 必須在 server 啟動之前設定")
	}
	s.World.Env[config.EnvDeployment] = kind
	return nil
}

// serverStartsWithFlag 同 `the server starts`，但帶 serve 的旗標；啟動失敗由 `the server fails to start with` 檢查。
func (s *Scenario) serverStartsWithFlag(flag string) error {
	if s.server != nil {
		return errors.New("server 已經在執行")
	}
	opts, err := app.ParseServeArgs(strings.Fields(flag))
	if err != nil {
		return err
	}
	if err := s.startServerWith(opts); err != nil {
		return nil
	}
	return nil
}

func (s *Scenario) composeDotEnv(table *godog.Table) error {
	if table == nil || len(table.Rows) < 1 || len(table.Rows[0].Cells) != 2 ||
		table.Rows[0].Cells[0].Value != "field" || table.Rows[0].Cells[1].Value != "value" {
		return errors.New("表格的欄位必須是 field、value")
	}
	if s.ops.dotenv == nil {
		s.ops.dotenv = map[string]string{}
	}
	for _, row := range table.Rows[1:] {
		s.ops.dotenv[row.Cells[0].Value] = row.Cells[1].Value
	}
	return nil
}

// composeVars 是 fakecompose 代入用的變數：固定的假值，加上 `the compose .env file contains:` 的值（優先）。
func (s *Scenario) composeVars() map[string]string {
	vars := make(map[string]string, len(composeEnv)+len(s.ops.dotenv))
	for k, v := range composeEnv {
		vars[k] = v
	}
	for k, v := range s.ops.dotenv {
		vars[k] = v
	}
	return vars
}
