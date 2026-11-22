package steps

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func newTestScenario(t *testing.T) *Scenario {
	t.Helper()
	s := &Scenario{}
	s.reset(harness.NewT(t))
	t.Cleanup(func() { _ = s.close() })
	return s
}

// 「有沒有準備字幕來源」只看場景累積的狀態：平台字幕軌、seed、腳本化的 fallback 來源。
func TestHasTranscriptSource(t *testing.T) {
	s := newTestScenario(t)
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("什麼都沒準備")
	}

	// 影片有人工字幕軌，但原文語言未知：不符合 caption_priority。
	v := s.World.Video("abc123xxxxx")
	v.Tracks = []faketool.Track{{Kind: faketool.KindManual, Lang: "en"}}
	if s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("原文語言未知時，字幕軌不符合條件")
	}
	v.Language = "en"
	if !s.hasTranscriptSource("yt:abc123xxxxx") {
		t.Error("符合條件的平台字幕軌")
	}

	// 只有機器翻譯的字幕軌不算。
	s.World.Video("tr0001xxxxx").Language = "en"
	s.World.Video("tr0001xxxxx").Tracks = []faketool.Track{{Kind: faketool.KindPlatformTranslated, Lang: "zh-Hans"}}
	if s.hasTranscriptSource("yt:tr0001xxxxx") {
		t.Error("機器翻譯的字幕軌不符合條件")
	}

	// 腳本化的 fallback 來源。
	s.World.Sources.Returns("supadata", "yt:abc999xxxxx", "supadata-en.json")
	if !s.hasTranscriptSource("yt:abc999xxxxx") {
		t.Error("腳本化了 supadata")
	}
	s.World.Sources.Nothing("supadata", "yt:none01xxxxx")
	if s.hasTranscriptSource("yt:none01xxxxx") {
		t.Error("has nothing 不算來源")
	}

	// 已 seed 的字幕。
	s.seeded = map[string]bool{"yt:seed01xxxxx": true}
	if !s.hasTranscriptSource("yt:seed01xxxxx") {
		t.Error("已 seed 的 transcript")
	}
}

func TestItemFixtureFor(t *testing.T) {
	s := newTestScenario(t)
	// 沒有準備來源、也沒有 fixture：pending。
	if _, err := s.itemFixtureFor("yt:nofx01xxxxx", "transcript"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("err = %v", err)
	}
	// DEL 場景用的 yt:abc123xxxxx 有 fixture（影片沒有任何字幕軌時才會用到）。
	if fx, err := s.itemFixtureFor("yt:abc123xxxxx", "transcript"); err != nil || fx == nil || fx.Source.Lang != "en" || len(fx.Source.Cues) < 3 {
		t.Errorf("abc123xxxxx 的 fixture = %+v, err = %v", fx, err)
	}
	// 目標是 metadata：不需要字幕來源。
	if fx, err := s.itemFixtureFor("yt:abc123xxxxx", "metadata"); err != nil || fx != nil {
		t.Errorf("fx = %v, err = %v", fx, err)
	}
	// 有準備來源：照常跑 pipeline，之後沒處理到就是失敗。
	s.World.Sources.Returns("supadata", "yt:abc123xxxxx", "supadata-en.json")
	if fx, err := s.itemFixtureFor("yt:abc123xxxxx", "normalize"); err != nil || fx != nil {
		t.Errorf("fx = %v, err = %v", fx, err)
	}

	// 有 fixture、影片沒有任何字幕軌：回傳 fixture。
	fx, err := s.itemFixtureFor("yt:pg100xxxxxx", "normalize")
	if err != nil || fx == nil || fx.Source.Lang != "en" || len(fx.Source.Cues) < 3 {
		t.Fatalf("fx = %+v, err = %v", fx, err)
	}
	// 影片有字幕軌（即使不符合 caption_priority）：不用 fixture，也不 pending 以外的結果。
	s.World.Video("pg100xxxxxx").Tracks = []faketool.Track{{Kind: faketool.KindPlatformTranslated, Lang: "zh-Hans"}}
	if _, err := s.itemFixtureFor("yt:pg100xxxxxx", "normalize"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("有字幕軌時不該用 fixture：err = %v", err)
	}
}

