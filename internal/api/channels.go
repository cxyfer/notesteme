package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是頻道層的覆寫（PRF-R8，D14，M2 S1/S2 設計第 6.3 節）：讀取用 read，修改用 admin。
// {id} 是頻道的 UUID，或 "<platform>:<external_id>"。

// channelService 是頻道覆寫 handler 需要的 service。*pipeline.Pipeline 實作它；
// 定義成介面是為了讓測試用假的實作驗證 handler 的對應。
type channelService interface {
	ChannelOverrides(ctx context.Context, key string) (pipeline.ChannelOverrides, error)
	PatchChannelOverrides(ctx context.Context, key string, patch map[string]any) (pipeline.ChannelOverrides, error)
}

var _ channelService = (*pipeline.Pipeline)(nil)

// channelsAPI 把路由接到 service；svc 是 nil 代表沒有注入 Pipeline（設定錯誤，回 500）。
type channelsAPI struct {
	rt  *routes
	svc channelService
}

func (rt *routes) registerChannels(api huma.API) {
	h := &channelsAPI{rt: rt}
	if rt.d.Pipeline != nil {
		h.svc = rt.d.Pipeline
	}
	h.register(api)
}

func (h *channelsAPI) register(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "channels-overrides-get", Method: http.MethodGet, Path: "/api/channels/{id}/overrides",
		Summary: "讀取頻道層的覆寫；{id} 是頻道 UUID 或 <platform>:<external_id>（找不到的 natural key 回空的覆寫）",
	}, auth.ScopeRead, h.get)
	registerScoped(api, huma.Operation{
		OperationID: "channels-overrides-patch", Method: http.MethodPatch, Path: "/api/channels/{id}/overrides",
		Summary: "以 merge patch（RFC 7396）修改頻道覆寫；natural key 找不到頻道時建立它，local 平台回 422",
	}, auth.ScopeAdmin, h.patch)
}

// ChannelOverrides 是頻道的覆寫。ChannelID 是空字串代表頻道還不存在（用 natural key 讀取時）。
type ChannelOverrides struct {
	ChannelID  string         `json:"channel_id" doc:"頻道的 UUID；頻道還不存在時是空字串"`
	Platform   string         `json:"platform"`
	ExternalID string         `json:"external_id"`
	Overrides  map[string]any `json:"overrides" doc:"存下來的這一層設定；沒有覆寫時是 {}"`
}

type channelOverridesOutput struct{ Body ChannelOverrides }

func toChannelOverrides(c pipeline.ChannelOverrides) ChannelOverrides {
	ov := c.Overrides
	if ov == nil {
		ov = map[string]any{}
	}
	return ChannelOverrides{ChannelID: c.ChannelID, Platform: c.Platform, ExternalID: c.ExternalID, Overrides: ov}
}

type channelIDInput struct {
	ID string `path:"id" doc:"頻道的 UUID，或 <platform>:<external_id>，例如 youtube:UCfixture000000000000000"`
}

// service 回傳 service；沒有注入時回 500（設定錯誤，不是 501）。
func (h *channelsAPI) service(ctx context.Context) (channelService, error) {
	if h.svc == nil {
		return nil, h.rt.noPipeline(ctx)
	}
	return h.svc, nil
}

func (h *channelsAPI) get(ctx context.Context, in *channelIDInput) (*channelOverridesOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.ChannelOverrides(ctx, in.ID)
	if err != nil {
		return nil, h.mapChannelErr(ctx, err)
	}
	return &channelOverridesOutput{Body: toChannelOverrides(c)}, nil
}

type patchChannelOverridesInput struct {
	ID string `path:"id" doc:"頻道的 UUID，或 <platform>:<external_id>"`
	// Body 是 RFC 7396 merge patch，整個 body 就是 patch：null 刪除該鍵，物件遞迴合併，陣列與純量整個取代。
	Body map[string]any
}

func (h *channelsAPI) patch(ctx context.Context, in *patchChannelOverridesInput) (*channelOverridesOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	if in.Body == nil {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供 JSON 物件格式的覆寫內容。")
	}
	c, err := svc.PatchChannelOverrides(ctx, in.ID, in.Body)
	if err != nil {
		return nil, h.mapChannelErr(ctx, err)
	}
	return &channelOverridesOutput{Body: toChannelOverrides(c)}, nil
}

// mapChannelErr 同 mapErr，只是頻道（UUID）找不到時的訊息是頻道，不是影音。
func (h *channelsAPI) mapChannelErr(ctx context.Context, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return newError(http.StatusNotFound, CodeNotFound, "找不到這個頻道。")
	}
	return h.rt.mapErr(ctx, err)
}
