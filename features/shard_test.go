package features

// 驗收場景的分片：go test ./features -features.shard=<i>/<n> 只執行第 i 份（從 0 開始）的 feature 檔。
//
// -race 下預設 tag 的場景在一台 runner 上要 20 分鐘以上，CI 的 bdd job 因此拆成多份平行執行。
// 以檔案為單位分配，每個檔案剛好分到一份：依每個檔案的預估耗時由多到少，依序分給目前總耗時最少的一份。
// 預估耗時取自 shard-weights.txt（不開 -race 的完整執行量到的每個檔案毫秒數，用 -features.record-weights 重寫）；
// 清單沒有的檔案以「已量測檔案的平均每場景耗時 × 這個檔案在目前 tag 下的 pickle 數」估計。
// 各檔案每個場景的耗時差很多（AGT 的場景是 GRD 的數倍），只按場景數分時，最慢的一份是最快的 4～5 倍。
// 權重只影響各份是否平衡，不影響執行哪些場景；過時了只是比較不平衡。
// 分片時 passing.txt 的棘輪只檢查有執行到的場景（不是完整執行）；「清單中的場景都有被執行」
// 由不分片的 bdd-fast job 檢查。

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

var shardFlag = flag.String("features.shard", "", "只執行第 i 份的 feature 檔，格式 <i>/<n>（i 從 0 開始）；空字串表示全部")

// recordWeights 用這次的耗時重寫 shard-weights.txt：go test ./features -run '^TestFeatures$' -features.record-weights
var recordWeights = flag.Bool("features.record-weights", false, "用這次每個 feature 檔的耗時重寫 "+weightsFile+"（只能用預設 tag、不分片、不開 -race）")

// weightsFile 記錄每個 feature 檔的耗時（毫秒），供分片時平衡各份。
const weightsFile = "shard-weights.txt"

// loadWeights 讀取 weightsFile：每行「檔名 毫秒」；空行與 # 開頭的行忽略。檔案不存在時回傳空的 map。
func loadWeights(path string) (map[string]int, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d：格式是「檔名 毫秒」：%q", path, line, text)
		}
		ms, err := strconv.Atoi(fields[1])
		if err != nil || ms < 0 {
			return nil, fmt.Errorf("%s:%d：毫秒數不是非負整數：%q", path, line, fields[1])
		}
		out[fields[0]] = ms
	}
	return out, sc.Err()
}

// shardCosts 回傳每個檔案的預估耗時：有量測值的用量測值，沒有的以已量測檔案的平均每場景耗時 × pickle 數估計
// （完全沒有量測值時就是 pickle 數）。pickles 是 shardWeights 的結果。
func shardCosts(pickles, measured map[string]int) map[string]int {
	ms, n := 0, 0
	for f, c := range pickles {
		if m, ok := measured[f]; ok {
			ms += m
			n += c
		}
	}
	per := 1
	if n > 0 {
		per = max(1, ms/n)
	}
	out := make(map[string]int, len(pickles))
	for f, c := range pickles {
		if m, ok := measured[f]; ok && m > 0 {
			out[f] = m
		} else {
			out[f] = per * c
		}
	}
	return out
}

// fileTimer 量每個 feature 檔的牆鐘時間。godog 一次執行一個檔案（concurrency 1），所以在場景的 Before hook
// 看到新的檔案時結算上一個檔案；上一個檔案最後一個場景的 After hook 與清理也算在它身上。
type fileTimer struct {
	mu    sync.Mutex
	cur   string
	start time.Time
	ms    map[string]int
}

func newFileTimer() *fileTimer { return &fileTimer{ms: map[string]int{}} }

func (ft *fileTimer) enter(uri string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	name := filepath.Base(uri)
	if name == ft.cur {
		return
	}
	now := time.Now()
	if ft.cur != "" {
		ft.ms[ft.cur] += int(now.Sub(ft.start).Milliseconds())
	}
	ft.cur, ft.start = name, now
}

func (ft *fileTimer) finish() map[string]int {
	ft.enter("")
	ft.mu.Lock()
	defer ft.mu.Unlock()
	delete(ft.ms, ".")
	return ft.ms
}

