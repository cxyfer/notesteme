// Package hubsettings 是 Client 的 Hub 設定（HSHR-R1、R12）：hub.url 只來自設定檔；token 存成 secret；
// ASR 前查詢的開關可以寫在設定檔，或經 API 開啟（要確認目前版本的說明，存在 settings 表）。
package hubsettings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

// DisclosureVersion 是 ASR 前查詢說明的版本；說明的內容改變時要換版本，使用者要重新確認。
const DisclosureVersion = "hub-precheck-v1"

// settingPrecheck 是 settings 表的鍵：{"enabled": bool, "acknowledged": "<版本>"}。
const settingPrecheck = "hub.precheck"

// Disclosure 回傳開啟 ASR 前查詢之前要確認的說明（HSHR-R12）。
func Disclosure(hubURL string) string {
	if hubURL == "" {
		hubURL = "（尚未設定的 Hub）"
	}
	return "開啟後，Notesteme 在付費轉錄（Supadata、ASR）之前，會把平台名稱與影片 ID 的 SHA-256 前 4 個十六進位字元送到 " +
		hubURL + "，並用你的 Hub token 驗證身分。Hub 會知道你的帳號在什麼時候查詢了哪個前綴，但無法從前綴得知是哪一部影片。" +
		"本機檔案與非公開影音不會查詢。"
}

// ErrAcknowledgementRequired 表示開啟 ASR 前查詢時沒有確認目前版本的說明。
var ErrAcknowledgementRequired = errors.New("開啟 Hub 查詢之前，請先確認會送出哪些資料。")

// HubPrecheck 是 GET 回應的 precheck。
type HubPrecheck struct {
	Enabled           bool   `json:"enabled"`
	Disclosure        string `json:"disclosure"`
	DisclosureVersion string `json:"disclosure_version"`
}

// HubSettings 是 GET／PUT /api/settings/hub 的回應：不含 token。
type HubSettings struct {
	URL      string      `json:"url"`
	TokenSet bool        `json:"token_set"`
	Precheck HubPrecheck `json:"precheck"`
}

// PrecheckUpdate 是 precheck 的部分更新。
type PrecheckUpdate struct {
	Enabled     *bool  `json:"enabled,omitempty"`
	Acknowledge string `json:"acknowledge,omitempty"`
}

// Update 是 PUT 的部分更新：省略的欄位不變，Token 為 "" 時刪除 token。
type Update struct {
	Token    *string         `json:"token,omitempty"`
	Precheck *PrecheckUpdate `json:"precheck,omitempty"`
}

// Settings 讀寫 Hub 設定。
type Settings struct {
	// URL 是設定檔的 hub.url（空字串代表沒有設定 Hub）。
	URL string
	// ConfigPrecheck 是設定檔的 hub.precheck.enabled。
	ConfigPrecheck bool
	Store          *store.Store
	Secrets        *secrets.Services
}

type storedPrecheck struct {
	Enabled      bool   `json:"enabled"`
	Acknowledged string `json:"acknowledged"`
}

// Token 回傳 Hub token。
func (s *Settings) Token(ctx context.Context) (string, bool, error) {
	if s == nil || s.Secrets == nil {
		return "", false, nil
	}
	return s.Secrets.HubToken(ctx)
}

// PrecheckEnabled 回報 ASR 前查詢是否開啟：API 的設定優先（確認的必須是目前版本的說明），否則看設定檔。
func (s *Settings) PrecheckEnabled(ctx context.Context) (bool, error) {
	if s == nil {
		return false, nil
	}
	raw, ok, err := s.Store.GetSetting(ctx, settingPrecheck)
	if err != nil {
		return false, err
	}
	if !ok {
		return s.ConfigPrecheck, nil
	}
	var sp storedPrecheck
	if json.Unmarshal([]byte(raw), &sp) != nil {
		return false, nil
	}
	return sp.Enabled && sp.Acknowledged == DisclosureVersion, nil
}

// Get 回傳目前的設定。
func (s *Settings) Get(ctx context.Context) (HubSettings, error) {
	_, tokenSet, err := s.Token(ctx)
	if err != nil {
		return HubSettings{}, err
	}
	on, err := s.PrecheckEnabled(ctx)
	if err != nil {
		return HubSettings{}, err
	}
	return HubSettings{URL: s.URL, TokenSet: tokenSet, Precheck: HubPrecheck{Enabled: on, Disclosure: Disclosure(s.URL), DisclosureVersion: DisclosureVersion}}, nil
}

// Put 套用部分更新；全部檢查通過才寫入（開啟查詢沒有確認說明時，token 也不寫入）。設定 token 不會連線到 Hub。
func (s *Settings) Put(ctx context.Context, u Update) (HubSettings, error) {
	var stored *storedPrecheck
	if p := u.Precheck; p != nil && p.Enabled != nil {
		if *p.Enabled && p.Acknowledge != DisclosureVersion {
			return HubSettings{}, ErrAcknowledgementRequired
		}
		stored = &storedPrecheck{Enabled: *p.Enabled}
		if *p.Enabled {
			stored.Acknowledged = DisclosureVersion
		}
	}
	if u.Token != nil {
		if err := s.Secrets.SetHubToken(ctx, *u.Token); err != nil {
			return HubSettings{}, err
		}
	}
	if stored != nil {
		b, err := json.Marshal(stored)
		if err != nil {
			return HubSettings{}, err
		}
		if err := s.Store.SetSetting(ctx, settingPrecheck, string(b)); err != nil {
			return HubSettings{}, err
		}
	}
	return s.Get(ctx)
}
