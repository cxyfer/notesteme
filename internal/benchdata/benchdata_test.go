package benchdata_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/benchdata"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func TestGenerateWritesUsableDataDir(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	prof := benchdata.Lib10k
	prof.Items, prof.Channels, prof.CuesPerItem = 12, 4, 30
	res, err := benchdata.Generate(ctx, dir, prof, benchdata.Options{BatchSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ItemIDs) != 12 || res.IndexedCues != 12*30 {
		t.Fatalf("結果 = %d 部、%d 句", len(res.ItemIDs), res.IndexedCues)
	}

	st, err := store.Open(ctx, dir, benchdata.DefaultClock())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i, id := range res.ItemIDs {
		// 每部影音每個語言只索引一份（SRCH-R1），索引與外部內容表一致。
		n, err := st.SearchIndexCheck(ctx, id)
		if err != nil || n != 30 {
			t.Errorf("影音 %d 的索引列 = %d, %v", i, n, err)
		}
		cur, err := st.Read().CurrentArtifacts(ctx, id, "transcript")
		if err != nil {
			t.Fatal(err)
		}
		want := 3
		if i == 5 { // i%97 == 5：segment 失敗，沒有 segmented
			want = 2
		}
		if len(cur) != want {
			t.Errorf("影音 %d 的現行字幕 = %d 份，預期 %d", i, len(cur), want)
		}
		for _, a := range cur {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(a.Path)))
			if err != nil {
				t.Fatal(err)
			}
			if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != a.SHA256 || int64(len(b)) != a.Size {
				t.Errorf("%s 的 sha256 或大小不符", a.Path)
			}
			fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(a.Path)))
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s 的權限 = %v, %v", a.Path, fi.Mode().Perm(), err)
			}
		}
	}
	// 暫存名稱都已經 rename 就位。
	_ = filepath.WalkDir(filepath.Join(dir, "artifacts"), func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("留下暫存檔 %s", p)
		}
		return nil
	})
	// 全文搜尋找得到決定論的常見詞。
	page, err := st.Read().Search(ctx, store.SearchQuery{Folded: benchdata.CommonTerm, Limit: 10})
	if err != nil || page.Total == 0 {
		t.Errorf("搜尋 %q：total=%d err=%v", benchdata.CommonTerm, page.Total, err)
	}
}

func TestCuesAreDeterministicAndItemLongIsThreeHours(t *testing.T) {
	a := benchdata.Cues(benchdata.ItemLong.CuesPerItem, "en", 7)
	b := benchdata.Cues(benchdata.ItemLong.CuesPerItem, "en", 7)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("同一個 seed 應該產生相同的字幕")
	}
	end := time.Duration(a[len(a)-1].EndMS) * time.Millisecond
	if end < benchdata.ItemLongDuration*9/10 || end > benchdata.ItemLongDuration*11/10 {
		t.Errorf("item-long 長度 = %s，預期約 %s", end, benchdata.ItemLongDuration)
	}
	for i, c := range a {
		if c.Index != i+1 || c.EndMS <= c.StartMS || (i > 0 && c.StartMS < a[i-1].EndMS) || c.Timing != subtitle.TimingSource {
			t.Fatalf("第 %d 句不合法：%+v", i+1, c)
		}
	}
	zh := benchdata.Cues(50, "zh-Hant", 7)
	if !strings.HasSuffix(zh[0].Text, "。") {
		t.Errorf("中文句子 = %q", zh[0].Text)
	}
}

func TestProfiles(t *testing.T) {
	for _, name := range []string{"lib-10k", "item-long"} {
		if p, ok := benchdata.ProfileByName(name); !ok || p.Name != name {
			t.Errorf("ProfileByName(%q) = %+v, %v", name, p, ok)
		}
	}
	if _, ok := benchdata.ProfileByName("nope"); ok {
		t.Error("不認識的名稱應該回傳 false")
	}
	s := benchdata.Lib10k.Scaled(0.05)
	if s.Items != 500 || s.Channels != 25 || s.CuesPerItem != 600 || s.Name != "lib-10k@0.05" {
		t.Errorf("Scaled = %+v", s)
	}
	if benchdata.Lib10k.Scaled(1).Items != 10000 {
		t.Error("比例 1 應該原樣回傳")
	}
	bad := benchdata.Lib10k
	bad.Variants = []subtitle.Variant{subtitle.VariantSegmented, subtitle.VariantSource}
	bad.Items = 1
	if _, err := benchdata.Generate(context.Background(), t.TempDir(), bad, benchdata.Options{}); err == nil {
		t.Error("Variants 順序錯誤時應該拒絕")
	}
}

// 正式的 binary 不得 import benchdata（只給測試、bench 與 genlib 使用）。
func TestNotImportedByBinaries(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("找不到 go 指令")
	}
	out, err := exec.Command(gobin, "list", "-deps", "github.com/cxyfer/notesteme/cmd/...").Output()
	if err != nil {
		t.Fatalf("go list：%v", err)
	}
	for _, line := range strings.Fields(string(out)) {
		if strings.HasPrefix(line, "github.com/cxyfer/notesteme/internal/benchdata") || strings.HasPrefix(line, "github.com/cxyfer/notesteme/internal/nfrbench") {
			t.Errorf("正式的 binary import 了 %s", line)
		}
	}
}
