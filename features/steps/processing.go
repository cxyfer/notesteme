package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 這個檔案放「讓影音真的處理到某個步驟」與「步驟結果的斷言」，步驟的註冊在 items.go。

// stageOf 取得影音某個步驟目前的狀態。影音在垃圾桶中（詳情回 404 in_trash）時改讀資料庫：
// 只有 Status 有值，其他欄位（錯誤類別、not_before 等）資料庫查詢沒有提供。
func (s *Scenario) stageOf(ref, stage string) (stageJSON, error) {
	it, err := s.item(ref)
	if _, trashed := inTrashBody(err); trashed {
		status, err := s.dbStageStatus(stage, ref, "影音在垃圾桶中")
		return stageJSON{Stage: stage, Status: status}, err
	}
	if err != nil {
		return stageJSON{}, err
	}
	for _, st := range it.Stages {
		if st.Stage == stage {
			return st, nil
		}
	}
	return stageJSON{}, fmt.Errorf("%s 沒有步驟 %q", ref, stage)
}

// stageFailedWith 比對步驟為 failed，且錯誤類別相符。
func (s *Scenario) stageFailedWith(stage, ref, class string) error {
	st, err := s.stageOf(ref, stage)
	if err != nil {
		return err
	}
	if st.Status != "failed" || st.ErrorClass != class {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期 failed 且錯誤類別為 %q（%s）", ref, stage, st.Status, class, describeStage(st))
	}
	return nil
}

// stageRetriesAt 比對步驟為 pending（重試中），且下一次嘗試的時間相符。
func (s *Scenario) stageRetriesAt(stage, ref, at string) error {
	want, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339：%w", at, err)
	}
	st, err := s.stageOf(ref, stage)
	if err != nil {
		return err
	}
	if st.Status != "pending" || st.NotBefore == nil {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期重試中（pending 並有 not_before）（%s）", ref, stage, st.Status, describeStage(st))
	}
	got, err := time.Parse(time.RFC3339, *st.NotBefore)
	if err != nil {
		return fmt.Errorf("not_before %q 不是 RFC 3339：%w", *st.NotBefore, err)
	}
	if !got.Equal(want) {
		return fmt.Errorf("%s 的步驟 %s 預計在 %s 重試，預期 %s", ref, stage, got.UTC().Format(time.RFC3339), want.UTC().Format(time.RFC3339))
	}
	return nil
}

// liveStage 取得影音步驟目前的狀態（讀 API）。場景讓 server 停下之後，讀 API 會隱含啟動新的 server，
// 啟動時的租約回收會蓋掉要驗證的結果（ADR-0015），所以直接失敗，不默默改變狀態。
// message、actions 資料庫沒有對應的唯讀查詢，目前沒有場景在 server 停下後用到這些步驟。
func (s *Scenario) liveStage(ref, stage string) (stageJSON, error) {
	if s.stopped {
		return stageJSON{}, fmt.Errorf("server 已停止：這個步驟還不支援讀資料庫，請在 server 執行時斷言步驟 %s 的細節", stage)
	}
	return s.stageOf(ref, stage)
}

// stageIsBlockedWith 比對步驟為 blocked，且 block_reason 相符。
func (s *Scenario) stageIsBlockedWith(stage, ref, reason string) error {
	st, err := s.liveStage(ref, stage)
	if err != nil {
		return err
	}
	return checkBlocked(ref, st, reason)
}

func checkBlocked(ref string, st stageJSON, reason string) error {
	if st.Status != "blocked" || st.BlockReason != reason {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期 blocked 且原因為 %q（%s）", ref, st.Stage, st.Status, reason, describeStage(st))
	}
	return nil
}

