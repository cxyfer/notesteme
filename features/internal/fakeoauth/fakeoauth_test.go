package fakeoauth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	clientID = "hub-test"
	secret   = "hub-secret"
	callback = "https://hub.example.org/auth/github/callback"
)

func noRedirect(srv *httptest.Server) *http.Client {
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// authorize 送出 authorize 請求，回傳轉回 callback 的 query。
func authorize(t *testing.T, srv *httptest.Server, p *Provider, extra url.Values) url.Values {
	t.Helper()
	q := url.Values{"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {callback}, "state": {"st-1"}}
	for k, v := range extra {
		q[k] = v
	}
	resp, err := noRedirect(srv).Get(srv.URL + paths[p.kind].authorize + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: got %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), callback) {
		t.Fatalf("authorize redirected to %q", resp.Header.Get("Location"))
	}
	return loc.Query()
}

func exchange(t *testing.T, srv *httptest.Server, p *Provider, form url.Values) (int, map[string]any) {
	t.Helper()
	base := url.Values{"grant_type": {"authorization_code"}, "redirect_uri": {callback}, "client_id": {clientID}, "client_secret": {secret}}
	for k, v := range form {
		base[k] = v
	}
	resp, err := srv.Client().PostForm(srv.URL+paths[p.kind].token, base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func newProvider(t *testing.T, kind Kind) (*Provider, *httptest.Server) {
	t.Helper()
	p, err := New(kind, clientID, secret, func() time.Time { return time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(p)
	t.Cleanup(srv.Close)
	return p, srv
}

func TestPKCEIsVerified(t *testing.T) {
	p, srv := newProvider(t, LinuxDo)
	verifier := strings.Repeat("v", 64)
	for _, tc := range []struct {
		name, verifier string
		want           int
	}{
		{"wrong verifier", strings.Repeat("w", 64), http.StatusBadRequest},
		{"right verifier", verifier, http.StatusOK},
	} {
		p.NextAs("alice")
		cb := authorize(t, srv, p, url.Values{"code_challenge": {S256(verifier)}, "code_challenge_method": {"S256"}})
		code, _ := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}, "code_verifier": {tc.verifier}})
		if code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, code, tc.want)
		}
		if got := p.ValidPKCE(); got != (tc.want == http.StatusOK) {
			t.Fatalf("%s: ValidPKCE = %v", tc.name, got)
		}
	}
}

func TestCodesAreSingleUseAndBoundToTheClient(t *testing.T) {
	p, srv := newProvider(t, GitHub)
	p.NextAs("alice")
	cb := authorize(t, srv, p, nil)
	if cb.Get("state") != "st-1" {
		t.Fatalf("state not echoed: %v", cb)
	}
	if code, _ := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}, "client_secret": {"wrong"}}); code != http.StatusBadRequest {
		t.Fatalf("wrong secret: got %d", code)
	}
	code, out := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}})
	if code != http.StatusOK || out["access_token"] == "" {
		t.Fatalf("exchange: %d %v", code, out)
	}
	if code, _ := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}}); code != http.StatusBadRequest {
		t.Fatalf("reused code: got %d", code)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+out["access_token"].(string))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var user map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&user)
	if user["login"] != "alice" || int64(user["id"].(float64)) != NumericID("github-alice") {
		t.Fatalf("user = %v", user)
	}
}

func TestDenyNextReturnsAccessDenied(t *testing.T) {
	p, srv := newProvider(t, GitHub)
	p.NextAs("alice")
	p.DenyNext()
	cb := authorize(t, srv, p, nil)
	if cb.Get("error") != "access_denied" || cb.Get("code") != "" {
		t.Fatalf("callback = %v", cb)
	}
	// 只影響下一次。
	if cb := authorize(t, srv, p, nil); cb.Get("code") == "" {
		t.Fatalf("second authorize should succeed: %v", cb)
	}
}

func TestGoogleIDTokenIsSignedAndCarriesTheNonce(t *testing.T) {
	p, srv := newProvider(t, Google)
	p.SetUser(User{Name: "carol", Subject: "1234", Email: "carol@example.com", EmailVerified: false})
	p.NextAs("carol")
	cb := authorize(t, srv, p, url.Values{"nonce": {"n-1"}})
	code, out := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}})
	if code != http.StatusOK {
		t.Fatalf("exchange: %d", code)
	}
	parts := strings.Split(out["id_token"].(string), ".")
	if len(parts) != 3 {
		t.Fatalf("id_token = %v", out["id_token"])
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(p.PublicKey(), crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	if claims["nonce"] != "n-1" || claims["aud"] != clientID || claims["iss"] != GoogleIssuer || claims["sub"] != "1234" || claims["email_verified"] != false {
		t.Fatalf("claims = %v", claims)
	}

	resp, err := srv.Client().Get(srv.URL + "/oauth2/v3/certs")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"kid":"fakeoauth-1"`) {
		t.Fatalf("jwks = %s", b)
	}
}

func TestLinuxDoUserHasTrustLevel(t *testing.T) {
	p, srv := newProvider(t, LinuxDo)
	u := DefaultUser(LinuxDo, "dave")
	u.TrustLevel = 3
	p.SetUser(u)
	p.NextAs("dave")
	cb := authorize(t, srv, p, nil)
	_, out := exchange(t, srv, p, url.Values{"code": {cb.Get("code")}})
	req, _ := http.NewRequest("GET", srv.URL+"/api/user", nil)
	req.Header.Set("Authorization", "Bearer "+out["access_token"].(string))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var user map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&user)
	if user["trust_level"] != float64(3) || int64(user["id"].(float64)) != NumericID("linuxdo-dave") {
		t.Fatalf("user = %v", user)
	}
}

func TestUnknownKindIsRejected(t *testing.T) {
	if _, err := New("gitlab", clientID, secret, nil); err == nil {
		t.Fatal("unknown provider should be rejected")
	}
}
