package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
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
// TODO：fixtures/items/ 還沒有，影音不是用代號新增時無法從 fixture 建立。
func (s *Scenario) itemProcessedUpTo(ref, stage string) error {
	if !pipeline.ValidStage(stage) {
		return fmt.Errorf("步驟 %q 不正確，應是 %v 其中之一", stage, pipeline.Stages)
	}
	if err := s.pendingWithoutItemFixture(ref, stage); err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
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
	id, err := youtubeID(ref)
	if err != nil {
		return false
	}
	v, ok := s.World.Tools.Videos[id]
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

// pendingWithoutItemFixture 在執行之前處理這個 Given 缺的一塊：features/fixtures/items/ 還沒有
// （steps.md：「影音或 fixture 不存在時，依代號從 fixtures/items/ 建立」）。
// 目標步驟需要字幕（transcript 以後），而場景沒有替影音準備任何字幕來源時，產出只能靠 fixture 填入，
// 目前建立不起來：視為步驟尚未完整實作而 pending。有準備來源卻沒處理到目標步驟，由呼叫端一律當作失敗。
// 實作 fixtures/items/ 之後整個函式刪除。
func (s *Scenario) pendingWithoutItemFixture(ref, stage string) error {
	if slices.Index(pipeline.Stages, stage) < slices.Index(pipeline.Stages, pipeline.StageTranscript) || s.hasTranscriptSource(ref) {
		return nil
	}
	return fmt.Errorf("場景沒有替 %s 準備字幕來源，需要 features/fixtures/items/ 填入產出（還沒實作）：%w", ref, godog.ErrPending)
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