// stageIsSkippedWith 比對步驟為 skipped，且略過的原因（block_reason）相符。
func (s *Scenario) stageIsSkippedWith(stage, ref, reason string) error {
	st, err := s.liveStage(ref, stage)
	if err != nil {
		return err
	}
	if st.Status != "skipped" || st.BlockReason != reason {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期 skipped 且原因為 %q（%s）", ref, st.Stage, st.Status, reason, describeStage(st))
	}
	return nil
}

// stageWaitsUntil 比對步驟為 waiting，且 not_before 等於指定時間（RFC 3339，以時間點比較，不比時區寫法）。
func (s *Scenario) stageWaitsUntil(stage, ref, at string) error {
	st, err := s.liveStage(ref, stage)
	if err != nil {
		return err
	}
	return checkWaits(ref, st, at)
}

func checkWaits(ref string, st stageJSON, at string) error {
	want, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339：%w", at, err)
	}
	stage := st.Stage
	if st.Status != "waiting" || st.NotBefore == nil {
		return fmt.Errorf("%s 的步驟 %s 是 %q，預期 waiting 並有 not_before（%s）", ref, stage, st.Status, describeStage(st))
	}
	got, err := time.Parse(time.RFC3339, *st.NotBefore)
	if err != nil {
		return fmt.Errorf("not_before %q 不是 RFC 3339：%w", *st.NotBefore, err)
	}
	if !got.Equal(want) {
		return fmt.Errorf("%s 的步驟 %s 會等到 %s，預期 %s", ref, stage, got.UTC().Format(time.RFC3339), want.UTC().Format(time.RFC3339))
	}
	return nil
}

// attentionStage 回傳影音「正在顯示訊息」的步驟：依 pipeline 的順序，第一個不是 done 且有 message 的步驟。
// 沒有這樣的步驟時回傳錯誤並列出各步驟的狀態。
func (s *Scenario) attentionStage(ref string) (stageJSON, error) {
	if s.stopped {
		return stageJSON{}, errors.New("server 已停止：這個步驟還不支援讀資料庫，請在 server 執行時斷言訊息與動作")
	}
	if b, ok := s.lastInTrashConflict(ref); ok {
		// 新增被 409 in_trash 擋下：訊息與動作就在那個回應裡（DEL-R10），比對它（steps.md 4.4）。
		return stageJSON{Stage: "in_trash", Status: "409", Message: b.Message, Actions: b.Actions}, nil
	}
	it, err := s.item(ref)
	if b, trashed := inTrashBody(err); trashed {
		// 影音在垃圾桶中：詳情的 404 回應本身帶著訊息與動作（DEL-R1），比對它們（steps.md 4.4）。
		return stageJSON{Stage: "in_trash", Status: "404", Message: b.Message, Actions: b.Actions}, nil
	}
	if err != nil {
		return stageJSON{}, err
	}
	return pickAttentionStage(ref, it)
}

// lastInTrashConflict 在最近一次送出的請求回應是這部影音的 409 in_trash 時回傳它的內容。
// 斷言步驟不送出請求，所以 s.last 就是它們要檢查的那個回應；影音的 ID 必須與回應的 item_id 相同，
// 別部影音的 409 不算。代號對應不到 ID 時不採用（交給詳情查詢回報）。
func (s *Scenario) lastInTrashConflict(ref string) (trashBody, bool) {
	if s.last == nil || s.last.status != http.StatusConflict {
		return trashBody{}, false
	}
	var b trashBody
	if json.Unmarshal(s.last.body, &b) != nil || b.Code != "in_trash" {
		return trashBody{}, false
	}
	id, err := s.itemID(ref)
	if err != nil || (b.ItemID != "" && b.ItemID != id) {
		return trashBody{}, false
	}
	return b, true
}

