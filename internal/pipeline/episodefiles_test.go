package pipeline

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
)

func newEpisodeFiles() *episodeFiles { return &episodeFiles{owned: map[string]struct{}{}} }

func writePart(t testing.TB, dir, id, body string) string {
	t.Helper()
	p := filepath.Join(dir, ".episode-"+id+".part")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// 規則 1、2、3、4 逐條驗證（見 episodeFiles 的說明）。
func TestEpisodeFilesRules(t *testing.T) {
	t.Run("Stage 不蓋掉既有檔案", func(t *testing.T) {
		e, dir := newEpisodeFiles(), t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "episode.mp3"), []byte("EXISTING"), 0o600); err != nil {
			t.Fatal(err)
		}
		name, err := e.Stage(writePart(t, dir, "a", "NEW"), dir, ".mp3", sumHex("NEW"))
		if err != nil || name == "episode.mp3" {
			t.Fatalf("name = %q, %v", name, err)
		}
		if readFile(dir, "episode.mp3") != "EXISTING" || readFile(dir, name) != "NEW" {
			t.Errorf("episode.mp3 = %q，%s = %q", readFile(dir, "episode.mp3"), name, readFile(dir, name))
		}
	})
	t.Run("Sweep 與 Commit 不動擁有中的檔案", func(t *testing.T) {
		e, dir := newEpisodeFiles(), t.TempDir()
		a, _ := e.Stage(writePart(t, dir, "a", "A"), dir, ".mp3", sumHex("A"))
		b, _ := e.Stage(writePart(t, dir, "b", "B"), dir, ".mp3", sumHex("B"))
		e.Sweep(dir)
		e.Commit(dir, b)
		if readFile(dir, a) != "A" || readFile(dir, b) != "B" {
			t.Errorf("a = %q，b = %q", readFile(dir, a), readFile(dir, b))
		}
	})
	t.Run("Commit 刪除沒有擁有者的舊檔，保留 keep", func(t *testing.T) {
		e, dir := newEpisodeFiles(), t.TempDir()
		for n, body := range map[string]string{"episode.m4a": "OLD", "episode-deadbeef.mp3": "ORPHAN", "notes.txt": "KEEP", ".episode-x.part": "PART"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := e.Stage(writePart(t, dir, "b", "B"), dir, ".mp3", sumHex("B"))
		e.Commit(dir, b)
		for n, want := range map[string]string{b: "B", "notes.txt": "KEEP", ".episode-x.part": "PART", "episode.m4a": "<missing>", "episode-deadbeef.mp3": "<missing>"} {
			if got := readFile(dir, n); got != want {
				t.Errorf("%s = %q，預期 %q", n, got, want)
			}
		}
	})
	t.Run("Discard 只刪自己的檔案，檔名釋放之後才能重用", func(t *testing.T) {
		e, dir := newEpisodeFiles(), t.TempDir()
		a, _ := e.Stage(writePart(t, dir, "a", "A"), dir, ".mp3", sumHex("A"))
		b, _ := e.Stage(writePart(t, dir, "b", "B"), dir, ".mp3", sumHex("B"))
		if a != "episode.mp3" || b == a {
			t.Fatalf("a = %q，b = %q", a, b)
		}
		e.Discard(dir, a)
		if readFile(dir, a) != "<missing>" || readFile(dir, b) != "B" {
			t.Errorf("a = %q，b = %q", readFile(dir, a), readFile(dir, b))
		}
		if c, _ := e.Stage(writePart(t, dir, "c", "C"), dir, ".mp3", sumHex("C")); c != a {
			t.Errorf("c = %q，%q 釋放之後應可重用", c, a)
		}
	})
	t.Run("Recover 不蓋掉擁有中或已存在的檔案", func(t *testing.T) {
		e, dir := newEpisodeFiles(), t.TempDir()
		a, _ := e.Stage(writePart(t, dir, "a", "A"), dir, ".mp3", sumHex("A"))
		err := e.Recover(writePart(t, dir, "r1", "R1"), dir, a)
		if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassRetryable {
			t.Fatalf("err = %v，預期可重試的錯誤", err)
		}
		e.Commit(dir, a) // 已 commit（登記放掉）：檔案存在，沿用它
		part := writePart(t, dir, "r2", "R2")
		if err := e.Recover(part, dir, a); err != nil || readFile(dir, a) != "A" {
			t.Fatalf("err = %v，檔案 = %q", err, readFile(dir, a))
		}
		if _, err := os.Stat(part); !os.IsNotExist(err) {
			t.Errorf("這次下載的 part 應被刪掉：%v", err)
		}
		// 檔案不見了才放回。
		if err := os.Remove(filepath.Join(dir, a)); err != nil {
			t.Fatal(err)
		}
		if err := e.Recover(writePart(t, dir, "r3", "R3"), dir, a); err != nil || readFile(dir, a) != "R3" {
			t.Errorf("err = %v，檔案 = %q", err, readFile(dir, a))
		}
	})
}

// 併發壓力測試：多個「執行」同時暫存、commit 或丟棄，另外有清理與 ASR 復原一直在跑。
// 不變條件：每個執行在 commit 或丟棄之前，它的檔案內容始終是自己下載的位元組；沒有被清理、復原或別人的丟棄動過。
// 以 -race 執行。
func TestEpisodeFilesConcurrentOwnership(t *testing.T) {
	e, dir := newEpisodeFiles(), t.TempDir()
	const workers, rounds = 8, 60
	var (
		wg   sync.WaitGroup
		stop = make(chan struct{})
		bg   sync.WaitGroup
	)
	errc := make(chan string, workers*rounds)

	// 背景：不停清理、復原（目標是各種可能被占用的檔名）。
	for i := range 2 {
		bg.Add(2)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					e.Sweep(dir, "episode-keepkeep.mp3")
				}
			}
		}()
		go func() {
			defer bg.Done()
			r := rand.New(rand.NewPCG(uint64(i), 7))
			names := []string{"episode.mp3", "episode-00000000.mp3", "episode-11111111.mp3"}
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
					_ = e.Recover(writePart(t, dir, fmt.Sprintf("bg%d-%d", i, n), "RECOVERED"), dir, names[r.IntN(len(names))])
				}
			}
		}()
	}

	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(w), 1))
			for n := range rounds {
				body := fmt.Sprintf("W%d-ROUND%d", w, n)
				name, err := e.Stage(writePart(t, dir, fmt.Sprintf("w%d-%d", w, n), body), dir, ".mp3", sumHex(body))
				if err != nil {
					errc <- fmt.Sprintf("Stage: %v", err)
					return
				}
				for range 5 {
					if got := readFile(dir, name); got != body {
						errc <- fmt.Sprintf("worker %d round %d：%s = %q，預期 %q", w, n, name, got, body)
						return
					}
				}
				if r.IntN(2) == 0 {
					e.Commit(dir, name)
				} else {
					e.Discard(dir, name)
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	bg.Wait()
	close(errc)
	for msg := range errc {
		t.Error(msg)
	}
}
