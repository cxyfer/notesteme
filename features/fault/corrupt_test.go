//go:build faultinject && unix

package fault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/sqlitedb"
)

// TestCorruptArtifactIsDetectedAndRerun 是 NFR-REL-2 的「讀取時比對 sha256，不符就標記損毀並重跑該步驟」：
// 把劇本做完之後，每一份現行的字幕產出（source、normalized、segmented、corrected、translated）各做一次：改掉檔案中間的一個 byte，經 API 讀取時回 409 artifact_corrupt，
// 產出被標記損毀（corrupt_at），產生它的步驟重跑，做完之後結果與基準相同、所有產出的 sha256 相符。
// 付費：重新產生字幕來源（groq）要再付一次（規格已記錄）；LLM 步驟的 chunk 層快取照常重用，不再呼叫 provider。
func TestCorruptArtifactIsDetectedAndRerun(t *testing.T) {
	base := t.TempDir()
	if res := runChild(t, base, phaseFirst, ""); res.err != nil {
		t.Fatalf("基準執行失敗：%v\n%s", res.err, res.output)
	}
	want, err := inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ variant, lang, paidAgain string }{
		{"source", "en", "api.groq.com"},
		{"normalized", "en", ""},
		{"segmented", "en", ""},
		{"corrected", "en", ""},
		{"translated", "zh-Hant-TW", ""},
	}
	for _, c := range cases {
		if _, ok := want.artifacts["transcript/"+c.variant+"/"+c.lang]; !ok {
			t.Fatalf("基準執行沒有 %s/%s 的字幕產出：%v", c.variant, c.lang, want.artifacts)
		}
		t.Run(c.variant, func(t *testing.T) {
			root := t.TempDir()
			t.Cleanup(func() { _ = removeRoot(root) })
			if res := runChild(t, root, phaseFirst, ""); res.err != nil {
				t.Fatalf("劇本失敗：%v\n%s", res.err, res.output)
			}
			before, err := readPaidCalls(root)
			if err != nil {
				t.Fatal(err)
			}
			id, rel := currentArtifact(t, root, c.variant, c.lang)
			path := filepath.Join(root, "data", filepath.FromSlash(rel))
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			b[len(b)/2] ^= 1
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			res := runChild(t, root, phaseCorrupt, "", envChildRead+"=variant="+c.variant+"&lang="+c.lang)
			if res.err != nil {
				t.Fatalf("讀取損毀的產出：%v\n%s", res.err, res.output)
			}
			if !artifactMarkedCorrupt(t, root, id) {
				t.Errorf("損毀的產出 %s 沒有被標記 corrupt_at", id)
			}
			// 損毀的那一份已被取代；inspect 只檢查現行與舊版的 sha256，損毀的舊版本身例外。
			got, err := inspectExcept(root, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := got.complete(); err != nil {
				t.Fatal(err)
			}
			if diff := want.diff(got); diff != "" {
				t.Errorf("重跑之後的結果與基準不同：\n%s", diff)
			}
			after, err := readPaidCalls(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diffCalls(before, after) {
				if c.paidAgain == "" || !strings.HasPrefix(d, c.paidAgain+" ") || !strings.HasSuffix(d, "基準 1 次，實際 2 次") {
					t.Errorf("重新產生 %s 時多付了：%s", c.variant, d)
				}
			}
			if c.paidAgain != "" && len(diffCalls(before, after)) != 1 {
				t.Errorf("重新產生 %s 應該再呼叫一次 %s：%v", c.variant, c.paidAgain, diffCalls(before, after))
			}
		})
	}
}

// currentArtifact 回傳現行的 transcript 產出（variant、lang）的 ID 與相對路徑。
func currentArtifact(t *testing.T, root, variant, lang string) (id, rel string) {
	t.Helper()
	db, err := sqlitedb.Open(dbPath(root), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.QueryRowContext(context.Background(),
		"SELECT id, path FROM artifacts WHERE kind = 'transcript' AND variant = ? AND lang = ? AND superseded_by IS NULL", variant, lang).Scan(&id, &rel)
	if err != nil {
		t.Fatalf("找現行的 %s/%s：%v", variant, lang, err)
	}
	return id, rel
}

func artifactMarkedCorrupt(t *testing.T, root, id string) bool {
	t.Helper()
	db, err := sqlitedb.Open(dbPath(root), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var corrupt bool
	if err := db.QueryRowContext(context.Background(), "SELECT corrupt_at IS NOT NULL FROM artifacts WHERE id = ?", id).Scan(&corrupt); err != nil {
		t.Fatal(err)
	}
	return corrupt
}
