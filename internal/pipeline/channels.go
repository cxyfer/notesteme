package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// ChannelOverrides 是頻道層的覆寫（PRF-R8）。ChannelID 為空字串代表頻道還不存在（用 natural key 讀取時）。
type ChannelOverrides struct {
	ChannelID  string
	Platform   string
	ExternalID string
	Overrides  map[string]any
	UpdatedAt  time.Time
}

// overridablePlatforms 是可以用 natural key 建立頻道覆寫的平台（local 沒有頻道，D14）。
var overridablePlatforms = []string{source.PlatformYouTube, source.PlatformBilibili, source.PlatformPodcast}

// splitChannelKey 把 "<platform>:<external_id>" 拆開；UUID（沒有冒號）回傳 ok 為 false。
func splitChannelKey(key string) (platform, externalID string, ok bool) {
	platform, externalID, ok = strings.Cut(key, ":")
	return platform, externalID, ok
}

func channelOverridesOf(ch store.Channel) (ChannelOverrides, error) {
	ov, err := decodeOverrides(ch.Overrides)
	if err != nil {
		return ChannelOverrides{}, err
	}
	return ChannelOverrides{
		ChannelID: ch.ID, Platform: ch.Platform, ExternalID: ch.ExternalID, Overrides: orEmpty(ov), UpdatedAt: ch.UpdatedAt,
	}, nil
}

// ChannelOverrides 讀取頻道覆寫。key 是頻道的 UUID，或 "<platform>:<external_id>"（D14）。
// UUID 找不到回傳 store.ErrNotFound；natural key 找不到時不是錯誤（ChannelID 為空、Overrides 為空物件）。
func (p *Pipeline) ChannelOverrides(ctx context.Context, key string) (ChannelOverrides, error) {
	r := p.st.Read()
	platform, externalID, natural := splitChannelKey(key)
	if !natural {
		ch, err := r.ChannelByID(ctx, key)
		if err != nil {
			return ChannelOverrides{}, err
		}
		return channelOverridesOf(ch)
	}
	ch, err := r.ChannelByKey(ctx, platform, externalID)
	if errors.Is(err, store.ErrNotFound) {
		return ChannelOverrides{Platform: platform, ExternalID: externalID, Overrides: map[string]any{}}, nil
	}
	if err != nil {
		return ChannelOverrides{}, err
	}
	return channelOverridesOf(ch)
}

// PatchChannelOverrides 以 RFC 7396 merge patch 修改頻道覆寫（驗證種類是 KindOverride）。
// natural key 找不到頻道時建立它（title 留空）；platform 只能是 youtube、bilibili、podcast（D14）。
func (p *Pipeline) PatchChannelOverrides(ctx context.Context, key string, patch map[string]any) (ChannelOverrides, error) {
	platform, externalID, natural := splitChannelKey(key)
	if natural {
		if !slices.Contains(overridablePlatforms, platform) {
			return ChannelOverrides{}, invalidFields([]validate.FieldError{{Path: "id", Message: "platform 只能是 youtube、bilibili 或 podcast。"}})
		}
		if externalID == "" {
			return ChannelOverrides{}, invalidFields([]validate.FieldError{{Path: "id", Message: "缺少頻道的 external_id。"}})
		}
	}
	var out ChannelOverrides
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		var ch store.Channel
		var err error
		if natural {
			ch, err = r.ChannelByKey(ctx, platform, externalID)
			if errors.Is(err, store.ErrNotFound) {
				var id string
				if id, err = r.UpsertChannel(ctx, store.Channel{Platform: platform, ExternalID: externalID}); err == nil {
					ch, err = r.ChannelByID(ctx, id)
				}
			}
		} else {
			ch, err = r.ChannelByID(ctx, key)
		}
		if err != nil {
			return err
		}
		base, err := decodeOverrides(ch.Overrides)
		if err != nil {
			return err
		}
		merged := MergePatch(base, patch)
		if err := p.validateOverrides(ctx, merged); err != nil {
			return err
		}
		if _, err := BuildEffective(merged); err != nil {
			return err
		}
		raw, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		if err := r.SetChannelOverrides(ctx, ch.ID, string(raw)); err != nil {
			return err
		}
		ch, err = r.ChannelByID(ctx, ch.ID)
		if err != nil {
			return err
		}
		out, err = channelOverridesOf(ch)
		return err
	})
	if err != nil {
		return ChannelOverrides{}, err
	}
	return out, nil
}
