package app

import (
	"net/url"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/oidc"
)

// oidcConfig 依設定檔 auth.oidc 建立 OIDC 的 relying party（AUTH-R16）；沒有設定時回傳零值（沒有啟用）。
//
// 對 IdP 的請求經過 netguard 的 Transport，但 issuer 的主機名稱加進私有網段的允許清單：issuer 只能寫在設定檔，
// 自架的 IdP 常在內網（待決問題 A3）。雲端 metadata 位址仍然被擋下；discovery 中其他主機的 endpoint 照一般規則。
func oidcConfig(cfg *config.Config, netw netguard.Deps, clk clock.Clock) (auth.OIDCConfig, error) {
	o := cfg.M4.OIDC
	if o.Issuer == "" {
		return auth.OIDCConfig{}, nil
	}
	u, err := url.Parse(o.Issuer)
	if err != nil {
		return auth.OIDCConfig{}, err
	}
	d := netw
	d.AllowPrivate = netguard.Allowlist{
		Prefixes: slices.Clone(netw.AllowPrivate.Prefixes),
		Hosts:    append(slices.Clone(netw.AllowPrivate.Hosts), u.Hostname()),
	}
	p, err := oidc.New(oidc.Config{
		Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: o.ClientSecret, RedirectURL: o.RedirectURL,
		HTTP: netguard.NewClient(d), Now: func() time.Time { return clk.Now() },
	})
	if err != nil {
		return auth.OIDCConfig{}, err
	}
	return auth.OIDCConfig{Provider: p, AllowedSubjects: slices.Clone(o.AllowedSubjects)}, nil
}