// hook 在每個場景開始前記錄所在的檔案。
func (ft *fileTimer) hook(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		ft.enter(s.Uri)
		return ctx, nil
	})
}

// writeWeights 依檔名排序寫出 weightsFile。
func writeWeights(path string, ms map[string]int) error {
	names := make([]string, 0, len(ms))
	for name := range ms {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# 每個 feature 檔在預設 tag 下的耗時（毫秒），供 -features.shard 平衡各份（features/shard_test.go）。\n")
	b.WriteString("# 以不開 -race 的完整執行量測，由下列指令重寫；數字過時只影響平衡，不影響執行哪些場景：\n")
	b.WriteString("#   go test ./features -run '^TestFeatures$' -count=1 -features.record-weights\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s %d\n", name, ms[name])
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

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
// 與 godog（internal/tags）相同，比對前把兩邊的 "@" 都去掉，所以 "M1" 與 "@M1" 等價。
func matchTags(expr string, tags []string) bool {
	has := map[string]bool{}
	for _, t := range tags {
		has[strings.ReplaceAll(t, "@", "")] = true
	}
	for _, or := range strings.Split(expr, ",") {
		all := true
		for _, and := range strings.Split(or, "&&") {
			and = strings.TrimSpace(and)
			name, neg := strings.CutPrefix(and, "~")
			if has[strings.ReplaceAll(name, "@", "")] == neg {
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

// assignShards 把檔案分成 n 份：依權重（預估耗時）由大到小（同權重依檔名）分給目前總權重最小的一份（同總權重取編號小的）。
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
		{"M1 && ~wip", []string{"@M1"}, true},
		{"M1 && ~wip", []string{"@M1", "@wip"}, false},
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
// 而且各份的預估耗時相差不超過最大的單一檔案。
func TestShardsPartitionFeatures(t *testing.T) {
	pickles, err := shardWeights(featuresDir, defaultTags)
	if err != nil {
		t.Fatal(err)
	}
	if len(pickles) == 0 {
		t.Fatal("預設 tag 下沒有任何場景")
	}
	measured, err := loadWeights(weightsFile)
	if err != nil {
		t.Fatal(err)
	}
	weights := shardCosts(pickles, measured)
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
			t.Errorf("n=%d：各份預估耗時 %v 相差超過最大的單一檔案（%d）", n, loads, maxW)
		}
		t.Logf("n=%d：各份預估耗時（毫秒）%v", n, loads)
	}
}

func TestShardCosts(t *testing.T) {
	pickles := map[string]int{"A.feature": 10, "B.feature": 5, "C.feature": 4}
	got := shardCosts(pickles, map[string]int{"A.feature": 3000, "B.feature": 2000, "gone.feature": 99})
	// 平均每場景 (3000+2000)/(10+5) = 333 毫秒；C 沒有量測值，估 333×4。
	want := map[string]int{"A.feature": 3000, "B.feature": 2000, "C.feature": 1332}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("shardCosts[%s] = %d, want %d（全部：%v）", f, got[f], w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("shardCosts 只應包含有場景的檔案：%v", got)
	}
	if got := shardCosts(pickles, nil); got["A.feature"] != 10 || got["C.feature"] != 4 {
		t.Errorf("沒有量測值時應等於 pickle 數：%v", got)
	}
}

func TestLoadWeights(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(p, []byte("# c\n\nA.feature 12\nB.feature 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadWeights(p)
	if err != nil || got["A.feature"] != 12 || got["B.feature"] != 0 || len(got) != 2 {
		t.Fatalf("loadWeights = %v, %v", got, err)
	}
	if err := os.WriteFile(p, []byte("A.feature x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWeights(p); err == nil {
		t.Error("毫秒數不是整數時應回傳錯誤")
	}
	if got, err := loadWeights(filepath.Join(dir, "missing.txt")); err != nil || len(got) != 0 {
		t.Errorf("檔案不存在時應回傳空的 map：%v, %v", got, err)
	}
	// 寫出再讀回要一致。
	if err := writeWeights(p, map[string]int{"B.feature": 7, "A.feature": 5}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadWeights(p); err != nil || got["A.feature"] != 5 || got["B.feature"] != 7 {
		t.Errorf("writeWeights 後讀回 = %v, %v", got, err)
	}
}
