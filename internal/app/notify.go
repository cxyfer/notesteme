package app

import (
	"log/slog"
	"net/netip"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/notify"
)

// newNotify 組出通知（NOTIFY）：管理 notifier 與送出 outbox 的 Service，以及給 pipeline 在交易中寫入事件的 Outbox。
// 所有請求經過 netguard（同一份 allowlist），不跟隨轉址（NOTIFY-R7）。
func (s *Server) newNotify(clk clock.Clock, cfg *config.Config, log *slog.Logger) (*notify.Service, *notify.Outbox) {
	nt := cfg.M4.Notify
	allow := s.netw.AllowPrivate
	svc := notify.New(notify.Deps{
		Store: s.store, Keys: s.keys, Clock: clk, Log: log, HTTP: notify.NewClient(s.netw), Version: version,
		PublicURL: cfg.M4.PublicURL, RateCount: nt.RateCount, RatePer: nt.RatePer,
		Hosts: notify.HostPolicy{AllowHosts: nt.AllowHosts, AllowHTTP: func(host string) bool { return allowsPrivate(allow, host) }},
	})
	return svc, &notify.Outbox{Clock: clk, DedupeWindow: nt.DedupeWindow}
}

// allowsPrivate 回報 http:// 的主機是否列在 network.allow_private（主機名稱，或 IP 字面值落在允許的網段，GRD-R3）。
func allowsPrivate(a netguard.Allowlist, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return slices.ContainsFunc(a.Prefixes, func(p netip.Prefix) bool { return p.Contains(ip.Unmap()) })
	}
	return slices.ContainsFunc(a.Hosts, func(h string) bool { return strings.TrimSuffix(strings.ToLower(h), ".") == host })
}
