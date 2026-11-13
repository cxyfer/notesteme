package app

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 版本數以同一組 (kind, variant, lang) 計算；符合多組時失敗並列出各組，不加總。
func TestArtifactVersionsCountsOneGroup(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	id := ingest(t, login(t, srv))
	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}

	count := func(name string) (int, error) { return srv.ArtifactVersions(t.Context(), id, name) }
	for _, name := range []string{"source", "normalized"} {
		if n, err := count(name); err != nil || n != 1 {
			t.Fatalf("%s = %d, %v；預期 1", name, n, err)
		}
	}
	// 沒有符合的產出：0。
	if n, err := count("nope"); err != nil || n != 0 {
		t.Fatalf("nope = %d, %v；預期 0", n, err)
	}
	// kind 同時符合 source 與 normalized 兩組：失敗並列出各組。
	if _, err := count("transcript"); err == nil || !strings.Contains(err.Error(), "transcript/source") || !strings.Contains(err.Error(), "transcript/normalized") {
		t.Fatalf("transcript 符合兩組，應該失敗並列出各組：%v", err)
	}

	seed := func(lang string) {
		t.Helper()
		err := srv.SeedTranscript(t.Context(), pipeline.SeedRequest{ItemID: id, Variant: subtitle.VariantSource, Lang: lang,
			Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "x"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 同一組再寫一次：版本數加一，含被取代的。
	seed("en")
	if n, err := count("source"); err != nil || n != 2 {
		t.Fatalf("source（en 寫兩次）= %d, %v；預期 2", n, err)
	}
	// 另一個語言是另一組：不能把兩組加總成 3。
	seed("ja")
	if n, err := count("source"); err == nil || !strings.Contains(err.Error(), `"en"`) || !strings.Contains(err.Error(), `"ja"`) {
		t.Fatalf("source 有 en 與 ja 兩組，應該失敗並列出各組：%d, %v", n, err)
	}
}
