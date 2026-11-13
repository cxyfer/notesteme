package features

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// demoFeature 涵蓋棘輪要分辨的情況：通過、前面步驟 pending 而最後一個步驟被跳過、失敗、undefined、Scenario Outline。
const demoFeature = `Feature: Demo

  Scenario: plain pass
    Given ok

  Scenario: pending then skipped
    Given pend
    And ok

  Scenario: failing
    Given boom

  Scenario: undefined step
    Given nothing matches this

  Scenario Outline: outline
    Given <step>

    Examples:
      | step |
      | ok   |
      | pend |
      | ok   |

  Rule: grouped
    Scenario: inside a rule
      Given ok
`

// runDemo 用 ratchet formatter 執行 demoFeature，回傳收集到的結果（key 對應狀態）。
func runDemo(t *testing.T) ([]*scenarioResult, map[string]string) {
	t.Helper()
	suite := godog.TestSuite{
		Name: "demo",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^ok$`, func() error { return nil })
			sc.Step(`^pend$`, func() error { return godog.ErrPending })
			sc.Step(`^boom$`, func() error { return io.ErrUnexpectedEOF })
		},
		Options: &godog.Options{
			Format:          withRatchetFormat("pretty"),
			Output:          io.Discard,
			NoColors:        true,
			Strict:          false,
			FeatureContents: []godog.Feature{{Name: "dir/DEMO-demo.feature", Contents: []byte(demoFeature)}},
		},
	}
	suite.Run() // 有失敗的場景，結束碼不是 0，不影響要檢查的收集結果
	results := ratchet.results()
	got := map[string]string{}
	for _, r := range results {
		got[r.key] = r.status()
	}
	return results, got
}

// 收集器要看每個步驟的結果：最後一個步驟被跳過時，場景仍然是 pending，不是 passed。
func TestRatchetCollectsScenarioResults(t *testing.T) {
	_, got := runDemo(t)
	want := map[string]string{
		"DEMO-demo.feature: plain pass":           "passed",
		"DEMO-demo.feature: pending then skipped": "pending",
		"DEMO-demo.feature: failing":              "failed",
		"DEMO-demo.feature: undefined step":       "undefined",
		"DEMO-demo.feature: outline [example 1]":  "passed",
		"DEMO-demo.feature: outline [example 2]":  "pending",
		"DEMO-demo.feature: outline [example 3]":  "passed",
		"DEMO-demo.feature: inside a rule":        "passed",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("結果不符\n got: %v\nwant: %v", got, want)
	}
}

// 結果依 feature 檔中的順序排列，寫出來的清單才穩定。
func TestRatchetResultOrder(t *testing.T) {
	results, _ := runDemo(t)
	var keys []string
	for _, r := range results {
		keys = append(keys, r.key)
	}
	want := []string{
		"DEMO-demo.feature: plain pass",
		"DEMO-demo.feature: pending then skipped",
		"DEMO-demo.feature: failing",
		"DEMO-demo.feature: undefined step",
		"DEMO-demo.feature: outline [example 1]",
		"DEMO-demo.feature: outline [example 2]",
		"DEMO-demo.feature: outline [example 3]",
		"DEMO-demo.feature: inside a rule",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("順序不符\n got: %q\nwant: %q", keys, want)
	}
}

func TestParsePassing(t *testing.T) {
	got, err := parsePassing("# 註解\n\nA.feature: one\n  B.feature: two [example 2]  \n")
	if err != nil || !reflect.DeepEqual(got, []string{"A.feature: one", "B.feature: two [example 2]"}) {
		t.Errorf("parsePassing = %q, %v", got, err)
	}
	if _, err := parsePassing("A.feature: one\nA.feature: one\n"); err == nil || !strings.Contains(err.Error(), "重複") {
		t.Errorf("重複的行應該失敗，得到 %v", err)
	}
}

func result(key, status string) *scenarioResult {
	r := &scenarioResult{key: key, total: 1}
	switch status {
	case statusPassed:
		r.passed = 1
	default:
		r.other = status
	}
	return r
}

func TestCheckRatchet(t *testing.T) {
	results := []*scenarioResult{
		result("A: stays", "passed"),
		result("A: regressed to pending", "pending"),
		result("A: broke", "failed"),
		result("A: new and passing", "passed"),
		result("A: not listed and pending", "pending"),
	}
	listed := []string{"A: stays", "A: regressed to pending", "A: broke", "A: gone"}

	t.Run("預設 tag：退步與沒有執行的都失敗，新通過的只提示", func(t *testing.T) {
		rep := checkRatchet(listed, results, true)
		if len(rep.failures) != 3 {
			t.Fatalf("failures = %q", rep.failures)
		}
		for i, want := range []string{"pending", "failed", "沒有被執行"} {
			if !strings.Contains(rep.failures[i], want) {
				t.Errorf("failures[%d] = %q，預期含 %q", i, rep.failures[i], want)
			}
		}
		if !reflect.DeepEqual(rep.unlisted, []string{"A: new and passing"}) {
			t.Errorf("unlisted = %q", rep.unlisted)
		}
	})

	t.Run("指定其他 tag：沒有執行到的不檢查，執行到的仍然檢查", func(t *testing.T) {
		rep := checkRatchet(listed, results, false)
		if len(rep.failures) != 2 {
			t.Fatalf("failures = %q", rep.failures)
		}
		if strings.Contains(strings.Join(rep.failures, "\n"), "A: gone") {
			t.Error("沒有執行到的場景不應該失敗")
		}
	})

	t.Run("全部維持通過", func(t *testing.T) {
		rep := checkRatchet([]string{"A: stays"}, []*scenarioResult{result("A: stays", "passed")}, true)
		if len(rep.failures) != 0 || len(rep.unlisted) != 0 {
			t.Errorf("rep = %+v", rep)
		}
	})

	t.Run("不完整的結果不算通過", func(t *testing.T) {
		incomplete := &scenarioResult{key: "A: stays", total: 2, passed: 1}
		if rep := checkRatchet([]string{"A: stays"}, []*scenarioResult{incomplete}, true); len(rep.failures) != 1 {
			t.Errorf("rep = %+v", rep)
		}
	})

	t.Run("名稱重複無法區分", func(t *testing.T) {
		dup := []*scenarioResult{result("A: same", "passed"), result("A: same", "passed")}
		if rep := checkRatchet(nil, dup, true); len(rep.failures) != 1 || !strings.Contains(rep.failures[0], "重複") {
			t.Errorf("rep = %+v", rep)
		}
	})
}

func TestWithRatchetFormat(t *testing.T) {
	for in, want := range map[string]string{
		"pretty":         "pretty,ratchet",
		"":               "ratchet",
		"pretty,ratchet": "pretty,ratchet",
		"junit:out.xml":  "junit:out.xml,ratchet",
	} {
		if got := withRatchetFormat(in); got != want {
			t.Errorf("withRatchetFormat(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestIsDefaultRun(t *testing.T) {
	if !isDefaultRun(godog.Options{Paths: []string{"."}}) {
		t.Error("預設設定是完整執行")
	}
	if isDefaultRun(godog.Options{Paths: []string{"."}, Tags: "@cli"}) {
		t.Error("指定 tag 不是完整執行")
	}
	if isDefaultRun(godog.Options{Paths: []string{"CLI-cli.feature"}}) {
		t.Error("指定 paths 不是完整執行")
	}
}