// pickAttentionStage 是 attentionStage 的選擇規則（純函式）：依 pipeline 順序取第一個「不是 done 而且有訊息」的步驟；
// 沒有時退回第一個「done 而且有訊息」的步驟（例如 SRC-R15 的「時間可能偏移」掛在已完成的 transcript 步驟）。
func pickAttentionStage(ref string, it itemJSON) (stageJSON, error) {
	byName := map[string]stageJSON{}
	for _, st := range it.Stages {
		byName[st.Stage] = st
	}
	var seen []string
	var doneWithMessage *stageJSON
	for _, name := range pipeline.Stages {
		st, ok := byName[name]
		if !ok {
			continue
		}
		if st.Status != "done" && st.Message != "" {
			return st, nil
		}
		if st.Status == "done" && st.Message != "" && doneWithMessage == nil {
			doneWithMessage = &st
		}
		seen = append(seen, fmt.Sprintf("%s=%s", name, st.Status))
	}
	if doneWithMessage != nil {
		return *doneWithMessage, nil
	}
	return stageJSON{}, &noAttentionStageError{msg: fmt.Sprintf("%s 沒有任何有訊息的步驟（先找不是 done 的，再找 done 的）（%s）", ref, strings.Join(seen, "、"))}
}

// noAttentionStageError 表示影音沒有任何有訊息的步驟。和「查詢影音失敗」分開，
// 才能只把這種「訊息還沒出現」的結果在前一個動作是 501 時轉成 pending。
type noAttentionStageError struct{ msg string }

func (e *noAttentionStageError) Error() string { return e.msg }

// pendingIfPreviousNotImplemented 在緊接在前一個步驟送出的請求回 501 not_implemented 時，把 err 轉成 pending
// （那個動作沒有發生，所以訊息與動作不符，見 ADR-0015）；否則原樣回傳 err。
func (s *Scenario) pendingIfPreviousNotImplemented(err error) error {
	if s.previousRequestNotImplemented() {
		return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
	}
	return err
}

// itemShowsMessage 比對影音顯示的狀態訊息（見 attentionStage），訊息必須完全相同。
// 訊息不符、或沒有任何有訊息的步驟，而且前一個步驟的請求是 501 時為 pending；其他情況仍然失敗。
func (s *Scenario) itemShowsMessage(ref, want string) error {
	st, err := s.attentionStage(ref)
	if err != nil {
		var none *noAttentionStageError
		if errors.As(err, &none) {
			return s.pendingIfPreviousNotImplemented(err)
		}
		return err
	}
	if st.Message != want {
		return s.pendingIfPreviousNotImplemented(fmt.Errorf("%s 的步驟 %s（%s）顯示訊息 %q，預期 %q", ref, st.Stage, st.Status, st.Message, want))
	}
	return nil
}

// itemOffersActions 比對同一個步驟（見 attentionStage）可以執行的動作：
// 參數以逗號切開並去掉前後空白，與動作清單依序完全相等。pending 的條件同 itemShowsMessage。
func (s *Scenario) itemOffersActions(ref, want string) error {
	st, err := s.attentionStage(ref)
	if err != nil {
		var none *noAttentionStageError
		if errors.As(err, &none) {
			return s.pendingIfPreviousNotImplemented(err)
		}
		return err
	}
	wantList := splitList(want)
	if !slices.Equal(st.Actions, wantList) {
		return s.pendingIfPreviousNotImplemented(fmt.Errorf("%s 的步驟 %s（%s）提供動作 %q，預期 %q", ref, st.Stage, st.Status, st.Actions, wantList))
	}
	return nil
}

// splitList 以逗號切開並去掉前後空白；空字串代表空清單。
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// sourceRejectedFor 比對影音詳情的 sources 中，這個來源的狀態是 rejected 且原因相符（SRC-R4、R17）。
func (s *Scenario) sourceRejectedFor(src, ref, reason string) error {
	it, err := s.item(ref)
	if err != nil {
		return err
	}
	return checkRejected(ref, it, src, reason)
}

func checkRejected(ref string, it itemJSON, src, reason string) error {
	for _, e := range it.Sources {
		if e.Source == src && e.State == "rejected" && e.Reason == reason {
			return nil
		}
	}
	return fmt.Errorf("%s 的 sources 沒有「%s 因 %s 被不採用」：%+v", ref, src, reason, it.Sources)
}

