package steps

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakesponsorblock"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// registerChapters 註冊「SponsorBlock 與章節」的步驟（steps.md 4.13，M3）。
//
// SponsorBlock 的四個句型由 M3-S1 實作；`the chapters of … are:` 由 M3-S4 實作（NOTE 的閘門，也用於 FRM-R9）。
// SponsorBlock 的句型也是 CHAP、FRM、NOTE 場景的閘門：只對 SPB 的場景放行（見 spbGate），
// 其他領域的場景在它們的切片實作之前維持 pending，停在同一個閘門。
func registerChapters(r *Registry, s *Scenario) {
	r.Step(`SponsorBlock has the segments for {string}:`, s.sponsorBlockHasSegments)
	r.Step(`SponsorBlock fails for {string} with HTTP {int}`, s.sponsorBlockFails)
	r.Step(`SponsorBlock received {int} requests for {string}`, s.sponsorBlockReceived)
	r.Step(`SponsorBlock never received the video ID of {string}`, s.sponsorBlockNeverSawID)
	r.Step(`the chapters of {string} are:`, s.chaptersAre)
}

// spbGate 讓 SponsorBlock 的句型只在 SPB 的場景（有 @SPB- 開頭的 tag）執行；其他領域的場景是 pending，
// 等它們的切片實作（steps.md 4.13 開頭）。@SPB-R7 另外需要 note 步驟（NOTE 的切片），在那之前也是 pending。
func (s *Scenario) spbGate() error {
	// M3-S2：CHAP 的場景在 chapters 步驟實作之後也放行（CHAP 的 Background 用 SponsorBlock 的片段）。
	if pipeline.StageAvailable(pipeline.StageChapters) && slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@CHAP-") }) {
		return nil
	}
	// M3-S3：FRM 的場景也放行（FRM-R6 用 SponsorBlock 登記片段）。
	if slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@FRM-") }) {
		return nil
	}
	// M3-S4：NOTE 的場景也放行（NOTE-R2 用 SponsorBlock 的失敗）；SPB-R7 在 note 步驟實作之後通過下面的檢查。
	if pipeline.StageAvailable(pipeline.StageNote) && slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@NOTE-") }) {
		return nil
	}
	// SRC-10：SponsorBlock 的重新查詢（SRC-R20）也放行。
	if slices.Contains(s.tags, "@SRC-R20") {
		return nil
	}
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@SPB-") }) {
		return fmt.Errorf("SponsorBlock 的句型只對 SPB 的場景實作（其他 M3 領域還沒有實作）：%w", godog.ErrPending)
	}
	if slices.Contains(s.tags, "@SPB-R7") && !pipeline.StageAvailable(pipeline.StageNote) {
		return fmt.Errorf("SPB-R7 需要 note 步驟（還沒有實作）：%w", godog.ErrPending)
	}
	return nil
}

// sponsorBlockVideoID 把 yt: 代號轉成影片 ID；只接受 YouTube（steps.md 4.13）。
func sponsorBlockVideoID(ref string) (string, error) {
	id, ok := strings.CutPrefix(ref, "yt:")
	if !ok || !sponsorblock.ValidVideoID(id) {
		return "", fmt.Errorf("SponsorBlock 只接受 YouTube 的代號（yt: 加 11 字元的影片 ID）：%q", ref)
	}
	return id, nil
}

// sponsorBlockHasSegments 在假 SponsorBlock 登記影片的片段；表格欄位 category、start、end，可加 action（預設 skip）。
func (s *Scenario) sponsorBlockHasSegments(ref string, table *godog.Table) error {
	if err := s.spbGate(); err != nil {
		return err
	}
	id, err := sponsorBlockVideoID(ref)
	if err != nil {
		return err
	}
	segs, err := parseSponsorSegments(table)
	if err != nil {
		return err
	}
	s.World.SponsorBlock.SetSegments(id, segs)
	return nil
}

