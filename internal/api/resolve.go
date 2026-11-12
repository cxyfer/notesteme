package api

import (
	"context"
	"net/http"
)

type resolveInput struct {
	Body struct {
		URL string `json:"url" minLength:"1" doc:"要辨識的網址"`
	}
}

// Episode 是 Podcast feed 列出的一集。
type Episode struct {
	GUID  string `json:"guid"`
	Title string `json:"title"`
}

// Resolution 是網址辨識的結果。
type Resolution struct {
	Platform       string    `json:"platform" doc:"youtube、bilibili 或 podcast"`
	Kind           string    `json:"kind" doc:"video、playlist、channel 或 feed"`
	ExternalID     string    `json:"external_id,omitempty"`
	URL            string    `json:"url" doc:"正規化的網址"`
	Part           int       `json:"part,omitempty" doc:"Bilibili 網址指定的分P"`
	Title          string    `json:"title,omitempty"`
	Subscribable   bool      `json:"subscribable" doc:"頻道與個人空間不能直接新增，要建立訂閱（ING-R5）"`
	Availability   string    `json:"availability,omitempty" doc:"YouTube 影片的公開狀態；取不到時省略"`
	RequiresCookie bool      `json:"requires_cookie" doc:"內容需要 cookie 才能取得（ING-R9）"`
	SettingsLink   string    `json:"settings_link,omitempty" doc:"設定 cookie 的頁面"`
	Episodes       []Episode `json:"episodes,omitempty" doc:"Podcast feed 列出的各集"`
}

type resolveOutput struct{ Body Resolution }

func (rt *routes) resolve(ctx context.Context, in *resolveInput) (*resolveOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	r, err := rt.d.Pipeline.Resolve(ctx, in.Body.URL)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &resolveOutput{Body: Resolution{
		Platform: r.Platform, Kind: string(r.Kind), ExternalID: r.ExternalID, URL: r.URL, Part: r.Part, Title: r.Title,
		Subscribable: r.Subscribable, Availability: r.Availability, RequiresCookie: r.RequiresCookie, SettingsLink: r.SettingsLink,
	}}
	for _, e := range r.Episodes {
		out.Body.Episodes = append(out.Body.Episodes, Episode{GUID: e.GUID, Title: e.Title})
	}
	return out, nil
}
