package hubauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/idtoken"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// 對供應商的請求（HACC-R3）：只接受 https、不跟隨轉址、回應大小有上限；錯誤只描述原因，不帶 code、token 或回應內容。

// maxIdPBody 是供應商回應的大小上限。
const maxIdPBody = 1 << 20

// userAgent 是送給供應商的 User-Agent（GitHub 的 API 要求一定要有）。
const userAgent = "notesteme-hub"

// idpError 是向供應商完成登入失敗的原因（只寫進 log）。
type idpError struct{ step, reason string }

func (e *idpError) Error() string { return e.step + "：" + e.reason }

func fail(step, format string, a ...any) error { return &idpError{step: step, reason: fmt.Sprintf(format, a...)} }

// noRedirectClient 複製 base，不跟隨轉址（3xx 視為失敗）。
func noRedirectClient(base *http.Client) *http.Client {
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// send 送出請求並讀完回應（有大小上限）；網址必須是 https。
func (s *Service) send(req *http.Request, step string) (int, http.Header, []byte, error) {
	if req.URL.Scheme != "https" {
		return 0, nil, nil, fail(step, "只接受 https 的 endpoint")
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			// url.Error 的訊息含完整網址（可能帶 query）；只保留底層原因。
			err = ue.Err
		}
		return 0, nil, nil, fail(step, "連線失敗：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(netguard.LimitBody(resp, maxIdPBody))
	if err != nil {
		return 0, nil, nil, fail(step, "讀取回應失敗：%v", err)
	}
	return resp.StatusCode, resp.Header, b, nil
}

// tokenResponse 是 token endpoint 的回應。
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	Error       string `json:"error"`
}

// exchange 以授權碼與 PKCE verifier 換 token。
func (s *Service) exchange(ctx context.Context, spec Spec, client hubconfig.OAuthClient, code, verifier string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.redirectURI(spec.ID)},
		"code_verifier": {verifier},
	}
	if !spec.BasicAuth {
		form.Set("client_id", client.ClientID)
		form.Set("client_secret", client.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fail("token", "建立請求失敗")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if spec.BasicAuth {
		req.SetBasicAuth(url.QueryEscape(client.ClientID), url.QueryEscape(client.ClientSecret))
	}
	status, hdr, body, err := s.send(req, "token")
	if err != nil {
		return tokenResponse{}, err
	}
	if status != http.StatusOK {
		return tokenResponse{}, fail("token", "HTTP %d", status)
	}
	tr, err := parseTokenResponse(hdr.Get("Content-Type"), body)
	if err != nil {
		return tokenResponse{}, err
	}
	// GitHub 的錯誤常常是 200 加 {"error": …}：沒有 access_token 一律視為失敗。
	if tr.Error != "" || tr.AccessToken == "" {
		return tokenResponse{}, fail("token", "沒有 access_token（error=%q）", safeCode(tr.Error))
	}
	if spec.OIDC && tr.IDToken == "" {
		return tokenResponse{}, fail("token", "沒有 id_token")
	}
	return tr, nil
}

// parseTokenResponse 解析 JSON 或 form-encoded（GitHub 沒有收到 Accept: application/json 時）的 token 回應。
func parseTokenResponse(contentType string, body []byte) (tokenResponse, error) {
	mt, _, _ := mime.ParseMediaType(contentType)
	if mt == "application/x-www-form-urlencoded" || mt == "text/plain" {
		v, err := url.ParseQuery(string(body))
		if err != nil {
			return tokenResponse{}, fail("token", "回應格式不正確")
		}
		return tokenResponse{AccessToken: v.Get("access_token"), IDToken: v.Get("id_token"), Error: v.Get("error")}, nil
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fail("token", "回應不是 JSON")
	}
	return tr, nil
}

// safeCode 只保留錯誤代碼中安全的字元，避免把供應商回應的任意內容寫進 log。
func safeCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	return b.String()
}

