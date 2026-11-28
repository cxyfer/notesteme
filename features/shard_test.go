package features

// 驗收場景的分片：go test ./features -features.shard=<i>/<n> 只執行第 i 份（從 0 開始）的 feature 檔。
//
// -race 下預設 tag 的場景在一台 runner 上要 20 分鐘以上，CI 的 bdd job 因此拆成多份平行執行。
// 以檔案為單位分配：依目前 tag 表達式下每個檔案的 pickle 數（Scenario Outline 的每一列各算一個），
// 由多到少依序分給目前最少的一份，所以各份的場景數接近，每個檔案剛好分到一份。
// 分片時 passing.txt 的棘輪只檢查有執行到的場景（不是完整執行）；「清單中的場景都有被執行」
// 由不分片的 bdd-fast job 檢查。

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

var shardFlag = flag.String("features.shard", "", "只執行第 i 份的 feature 檔，格式 <i>/<n>（i 從 0 開始）；空字串表示全部")

// parseShard 解析 "<i>/<n>"。
func parseShard(s string) (i, n int, err error) {
	a, b, ok := strings.Cut(s, "/")
	if ok {
		i, err = strconv.Atoi(a)
		if err == nil {
			n, err = strconv.Atoi(b)
		}
	}
	if !ok || err != nil || n < 1 || i < 0 || i >= n {
		return 0, 0, fmt.Errorf("-features.shard=%q：格式是 <i>/<n>，0 <= i < n", s)
	}
	return i, n, nil
}

// matchTags 以 godog 的 tag 表達式語意比對：先以 "," 切成 OR 項，每項以 "&&" 切成 AND 項，"~" 表示排除。
func matchTags(expr string, tags []string) bool {
	has := map[string]bool{}
	for _, t := range tags {
		has[t] = true
	}
	for _, or := range strings.Split(expr, ",") {
		all := true
		for _, and := range strings.Split(or, "&&") {
			and = strings.TrimSpace(and)
			name, neg := strings.CutPrefix(and, "~")
			if has[name] == neg {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// shardWeights 回傳 dir 下每個 feature 檔（檔名）在 tag 表達式 expr 下的 pickle 數；0 的檔案不列出。
// 與 godog 的 Paths: ["."] 相同，只看 dir 這一層的 *.feature（parseFeatures 同樣只看這一層）。
func shardWeights(dir, expr string) (map[string]int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.feature"))
	if err != nil {
		return nil, err
	}
	weights := map[string]int{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		doc, err := gherkin.ParseGherkinDocument(strings.NewReader(string(b)), (&messages.Incrementing{}).NewId)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		n := 0
		for _, pk := range gherkin.Pickles(*doc, p, (&messages.Incrementing{}).NewId) {
			tags := make([]string, 0, len(pk.Tags))
			for _, t := range pk.Tags {
				tags = append(tags, t.Name)
			}
			if matchTags(expr, tags) {
				n++
			}
		}
		if n > 0 {
			weights[filepath.Base(p)] = n
		}
	}
	return weights, nil
}

// assignShards 把檔案分成 n 份：依權重由大到小（同權重依檔名）分給目前總權重最小的一份（同總權重取編號小的）。
// 回傳每一份的檔名（排序過）與總權重。
func assignShards(weights map[string]int, n int) (files [][]string, loads []int) {
	names := make([]string, 0, len(weights))
	for name := range weights {
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool {
		if weights[names[a]] != weights[names[b]] {
			return weights[names[a]] > weights[names[b]]
		}
		return names[a] < names[b]
	})
	files = make([][]string, n)
	loads = make([]int, n)
	for _, name := range names {
		min := 0
		for i := 1; i < n; i++ {
			if loads[i] < loads[min] {
				min = i
			}
		}
		files[min] = append(files[min], name)
		loads[min] += weights[name]
	}
	for i := range files {
		sort.Strings(files[i])
	}
	return files, loads
}

func TestParseShard(t *testing.T) {
	for _, tc := range []struct {
		in   string
		i, n int
		ok   bool
	}{
		{"0/1", 0, 1, true},
		{"2/4", 2, 4, true},
		{"4/4", 0, 0, false},
		{"-1/4", 0, 0, false},
		{"0/0", 0, 0, false},
		{"1", 0, 0, false},
		{"a/b", 0, 0, false},
	} {
		i, n, err := parseShard(tc.in)
		if (err == nil) != tc.ok || i != tc.i || n != tc.n {
			t.Errorf("parseShard(%q) = %d, %d, %v", tc.in, i, n, err)
		}
	}
}

func TestMatchTags(t *testing.T) {
	for _, tc := range []struct {
		expr string
		tags []string
		want bool
	}{
		{"@M1", []string{"@M1"}, true},
		{"@M1", []string{"@M2"}, false},
		{"@M1 && ~@wip", []string{"@M1", "@wip"}, false},
		{"@M1 && ~@wip,@M2", []string{"@M2", "@wip"}, true},
		{defaultTags, []string{"@M1", "@AUTH-R1"}, true},
		{defaultTags, []string{"@M2", "@manual"}, false},
		{defaultTags, []string{"@M3"}, false},
	} {
		if got := matchTags(tc.expr, tc.tags); got != tc.want {
			t.Errorf("matchTags(%q, %q) = %v, want %v", tc.expr, tc.tags, got, tc.want)
		}
	}
}

// TestShardsPartitionFeatures 確認實際的 feature 檔在預設 tag 下分成數份時，每個有場景的檔案剛好在一份，
// 而且各份的場景數相差不超過最大的單一檔案。
func TestShardsPartitionFeatures(t *testing.T) {
	weights, err := shardWeights(featuresDir, defaultTags)
	if err != nil {
		t.Fatal(err)
	}
	if len(weights) == 0 {
		t.Fatal("預設 tag 下沒有任何場景")
	}
	maxW := 0
	for _, w := range weights {
		maxW = max(maxW, w)
	}
	for n := 1; n <= 6; n++ {
		files, loads := assignShards(weights, n)
		seen := map[string]int{}
		for i, fs := range files {
			for _, f := range fs {
				if prev, dup := seen[f]; dup {
					t.Errorf("n=%d：%s 同時在第 %d 與第 %d 份", n, f, prev, i)
				}
				seen[f] = i
			}
		}
		if len(seen) != len(weights) {
			t.Errorf("n=%d：分到 %d 個檔案，應為 %d 個", n, len(seen), len(weights))
		}
		lo, hi := loads[0], loads[0]
		for _, l := range loads {
			lo, hi = min(lo, l), max(hi, l)
		}
		if hi-lo > maxW {
			t.Errorf("n=%d：各份場景數 %v 相差超過最大的單一檔案（%d）", n, loads, maxW)
		}
		t.Logf("n=%d：各份場景數 %v", n, loads)
	}
}
