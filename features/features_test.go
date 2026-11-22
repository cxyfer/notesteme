// Package features 執行驗收場景（features/*.feature），並以 speccheck_test.go 靜態檢查規格。
package features

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/steps"
)

// defaultTags 是沒有指定 -godog.tags 時的過濾條件（ADR-0015）。
//
// M2 的場景全部通過之後，預設 tag 也涵蓋 @M2（OR 項各自附加排除條件，見 tagExpr）。
const defaultTags = "@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui,@M2 && ~@manual && ~@pending-m0 && ~@wip && ~@ui"

// excludedTags 是預設排除的 tag；指定 -godog.tags 時會併入每一個 OR 項。
var excludedTags = []string{"manual", "pending-m0", "wip", "ui"}

var opts = godog.Options{
	Format: "pretty",
	Paths:  []string{"."},
	// M1 期間使用非 strict 模式，pending 不算失敗；undefined 與 ambiguous 由 speccheck 檢查（ADR-0015）。
	Strict: false,
}

func init() {
	// 綁到標準庫的 flag.CommandLine，go test 才能用 -godog.tags 這樣的單橫線寫法覆寫。
	// godog.BindCommandLineFlags 綁的是 pflag.CommandLine，go test 不會解析，要寫成 --godog.tags。
	godog.BindFlags("godog.", flag.CommandLine, &opts)
}

func TestMain(m *testing.M) {
	// 測試 binary 以 yt-dlp、ffmpeg、ffprobe 的名稱被執行時，改扮假工具（ADR-0015）。
	faketool.Dispatch()
	// 繞過 netguard 的對外連線一律失敗並記錄違規（NFR-TEST-1）。
	restore := fakenet.InstallTrap()
	code := m.Run()
	restore()
	os.Exit(code)
}

// TestOutboundTrap 確認繞過 netguard 的連線在測試程序中會失敗並被記錄。
func TestOutboundTrap(t *testing.T) {
	fakenet.DrainTrapViolations()
	resp, err := http.Get("http://example.com/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("http.Get 應該被 trap 擋下")
	}
	if v := fakenet.DrainTrapViolations(); len(v) == 0 {
		t.Fatal("trap 沒有記錄違規")
	}
}

// updatePassing 用這次預設 tag 的結果重寫 passing.txt：go test ./features -run TestFeatures -update-passing
var updatePassing = flag.Bool("update-passing", false, "用這次通過的場景重寫 passing.txt（只能用預設 tag）")

// isDefaultRun 回報這次是不是沒有被使用者改過範圍的完整執行：沒有指定 -godog.tags，也沒有改 paths。
// 只有完整執行時，passing.txt 的場景都應該被執行到。
func isDefaultRun(o godog.Options) bool {
	return strings.TrimSpace(o.Tags) == "" && len(o.Paths) == 1 && o.Paths[0] == "."
}

func TestFeatures(t *testing.T) {
	o := opts
	o.Tags = tagExpr(opts.Tags)
	o.Format = withRatchetFormat(opts.Format)
	o.TestingT = t

	suite := godog.TestSuite{
		Name:                "notesteme",
		ScenarioInitializer: steps.Initialize,
		Options:             &o,
	}
	status := suite.Run()
	// pending 場景的 After hook 錯誤會被 godog 吞掉（非 strict 模式），
	// 所以另外讀取收集器，確保 harness 違規一定讓測試失敗。
	for _, f := range steps.HarnessFailures() {
		t.Errorf("harness 違規：%s", f)
	}
	checkPassing(t, isDefaultRun(opts))
	if status != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// checkPassing 做通過場景的棘輪檢查（見 ratchet_test.go）：
// passing.txt 中的場景不是 passed 就讓測試失敗；通過但不在清單中的場景只提示。
func checkPassing(t *testing.T, full bool) {
	t.Helper()
	results := ratchet.results()
	if *updatePassing {
		if !full {
			t.Fatal("-update-passing 只能搭配預設 tag 與 paths 使用")
		}
		if err := writePassing(passingFile, results); err != nil {
			t.Fatalf("寫入 %s：%v", passingFile, err)
		}
		t.Logf("已重寫 %s：%d 個通過的場景", passingFile, len(passedKeys(results)))
		return
	}
	content, err := os.ReadFile(passingFile)
	if err != nil {
		t.Errorf("讀取 %s：%v（用 -update-passing 產生）", passingFile, err)
		return
	}
	listed, err := parsePassing(string(content))
	if err != nil {
		t.Errorf("%v", err)
		return
	}
	rep := checkRatchet(listed, results, full)
	for _, f := range rep.failures {
		t.Errorf("通過場景的棘輪：%s", f)
	}
	if len(rep.unlisted) > 0 {
		t.Logf("以下 %d 個場景通過，但不在 %s，請加入（或執行 -update-passing）：\n  %s",
			len(rep.unlisted), passingFile, strings.Join(rep.unlisted, "\n  "))
	}
}

// tagExpr 回傳實際使用的 godog tag 表達式。
//
// godog 的 tag 表達式沒有括號：先以 "," 切成 OR 項，每項再以 "&&" 切成 AND 項，
// "~" 表示排除。因此使用者指定的表達式要把排除條件附加到每一個 OR 項，
// 否則 "@A,@B" 會變成「@A，或 @B」而繞過排除。
// OR 項本身提到的 tag（不論是否加 "~"）不再附加，讓使用者可以明確選到被排除的 tag。
func tagExpr(user string) string {
	if strings.TrimSpace(user) == "" {
		return defaultTags
	}

	var terms []string
	for _, term := range strings.Split(user, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		mentioned := map[string]bool{}
		for _, and := range strings.Split(term, "&&") {
			and = strings.TrimSpace(and)
			and = strings.TrimPrefix(and, "~")
			and = strings.TrimPrefix(and, "@")
			mentioned[and] = true
		}
		for _, ex := range excludedTags {
			if !mentioned[ex] {
				term += " && ~@" + ex
			}
		}
		terms = append(terms, term)
	}
	if len(terms) == 0 {
		return defaultTags
	}
	return strings.Join(terms, ",")
}

func TestTagExpr(t *testing.T) {
	const ex = " && ~@manual && ~@pending-m0 && ~@wip && ~@ui"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"預設", "", defaultTags},
		{"只有空白", "   ", defaultTags},
		{"單一 tag", "@AUTH-R1", "@AUTH-R1" + ex},
		{"AND 項", "@M1 && @api", "@M1 && @api" + ex},
		{"OR 項各自附加", "@ING-01,@ING-02", "@ING-01" + ex + ",@ING-02" + ex},
		{"OR 項周圍的空白", "@ING-01 , @ING-02", "@ING-01" + ex + ",@ING-02" + ex},
		{"指定被排除的 tag", "@wip", "@wip && ~@manual && ~@pending-m0 && ~@ui"},
		{"明確用 ~ 排除", "@M1 && ~@ui", "@M1 && ~@ui && ~@manual && ~@pending-m0 && ~@wip"},
		{"只有一個 OR 項指定被排除的 tag", "@wip,@M1", "@wip && ~@manual && ~@pending-m0 && ~@ui,@M1" + ex},
		{"空的 OR 項會被丟掉", "@M1,", "@M1" + ex},
		{"全部是空的 OR 項", ",", defaultTags},
		{"沒有 @ 的 tag 名稱", "wip", "wip && ~@manual && ~@pending-m0 && ~@ui"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tagExpr(tt.in); got != tt.want {
				t.Errorf("tagExpr(%q)\n got: %s\nwant: %s", tt.in, got, tt.want)
			}
		})
	}
}