// artifactHasVersions 比對產出的版本數，包含被取代的版本。
func (s *Scenario) artifactHasVersions(name, ref string, want int) error {
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	got, err := s.server.ArtifactVersions(s.ctx, id, name)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s 的 %q 產出有 %d 個版本，預期 %d 個", ref, name, got, want)
	}
	return nil
}

// m2TargetStages 是 M2 的步驟：`processed up to` 這些步驟時，目標步驟是 done 或 skipped 都算完成
// （X1：沒有設定 provider 的 LLM 步驟、off、沒有輸出的 publish 會 skipped，steps.md 4.4 規則 (2c)）。
var m2TargetStages = []string{"segment", "correct", "translate", "publish"}

// itemProcessedUpTo 讓影音真的處理到指定步驟（steps.md 第 4.4 節）：
// 影音還不存在就新增（until 為該步驟）；已存在就從第一個還沒完成的步驟重跑到該步驟為止。
// 接著執行 worker 到閒置，最後確認該步驟是 done，否則失敗。
// 目標步驟需要字幕而場景沒有準備來源時，改由 features/fixtures/items/ 填入 source（見 itemFixtureFor）。
func (s *Scenario) itemProcessedUpTo(ref, stage string) error {
	if !pipeline.ValidStage(stage) {
		return fmt.Errorf("步驟 %q 不正確，應是 %v 其中之一", stage, pipeline.Stages)
	}
	fx, err := s.itemFixtureFor(ref, stage)
	if err != nil {
		return err
	}
	wasRunning := s.server != nil
	if err := s.prepareOwner(); err != nil {
		return err
	}
	if fx != nil {
		if err := s.seedItemFixture(ref, fx); err != nil {
			return err
		}
	}
	items, err := s.listItems()
	if err != nil {
		return err
	}
	var existing *itemJSON
	for i := range items {
		if items[i].Ref == ref {
			existing = &items[i]
		}
	}
	switch {
	case existing == nil:
		body := s.ingestBody(ref)
		body["until"] = stage
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		resp, err := s.apiCall(http.MethodPost, "/api/items", raw)
		if err != nil {
			return err
		}
		if resp.status == http.StatusNotImplemented && isNotImplemented(resp.body) {
			return godog.ErrPending
		}
		if resp.status != http.StatusCreated {
			return fmt.Errorf("新增 %s 失敗：HTTP %d %s", ref, resp.status, snippet(resp.body))
		}
		s.recordRefs(resp)
	default:
		// 清單不含步驟，要看詳情。
		detail, err := s.item(ref)
		if err != nil {
			return err
		}
		from := firstUnfinished(detail, stage)
		if from == "" {
			break
		}
		raw, err := json.Marshal(map[string]string{"from": from, "until": stage})
		if err != nil {
			return err
		}
		resp, err := s.apiCall(http.MethodPost, "/api/items/"+url.PathEscape(existing.ID)+"/rerun", raw)
		if err != nil {
			return err
		}
		if resp.status == http.StatusNotImplemented && isNotImplemented(resp.body) {
			return godog.ErrPending
		}
		if resp.status < 200 || resp.status > 299 {
			return fmt.Errorf("重跑 %s 失敗：HTTP %d %s", ref, resp.status, snippet(resp.body))
		}
	}
	if err := s.workerRunsUntilIdle(); err != nil {
		return err
	}
	st, err := s.stageOf(ref, stage)
	if err != nil {
		return err
	}
	if !reached(st, stage) {
		return fmt.Errorf("%s 沒有處理到 %s：步驟是 %q（%s）", ref, stage, st.Status, describeStage(st))
	}
	if !wasRunning && s.server != nil {
		return s.stopTemporaryServer()
	}
	return nil
}