// parseSponsorSegments 解析片段表格（第一列是表頭）。
func parseSponsorSegments(table *godog.Table) ([]fakesponsorblock.Segment, error) {
	if table == nil || len(table.Rows) < 2 {
		return nil, errors.New("片段表格至少要有表頭與一列")
	}
	var head []string
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	for _, need := range []string{"category", "start", "end"} {
		if !slices.Contains(head, need) {
			return nil, fmt.Errorf("片段表格缺少欄位 %q（表頭 %v）", need, head)
		}
	}
	var out []fakesponsorblock.Segment
	for i, r := range table.Rows[1:] {
		if len(r.Cells) != len(head) {
			return nil, fmt.Errorf("片段表格第 %d 列的欄數與表頭不同", i+1)
		}
		cells := map[string]string{}
		for j, c := range r.Cells {
			cells[head[j]] = c.Value
		}
		start, err := subtitle.ParseTimecode(cells["start"])
		if err != nil {
			return nil, fmt.Errorf("片段表格第 %d 列的 start：%w", i+1, err)
		}
		end, err := subtitle.ParseTimecode(cells["end"])
		if err != nil {
			return nil, fmt.Errorf("片段表格第 %d 列的 end：%w", i+1, err)
		}
		out = append(out, fakesponsorblock.Segment{
			Category: cells["category"], Action: cells["action"], Start: float64(start) / 1000, End: float64(end) / 1000,
		})
	}
	return out, nil
}

// sponsorBlockFails 讓查詢這部影片（含相同 hash 前綴的查詢）時回應指定的狀態碼。
func (s *Scenario) sponsorBlockFails(ref string, status int) error {
	if err := s.spbGate(); err != nil {
		return err
	}
	id, err := sponsorBlockVideoID(ref)
	if err != nil {
		return err
	}
	if status < 400 || status > 599 {
		return fmt.Errorf("SponsorBlock 失敗的狀態碼應是 4xx 或 5xx：%d", status)
	}
	s.World.SponsorBlock.Fail(id, status)
	return nil
}

// sponsorBlockReceived 比對假 SponsorBlock 收到、hash 前綴與這部影片相同的請求數（含失敗的請求）。
func (s *Scenario) sponsorBlockReceived(want int, ref string) error {
	if err := s.spbGate(); err != nil {
		return err
	}
	id, err := sponsorBlockVideoID(ref)
	if err != nil {
		return err
	}
	if got := s.World.SponsorBlock.RequestsFor(id); got != want {
		return fmt.Errorf("SponsorBlock 收到 %d 個 %s 的請求，預期 %d 個", got, ref, want)
	}
	return nil
}

// sponsorBlockNeverSawID 確認假 SponsorBlock 收到的請求都不含這部影片的 ID；完全沒有請求時失敗（沒有東西可以驗證）。
func (s *Scenario) sponsorBlockNeverSawID(ref string) error {
	if err := s.spbGate(); err != nil {
		return err
	}
	id, err := sponsorBlockVideoID(ref)
	if err != nil {
		return err
	}
	if len(s.World.SponsorBlock.Requests()) == 0 {
		return errors.New("SponsorBlock 沒有收到任何請求，無法驗證沒有送出影片 ID")
	}
	if s.World.SponsorBlock.SawVideoID(id) {
		return fmt.Errorf("SponsorBlock 收到的請求含有 %s 的影片 ID", ref)
	}
	return nil
}

// chaptersAre 直接寫入現行的 chapters 產出（steps.md 4.13）：表格欄位 start（時間碼）、title，可加 cue；
// 有 cue 欄時 origin 是 llm，否則是 platform。影音不存在時先新增並做到 metadata。
func (s *Scenario) chaptersAre(ref string, table *godog.Table) error {
	if table == nil || len(table.Rows) < 2 {
		return errors.New("章節表格至少要有表頭與一列")
	}
	var head []string
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	for _, h := range head {
		if h != "start" && h != "title" && h != "cue" {
			return fmt.Errorf("章節表格只支援 start、title、cue 欄位，收到 %q", h)
		}
	}
	if !slices.Contains(head, "start") || !slices.Contains(head, "title") {
		return fmt.Errorf("章節表格缺少 start 或 title（表頭 %v）", head)
	}
	var list []pipeline.SeedChapter
	for i, r := range table.Rows[1:] {
		if len(r.Cells) != len(head) {
			return fmt.Errorf("章節表格第 %d 列的欄數與表頭不同", i+1)
		}
		var ch pipeline.SeedChapter
		for j, c := range r.Cells {
			switch head[j] {
			case "start":
				ms, err := subtitle.ParseTimecode(c.Value)
				if err != nil {
					return fmt.Errorf("章節表格第 %d 列的 start：%w", i+1, err)
				}
				ch.StartMS = ms
			case "title":
				ch.Title = c.Value
			case "cue":
				n, err := strconv.Atoi(c.Value)
				if err != nil || n < 1 {
					return fmt.Errorf("章節表格第 %d 列的 cue 應是從 1 起算的整數：%q", i+1, c.Value)
				}
				ch.Cue = n
			}
		}
		list = append(list, ch)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	return s.server.SeedChapters(s.ctx, id, list)
}
