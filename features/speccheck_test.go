package features

// 規格的靜態一致性檢查（NFR-TEST-4），取代 scripts/spec/ 的 Python 腳本：
// parse_all.py、check_steps.py、check_trace.py，並額外檢查註冊的步驟定義（ADR-0015）。
// 計數語意與 Python 版相同，數字可以直接比對。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"

	"github.com/cxyfer/notesteme/features/internal/vocab"
	"github.com/cxyfer/notesteme/features/steps"
)

const (
	featuresDir = "."
	specsDir    = "../docs/specs"
)

// feature 是解析完成的 .feature 檔。
type feature struct {
	name string // 檔名，例如 AUTH-access.feature
	doc  *messages.GherkinDocument
}

func parseFeatures(t *testing.T) []feature {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var out []feature
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := gherkin.ParseGherkinDocument(f, (&messages.Incrementing{}).NewId)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		out = append(out, feature{name: filepath.Base(p), doc: doc})
	}
	return out
}

func loadVocabulary(t *testing.T) []vocab.Pattern {
	t.Helper()
	patterns, err := vocab.Load(filepath.Join(specsDir, "steps.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) == 0 {
		t.Fatal("steps.md 中沒有找到任何句型")
	}
	return patterns
}

func hasTag(tags []*messages.Tag, name string) bool {
	for _, tag := range tags {
		if tag.Name == name {
			return true
		}
	}
	return false
}

// TestGherkinParses 等同 parse_all.py：每個 .feature 都能解析。
func TestGherkinParses(t *testing.T) {
	features := parseFeatures(t)
	scenarios := 0
	for _, f := range features {
		if f.doc.Feature == nil {
			t.Errorf("%s: 沒有 Feature", f.name)
			continue
		}
		for _, c := range f.doc.Feature.Children {
			if c.Scenario != nil {
				scenarios++
			}
		}
	}
	if len(features) == 0 {
		t.Fatal("沒有找到任何 .feature")
	}
	t.Logf("%d files parsed, %d scenarios/outlines", len(features), scenarios)
}

var outlinePlaceholder = regexp.MustCompile(`<[a-z_]+>`)

// TestStepsInVocabulary 等同 check_steps.py：每個步驟都符合 steps.md 的某個句型。
//
// 計數語意與 Python 版相同：以 AST 的步驟計算（不展開 Scenario Outline），
// Background 的步驟算一次，只看場景自己的 @manual（不繼承 Feature 的 tag），
// Outline 的 <placeholder> 以 1 代入。
func TestStepsInVocabulary(t *testing.T) {
	patterns := loadVocabulary(t)
	type compiled struct {
		template string
		expr     *regexp.Regexp
	}
	var exprs []compiled
	for tmpl := range vocab.Templates(patterns) {
		exprs = append(exprs, compiled{tmpl, vocab.Compile(tmpl)})
	}

	used := map[string]bool{} // 以 template 記錄被用到的句型
	total, unmatched := 0, 0
	check := func(file string, steps []*messages.Step) {
		for _, s := range steps {
			total++
			probe := outlinePlaceholder.ReplaceAllString(s.Text, "1")
			hit := false
			for _, e := range exprs {
				if e.expr.MatchString(probe) {
					used[e.template] = true
					hit = true
				}
			}
			if !hit {
				unmatched++
				t.Errorf("%s:%d: %s", file, s.Location.Line, s.Text)
			}
		}
	}

	for _, f := range parseFeatures(t) {
		if f.doc.Feature == nil {
			continue
		}
		for _, c := range f.doc.Feature.Children {
			switch {
			case c.Background != nil:
				check(f.name, c.Background.Steps)
			case c.Scenario != nil && !hasTag(c.Scenario.Tags, "@manual"):
				check(f.name, c.Scenario.Steps)
			}
		}
	}

	var unused []string
	for _, p := range patterns {
		if !used[p.Template] {
			unused = append(unused, p.Full())
		}
	}
	t.Logf("%d steps checked, %d unmatched, %d patterns, %d unused", total, unmatched, len(patterns), len(unused))
	for _, u := range unused {
		t.Logf("  unused: %s", u)
	}
}

var (
	ruleDef   = regexp.MustCompile(`(?m)^\| ([A-Z]+-R\d+) \|`)
	storyDef  = regexp.MustCompile(`(?m)^\| ([A-Z]+-\d{2}) \|`)
	traceable = regexp.MustCompile(`^@([A-Z]+-(?:R\d+|\d{2}))\b`)
)

// TestTraceability 等同 check_trace.py：場景的規則與故事 tag 都存在，且每條規則與故事都有場景。
// 與 Python 版不同的是，有問題時測試會失敗。
func TestTraceability(t *testing.T) {
	mds, err := filepath.Glob(filepath.Join(specsDir, "*-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	rules, stories := map[string]bool{}, map[string]bool{}
	for _, md := range mds {
		b, err := os.ReadFile(md)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ruleDef.FindAllStringSubmatch(string(b), -1) {
			rules[m[1]] = true
		}
		for _, m := range storyDef.FindAllStringSubmatch(string(b), -1) {
			stories[m[1]] = true
		}
	}

	used := map[string]bool{}
	var problems []string
	note := func(file string, tags []*messages.Tag) {
		for _, tag := range tags {
			m := traceable.FindStringSubmatch(tag.Name)
			if m == nil {
				continue
			}
			used[m[1]] = true
			if !rules[m[1]] && !stories[m[1]] {
				problems = append(problems, fmt.Sprintf("%s:%d: unknown tag @%s", file, tag.Location.Line, m[1]))
			}
		}
	}
	for _, f := range parseFeatures(t) {
		if f.doc.Feature == nil {
			continue
		}
		note(f.name, f.doc.Feature.Tags)
		for _, c := range f.doc.Feature.Children {
			if c.Scenario == nil {
				continue
			}
			note(f.name, c.Scenario.Tags)
			for _, ex := range c.Scenario.Examples {
				note(f.name, ex.Tags)
			}
		}
	}

	var missingRules, missingStories []string
	for r := range rules {
		if !used[r] {
			missingRules = append(missingRules, r)
		}
	}
	for s := range stories {
		if !used[s] {
			missingStories = append(missingStories, s)
		}
	}
	sort.Slice(missingRules, func(i, j int) bool { return lessRule(missingRules[i], missingRules[j]) })
	sort.Strings(missingStories)

	if len(problems) == 0 {
		t.Log("no unknown tags")
	}
	for _, p := range problems {
		t.Error(p)
	}
	report := func(label string, ids []string) {
		if len(ids) == 0 {
			t.Logf("%s: none", label)
			return
		}
		t.Errorf("%s: %s", label, strings.Join(ids, ", "))
	}
	report("rules without scenario", missingRules)
	report("stories without scenario", missingStories)
	t.Logf("%d rules, %d stories", len(rules), len(stories))
}

// lessRule 依前綴再依編號排序，例如 ING-R2 在 ING-R10 前面。
func lessRule(a, b string) bool {
	pa, na := splitRule(a)
	pb, nb := splitRule(b)
	if pa != pb {
		return pa < pb
	}
	return na < nb
}

func splitRule(id string) (string, int) {
	prefix, num, _ := strings.Cut(id, "-R")
	n, _ := strconv.Atoi(num)
	return prefix, n
}

// stepLocations 建立 AST step ID 到行號的對照，供 pickle 的步驟回報位置。
func stepLocations(doc *messages.GherkinDocument) map[string]int64 {
	lines := map[string]int64{}
	add := func(steps []*messages.Step) {
		for _, s := range steps {
			lines[s.Id] = s.Location.Line
		}
	}
	if doc.Feature == nil {
		return lines
	}
	for _, c := range doc.Feature.Children {
		if c.Background != nil {
			add(c.Background.Steps)
		}
		if c.Scenario != nil {
			add(c.Scenario.Steps)
		}
	}
	return lines
}

// TestRegistryMatchesVocabulary 檢查 features/steps 的註冊定義：
// 句型集合與 steps.md 雙向相等，且每個（非 @manual 的）步驟都剛好對到一個定義。
func TestRegistryMatchesVocabulary(t *testing.T) {
	defs := steps.Definitions()

	// 1. 句型集合雙向相等
	want := vocab.Templates(loadVocabulary(t))
	have := map[string]bool{}
	for _, d := range defs {
		have[d.Template] = true
	}
	for _, tmpl := range sortedKeys(want) {
		if !have[tmpl] {
			t.Errorf("steps.md 有、但沒有註冊: %s", tmpl)
		}
	}
	for _, tmpl := range sortedKeys(have) {
		if !want[tmpl] {
			t.Errorf("已註冊、但 steps.md 沒有: %s", tmpl)
		}
	}

	// 2. 每個步驟依類型剛好對到一個定義
	reported := map[string]bool{}
	checked := 0
	for _, f := range parseFeatures(t) {
		if f.doc.Feature == nil {
			continue
		}
		lines := stepLocations(f.doc)
		for _, p := range gherkin.Pickles(*f.doc, f.name, (&messages.Incrementing{}).NewId) {
			if hasPickleTag(p.Tags, "@manual") {
				continue
			}
			for _, s := range p.Steps {
				checked++
				var matched []string
				for _, d := range defs {
					if keywordAccepts(d.Keyword, s.Type) && d.Expr.MatchString(s.Text) {
						matched = append(matched, strings.TrimSpace(d.Keyword+" "+d.Template))
					}
				}
				if len(matched) == 1 {
					continue
				}
				var line int64
				if len(s.AstNodeIds) > 0 {
					line = lines[s.AstNodeIds[0]]
				}
				key := fmt.Sprintf("%s:%d:%s", f.name, line, s.Text)
				if reported[key] {
					continue
				}
				reported[key] = true
				if len(matched) == 0 {
					t.Errorf("%s:%d: 沒有對到任何定義 (%s): %s", f.name, line, s.Type, s.Text)
				} else {
					t.Errorf("%s:%d: 對到 %d 個定義: %s\n    %s", f.name, line, len(matched), s.Text, strings.Join(matched, "\n    "))
				}
			}
		}
	}
	t.Logf("%d definitions, %d pickle steps checked", len(defs), checked)
}

// keywordAccepts 判斷定義的關鍵字限制是否接受步驟的類型（與 godog 的 keywordMatches 相同）。
func keywordAccepts(keyword string, typ messages.PickleStepType) bool {
	if keyword == "" {
		return true
	}
	switch typ {
	case messages.PickleStepType_CONTEXT:
		return keyword == "Given"
	case messages.PickleStepType_ACTION:
		return keyword == "When"
	case messages.PickleStepType_OUTCOME:
		return keyword == "Then"
	}
	return true
}

func hasPickleTag(tags []*messages.PickleTag, name string) bool {
	for _, tag := range tags {
		if tag.Name == name {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