// getJSON 以 access token 取得 JSON。
func (s *Service) getJSON(ctx context.Context, rawURL, accessToken, step string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fail(step, "建立請求失敗")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	status, _, body, err := s.send(req, step)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fail(step, "HTTP %d", status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fail(step, "回應不是預期的 JSON")
	}
	return nil
}

// numericID 把 GitHub、Linux.do 的數字 id 轉成字串；不是正整數時失敗。
func numericID(n json.Number) (string, bool) {
	v, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || v <= 0 {
		return "", false
	}
	return strconv.FormatInt(v, 10), true
}

// fetchIdentity 取得供應商的身分。Google 的 sub 來自驗證過的 id_token，使用者資訊的 sub 必須相同。
func (s *Service) fetchIdentity(ctx context.Context, spec Spec, client hubconfig.OAuthClient, tr tokenResponse, nonce string) (hubstore.ExternalIdentity, string, error) {
	out := hubstore.ExternalIdentity{Provider: string(spec.ID)}
	switch spec.ID {
	case hubconfig.Google:
		claims, err := s.jwks.Verify(ctx, tr.IDToken, idtoken.Expect{
			Issuers: spec.Issuers, Audience: client.ClientID, Nonce: nonce, Now: s.clock.Now(),
		})
		if err != nil {
			return out, "", fail("id_token", "%v", err)
		}
		var info struct {
			Sub           string `json:"sub"`
			Name          string `json:"name"`
			Email         string `json:"email"`
			EmailVerified any    `json:"email_verified"`
		}
		if err := s.getJSON(ctx, spec.UserURL, tr.AccessToken, "userinfo", &info); err != nil {
			return out, "", err
		}
		if info.Sub != claims.Subject {
			return out, "", fail("userinfo", "sub 與 id_token 不同")
		}
		// email 與 email_verified 以簽章驗證過的 id_token 為準（email_domain 規則只在 email_verified 時成立，HACC-R11）。
		out.Subject, out.Email, out.EmailVerified = claims.Subject, claims.Email, claims.EmailVerified
		out.Login = claims.Email
		if out.Login == "" {
			out.Login = info.Name
		}
		name := info.Name
		if name == "" {
			name, _, _ = strings.Cut(claims.Email, "@")
		}
		return out, name, nil
	case hubconfig.GitHub:
		var u struct {
			ID    json.Number `json:"id"`
			Login string      `json:"login"`
			Email string      `json:"email"`
		}
		if err := s.getJSON(ctx, spec.UserURL, tr.AccessToken, "user", &u); err != nil {
			return out, "", err
		}
		id, ok := numericID(u.ID)
		if !ok || u.Login == "" {
			return out, "", fail("user", "沒有使用者 id 或 login")
		}
		out.Subject, out.Login, out.Email = id, u.Login, u.Email
		if s.needsGitHubOrgs() {
			var orgs []struct {
				Login string `json:"login"`
			}
			if err := s.getJSON(ctx, spec.OrgsURL, tr.AccessToken, "orgs", &orgs); err != nil {
				return out, "", err
			}
			for _, o := range orgs {
				out.Attrs.Orgs = append(out.Attrs.Orgs, o.Login)
			}
		}
		return out, u.Login, nil
	case hubconfig.LinuxDo:
		var u struct {
			ID         json.Number `json:"id"`
			Username   string      `json:"username"`
			Email      string      `json:"email"`
			TrustLevel *int        `json:"trust_level"`
		}
		if err := s.getJSON(ctx, spec.UserURL, tr.AccessToken, "user", &u); err != nil {
			return out, "", err
		}
		id, ok := numericID(u.ID)
		if !ok || u.Username == "" {
			return out, "", fail("user", "沒有使用者 id 或 username")
		}
		out.Subject, out.Login, out.Email, out.Attrs.TrustLevel = id, u.Username, u.Email, u.TrustLevel
		return out, u.Username, nil
	}
	return out, "", fail("user", "不支援的登入方式")
}
