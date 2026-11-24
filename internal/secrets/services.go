package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/store"
)

// ProviderKeyKind 是服務商 API key 在 secrets 表中的 kind。AAD 為 "provider_api_key/<id>"。
const ProviderKeyKind = "provider_api_key"

// allowedServiceProviders 是可透過 /api/settings/services 管理 API key 的服務商白名單。
var allowedServiceProviders = []string{
	"groq",
	"supadata",
	"anthropic",
	"openai",
	"gemini",
	"mistral",
	"openai-compatible",
}

var (
	// ErrUnknownProvider 表示服務商 ID 不在支援清單中。
	ErrUnknownProvider = errors.New("不支援的服務提供者。")
	// ErrInvalidAPIKey 表示 API key 包含控制字元等不合法字元；訊息永遠不含 key 的內容。
	ErrInvalidAPIKey = errors.New("API key 格式不正確。")
)

// ValidServiceProvider 回報 id 是否為可設定 API key 的服務商。
func ValidServiceProvider(id string) bool {
	return slices.Contains(allowedServiceProviders, id)
}

// ServiceProviderUpdate 表示單一服務商的 API key 更新指令：
// APIKey 為 nil 時保留既有值；空字串時刪除；非空時加密儲存。
type ServiceProviderUpdate struct {
	APIKey *string `json:"api_key"`
}

// ServiceSettingsUpdate 是 PUT /api/settings/services 的輸入結構。
type ServiceSettingsUpdate struct {
	Providers map[string]ServiceProviderUpdate `json:"providers"`
}

// ServiceProviderInfo 是單一服務商的公開資訊（只含末四碼，SEC-R6）。
type ServiceProviderInfo struct {
	APIKeyLast4 string `json:"api_key_last4,omitempty"`
}

// ServiceSettings 是 GET／PUT /api/settings/services 的輸出結構。
type ServiceSettings struct {
	Providers map[string]ServiceProviderInfo `json:"providers"`
}

// Services 以 Keyring 加解密並管理各服務商的 API key（SEC-R4、SEC-R6）。
// last4 每次讀取時由 secrets 表即時解密計算，永不持久化。
type Services struct {
	st   *store.Store
	keys *Keyring
}

// NewServices 建立 Services。
func NewServices(st *store.Store, keys *Keyring) *Services {
	return &Services{st: st, keys: keys}
}

// Get 讀取所有已儲存的服務商 API key，解密後只回傳末四碼。
func (s *Services) Get(ctx context.Context) (ServiceSettings, error) {
	list, err := s.st.Read().ListSecrets(ctx)
	if err != nil {
		return ServiceSettings{}, err
	}
	out := ServiceSettings{Providers: make(map[string]ServiceProviderInfo)}
	for _, info := range list {
		if info.Kind != ProviderKeyKind || !ValidServiceProvider(info.Name) {
			continue
		}
		key, ok, err := s.ProviderKey(ctx, info.Name)
		if err != nil {
			return ServiceSettings{}, err
		}
		if !ok || key == "" {
			continue
		}
		out.Providers[info.Name] = ServiceProviderInfo{APIKeyLast4: last4(key)}
	}
	return out, nil
}

// Put 依 update 在單一交易中更新或刪除服務商的 API key，並回傳更新後的公開設定（只含末四碼）。
// 所有欄位都先驗證完畢才寫入，任何錯誤都不會留下部分更新。
func (s *Services) Put(ctx context.Context, update ServiceSettingsUpdate) (ServiceSettings, error) {
	type op struct {
		id         string
		del        bool
		nonce      []byte
		ciphertext []byte
	}
	ops := make([]op, 0, len(update.Providers))
	for id, u := range update.Providers {
		if !ValidServiceProvider(id) {
			return ServiceSettings{}, fmt.Errorf("%w: %s", ErrUnknownProvider, id)
		}
		if u.APIKey == nil {
			continue
		}
		key := *u.APIKey
		if key == "" {
			ops = append(ops, op{id: id, del: true})
			continue
		}
		if hasControlChar(key) {
			return ServiceSettings{}, ErrInvalidAPIKey
		}
		nonce, ct, err := s.keys.Seal(ProviderKeyKind, id, []byte(key))
		if err != nil {
			return ServiceSettings{}, fmt.Errorf("加密 %s 的 API key 失敗：%w", id, err)
		}
		ops = append(ops, op{id: id, nonce: nonce, ciphertext: ct})
	}

	if len(ops) > 0 {
		err := s.st.Tx(ctx, func(tx *store.Repo) error {
			for _, o := range ops {
				if o.del {
					if _, err := tx.DeleteSecret(ctx, ProviderKeyKind, o.id); err != nil {
						return err
					}
					continue
				}
				if err := tx.UpsertSecret(ctx, ProviderKeyKind, o.id, o.nonce, o.ciphertext); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return ServiceSettings{}, err
		}
	}
	return s.Get(ctx)
}

// ProviderKey 讀取並解密指定服務商的 API key；尚未儲存時 ok 為 false。
func (s *Services) ProviderKey(ctx context.Context, id string) (string, bool, error) {
	if !ValidServiceProvider(id) {
		return "", false, fmt.Errorf("%w: %s", ErrUnknownProvider, id)
	}
	sec, err := s.st.Read().Secret(ctx, ProviderKeyKind, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := s.keys.Open(ProviderKeyKind, id, sec.Nonce, sec.Ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("解開 %s 的 API key 失敗：%w", id, err)
	}
	return string(plain), true, nil
}

func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func last4(s string) string {
	r := []rune(s)
	if len(r) > 4 {
		r = r[len(r)-4:]
	}
	return string(r)
}

// Values 解密並回傳所有 secret 的值（API key、cookie、Hub token…），只給送出資料前的洩漏掃描使用；
// 呼叫端不能記錄或回傳這些值。
func (s *Services) Values(ctx context.Context) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	list, err := s.st.Read().ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, info := range list {
		sec, err := s.st.Read().Secret(ctx, info.Kind, info.Name)
		if err != nil {
			return nil, err
		}
		plain, err := s.keys.Open(info.Kind, info.Name, sec.Nonce, sec.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("解開 secret %s/%s 失敗：%w", info.Kind, info.Name, err)
		}
		out = append(out, string(plain))
	}
	return out, nil
}
