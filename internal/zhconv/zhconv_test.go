package zhconv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// stripTrailingCommas 移除 JSON 物件與陣列結尾多餘的逗號（上游 testcases.json 有這種寫法）。
// 逐字掃描並追蹤字串狀態，不會動到字串內容。
func stripTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		if c == ',' {
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\n' || b[j] == '\r' || b[j] == '\t') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func TestEmbeddedChecksums(t *testing.T) {
	data, err := resources.ReadFile("opencc/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		CommitID string `json:"commit_id"`
		Entries  map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// 10 份設定 + 17 份字典。
	if len(m.Entries) != 27 {
		t.Errorf("manifest 有 %d 筆，預期 27", len(m.Entries))
	}
	for name, e := range m.Entries {
		b, err := resources.ReadFile("opencc/" + name)
		if err != nil {
			t.Errorf("%s：%v", name, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != e.SHA256 {
			t.Errorf("%s 的 sha256 = %s，預期 %s", name, got, e.SHA256)
		}
		if int64(len(b)) != e.Size {
			t.Errorf("%s 的大小 = %d，預期 %d", name, len(b), e.Size)
		}
	}
	// 每個轉換表的設定檔都在 manifest 中。
	for _, tb := range allTables {
		if _, ok := m.Entries[string(tb)+".json"]; !ok {
			t.Errorf("manifest 缺少 %s.json", tb)
		}
	}
	notice, _ := resources.ReadFile("opencc/NOTICE.md")
	if !strings.Contains(string(notice), m.CommitID) {
		t.Error("NOTICE.md 沒有記錄 manifest 的 commit")
	}
}

func TestDataVersionMatchesFile(t *testing.T) {
	if got := versionFile(); got != DataVersion {
		t.Errorf("opencc/VERSION = %q，DataVersion = %q", got, DataVersion)
	}
}

func TestUnknownTable(t *testing.T) {
	for _, tb := range []Table{"", "t2jp", "hk2t", "../s2t", "s2t.json"} {
		if _, err := Convert(tb, "字"); !errors.Is(err, ErrUnknownTable) {
			t.Errorf("Convert(%q) 錯誤 = %v，預期 ErrUnknownTable", tb, err)
		}
		if Valid(tb) {
			t.Errorf("Valid(%q) 應為 false", tb)
		}
	}
	if len(Tables()) != 12 {
		t.Errorf("Tables() = %d 個", len(Tables()))
	}
}

// TestUpstreamTestcases 對上游 ver.1.4.2 的 golden 測試，逐一執行這十個轉換表的所有案例。
func TestUpstreamTestcases(t *testing.T) {
	raw, err := os.ReadFile("testdata/testcases.json")
	if err != nil {
		t.Fatal(err)
	}
	var tc struct {
		Cases []struct {
			ID       string            `json:"id"`
			Input    string            `json:"input"`
			Expected map[string]string `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(stripTrailingCommas(raw), &tc); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, tb := range allTables {
		t.Run(string(tb), func(t *testing.T) {
			n := 0
			for _, c := range tc.Cases {
				want, ok := c.Expected[string(tb)]
				if !ok {
					continue
				}
				n++
				got, err := Convert(tb, c.Input)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s：Convert(%s, %q) = %q，預期 %q", c.ID, tb, c.Input, got, want)
				}
			}
			if n == 0 {
				t.Error("沒有任何案例")
			}
			total += n
		})
	}
	t.Logf("共執行 %d 個案例", total)
}

// TestScenarioStrings 是驗收場景用到的字串：TRN-R8 的三列、TRN-R9 的前置轉換、COR-R4 的詞彙表。
func TestScenarioStrings(t *testing.T) {
	cases := []struct {
		table      Table
		in, expect string
	}{
		{S2TWP, "打开软件的设置", "開啟軟體的設定"},     // TRN-R8
		{S2HK, "打开软件的设置", "打開軟件的設置"},      // TRN-R8
		{T2S, "這是一個測試", "这是一个测试"},         // TRN-R8
		{S2TW, "这个视频的质量很好", "這個視頻的質量很好"},  // TRN-R9：localize 的前置轉換不做詞彙轉換
		{S2TWP, "这个视频的质量很好", "這個影片的質量很好"}, // 對照：convert 會做詞彙轉換
		{TW2SP, "資料庫", "数据库"},             // COR-R4
		{T2S, "資料庫", "资料库"},               // 對照：沒有詞彙轉換
		{TW2SP, "微服務", "微服务"},
	}
	for _, c := range cases {
		got, err := Convert(c.table, c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.expect {
			t.Errorf("Convert(%s, %q) = %q，預期 %q", c.table, c.in, got, c.expect)
		}
	}
}

func TestConvertEdgeCases(t *testing.T) {
	for _, tb := range Tables() {
		if got, err := Convert(tb, ""); err != nil || got != "" {
			t.Errorf("%s 空字串 = %q, %v", tb, got, err)
		}
		// 非中文、標點與換行原樣保留。
		const keep = "Hello, Kafka 3.0!\n第二行 ⟨|⟩ 🎉"
		got, err := Convert(tb, keep)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{"Hello, Kafka 3.0!\n", "⟨|⟩ 🎉"} {
			if !strings.Contains(got, frag) {
				t.Errorf("%s：%q 轉換後遺失 %q，得到 %q", tb, keep, frag, got)
			}
		}
	}
	// 無效的 UTF-8 位元組不會讓轉換 panic，也不會被吃掉。
	if got, _ := Convert(S2T, "a\xffb"); !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") {
		t.Errorf("無效 UTF-8：%q", got)
	}
}

func TestConvertConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tb := Tables()[i%len(Tables())]
			for j := 0; j < 20; j++ {
				if _, err := Convert(tb, "打开软件的设置，這是一個測試"); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	if got, _ := Convert(S2TWP, "打开软件的设置"); got != "開啟軟體的設定" {
		t.Errorf("並行呼叫後結果不同：%q", got)
	}
}

func TestGroupPolicies(t *testing.T) {
	d1 := &dict{m: map[string]string{"ab": "1", "a": "x"}, maxRune: 2}
	d2 := &dict{m: map[string]string{"abc": "2", "a": "y"}, maxRune: 3}
	short := &group{ms: []matcher{d1, d2}}
	if l, v, _ := short.match("abcd"); l != 2 || v != "1" {
		t.Errorf("short_circuit：第一個有匹配的字典勝出（%d %q）", l, v)
	}
	union := &group{union: true, ms: []matcher{d1, d2}}
	if l, v, _ := union.match("abcd"); l != 3 || v != "2" {
		t.Errorf("union：最長匹配勝出（%d %q）", l, v)
	}
	if l, v, _ := union.match("axx"); l != 1 || v != "x" {
		t.Errorf("union：同長取先出現的（%d %q）", l, v)
	}
	if _, _, ok := union.match("zzz"); ok {
		t.Error("沒有匹配時 ok 應為 false")
	}
}

func TestSegmentMaxMatch(t *testing.T) {
	d := &dict{m: map[string]string{"ab": "", "abc": "", "de": ""}, maxRune: 3}
	got := segment(d, "xyabcdez")
	want := []string{"xy", "abc", "de", "z"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("segment = %q，預期 %q", got, want)
	}
}

func TestStripTrailingCommas(t *testing.T) {
	in := `{"a": ["x,]", "y",], "b": {"c": 1,},}`
	var v map[string]any
	if err := json.Unmarshal(stripTrailingCommas([]byte(in)), &v); err != nil {
		t.Fatal(err)
	}
	if a := v["a"].([]any); a[0] != "x,]" || len(a) != 2 {
		t.Errorf("a = %v", a)
	}
}

func BenchmarkConvertS2TWP(b *testing.B) {
	if _, err := Convert(S2TWP, "预热"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Convert(S2TWP, "打开软件的设置，这个视频的质量很好。我们在这里讨论微服务实践。")
	}
}

// 繁體地區之間的轉換先把來源地區的字形轉成 OpenCC Standard，再套用目標地區（Codex P2）。
func TestRegionalTraditionalComposite(t *testing.T) {
	cases := []struct {
		table    Table
		in, want string
	}{
		{HK2TW, "紅綫", "紅線"},     // 香港字形「綫」要先轉成標準字形
		{TW2HK, "屋裡", "屋裏"},     // 台灣字形「裡」要先轉成標準字形，再轉香港的「裏」
		{T2TW, "紅綫", "紅綫"},      // 只套用目標地區的表（輸入要是標準字形）：不會處理來源地區的字形
		{HK2TW, "拉麪", "拉麵"},     // 兩邊的字都要經過標準字形
		{TW2HK, "一口喫個", "一口吃個"}, // 詞組也走反向字典
	}
	for _, c := range cases {
		got, err := Convert(c.table, c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("Convert(%s, %q) = %q，預期 %q", c.table, c.in, got, c.want)
		}
	}
}