// stopTemporaryServer 關閉由 Given 前置步驟暫時啟動的 server，並重設執行狀態，
// 避免在沒有呼叫 `the server is running` 的場景中佔住 /data 鎖（例如 secrets reset、server starts）。
func (s *Scenario) stopTemporaryServer() error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	srv := s.server
	s.server = nil
	s.started = false
	s.stopped = false
	s.ownerLoggedIn = false
	s.owner = map[string]string{}
	if err := srv.Shutdown(ctx); err != nil {
		srv.Kill()
		return fmt.Errorf("關閉暫時啟動的 server：%w", err)
	}
	return nil
}

// hasTranscriptSource 回報場景有沒有替影音準備任何字幕來源。只看場景自己累積的狀態，不看執行結果：
//   - 已用 `the "source" transcript of … is:` 寫入 source 字幕（其他變體不算來源）。
//   - 假 yt-dlp 的狀態中，有符合 caption_priority（內建預設）的平台字幕軌；原文語言用假 yt-dlp 實際回報的（steps.md 4.3）。
//   - 腳本化了已實作的 fallback 來源（`the source … returns …`）。
//   - 本機影音（local:）在 inbox 有對應的 sidecar 字幕檔，或標記帶有文字字幕串流（見 hasLocalTranscriptSource）。
func (s *Scenario) hasTranscriptSource(ref string) bool {
	if s.seeded[ref] {
		return true
	}
	for _, id := range s.World.Sources.ReturnedFor(ref) {
		if source.Implemented(id) {
			return true
		}
	}
	if strings.HasPrefix(ref, "local:") {
		return s.hasLocalTranscriptSource(ref)
	}
	if strings.HasPrefix(ref, "pod:") {
		// Podcast：feed 腳本化了 <podcast:transcript>，而且 production 已實作這個來源。
		return source.Implemented(podcastTranscriptID) && s.World.Podcasts.HasTranscript(ref)
	}
	key, err := s.videoKey(ref) // yt:<id> 或 bili:<bvid>/p<n>
	if err != nil {
		return false
	}
	v, ok := s.World.Tools.Videos[key]
	if !ok {
		return false
	}
	tracks := make([]transcript.Track, 0, len(v.Tracks))
	for _, t := range v.Tracks {
		tracks = append(tracks, transcript.Track{Lang: t.Lang, Kind: t.Kind})
	}
	chosen, _ := transcript.Select(tracks, v.Language, pipeline.DefaultLanguage().CaptionPriority, false)
	return chosen != nil
}

// itemFixture 是 features/fixtures/items/<代號>.json 的內容：影音處理到 transcript 時 source 字幕的 cue。
// source 用 cues（逐句寫出）或 generate（依規則產生）其中一種，不能同時有。
type itemFixture struct {
	Ref    string `json:"ref"`
	Source struct {
		Lang     string            `json:"lang"`
		Cues     []fixtureCue      `json:"cues"`
		Generate []fixtureGenerate `json:"generate"`
	} `json:"source"`
}

type fixtureCue struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Text  string `json:"text"`
}

// fixtureGenerate 是 generate 的一個區塊：count 句，每 step 開一句、每句長 length，文字取自 text 範本。
// 範本的 {n} 換成從 1 起算的全域 cue 編號（補零到 4 位）。
// 區塊接在前一個區塊的結尾（前一個區塊的開始 + count × step）之後；第一個區塊從 0 開始；start 可以明確指定。
type fixtureGenerate struct {
	Count  int    `json:"count"`
	Step   string `json:"step"`
	Length string `json:"length"`
	Text   string `json:"text"`
	Start  string `json:"start,omitempty"`
}

// minGeneratedCueMS 是 generate 每句的最短長度：短於它會被 NRM-R2 併入前一句。
const minGeneratedCueMS = 300

