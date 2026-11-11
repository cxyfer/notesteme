// Package features 執行驗收場景（features/*.feature），並以 speccheck_test.go 靜態檢查規格。
package features

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/steps"
)

// defaultTags 是沒有指定 -godog.tags 時的過濾條件（ADR-0015）。
const defaultTags = "@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui"

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
	// TODO(slice0-B): 假工具分派（faketool.Dispatch）
	os.Exit(m.Run())
}

func TestFeatures(t *testing.T) {
	o := opts
	o.Tags = tagExpr(opts.Tags)
	o.TestingT = t

	suite := godog.TestSuite{
		Name:                "notesteme",
		ScenarioInitializer: steps.Initialize,
		Options:             &o,
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
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
