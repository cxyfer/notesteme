package api

import (
	"context"
	"net/http"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

type estimateInput struct {
	Body struct {
		URL       string         `json:"url,omitempty" doc:"影音的網址"`
		Path      string         `json:"path,omitempty" doc:"本機檔案的路徑；還不支援估算，回 422"`
		Parts     []int          `json:"parts,omitempty" doc:"Bilibili 要估算的分P；沒有指定時估算全部"`
		Episodes  []string       `json:"episodes,omitempty" doc:"Podcast 要估算的集（guid）"`
		Profile   string         `json:"profile,omitempty" doc:"處理方案的名稱；沒有指定時使用預設處理方案；不存在回 422"`
		Overrides map[string]any `json:"overrides,omitempty" doc:"單次覆寫，與新增相同"`
		Until     string         `json:"until,omitempty" doc:"與新增相同的欄位；估算時忽略"`
	}
}

// EstimateItem 是估算結果中的一部影音。金額欄位為 null 代表不知道，0 代表確定不用錢。
type EstimateItem struct {
	Ref              string   `json:"ref" doc:"影音的 ref，與新增之後的 ref 相同（Podcast 的 feed 代號在新增時才會決定）"`
	Title            string   `json:"title"`
	DurationS        *float64 `json:"duration_s" doc:"長度（秒）；平台沒有提供時為 null。Podcast 是 feed 的宣稱，以下載的音檔為準"`
	ExpectedSource   *string  `json:"expected_source" doc:"預計使用的字幕來源；沒有可用的來源時為 null"`
	ASRUSD           *float64 `json:"asr_usd" doc:"語音轉錄費用：用平台字幕時為 0；長度或單價未知時為 null"`
	SourceUSD        *float64 `json:"source_usd" doc:"其他付費字幕來源（Supadata，以 credit 計價）的費用；沒有內建單價，目前一律為 null"`
	LLMUSD           *float64 `json:"llm_usd" doc:"LLM 費用的估算：還沒有字幕，用片長推估 token 數（COST-R5 的方法）；片長或單價未知、或有沒有估算方法的付費步驟時為 null；沒有付費的 LLM 步驟時為 0"`
	TotalUSD         *float64 `json:"total_usd" doc:"已知金額的和；沒有任何已知金額時為 null"`
	RequiresCookie   bool     `json:"requires_cookie" doc:"需要 cookie 才能取得（ING-R9）"`
	AlreadyInLibrary bool     `json:"already_in_library" doc:"已經在媒體庫中；仍照樣估價"`
	Notes            []string `json:"notes"`
}

// EstimateResult 是估算結果。
type EstimateResult struct {
	Items      []EstimateItem `json:"items"`
	TotalUSD   float64        `json:"total_usd" doc:"各部影音已知金額的和"`
	Currency   string         `json:"currency" example:"USD"`
	PricesAsOf string         `json:"prices_as_of" doc:"內建計價表最後確認的日期"`
	Notes      []string       `json:"notes"`
}

type estimateOutput struct{ Body EstimateResult }

// estimate 估算新增的花費，不建立影音（ING-R8）；錯誤對應同 POST /api/items。
func (rt *routes) estimate(ctx context.Context, in *estimateInput) (*estimateOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	switch {
	case in.Body.Path != "":
		if in.Body.URL != "" {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "url 與 path 只能擇一提供。")
		}
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "本機檔案還不支援估算花費。")
	case in.Body.URL == "":
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供要估算的網址（url）。")
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	est, err := rt.d.Pipeline.Estimate(ctx, pipeline.IngestRequest{
		URL: in.Body.URL, Parts: in.Body.Parts, Episodes: in.Body.Episodes, Profile: in.Body.Profile,
		Overrides: in.Body.Overrides, Until: in.Body.Until,
	})
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &estimateOutput{Body: EstimateResult{
		Items: make([]EstimateItem, 0, len(est.Items)), TotalUSD: est.TotalUSD, Currency: est.Currency,
		PricesAsOf: est.PricesAsOf, Notes: est.Notes,
	}}
	for _, it := range est.Items {
		out.Body.Items = append(out.Body.Items, EstimateItem{
			Ref: it.Ref, Title: it.Title, DurationS: it.DurationS, ExpectedSource: it.ExpectedSource,
			ASRUSD: it.ASRUSD, SourceUSD: it.SourceUSD, LLMUSD: it.LLMUSD, TotalUSD: it.TotalUSD,
			RequiresCookie: it.RequiresCookie, AlreadyInLibrary: it.AlreadyInLibrary, Notes: it.Notes,
		})
	}
	return out, nil
}