// cues 把 fixture 的 source 展開成 cue（編號從 1 起算）。cues 與 generate 必須恰好有一個。
func (f *itemFixture) cues() ([]subtitle.Cue, error) {
	src := f.Source
	switch {
	case len(src.Cues) > 0 && len(src.Generate) > 0:
		return nil, fmt.Errorf("fixture %s 的 source 不能同時有 cues 與 generate", f.Ref)
	case len(src.Cues) == 0 && len(src.Generate) == 0:
		return nil, fmt.Errorf("fixture %s 的 source 必須有 cues 或 generate", f.Ref)
	case len(src.Generate) > 0:
		return generateCues(f.Ref, src.Generate)
	}
	cues := make([]subtitle.Cue, 0, len(src.Cues))
	for i, c := range src.Cues {
		cue := subtitle.Cue{Index: i + 1, Text: c.Text}
		var err error
		if cue.StartMS, err = subtitle.ParseTimecode(c.Start); err != nil {
			return nil, fmt.Errorf("fixture %s 第 %d 個 cue 的 start：%w", f.Ref, i+1, err)
		}
		if cue.EndMS, err = subtitle.ParseTimecode(c.End); err != nil {
			return nil, fmt.Errorf("fixture %s 第 %d 個 cue 的 end：%w", f.Ref, i+1, err)
		}
		cues = append(cues, cue)
	}
	return cues, nil
}

// generateCues 依 generate 的區塊產生 cue。
func generateCues(ref string, blocks []fixtureGenerate) ([]subtitle.Cue, error) {
	var cues []subtitle.Cue
	var next int64 // 下一個區塊的預設開始時間（毫秒）
	for bi, b := range blocks {
		where := fmt.Sprintf("fixture %s 的 generate[%d]", ref, bi)
		step, err := time.ParseDuration(b.Step)
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("%s：step %q 必須是正的時間長度（例如 3s）", where, b.Step)
		}
		length, err := time.ParseDuration(b.Length)
		if err != nil || length.Milliseconds() < minGeneratedCueMS || length > step {
			return nil, fmt.Errorf("%s：length %q 必須在 %dms 與 step 之間", where, b.Length, minGeneratedCueMS)
		}
		if b.Count < 1 || strings.TrimSpace(b.Text) == "" {
			return nil, fmt.Errorf("%s：count 至少 1，text 不能是空的", where)
		}
		start := next
		if b.Start != "" {
			if start, err = subtitle.ParseTimecode(b.Start); err != nil {
				return nil, fmt.Errorf("%s：start：%w", where, err)
			}
		}
		for i := 0; i < b.Count; i++ {
			n := len(cues) + 1
			s := start + int64(i)*step.Milliseconds()
			cues = append(cues, subtitle.Cue{
				Index:   n,
				StartMS: s,
				EndMS:   s + length.Milliseconds(),
				Text:    strings.ReplaceAll(b.Text, "{n}", fmt.Sprintf("%04d", n)),
			})
		}
		next = start + int64(b.Count)*step.Milliseconds()
	}
	return cues, nil
}

// itemFixtureName 把代號轉成 fixture 檔名：`:`、`/` 換成 `-`，例如 yt:pg100xxxxxx → yt-pg100xxxxxx.json。
func itemFixtureName(ref string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(ref) + ".json"
}

// usesItemFixture 回報這個代號能不能用影音 fixture：只有 yt: 與 bili:。
func usesItemFixture(ref string) bool {
	return strings.HasPrefix(ref, "yt:") || strings.HasPrefix(ref, "bili:")
}

// hasAnyTrack 回報假 yt-dlp 中這部影片是否有任何字幕軌（不論是否符合 caption_priority）。
func (s *Scenario) hasAnyTrack(ref string) bool {
	key, err := s.videoKey(ref)
	if err != nil {
		return false
	}
	v, ok := s.World.Tools.Videos[key]
	return ok && len(v.Tracks) > 0
}

