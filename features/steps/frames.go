package steps

import (
	"bytes"
	"errors"
	"fmt"
	"image/jpeg"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/features/internal/fakeframes"
	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// registerFrames 註冊「關鍵畫面」的步驟（steps.md 4.14，M3）。
//
// M3-S3 實作了假影片的畫面、最高畫質與 MCP 的圖片結果；`the frames of … are:`（直接寫入 frames 產出，
// NOTE、VAULT 的場景使用）由 M3-S4 實作，不經過 frmGate（VAULT 的場景在它自己的閘門之前就停下）。
func registerFrames(r *Registry, s *Scenario) {
	r.Step(`the video of {string} has the scenes:`, s.videoHasScenes)
	r.Step(`the best video format of {string} is {string}`, s.bestVideoFormat)
	r.Step(`the frames of {string} are:`, s.framesAre)
	r.Step(`the tool result has an image of type {string}`, s.toolResultHasImage)
}

// frmGate 讓畫面的句型只在 FRM 的場景（有 @FRM- 開頭的 tag）執行；其他領域的場景維持 pending（steps.md 4.13 開頭）。
// 目前只有 FRM 使用這些句型，閘門是為了之後別的領域借用時不會意外往下跑。
func (s *Scenario) frmGate() error {
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@FRM-") }) {
		return fmt.Errorf("畫面的句型只對 FRM 的場景實作：%w", godog.ErrPending)
	}
	return nil
}

// videoHasScenes 登記假影片的畫面（表格欄位 at、score、color）；也讓假 yt-dlp 為這部影片提供影像串流。
func (s *Scenario) videoHasScenes(ref string, table *godog.Table) error {
	if err := s.frmGate(); err != nil {
		return err
	}
	if table == nil || len(table.Rows) < 2 {
		return errors.New("畫面表格至少要有表頭與一列")
	}
	var head []string
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	if !slices.Equal(head, []string{"at", "score", "color"}) {
		return fmt.Errorf("畫面表格的欄位應是 at、score、color，收到 %v", head)
	}
	var scenes []fakeframes.Scene
	for i, r := range table.Rows[1:] {
		if len(r.Cells) != 3 {
			return fmt.Errorf("畫面表格第 %d 列的欄數不對", i+1)
		}
		at, err := subtitle.ParseTimecode(r.Cells[0].Value)
		if err != nil {
			return fmt.Errorf("畫面表格第 %d 列的 at：%w", i+1, err)
		}
		score, err := strconv.ParseFloat(r.Cells[1].Value, 64)
		if err != nil || score < 0 || score > 1 {
			return fmt.Errorf("畫面表格第 %d 列的 score 應是 0～1：%q", i+1, r.Cells[1].Value)
		}
		c, err := fakeframes.ParseColor(r.Cells[2].Value)
		if err != nil {
			return fmt.Errorf("畫面表格第 %d 列：%w", i+1, err)
		}
		scenes = append(scenes, fakeframes.Scene{At: float64(at) / 1000, Score: score, Color: c})
	}
	if !strings.HasPrefix(ref, "yt:") {
		return fmt.Errorf("畫面目前只支援 YouTube 的假影片：%q", ref)
	}
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Scenes = fakeframes.NewVideo(scenes).Scenes
		return nil
	})
}

// bestVideoFormat 設定假 yt-dlp 格式清單中最高畫質的影像格式（`寬x高`）。
func (s *Scenario) bestVideoFormat(ref, size string) error {
	if err := s.frmGate(); err != nil {
		return err
	}
	if _, _, ok := faketool.ParseSize(size); !ok {
		return fmt.Errorf("影像格式應是 寬x高，收到 %q", size)
	}
	if !strings.HasPrefix(ref, "yt:") {
		return fmt.Errorf("最高畫質目前只支援 YouTube 的假影片：%q", ref)
	}
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.BestFormat = size
		return nil
	})
}

// toolResultHasImage 確認最近一次 tool 呼叫的結果含有 MCP 的 image content：mimeType 相符，資料可以解碼成該格式的圖片。
func (s *Scenario) toolResultHasImage(mime string) error {
	if !s.mcp.called {
		return errors.New("還沒有呼叫任何 MCP tool")
	}
	if s.mcp.err != nil {
		return fmt.Errorf("tool 呼叫失敗：%v", s.mcp.err)
	}
	res := s.mcp.result
	if toolNotImplemented(res) {
		return godog.ErrPending
	}
	if res.IsError {
		return fmt.Errorf("tool 回傳錯誤：%s", toolText(res))
	}
	for _, c := range res.Content {
		img, ok := c.(*mcp.ImageContent)
		if !ok || img.MIMEType != mime {
			continue
		}
		if mime != "image/jpeg" {
			return fmt.Errorf("目前只能解碼 image/jpeg，收到 %q", mime)
		}
		if _, err := jpeg.Decode(bytes.NewReader(img.Data)); err != nil {
			return fmt.Errorf("image content 不是可以解碼的 JPEG：%w", err)
		}
		return nil
	}
	return fmt.Errorf("tool 結果沒有 %s 的 image content", mime)
}

// framesAre 直接寫入現行的 frames 產出與 1280x720 的純色圖片（steps.md 4.14）：表格欄位 #、at、selected（yes／no），
// 可加 caption、chapter、color。# 必須從 1 起依序編號。影音不存在時先新增並做到 metadata。
func (s *Scenario) framesAre(ref string, table *godog.Table) error {
	if table == nil || len(table.Rows) < 2 {
		return errors.New("畫面表格至少要有表頭與一列")
	}
	var head []string
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	for _, need := range []string{"#", "at", "selected"} {
		if !slices.Contains(head, need) {
			return fmt.Errorf("畫面表格缺少欄位 %q（表頭 %v）", need, head)
		}
	}
	var list []pipeline.SeedFrame
	for i, r := range table.Rows[1:] {
		if len(r.Cells) != len(head) {
			return fmt.Errorf("畫面表格第 %d 列的欄數與表頭不同", i+1)
		}
		var f pipeline.SeedFrame
		for j, c := range r.Cells {
			v := c.Value
			switch head[j] {
			case "#":
				if n, err := strconv.Atoi(v); err != nil || n != i+1 {
					return fmt.Errorf("畫面表格第 %d 列的 # 應是 %d，收到 %q", i+1, i+1, v)
				}
			case "at":
				ms, err := subtitle.ParseTimecode(v)
				if err != nil {
					return fmt.Errorf("畫面表格第 %d 列的 at：%w", i+1, err)
				}
				f.AtMS = ms
			case "selected":
				switch v {
				case "yes":
					f.Selected = true
				case "no":
				default:
					return fmt.Errorf("畫面表格第 %d 列的 selected 應是 yes 或 no，收到 %q", i+1, v)
				}
			case "caption":
				f.Caption = v
			case "chapter":
				f.Chapter = v
			case "color":
				f.Color = v
			default:
				return fmt.Errorf("畫面表格不支援欄位 %q", head[j])
			}
		}
		list = append(list, f)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.ensureItemForSeed(ref)
	if err != nil {
		return err
	}
	return s.server.SeedFrames(s.ctx, id, list)
}
