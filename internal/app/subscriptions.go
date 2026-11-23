package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/subscription"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// startSubscriptions 建立訂閱的服務，並在排程器登記 subscription.check（M4-S2，必須在 worker 開始之前）。
// 探索的對外 HTTP 用 netguard 的 client（SUB-R11）；yt-dlp 與 pipeline 共用 runner 與工具位置。
func (s *Server) startSubscriptions(clk clock.Clock, cfg *config.Config, log *slog.Logger, httpc *http.Client,
	runner toolexec.Runner, tools toolexec.Resolver, yt *youtube.Client, bili *bilibili.Client) error {
	s.subscriptions = subscription.New(subscription.Deps{
		Store: s.store, Clock: clk, Log: log, HTTP: httpc, Ingest: s.pipeline, Runner: runner, Tools: tools,
		Probe: videoProber(yt, bili), Config: cfg.M4.Subscriptions, JitterMax: cfg.M4.JitterMax,
	})
	return s.subscriptions.Register(s.scheduler)
}

// videoProber 以 yt-dlp -J 補齊單一影片的片長與直播狀態（SUB-R2）。
func videoProber(yt *youtube.Client, bili *bilibili.Client) subscription.Prober {
	return func(ctx context.Context, platform, id string) (subscription.Details, error) {
		if platform == subscription.PlatformBilibili && bili != nil {
			info, err := bili.Probe(ctx, id, 1)
			if err != nil {
				return subscription.Details{}, err
			}
			return subscription.Details{DurationS: info.DurationS}, nil
		}
		info, err := yt.Probe(ctx, id)
		if err != nil {
			return subscription.Details{}, err
		}
		return subscription.Details{DurationS: info.DurationS, LiveStatus: info.LiveStatus}, nil
	}
}
