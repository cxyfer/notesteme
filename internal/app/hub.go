package app

import (
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/hubsettings"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// configureHub 組出 Client 的 Hub 設定與連線（HSHR、ADR-0032 第 4 點）：token 存在 secrets，
// 請求經 pipeline 的 netguard client（不跟隨轉址，逾時用注入的 clock）。
func (s *Server) configureHub(env Env, cfg *config.Config, deps *pipeline.Deps) {
	s.hub = &hubsettings.Settings{URL: cfg.HubURL, ConfigPrecheck: cfg.HubPrecheckEnabled, Store: s.store, Secrets: s.services}
	deps.Hub = hubclient.New(cfg.HubURL, s.hub.Token, deps.HTTP, env.Clock)
	deps.HubPrecheck = s.hub.PrecheckEnabled
	if s.services != nil {
		deps.SecretValues = s.services.Values
	}
}
