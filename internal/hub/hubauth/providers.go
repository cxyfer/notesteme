package hubauth

import "github.com/cxyfer/notesteme/internal/hub/hubconfig"

// Spec 是一個登入方式寫死在程式中的 endpoint（HACC-R1：不能設定）。
// 主機名稱與路徑依各供應商 2026-11 的公開文件；驗收測試以 fakenet 把同樣的主機名稱導到 fakeoauth。
type Spec struct {
	ID   hubconfig.Provider
	Name string // 給人看的名稱（錯誤訊息與 GET /api/v1/auth/providers）
	// AuthorizeURL、TokenURL、UserURL 是授權、換 token 與使用者資訊的 endpoint；OrgsURL 只有 GitHub。
	AuthorizeURL, TokenURL, UserURL, OrgsURL string
	// JWKSURL 與 Issuers 只有 OIDC（Google）。
	JWKSURL string
	Issuers []string
	// BasicAuth 為 true 時以 HTTP Basic 送 client 憑證，否則放在 form（client_secret_post）。
	BasicAuth bool
	// OIDC 為 true 時要求 id_token 並驗證 nonce。
	OIDC bool
}

// Specs 是支援的登入方式。
var Specs = map[hubconfig.Provider]Spec{
	hubconfig.Google: {
		ID: hubconfig.Google, Name: "Google",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		UserURL:      "https://openidconnect.googleapis.com/v1/userinfo",
		JWKSURL:      "https://www.googleapis.com/oauth2/v3/certs",
		Issuers:      []string{"https://accounts.google.com", "accounts.google.com"},
		OIDC:         true,
	},
	hubconfig.GitHub: {
		ID: hubconfig.GitHub, Name: "GitHub",
		AuthorizeURL: "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		UserURL:      "https://api.github.com/user",
		OrgsURL:      "https://api.github.com/user/orgs",
	},
	hubconfig.LinuxDo: {
		ID: hubconfig.LinuxDo, Name: "Linux.do",
		AuthorizeURL: "https://connect.linux.do/oauth2/authorize",
		TokenURL:     "https://connect.linux.do/oauth2/token",
		UserURL:      "https://connect.linux.do/api/user",
		BasicAuth:    true,
	},
}

// ProviderName 回傳登入方式給人看的名稱；不認得時原樣回傳。
func ProviderName(p string) string {
	if s, ok := Specs[hubconfig.Provider(p)]; ok {
		return s.Name
	}
	return p
}