func TestItemFixtureForRefMismatch(t *testing.T) {
	s := newTestScenario(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"ref":"yt:other01xxxx","source":{"lang":"en","cues":[]}}`
	if err := os.WriteFile(filepath.Join(dir, "items", "yt-abc123xxxxx.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s.World.FixturesDir = dir
	_, err := s.itemFixtureFor("yt:abc123xxxxx", "transcript")
	if err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("ref 與檔名不一致應失敗而非 pending：err = %v", err)
	}
}

// fixture 規則 2 只適用於 yt: 與 bili:；local: 與 pod: 即使有 fixture 也一律 pending（落到規則 3）。
func TestItemFixtureForOnlyYouTubeAndBilibili(t *testing.T) {
	s := newTestScenario(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"yt:fx0001xxxxx":       "yt-fx0001xxxxx.json",
		"bili:BV1xx411c7mD/p1": "bili-BV1xx411c7mD-p1.json",
		"local:meetup.mp4":     "local-meetup.mp4.json",
		"pod:show/ep1":         "pod-show-ep1.json",
	}
	for ref, name := range files {
		body := `{"ref":"` + ref + `","source":{"lang":"en","cues":[{"start":"00:00:01.000","end":"00:00:03.000","text":"hello"}]}}`
		if err := os.WriteFile(filepath.Join(dir, "items", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.World.FixturesDir = dir
	for _, ref := range []string{"yt:fx0001xxxxx", "bili:BV1xx411c7mD/p1"} {
		if fx, err := s.itemFixtureFor(ref, "normalize"); err != nil || fx == nil || fx.Ref != ref {
			t.Errorf("%s 應該使用 fixture：fx = %+v, err = %v", ref, fx, err)
		}
	}
	for _, ref := range []string{"local:meetup.mp4", "pod:show/ep1"} {
		if fx, err := s.itemFixtureFor(ref, "normalize"); fx != nil || !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s 不該使用 fixture，應該 pending：fx = %+v, err = %v", ref, fx, err)
		}
	}
}

func TestItemFixtureName(t *testing.T) {
	for ref, want := range map[string]string{
		"yt:pg100xxxxxx":       "yt-pg100xxxxxx.json",
		"bili:BV1xx411c7mD/p1": "bili-BV1xx411c7mD-p1.json",
	} {
		if got := itemFixtureName(ref); got != want {
			t.Errorf("itemFixtureName(%q) = %q，預期 %q", ref, got, want)
		}
	}
}

// generate 的展開規則：{n} 補零到 4 位、區塊接在前一個區塊的結尾、start 可以指定。
func TestGenerateCues(t *testing.T) {
	cues, err := generateCues("yt:gen01xxxxxx", []fixtureGenerate{
		{Count: 3, Step: "7.5s", Length: "5s", Text: "A {n}"},
		{Count: 2, Step: "6s", Length: "300ms", Text: "B {n}"},
		{Count: 1, Step: "1s", Length: "1s", Text: "C {n}", Start: "00:01:00.000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []subtitle.Cue{
		{Index: 1, StartMS: 0, EndMS: 5000, Text: "A 0001"},
		{Index: 2, StartMS: 7500, EndMS: 12500, Text: "A 0002"},
		{Index: 3, StartMS: 15000, EndMS: 20000, Text: "A 0003"},
		{Index: 4, StartMS: 22500, EndMS: 22800, Text: "B 0004"}, // 22.5s = 3 × 7.5s
		{Index: 5, StartMS: 28500, EndMS: 28800, Text: "B 0005"},
		{Index: 6, StartMS: 60000, EndMS: 61000, Text: "C 0006"},
	}
	if len(cues) != len(want) {
		t.Fatalf("得到 %d 句，預期 %d", len(cues), len(want))
	}
	for i := range want {
		if cues[i].Index != want[i].Index || cues[i].StartMS != want[i].StartMS || cues[i].EndMS != want[i].EndMS || cues[i].Text != want[i].Text {
			t.Errorf("cue %d = %+v，預期 %+v", i+1, cues[i], want[i])
		}
	}
}

// cues 與 generate 只能有一個；generate 的參數不合理時讓步驟失敗，不是 pending。
func TestFixtureSourceRejectsBadInput(t *testing.T) {
	good := fixtureGenerate{Count: 2, Step: "3s", Length: "2s", Text: "x {n}"}
	cue := fixtureCue{Start: "00:00:01.000", End: "00:00:02.000", Text: "hi"}
	mk := func(cues []fixtureCue, gen ...fixtureGenerate) *itemFixture {
		fx := &itemFixture{Ref: "yt:bad01xxxxxx"}
		fx.Source.Cues, fx.Source.Generate = cues, gen
		return fx
	}
	bad := func(mod func(*fixtureGenerate)) *itemFixture {
		g := good
		mod(&g)
		return mk(nil, g)
	}
	for name, fx := range map[string]*itemFixture{
		"兩種都有":           mk([]fixtureCue{cue}, good),
		"兩種都沒有":          mk(nil),
		"step 不是時間長度":    bad(func(g *fixtureGenerate) { g.Step = "three" }),
		"step 為零":        bad(func(g *fixtureGenerate) { g.Step = "0s" }),
		"length 太短":      bad(func(g *fixtureGenerate) { g.Length = "299ms" }),
		"length 超過 step": bad(func(g *fixtureGenerate) { g.Length = "3.001s" }),
		"count 為零":       bad(func(g *fixtureGenerate) { g.Count = 0 }),
		"text 是空的":       bad(func(g *fixtureGenerate) { g.Text = " " }),
		"start 不正確":      bad(func(g *fixtureGenerate) { g.Start = "soon" }),
	} {
		if _, err := fx.cues(); err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("%s：應該失敗而非通過或 pending：err = %v", name, err)
		}
	}
	if cues, err := mk([]fixtureCue{cue}).cues(); err != nil || len(cues) != 1 || cues[0].EndMS != 2000 {
		t.Errorf("只有 cues：%+v, %v", cues, err)
	}
}

// estimateTokens 是 LLM-execution 待決問題 L2 的估算：漢字、假名、諺文一字算 1，其他字元每 4 個算 1（無條件進位），每句再加 1。
func estimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4 + 1
}

// loadM2Fixture 讀取影音 fixture 並展開成 cue。
func loadM2Fixture(t *testing.T, ref string) (*itemFixture, []subtitle.Cue) {
	t.Helper()
	s := newTestScenario(t)
	fx, err := readItemFixture(s.World.FixturesDir, ref)
	if err != nil {
		t.Fatalf("%s：%v", ref, err)
	}
	cues, err := fx.cues()
	if err != nil {
		t.Fatalf("%s：%v", ref, err)
	}
	return fx, cues
}

func sumTokens(cues []subtitle.Cue) int {
	n := 0
	for _, c := range cues {
		n += estimateTokens(c.Text)
	}
	return n
}

// 影音 fixture 的共通限制：能解析、ref 與檔名一致、語言正確、正規化後句數與時間都不變、
// 沒有場景要驗證「由 production 加入」的文字（有的話那些斷言恆真）。
func TestVideoFixturesCommon(t *testing.T) {
	// 這些字串都是場景要驗證「prompt 由 production 組出」或「由詞彙表轉換」的文字。
	forbidden := []string{
		"HACKED", "<transcript_data>", "cache_control", "output was not valid JSON", "資料區塊",
		"Apache Kafka", "KRaft", "Confluent", "⟨|⟩", "<error>", "保留原本的語言",
		"数据库", "資料庫", "微服務", "微服务",
	}
	for ref, lang := range map[string]string{
		"yt:long01xxxxx": "en",
		"yt:chap01xxxxx": "en",
		"yt:long08xxxxx": "en",
		"yt:talk01xxxxx": "en",
		"yt:zh001xxxxxx": "zh-Hans",
		"yt:mix001xxxxx": "zh",
		"yt:tw001xxxxxx": "zh-Hant-TW",
	} {
		t.Run(ref, func(t *testing.T) {
			fx, cues := loadM2Fixture(t, ref)
			if fx.Source.Lang != lang {
				t.Errorf("lang = %q，預期 %q", fx.Source.Lang, lang)
			}
			norm := subtitle.Normalize(cues)
			if len(norm) != len(cues) {
				t.Fatalf("正規化後 %d 句，預期 %d 句不變", len(norm), len(cues))
			}
			for i, c := range cues {
				if norm[i].StartMS != c.StartMS || norm[i].EndMS != c.EndMS || norm[i].Text != c.Text {
					t.Fatalf("cue %d 正規化後改變：%+v → %+v", i+1, c, norm[i])
				}
				for _, bad := range forbidden {
					if strings.Contains(c.Text, bad) {
						t.Errorf("cue %d 含有禁用字串 %q", i+1, bad)
					}
				}
			}
		})
	}
}

// long01 與 long08：每句剛好 76 個 ASCII 字元、20 tokens、3 秒一句，200 句剛好 4000 tokens。
func TestLongVideoFixtures(t *testing.T) {
	for ref, count := range map[string]int{"yt:long01xxxxx": 400, "yt:long08xxxxx": 1600} {
		t.Run(ref, func(t *testing.T) {
			_, cues := loadM2Fixture(t, ref)
			if len(cues) != count {
				t.Fatalf("%d 句，預期 %d", len(cues), count)
			}
			for i, c := range cues {
				if len(c.Text) != 76 || strings.ContainsFunc(c.Text, func(r rune) bool { return r > unicode.MaxASCII }) {
					t.Fatalf("cue %d 應是 76 個 ASCII 字元：%q（%d）", i+1, c.Text, len(c.Text))
				}
				if got := estimateTokens(c.Text); got != 20 {
					t.Fatalf("cue %d 有 %d tokens，預期 20", i+1, got)
				}
				if c.StartMS != int64(i)*3000 || c.EndMS != c.StartMS+2500 {
					t.Fatalf("cue %d 時間 %d–%d，預期每 3 秒一句、長 2.5 秒", i+1, c.StartMS, c.EndMS)
				}
				if !strings.HasPrefix(c.Text, fmt.Sprintf("Line %04d ", i+1)) {
					t.Fatalf("cue %d 的編號不對：%q", i+1, c.Text)
				}
			}
			// 重疊句不計入 max_tokens（L2）：200 句剛好 4000，201 句就超過。
			if got := sumTokens(cues[:200]); got != 4000 {
				t.Errorf("前 200 句 %d tokens，預期 4000", got)
			}
			if got := sumTokens(cues[:201]); got <= 4000 {
				t.Errorf("前 201 句 %d tokens，預期超過 4000", got)
			}
		})
	}
}

// chap01：三章 40、70、40 句，第 41 句從 05:00、第 111 句從 12:00 開始（和 with: 的章節對齊），每章遠小於 4000 tokens。
func TestChapterVideoFixture(t *testing.T) {
	_, cues := loadM2Fixture(t, "yt:chap01xxxxx")
	if len(cues) != 150 {
		t.Fatalf("%d 句，預期 150", len(cues))
	}
	if cues[40].StartMS != 5*60*1000 || cues[110].StartMS != 12*60*1000 {
		t.Errorf("第 41 句 %d ms、第 111 句 %d ms，預期 300000、720000", cues[40].StartMS, cues[110].StartMS)
	}
	// 章節的結束是下一章的開始：前一章最後一句要在下一章開始之前結束。
	if cues[39].EndMS > cues[40].StartMS || cues[109].EndMS > cues[110].StartMS {
		t.Error("章節邊界上的句子不可以跨章")
	}
	for name, part := range map[string][]subtitle.Cue{"1-40": cues[:40], "41-110": cues[40:110], "111-150": cues[110:]} {
		if got := sumTokens(part); got > 1000 {
			t.Errorf("第 %s 句共 %d tokens，每章要遠小於 4000", name, got)
		}
	}
	for i, c := range cues {
		if got := estimateTokens(c.Text); got < 6 || got > 10 {
			t.Fatalf("cue %d 有 %d tokens，預期短句約 8", i+1, got)
		}
	}
}

// talk01：正好 120 句、每 5 秒一句，涵蓋 06:00–08:00（PRM-R6 的 playground，TRN-01 要求 120 句）。
func TestTalkVideoFixture(t *testing.T) {
	_, cues := loadM2Fixture(t, "yt:talk01xxxxx")
	if len(cues) != 120 {
		t.Fatalf("%d 句，預期 120（TRN-01 的 cue 數）", len(cues))
	}
	inWindow := 0
	for i, c := range cues {
		if i > 0 && c.StartMS-cues[i-1].StartMS != 5000 {
			t.Fatalf("cue %d 沒有和前一句相隔 5 秒", i+1)
		}
		if c.StartMS >= 6*60*1000 && c.EndMS <= 8*60*1000 {
			inWindow++
		}
	}
	if last := cues[len(cues)-1]; last.EndMS < 8*60*1000 || last.EndMS > 11*60*1000 {
		t.Errorf("最後一句結束於 %d ms，預期約 10 分鐘", last.EndMS)
	}
	if inWindow < 20 {
		t.Errorf("06:00–08:00 之間只有 %d 句", inWindow)
	}
}

// zh001、mix001、tw001：句數、文字內容與語言特徵。
func TestChineseVideoFixtures(t *testing.T) {
	hasCJK := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
	}
	hasLatin := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return r < unicode.MaxASCII && unicode.IsLetter(r) })
	}
	t.Run("zh001", func(t *testing.T) {
		_, cues := loadM2Fixture(t, "yt:zh001xxxxxx")
		if len(cues) < 10 || len(cues) > 14 {
			t.Errorf("%d 句，預期約 12", len(cues))
		}
		for i, c := range cues {
			if !hasCJK(c.Text) || hasLatin(c.Text) {
				t.Errorf("cue %d 應該是純中文：%q", i+1, c.Text)
			}
		}
	})
	t.Run("mix001", func(t *testing.T) {
		_, cues := loadM2Fixture(t, "yt:mix001xxxxx")
		if len(cues) < 10 || len(cues) > 14 {
			t.Errorf("%d 句，預期約 12", len(cues))
		}
		mixed := 0
		for _, c := range cues {
			if hasCJK(c.Text) && hasLatin(c.Text) {
				mixed++
			}
		}
		if mixed < len(cues)/2 {
			t.Errorf("只有 %d/%d 句中英夾雜", mixed, len(cues))
		}
	})
	t.Run("tw001", func(t *testing.T) {
		_, cues := loadM2Fixture(t, "yt:tw001xxxxxx")
		if len(cues) < 8 || len(cues) > 12 {
			t.Errorf("%d 句，預期約 10", len(cues))
		}
		for i, c := range cues {
			if !hasCJK(c.Text) {
				t.Errorf("cue %d 應該有中文：%q", i+1, c.Text)
			}
		}
	})
}

// `processed up to` 的 M2 目標（segment、correct、translate、publish）依 X1 沒有設定時是 skipped：
// 目標步驟是 skipped 算已處理到（被取消的不算）；中間的 skipped 不算完成，補上設定之後要能重跑。
func TestProcessedUpToTargetMayBeSkipped(t *testing.T) {
	for _, c := range []struct {
		name  string
		st    stageJSON
		stage string
		want  bool
	}{
		{"done", stageJSON{Status: "done"}, "publish", true},
		{"M2 步驟 skipped（no_outputs）", stageJSON{Status: "skipped", BlockReason: "no_outputs"}, "publish", true},
		{"M2 步驟 skipped（not_configured）", stageJSON{Status: "skipped", BlockReason: "not_configured"}, "correct", true},
		{"被取消的 skipped 不算", stageJSON{Status: "skipped", BlockReason: "cancelled"}, "publish", false},
		{"M1 步驟 skipped 不算", stageJSON{Status: "skipped"}, "normalize", false},
		{"blocked 不算", stageJSON{Status: "blocked", BlockReason: "needs_action"}, "publish", false},
		{"failed 不算", stageJSON{Status: "failed"}, "correct", false},
	} {
		if got := reached(c.st, c.stage); got != c.want {
			t.Errorf("%s：reached = %v，預期 %v", c.name, got, c.want)
		}
	}
}

// 中間被略過的步驟不算完成：之後場景設定了 provider 時，它要能重跑。
func TestFirstUnfinishedDoesNotSkipOverSkippedStages(t *testing.T) {
	var it itemJSON
	for _, name := range []string{"metadata", "transcript", "normalize", "segment", "correct", "translate", "publish"} {
		st := "done"
		if name == "correct" {
			st = "skipped"
		}
		it.Stages = append(it.Stages, stageJSON{Stage: name, Status: st})
	}
	if got := firstUnfinished(it, "publish"); got != "correct" {
		t.Errorf("firstUnfinished = %q，預期 correct", got)
	}
	if got := firstUnfinished(it, "segment"); got != "" {
		t.Errorf("只看到 segment 為止：firstUnfinished = %q，預期空字串", got)
	}
}

// `processed up to` 不認識的步驟一律失敗，不是 pending，也不啟動 server。
// M2 的目標（X1 已決定，規則 (2c)）照常執行，done 或 skipped 都算完成，由 LLM、PRM 的場景把關。
func TestProcessedUpToUnknownStageFails(t *testing.T) {
	s := newTestScenario(t)
	if err := s.itemProcessedUpTo("yt:talk01xxxxx", "nope"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("不認識的步驟應該失敗：%v", err)
	}
	if s.server != nil {
		t.Error("不認識的步驟不該啟動 server")
	}
}

// `from the origin`（access.go）不再是 pending：沒有 token 時失敗，有 token 時真的送出請求（無效 token 回 401）。
func TestClientSendsWithTokenFromOrigin(t *testing.T) {
	s := newTestScenario(t)
	if err := s.clientSendsWithTokenFromOrigin("POST /mcp", "missing", "https://evil.example.com"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Fatalf("未建立 token 時應該失敗而非 pending：%v", err)
	}
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	s.World.Refs["token:agent"] = "nst_invalid_token_for_test"
	if err := s.clientSendsWithTokenFromOrigin("POST /mcp", "agent", "https://evil.example.com"); err != nil {
		t.Fatalf("clientSendsWithTokenFromOrigin：%v", err)
	}
	if s.last == nil || s.last.status != http.StatusUnauthorized {
		t.Fatalf("無效 token 應回 401，得到 %+v", s.last)
	}
}

// `from the origin`（access.go）帶 bearer token 打尚未實作的路由時，預期的 403 屬於 handler 的規則：
// 路由回 501 時之後的狀態斷言是 pending（ADR-0015），不是 failed。沒有 token 時仍然失敗。
func TestClientSendsWithTokenFromOriginPendsOnNotImplemented(t *testing.T) {
	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.ownerIsSignedIn(); err != nil {
		t.Fatal(err)
	}
	if err := s.aTokenWithScopes("agent", "read,agent"); err != nil {
		t.Fatal(err)
	}
	// /mcp 已經實作（S9）；改用另一條帶 read scope 就會回 501 的路由（read_state 尚未支援）。
	if err := s.clientSendsWithTokenFromOrigin("GET /api/items?read_state=unread", "agent", "https://evil.example.com"); err != nil {
		t.Fatalf("clientSendsWithTokenFromOrigin：%v", err)
	}
	if s.last == nil || s.last.status != http.StatusNotImplemented || s.last.preRoute403 {
		t.Fatalf("帶 token 的請求打未實作路由：status=%d preRoute403=%v，預期 501 且 preRoute403 為 false", s.last.status, s.last.preRoute403)
	}
	if err := s.responseStatusIs(http.StatusForbidden); !errors.Is(err, godog.ErrPending) {
		t.Fatalf("預期 403 應該 pending，得到 %v", err)
	}
}

// 訊息與動作不符時，只有緊接在前一個步驟送出的請求回 501 才是 pending；其他情況仍然失敗，通過就是通過。
func TestShowsMessageAndActionsPendingOnlyAfter501(t *testing.T) {
	const ref = "yt:abc123xxxxx"
	s := newTestScenario(t)
	if err := s.ownerSendsWith("POST /api/items", &godog.DocString{Content: `{"url":"https://www.youtube.com/watch?v=abc123xxxxx","until":"metadata"}`}); err != nil {
		t.Fatal(err)
	}
	notImplemented := []byte(`{"code":"not_implemented"}`)
	last := func(status int, body []byte, stepsAgo int) {
		s.step = 10
		s.last = &response{status: status, body: body, step: s.step - stepsAgo}
	}
	wantPending := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s：預期 pending，得到 %v", what, err)
		}
	}
	wantFailure := func(what string, err error) {
		t.Helper()
		if err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("%s：預期失敗，得到 %v", what, err)
		}
	}

	// 沒有任何有訊息的步驟（新增後還沒執行）。
	last(http.StatusNotImplemented, notImplemented, 1)
	wantPending("沒有訊息、前一步 501（message）", s.itemShowsMessage(ref, "x"))
	wantPending("沒有訊息、前一步 501（actions）", s.itemOffersActions(ref, "x"))
	last(http.StatusOK, []byte(`{}`), 1)
	wantFailure("沒有訊息、前一步不是 501（message）", s.itemShowsMessage(ref, "x"))
	wantFailure("沒有訊息、前一步不是 501（actions）", s.itemOffersActions(ref, "x"))

	// 影音在垃圾桶中：404 in_trash 帶著訊息與動作，可以比對。
	if err := s.ownerSends("DELETE /api/items/" + ref); err != nil {
		t.Fatal(err)
	}
	last(http.StatusNotImplemented, notImplemented, 1)
	wantPending("訊息不符、前一步 501", s.itemShowsMessage(ref, "不是這句話"))
	wantPending("動作不符、前一步 501", s.itemOffersActions(ref, "不存在的動作"))
	if err := s.itemShowsMessage(ref, "這部影音在垃圾桶中。"); err != nil {
		t.Errorf("訊息相符就該通過，即使前一步是 501：%v", err)
	}
	if err := s.itemOffersActions(ref, "還原"); err != nil {
		t.Errorf("動作相符就該通過，即使前一步是 501：%v", err)
	}

	last(http.StatusInternalServerError, []byte(`{"code":"internal_error"}`), 1)
	wantFailure("訊息不符、前一步是 500", s.itemShowsMessage(ref, "不是這句話"))
	wantFailure("動作不符、前一步是 500", s.itemOffersActions(ref, "不存在的動作"))
	last(http.StatusNotImplemented, []byte(`{"code":"internal_error"}`), 1)
	wantFailure("訊息不符、501 但不是 not_implemented", s.itemShowsMessage(ref, "不是這句話"))
	last(http.StatusNotImplemented, notImplemented, 3)
	wantFailure("訊息不符、501 是更早的請求", s.itemShowsMessage(ref, "不是這句話"))
	wantFailure("動作不符、501 是更早的請求", s.itemOffersActions(ref, "不存在的動作"))
}
