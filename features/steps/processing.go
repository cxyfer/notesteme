package steps

import (
	"bytes"
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

// stageOf 取得影音某個步驟目前的狀態。
func (s *Scenario) stageOf(ref, stage string) (stageJSON, error) {
	it, err := s.item(ref)
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
	it, err := s.item(ref)
	if err != nil {
		return stageJSON{}, err
	}
	return pickAttentionStage(ref, it)
}

// pickAttentionStage 是 attentionStage 的選擇規則（純函式）。
func pickAttentionStage(ref string, it itemJSON) (stageJSON, error) {
	byName := map[string]stageJSON{}
	for _, st := range it.Stages {
		byName[st.Stage] = st
	}
	var seen []string
	for _, name := range pipeline.Stages {
		st, ok := byName[name]
		if !ok {
			continue
		}
		if st.Status != "done" && st.Message != "" {
			return st, nil
		}
		seen = append(seen, fmt.Sprintf("%s=%s", name, st.Status))
	}
	return stageJSON{}, fmt.Errorf("%s 沒有任何「不是 done 而且有訊息」的步驟（%s）", ref, strings.Join(seen, "、"))
}

// itemShowsMessage 比對影音顯示的狀態訊息（見 attentionStage），訊息必須完全相同。
func (s *Scenario) itemShowsMessage(ref, want string) error {
	st, err := s.attentionStage(ref)
	if err != nil {
		return err
	}
	if st.Message != want {
		return fmt.Errorf("%s 的步驟 %s（%s）顯示訊息 %q，預期 %q", ref, st.Stage, st.Status, st.Message, want)
	}
	return nil
}

// itemOffersActions 比對同一個步驟（見 attentionStage）可以執行的動作：
// 參數以逗號切開並去掉前後空白，與動作清單依序完全相等。
func (s *Scenario) itemOffersActions(ref, want string) error {
	st, err := s.attentionStage(ref)
	if err != nil {
		return err
	}
	wantList := splitList(want)
	if !slices.Equal(st.Actions, wantList) {
		return fmt.Errorf("%s 的步驟 %s（%s）提供動作 %q，預期 %q", ref, st.Stage, st.Status, st.Actions, wantList)
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
		raw, err := json.Marshal(map[string]string{"url": refToURL(ref), "until": stage})
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
	if st.Status != "done" {
		return fmt.Errorf("%s 沒有處理到 %s：步驟是 %q（%s）", ref, stage, st.Status, describeStage(st))
	}
	return nil
}

// hasTranscriptSource 回報場景有沒有替影音準備任何字幕來源。只看場景自己累積的狀態，不看執行結果：
//   - 已用 `the … transcript of … is:` 寫入字幕。
//   - 假 yt-dlp 的狀態中，有符合 caption_priority（內建預設）的平台字幕軌。
//   - 腳本化了已實作的 fallback 來源（`the source … returns …`）。
func (s *Scenario) hasTranscriptSource(ref string) bool {
	if s.seeded[ref] {
		return true
	}
	for _, id := range s.World.Sources.ReturnedFor(ref) {
		if source.Implemented(id) {
			return true
		}
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
	chosen, _ := transcript.Select(tracks, s.declaredLanguage(ref, v), pipeline.DefaultLanguage().CaptionPriority, false)
	return chosen != nil
}

// itemFixture 是 features/fixtures/items/<代號>.json 的內容：影音處理到 transcript 時 source 字幕的 cue。
type itemFixture struct {
	Ref    string `json:"ref"`
	Source struct {
		Lang string `json:"lang"`
		Cues []struct {
			Start string `json:"start"`
			End   string `json:"end"`
			Text  string `json:"text"`
		} `json:"cues"`
	} `json:"source"`
}

// itemFixtureName 把代號轉成 fixture 檔名：`:`、`/` 換成 `-`，例如 yt:pg100xxxxxx → yt-pg100xxxxxx.json。
func itemFixtureName(ref string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(ref) + ".json"
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
//  2. 影片沒有任何字幕軌、場景也沒寫入字幕或腳本化來源，而且 fixtures/items/<代號>.json 存在：回傳該 fixture。
//  3. 其他情況：pending（場景沒有任何方式產生字幕）。
//
// fixture 的 ref 必須與檔名一致，否則失敗。是否 pending 只看場景準備了什麼，不看步驟的參數內容。
func (s *Scenario) itemFixtureFor(ref, stage string) (*itemFixture, error) {
	if slices.Index(pipeline.Stages, stage) < slices.Index(pipeline.Stages, pipeline.StageTranscript) || s.hasTranscriptSource(ref) {
		return nil, nil
	}
	if !s.hasAnyTrack(ref) && len(s.World.Sources.ReturnedFor(ref)) == 0 {
		name := itemFixtureName(ref)
		raw, err := os.ReadFile(filepath.Join(s.World.FixturesDir, "items", name))
		if err == nil {
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
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("讀取 fixture %s：%w", name, err)
		}
	}
	return nil, fmt.Errorf("場景沒有替 %s 準備字幕來源，也沒有 features/fixtures/items/ 的 fixture：%w", ref, godog.ErrPending)
}

// seedItemFixture 建立影音（做到 metadata），再把 fixture 的 source 字幕直接寫成產出。
func (s *Scenario) seedItemFixture(ref string, fx *itemFixture) error {
	cues := make([]subtitle.Cue, 0, len(fx.Source.Cues))
	for i, c := range fx.Source.Cues {
		cue := subtitle.Cue{Index: i + 1, Text: c.Text}
		var err error
		if cue.StartMS, err = subtitle.ParseTimecode(c.Start); err != nil {
			return fmt.Errorf("fixture %s 第 %d 個 cue 的 start：%w", ref, i+1, err)
		}
		if cue.EndMS, err = subtitle.ParseTimecode(c.End); err != nil {
			return fmt.Errorf("fixture %s 第 %d 個 cue 的 end：%w", ref, i+1, err)
		}
		cues = append(cues, cue)
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

// firstUnfinished 回傳從開頭到 upTo（含）第一個還不是 done 的步驟；都完成了回傳空字串。
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
