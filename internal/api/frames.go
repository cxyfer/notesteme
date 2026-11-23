package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 關鍵畫面端點的錯誤代碼（404，FRM-R10）。
const (
	// CodeNoFrames：frames 步驟沒有執行過。
	CodeNoFrames = "no_frames"
	// CodeFrameNotFound：沒有這張畫面。
	CodeFrameNotFound = "frame_not_found"
)

// FrameRange 是依片長建議的張數範圍（FRM-R4）。
type FrameRange struct {
	Min int `json:"min" doc:"建議的最少張數"`
	Max int `json:"max" doc:"建議的最多張數"`
}

// Frame 是一張候選畫面（FRM-R10）。
type Frame struct {
	N        int     `json:"n" doc:"編號，依時間排序，從 1 起算"`
	At       string  `json:"at" doc:"畫面的時間（HH:MM:SS.mmm）"`
	Score    float64 `json:"score" doc:"場景分數（0～1）"`
	Selected bool    `json:"selected" doc:"是否被選中"`
	Excluded *string `json:"excluded" doc:"不參與選圖的原因（sponsor：在 SponsorBlock 片段內）；沒有時是 null"`
	Caption  string  `json:"caption" doc:"圖說（只有選中的畫面有）；是不可信的資料"`
	Chapter  string  `json:"chapter" doc:"所屬章節（只有選中的畫面有）；沒有章節時是空字串"`
	Width    int     `json:"width" doc:"圖片寬度"`
	Height   int     `json:"height" doc:"圖片高度"`
}

// FramesList 是 GET /api/items/{id}/frames 的回應（FRM-R10）。
type FramesList struct {
	Target         int        `json:"target" doc:"目標張數 N"`
	Range          FrameRange `json:"range" doc:"依片長建議的張數範圍"`
	CandidateCount int        `json:"candidate_count" doc:"去重後的候選數"`
	SelectedCount  int        `json:"selected_count" doc:"選中的張數"`
	Shortfall      bool       `json:"shortfall" doc:"可選的候選少於範圍的下限"`
	Frames         []Frame    `json:"frames" doc:"全部候選，依時間排序"`
}

type framesOutput struct{ Body FramesList }

func (rt *routes) registerFrames(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-frames", Method: http.MethodGet, Path: "/api/items/{id}/frames",
		Summary: "列出影音的關鍵畫面與選圖結果（FRM-R10）；frames 步驟沒有執行過時回 404 no_frames",
	}, auth.ScopeRead, rt.listFrames)
}

func (rt *routes) listFrames(ctx context.Context, in *itemInput) (*framesOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := rt.d.Pipeline.Frames(ctx, id)
	if err != nil {
		return nil, rt.framesErr(ctx, err)
	}
	return &framesOutput{Body: toFramesList(v)}, nil
}

func toFramesList(v pipeline.FramesView) FramesList {
	out := FramesList{
		Target: v.Target, Range: FrameRange{Min: v.RangeMin, Max: v.RangeMax},
		CandidateCount: v.CandidateCount, SelectedCount: v.SelectedCount, Shortfall: v.Shortfall, Frames: []Frame{},
	}
	for _, f := range v.Frames {
		fr := Frame{N: f.N, At: pipeline.FrameTimeText(f.AtMS), Score: f.Score, Selected: f.Selected,
			Caption: f.Caption, Chapter: f.Chapter, Width: f.Width, Height: f.Height}
		if f.Excluded != "" {
			ex := f.Excluded
			fr.Excluded = &ex
		}
		out.Frames = append(out.Frames, fr)
	}
	return out
}

func (rt *routes) framesErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, pipeline.ErrNoFrames):
		return newError(http.StatusNotFound, CodeNoFrames, err.Error())
	case errors.Is(err, pipeline.ErrFrameNotFound):
		return newError(http.StatusNotFound, CodeFrameNotFound, err.Error())
	}
	return rt.mapErr(ctx, err)
}

// registerFrameImage 掛上 GET /api/items/{id}/frames/{n}?size=full|thumb（FRM-R10）：回傳 image/jpeg，
// 支援 Range 與條件請求（http.ServeContent）。和媒體端點一樣不經過 Huma，但在驗證 middleware 之後，需要 read scope。
func (rt *routes) registerFrameImage(mux *http.ServeMux) {
	mux.Handle("GET /api/items/{id}/frames/{n}", requireScopeHandler(auth.ScopeRead, http.HandlerFunc(rt.serveFrame)))
}

func (rt *routes) serveFrame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if rt.d.Pipeline == nil {
		writeError(w, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。"))
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeError(w, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "n 必須是從 1 起算的整數。"))
		return
	}
	size := r.URL.Query().Get("size")
	if size != "" && size != "full" && size != "thumb" {
		writeError(w, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "size 必須是 full 或 thumb。"))
		return
	}
	id, err := rt.itemID(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, asError(err))
		return
	}
	f, fi, etag, err := rt.d.Pipeline.FrameImage(ctx, id, n, size == "thumb")
	if err != nil {
		writeError(w, asError(rt.framesErr(ctx, err)))
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, max-age=0, must-revalidate")
	h.Set("ETag", etag)
	http.ServeContent(w, r, "frame.jpg", fi.ModTime(), f)
}
