package steps

import (
	"errors"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v34"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

// ingestAndRun 為影音新增（帶單次覆寫的 YAML）並執行 worker 到閒置。
func ingestAndRun(t *testing.T, s *Scenario, ref, overrides string) {
	t.Helper()
	if err := s.ownerIngestsWithOverrides(ref, &godog.DocString{Content: overrides}); err != nil {
		t.Fatal(err)
	}
	if err := s.workerRunsUntilIdle(); err != nil {
		t.Fatal(err)
	}
}

// table 組出 godog 表格（第一列是表頭）。
func table(rows ...[]string) *godog.Table {
	t := &godog.Table{}
	for _, r := range rows {
		row := &messages.PickleTableRow{}
		for _, c := range r {
			row.Cells = append(row.Cells, &messages.PickleTableCell{Value: c})
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

// wantFail 確認 err 是真正的失敗（不是 pending），且訊息包含指定文字。
func wantFail(t *testing.T, what string, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s：應該失敗", what)
		return
	}
	if errors.Is(err, godog.ErrPending) {
		t.Errorf("%s：失敗不應該是 pending：%v", what, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("%s：錯誤訊息 %q 不含 %q", what, err, contains)
	}
}

func ptr(s string) *string { return &s }

// blocked 的斷言：狀態與原因都要符合。
func TestCheckBlocked(t *testing.T) {
	st := stageJSON{Stage: "transcript", Status: "blocked", BlockReason: "needs_action"}
	if err := checkBlocked("yt:a", st, "needs_action"); err != nil {
		t.Errorf("符合：%v", err)
	}
	wantFail(t, "原因不符", checkBlocked("yt:a", st, "budget_blocked"), "budget_blocked")
	st.Status = "pending"
	wantFail(t, "狀態不符", checkBlocked("yt:a", st, "needs_action"), `"pending"`)
}

// waits until 比對時間點：時區寫法不同但時間相同算相符。
func TestCheckWaits(t *testing.T) {
	st := stageJSON{Stage: "transcript", Status: "waiting", NotBefore: ptr("2026-11-12T12:00:00Z")}
	for _, at := range []string{"2026-11-12T12:00:00Z", "2026-11-12T20:00:00+08:00"} {
		if err := checkWaits("yt:a", st, at); err != nil {
			t.Errorf("%s 應該相符：%v", at, err)
		}
	}
	wantFail(t, "時間不符", checkWaits("yt:a", st, "2026-11-12T12:00:01Z"), "預期 2026-11-12T12:00:01Z")
	wantFail(t, "不是 RFC 3339", checkWaits("yt:a", st, "tomorrow"), "RFC 3339")
	retry := stageJSON{Stage: "transcript", Status: "pending", NotBefore: ptr("2026-11-12T12:00:00Z")}
	wantFail(t, "pending 不算 waiting", checkWaits("yt:a", retry, "2026-11-12T12:00:00Z"), `"pending"`)
	wantFail(t, "沒有 not_before", checkWaits("yt:a", stageJSON{Stage: "transcript", Status: "waiting"}, "2026-11-12T12:00:00Z"), "not_before")
}

// 訊息與動作取自 pipeline 順序中第一個「不是 done 且有 message」的步驟。
func TestPickAttentionStage(t *testing.T) {
	it := itemJSON{Stages: []stageJSON{
		// API 的順序與 pipeline 不同時，仍依 pipeline 的順序。
		{Stage: "normalize", Status: "failed", Message: "N"},
		{Stage: "transcript", Status: "waiting", Message: "T", Actions: []string{"a"}},
		{Stage: "metadata", Status: "done", Message: "完成時的備註"},
	}}
	st, err := pickAttentionStage("yt:a", it)
	if err != nil || st.Stage != "transcript" {
		t.Fatalf("得到 %+v, %v；預期 transcript（metadata 已 done，略過）", st, err)
	}
	// 沒有任何訊息：失敗並列出各步驟的狀態。
	none := itemJSON{Stages: []stageJSON{{Stage: "metadata", Status: "done"}, {Stage: "transcript", Status: "pending"}}}
	wantFail(t, "沒有符合的步驟", func() error { _, e := pickAttentionStage("yt:a", none); return e }(), "transcript=pending")
}

// 沒有「不是 done 而且有訊息」的步驟時，退回 pipeline 順序中第一個「done 而且有訊息」的步驟
// （SRC-R15 的提醒掛在 done 的 transcript）。
func TestPickAttentionStageFallsBackToDone(t *testing.T) {
	it := itemJSON{Stages: []stageJSON{
		{Stage: "normalize", Status: "done", Message: "N"},
		{Stage: "transcript", Status: "done", Message: "時間可能偏移"},
		{Stage: "metadata", Status: "done"},
	}}
	st, err := pickAttentionStage("pod:show/ep-43", it)
	if err != nil || st.Stage != "transcript" || st.Message != "時間可能偏移" {
		t.Fatalf("得到 %+v, %v；預期 transcript（pipeline 順序中第一個 done 且有訊息）", st, err)
	}
	// 同時有 done 與非 done 的訊息：非 done 優先，即使它在 pipeline 較後面。
	it.Stages[0] = stageJSON{Stage: "normalize", Status: "failed", Message: "失敗"}
	if st, err := pickAttentionStage("pod:show/ep-43", it); err != nil || st.Stage != "normalize" {
		t.Fatalf("得到 %+v, %v；預期 normalize（非 done 優先）", st, err)
	}
}

func TestSplitList(t *testing.T) {
	for in, want := range map[string][]string{
		"更新 yt-dlp, 上傳 cookie": {"更新 yt-dlp", "上傳 cookie"},
		"設定 API key":           {"設定 API key"},
		" a ,b,  c":            {"a", "b", "c"},
		"":                     {},
	} {
		got := splitList(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitList(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestCheckRejected(t *testing.T) {
	it := itemJSON{Sources: []sourceJSON{
		{Source: "supadata", State: "rejected", Reason: "language_mismatch"},
		{Source: "groq", State: "used"},
	}}
	if err := checkRejected("yt:a", it, "supadata", "language_mismatch"); err != nil {
		t.Errorf("符合：%v", err)
	}
	wantFail(t, "原因不符", checkRejected("yt:a", it, "supadata", "repetition"), "repetition")
	wantFail(t, "用到的來源不算 rejected", checkRejected("yt:a", it, "groq", "language_mismatch"), "groq")
	wantFail(t, "沒有這個來源", checkRejected("yt:a", it, "voxtral", "language_mismatch"), "voxtral")
}

func TestCheckCueTiming(t *testing.T) {
	cue := func(timing string) (c struct {
		Index  int    `json:"index"`
		Start  string `json:"start"`
		End    string `json:"end"`
		Text   string `json:"text"`
		Timing string `json:"timing"`
		Words  []struct {
			Text  string `json:"text"`
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"words"`
	}) {
		c.Timing = timing
		return c
	}
	var got cuesJSON
	wantFail(t, "沒有 cue 不能算通過", checkCueTiming("yt:a", "source", got, "source"), "沒有任何 cue")
	got.Cues = append(got.Cues, cue("source"), cue("source"))
	if err := checkCueTiming("yt:a", "source", got, "source"); err != nil {
		t.Errorf("全部符合：%v", err)
	}
	got.Cues = append(got.Cues, cue("interpolated"))
	wantFail(t, "有一句不同", checkCueTiming("yt:a", "source", got, "source"), "第 3 句")
	wantFail(t, "全部都不是", checkCueTiming("yt:a", "source", got, "word"), "第 1 句")
}

func TestProvenanceValue(t *testing.T) {
	doc := map[string]any{
		"source": "youtube-auto",
		"tool_versions": map[string]any{
			"yt-dlp": "2026.11.01",
		},
		"track":  map[string]any{"lang": "en"},
		"params": []any{map[string]any{"lang": "zh"}, "x"},
		"flag":   true,
		"none":   nil,
	}
	parents := []string{"source", "normalized"}
	for field, want := range map[string]string{
		"source":               "youtube-auto",
		"tool_versions.yt-dlp": "2026.11.01",
		"track.lang":           "en",
		"params.0.lang":        "zh",
		"params.1":             "x",
		"flag":                 "true",
		"none":                 "null",
		"track":                `{"lang":"en"}`,
		"parents":              "source, normalized",
		"parents.1":            "normalized",
	} {
		got, err := provenanceValue(doc, parents, field)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v；預期 %q", field, got, err, want)
		}
	}
	for _, field := range []string{"nope", "track.nope", "params.5", "params.x", "source.deeper", "parents.9"} {
		if got, err := provenanceValue(doc, parents, field); err == nil {
			t.Errorf("%s 不存在，應該失敗，得到 %q", field, got)
		}
	}
}

func TestProvenanceRows(t *testing.T) {
	rows, err := provenanceRows(table([]string{"field", "value"}, []string{"source", "youtube-auto"}, []string{"parents", ""}))
	if err != nil || len(rows) != 2 || rows[0].field != "source" || rows[1].value != "" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if _, err := provenanceRows(table([]string{"field", "value"})); err == nil {
		t.Error("只有表頭應該失敗")
	}
	if _, err := provenanceRows(table([]string{"a", "b", "c"})); err == nil {
		t.Error("欄數不對應該失敗")
	}
	if _, err := provenanceRows(nil); err == nil {
		t.Error("沒有表格應該失敗")
	}
}

// 來源紀錄與 cue 的時間來源：讀真實 server 的產出（假 yt-dlp 的自動字幕）。
func TestProvenanceAndTimingSteps(t *testing.T) {
	s := newTestScenario(t)
	s.World.Video("abc123xxxxx").Language = "en"
	if err := s.hasCaptions("yt:abc123xxxxx", "auto", "en"); err != nil {
		t.Fatal(err)
	}
	ingestAndRun(t, s, "yt:abc123xxxxx", "until: normalize\n")

	ok := table([]string{"field", "value"},
		[]string{"source", "youtube-auto"},
		[]string{"tool_versions.yt-dlp", "2026.11.01"},
		[]string{"track.lang", "en"},
		[]string{"parents", ""})
	if err := s.artifactHasProvenance("source", "yt:abc123xxxxx", ok); err != nil {
		t.Errorf("source 的來源紀錄：%v", err)
	}
	// 上游是變體名稱：normalized 來自 source。
	if err := s.artifactHasProvenance("normalized", "yt:abc123xxxxx", table([]string{"field", "value"}, []string{"parents", "source"})); err != nil {
		t.Errorf("normalized 的 parents：%v", err)
	}
	wantFail(t, "parents 不符", s.artifactHasProvenance("normalized", "yt:abc123xxxxx", table([]string{"field", "value"}, []string{"parents", "normalized"})), "parents")
	wantFail(t, "值不符", s.artifactHasProvenance("source", "yt:abc123xxxxx", table([]string{"field", "value"}, []string{"source", "supadata"})), `"youtube-auto"`)
	wantFail(t, "欄位不存在", s.artifactHasProvenance("source", "yt:abc123xxxxx", table([]string{"field", "value"}, []string{"tool_versions.ffmpeg", "x"})), "沒有欄位")
	wantFail(t, "沒有這種產出", s.artifactHasProvenance("translated", "yt:abc123xxxxx", ok), "沒有")
	// kind 同時符合 source 與 normalized：不猜是哪一個。
	wantFail(t, "符合多個產出", s.artifactHasProvenance("transcript", "yt:abc123xxxxx", ok), "多個")

	if err := s.everyCueHasTiming("source", "yt:abc123xxxxx", "source"); err != nil {
		t.Errorf("每個 cue 的時間來源：%v", err)
	}
	wantFail(t, "時間來源不符", s.everyCueHasTiming("source", "yt:abc123xxxxx", "interpolated"), "第 1 句")
}

// message 與 actions：讀真實 server 的 stage（沒有字幕來源時 transcript 失敗並附上動作）。
func TestMessageAndActionsSteps(t *testing.T) {
	s := newTestScenario(t)
	if err := s.youtubeVideoTitled("yt:nocap1xxxxx", "No captions here"); err != nil {
		t.Fatal(err)
	}
	s.World.Sources.Nothing("supadata", "yt:nocap1xxxxx")
	ingestAndRun(t, s, "yt:nocap1xxxxx", "language: { fallback: [supadata] }\nuntil: transcript\n")

	if err := s.itemShowsMessage("yt:nocap1xxxxx", "找不到可用的字幕來源。"); err != nil {
		t.Errorf("訊息：%v", err)
	}
	wantFail(t, "訊息不符", s.itemShowsMessage("yt:nocap1xxxxx", "找不到字幕"), "找不到可用的字幕來源。")
	if err := s.itemOffersActions("yt:nocap1xxxxx", "設定語音轉錄服務"); err != nil {
		t.Errorf("動作：%v", err)
	}
	wantFail(t, "動作不符", s.itemOffersActions("yt:nocap1xxxxx", "設定語音轉錄服務, 上傳 cookie"), "上傳 cookie")
	wantFail(t, "多餘的動作", s.itemOffersActions("yt:nocap1xxxxx", ""), "設定語音轉錄服務")
	// 狀態斷言走同一個 stage：failed 不是 blocked，也不是 waiting。
	wantFail(t, "不是 blocked", s.stageIsBlockedWith("transcript", "yt:nocap1xxxxx", "needs_action"), `"failed"`)
	wantFail(t, "不是 waiting", s.stageWaitsUntil("transcript", "yt:nocap1xxxxx", "2026-11-12T12:00:00Z"), `"failed"`)

	// server 停止之後，這些步驟不隱含啟動新的 server。
	if err := s.serverShutsDown(); err != nil {
		t.Fatal(err)
	}
	wantFail(t, "server 已停止", s.itemShowsMessage("yt:nocap1xxxxx", "x"), "server 已停止")
	wantFail(t, "server 已停止", s.stageIsBlockedWith("transcript", "yt:nocap1xxxxx", "needs_action"), "server 已停止")
	if s.server != nil {
		t.Error("不應該啟動新的 server")
	}
}

// 沒有 API key 的來源被略過，全部都不能用時 transcript 是 blocked。
func TestBlockedWithoutAPIKey(t *testing.T) {
	s := newTestScenario(t)
	if err := s.youtubeVideoTitled("yt:nocap1xxxxx", "No captions here"); err != nil {
		t.Fatal(err)
	}
	if err := s.providerHasNoAPIKey("supadata"); err != nil {
		t.Fatal(err)
	}
	ingestAndRun(t, s, "yt:nocap1xxxxx", "language: { fallback: [supadata] }\nuntil: transcript\n")
	if err := s.stageIsBlockedWith("transcript", "yt:nocap1xxxxx", "needs_action"); err != nil {
		t.Errorf("blocked：%v", err)
	}
	wantFail(t, "原因不符", s.stageIsBlockedWith("transcript", "yt:nocap1xxxxx", "awaiting_user"), "awaiting_user")
	if err := s.itemOffersActions("yt:nocap1xxxxx", "設定 API key"); err != nil {
		t.Errorf("動作：%v", err)
	}
}

func TestAutoCaptionsNotReady(t *testing.T) {
	s := newTestScenario(t)
	if err := s.hasCaptions("yt:fresh1xxxxx", "manual", "en"); err != nil {
		t.Fatal(err)
	}
	if err := s.autoCaptionsNotReady("yt:fresh1xxxxx"); err != nil {
		t.Fatal(err)
	}
	if !s.World.Tools.Videos["fresh1xxxxx"].AutoNotReady {
		t.Error("AutoNotReady 應該為 true")
	}
	if st, err := faketool.ReadState(s.World.Root); err != nil || !st.Videos["fresh1xxxxx"].AutoNotReady {
		t.Errorf("狀態檔沒有同步：%+v, %v", st, err)
	}
	if tr := s.World.Tools.Videos["fresh1xxxxx"].Tracks; len(tr) != 1 {
		t.Errorf("字幕軌不應被動到：%+v", tr)
	}
	wantFail(t, "只接受 yt:", s.autoCaptionsNotReady("bili:BV1ai411c7mD/p1"), "YouTube")
}

func TestYtdlpFails(t *testing.T) {
	s := newTestScenario(t)
	if err := s.ytdlpFails("yt:abc123xxxxx", "ERROR: HTTP Error 403: Forbidden"); err != nil {
		t.Fatal(err)
	}
	f := s.World.Tools.Videos["abc123xxxxx"].Fail
	if f == nil || f.Stderr != "ERROR: HTTP Error 403: Forbidden" || f.ExitCode != 1 || len(f.On) != 0 {
		t.Errorf("Fail = %+v；預期 stderr 如指定、結束碼 1、全部操作", f)
	}
	if st, err := faketool.ReadState(s.World.Root); err != nil || st.Videos["abc123xxxxx"].Fail == nil {
		t.Errorf("狀態檔沒有同步：%+v, %v", st, err)
	}
	wantFail(t, "只接受 yt:", s.ytdlpFails("local:a.mp4", "x"), "YouTube")
}

func TestProviderHasNoAPIKey(t *testing.T) {
	// LLM provider：移除環境變數與 fakellm 的 key 要求（細節見 llm_test.go）。
	s := newTestScenario(t)
	if err := s.providerHasNoAPIKey("anthropic"); err != nil {
		t.Errorf("LLM provider：%v", err)
	}
	if _, ok := s.World.Env[harness.AnthropicAPIKeyEnv]; ok {
		t.Error("anthropic 的環境變數應該被移除")
	}
	// 還沒實作的字幕來源：pending，環境不動（key 還在）。
	if err := s.providerHasNoAPIKey("whisper-1"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("未實作的來源應該 pending：%v", err)
	}
	if s.World.Env[harness.GroqAPIKeyEnv] == "" {
		t.Error("pending 時不應該移除 key")
	}
	// 平台字幕來源：沒有 API key，明確失敗（不是 pending），訊息指出需要 cookie。
	if err := s.providerHasNoAPIKey("bilibili-ai"); err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "cookie") {
		t.Errorf("bilibili-ai 應該明確失敗並說明需要 cookie：%v", err)
	}
	// 已實作的來源：移除環境變數與 fake 的 key 要求。
	if err := s.providerHasNoAPIKey("supadata"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.World.Env["NOTESTEME_SUPADATA_API_KEY"]; ok {
		t.Error("supadata 的環境變數應該被移除")
	}
	// 已實作、但沒有 API key 的概念（平台字幕）：失敗，不是 pending。
	wantFail(t, "youtube-manual", s.providerHasNoAPIKey("youtube-manual"), "youtube-manual")
	// server 已經在執行：環境變數不會重新讀取，失敗。
	s2 := newTestScenario(t)
	if err := s2.startServer(); err != nil {
		t.Fatal(err)
	}
	wantFail(t, "server 已在執行", s2.providerHasNoAPIKey("supadata"), "server 已經在執行")
	if s2.World.Env["NOTESTEME_SUPADATA_API_KEY"] == "" {
		t.Error("失敗時不應該移除 key")
	}
}