// itemFixtureFor 決定 `has been processed up to` 要怎麼取得字幕（steps.md 第 4.4 節）：
//  1. 目標步驟在 transcript 之前，或場景已準備字幕來源（hasTranscriptSource）：回傳 nil，照常跑 pipeline。
//  2. 代號是 yt: 或 bili:、影片沒有任何字幕軌、場景也沒寫入字幕或腳本化來源，而且 fixtures/items/<代號>.json 存在：回傳該 fixture。
//  3. 其他情況（包含 local:、pod:，即使有 fixture）：pending（場景沒有任何方式產生字幕）。
//
// fixture 的 ref 必須與檔名一致，否則失敗。是否 pending 只看場景準備了什麼，不看步驟的參數內容。
func (s *Scenario) itemFixtureFor(ref, stage string) (*itemFixture, error) {
	if slices.Index(pipeline.Stages, stage) < slices.Index(pipeline.Stages, pipeline.StageTranscript) || s.hasTranscriptSource(ref) {
		return nil, nil
	}
	// 規則 2 只適用於 yt: 與 bili:（假 yt-dlp 管的平台）；local:、pod: 不套用 fixture，落到規則 3。
	if usesItemFixture(ref) && !s.hasAnyTrack(ref) && len(s.World.Sources.ReturnedFor(ref)) == 0 {
		fx, err := readItemFixture(s.World.FixturesDir, ref)
		if err == nil {
			return fx, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("場景沒有替 %s 準備字幕來源，也沒有 features/fixtures/items/ 的 fixture：%w", ref, godog.ErrPending)
}

// readItemFixture 讀取 <fixturesDir>/items/<代號>.json：不認識的欄位、ref 與檔名不一致都是錯誤。
// 檔案不存在時回傳的錯誤符合 fs.ErrNotExist。
func readItemFixture(fixturesDir, ref string) (*itemFixture, error) {
	name := itemFixtureName(ref)
	raw, err := os.ReadFile(filepath.Join(fixturesDir, "items", name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 fixture %s：%w", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var fx itemFixture
	if err := dec.Decode(&fx); err != nil {
		return nil, fmt.Errorf("fixture %s 格式不正確：%w", name, err)
	}
	if fx.Ref != ref {
		return nil, fmt.Errorf("fixture %s 的 ref 是 %q，必須與檔名一致（%q）", name, fx.Ref, ref)
	}
	return &fx, nil
}

// seedItemFixture 建立影音（做到 metadata），再把 fixture 的 source 字幕直接寫成產出。
func (s *Scenario) seedItemFixture(ref string, fx *itemFixture) error {
	cues, err := fx.cues()
	if err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	req := pipeline.SeedRequest{ItemID: id, Variant: subtitle.VariantSource, Lang: fx.Source.Lang, Cues: cues}
	if err := s.server.SeedTranscript(s.ctx, req); err != nil {
		return err
	}
	if s.seeded == nil {
		s.seeded = map[string]bool{}
	}
	s.seeded[ref] = true
	return nil
}

// reached 回報目標步驟算不算已經處理到：done；M2 步驟沒有設定時依 X1 是 skipped（not_configured、no_outputs…），
// 也算（被取消的 cancelled 不算）。
func reached(st stageJSON, stage string) bool {
	if st.Status == "done" {
		return true
	}
	return st.Status == "skipped" && st.BlockReason != "cancelled" && slices.Contains(m2TargetStages, stage)
}

// firstUnfinished 回傳從開頭到 upTo（含）第一個還不是 done 的步驟；都完成了回傳空字串。
// skipped 不算完成：之前沒有設定而被略過的步驟，場景補上設定之後要能重跑（只有目標步驟接受 skipped，見 reached）。
func firstUnfinished(it itemJSON, upTo string) string {
	for _, name := range pipeline.Stages {
		for _, st := range it.Stages {
			if st.Stage == name && st.Status != "done" {
				return name
			}
		}
		if name == upTo {
			break
		}
	}
	return ""
}
